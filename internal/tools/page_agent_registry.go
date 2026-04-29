package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var pageAgentSnapshotRefPattern = regexp.MustCompile(`- ([^"]+?) "([^"]*)" \[ref=(e[0-9]+)\]`)
var quotedValuePattern = regexp.MustCompile(`"([^"]+)"|'([^']+)'`)
var pageAgentFieldValuePatterns = map[string]*regexp.Regexp{
	"email":    regexp.MustCompile(`(?i)\bemail(?: address)?(?:\s+is|\s+as|\s+to|=|:)?\s*("([^"]+)"|'([^']+)'|([^\s,;]+))`),
	"password": regexp.MustCompile(`(?i)\bpassword(?:\s+is|\s+as|\s+to|=|:)?\s*("([^"]+)"|'([^']+)'|([^\s,;]+))`),
	"username": regexp.MustCompile(`(?i)\b(?:username|user name|user id|login)(?:\s+is|\s+as|\s+to|=|:)?\s*("([^"]+)"|'([^']+)'|([^\s,;]+))`),
}
var pageAgentFieldAliases = map[string][]string{
	"email":    {"email", "e-mail", "email address"},
	"password": {"password", "passcode", "pwd"},
	"username": {"username", "user name", "user id", "login"},
}

type pageAgent struct {
	ID                 string
	Name               string
	Goal               string
	Status             string
	EnvironmentName    string
	TabID              string
	LastText           string
	LastSnapshot       string
	LastTool           string
	LastToolArgs       map[string]any
	LastResult         map[string]any
	LastProposal       map[string]any
	LastProposalSource string
	LastRunAt          time.Time
	History            []pageAgentHistoryEntry
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type pageAgentHistoryEntry struct {
	Step        int
	Status      string
	Summary     string
	Result      map[string]any
	StartedAt   time.Time
	CompletedAt time.Time
}

func (e *Executor) nextPageAgentIDLocked() string {
	e.nextPageAgentID++
	return fmt.Sprintf("page-agent-%d", e.nextPageAgentID)
}

func serializePageAgent(agent *pageAgent, page *pageRuntime) map[string]any {
	connected := false
	if page != nil && page.PageClient != nil {
		connected = true
	}
	refCount := 0
	if page != nil {
		refCount = len(page.AriaRefStore)
	}
	return map[string]any{
		"agentId":            agent.ID,
		"name":               agent.Name,
		"goal":               agent.Goal,
		"status":             agent.Status,
		"environment":        agent.EnvironmentName,
		"tabId":              agent.TabID,
		"pageConnected":      connected,
		"pageRefCount":       refCount,
		"lastText":           agent.LastText,
		"lastSnapshot":       agent.LastSnapshot,
		"lastTool":           agent.LastTool,
		"lastToolArgs":       agent.LastToolArgs,
		"lastResult":         agent.LastResult,
		"lastProposal":       agent.LastProposal,
		"lastProposalSource": agent.LastProposalSource,
		"lastRunAt":          formatAgentTime(agent.LastRunAt),
		"history":            serializePageAgentHistory(agent.History),
		"historyCount":       len(agent.History),
		"createdAt":          agent.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":          agent.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func serializePageAgentHistory(history []pageAgentHistoryEntry) []map[string]any {
	if len(history) == 0 {
		return []map[string]any{}
	}
	items := make([]map[string]any, 0, len(history))
	for _, entry := range history {
		items = append(items, map[string]any{
			"step":        entry.Step,
			"status":      entry.Status,
			"summary":     entry.Summary,
			"result":      entry.Result,
			"startedAt":   formatAgentTime(entry.StartedAt),
			"completedAt": formatAgentTime(entry.CompletedAt),
		})
	}
	return items
}

func formatAgentTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func summarizeStepResult(result map[string]any) string {
	if len(result) == 0 {
		return "no result"
	}
	if ok, _ := result["ok"].(bool); !ok {
		if msg, _ := result["error"].(string); strings.TrimSpace(msg) != "" {
			return msg
		}
		return "step failed"
	}
	text, _ := result["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return "step completed"
	}
	if len(text) > 120 {
		text = text[:120] + "..."
	}
	return text
}

func confidenceLevel(score string) string {
	score = strings.TrimSpace(strings.ToLower(score))
	switch score {
	case "high", "medium", "low":
		return score
	default:
		return "medium"
	}
}

func proposalIntent(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "browser_get_text", "browser_aria_snapshot", "browser_screenshot":
		return "observe"
	case "browser_wait_for_load":
		return "wait"
	case "browser_click_by_ref":
		return "act"
	case "browser_type_by_ref":
		return "fill"
	default:
		return "act"
	}
}

func expectedOutcomeForTool(toolName string, target map[string]any) string {
	name, _ := target["name"].(string)
	name = strings.TrimSpace(name)
	switch strings.TrimSpace(toolName) {
	case "browser_get_text":
		return "More visible page text is available for planning."
	case "browser_aria_snapshot":
		return "A fresh accessibility snapshot is available with concrete refs."
	case "browser_screenshot":
		return "A visual capture of the current page is available."
	case "browser_wait_for_load":
		return "The page reaches a more stable loaded state."
	case "browser_click_by_ref":
		if name != "" {
			return fmt.Sprintf("The page reacts after clicking %q.", name)
		}
		return "The page reacts after the click."
	case "browser_type_by_ref":
		if name != "" {
			return fmt.Sprintf("The field %q contains the intended input.", name)
		}
		return "The intended input is entered into the target field."
	default:
		return "The proposed action completes successfully."
	}
}

func verificationHintsForTool(toolName string, arguments map[string]any, target map[string]any) map[string]any {
	hints := map[string]any{}
	name, _ := target["name"].(string)
	name = strings.TrimSpace(name)
	switch strings.TrimSpace(toolName) {
	case "browser_click_by_ref":
		if name != "" {
			hints["targetName"] = name
			hints["targetGone"] = true
		}
		lowerName := strings.ToLower(name)
		signals := make([]string, 0, 6)
		if strings.Contains(lowerName, "sign in") || strings.Contains(lowerName, "log in") || strings.Contains(lowerName, "login") {
			signals = append(signals, "welcome", "dashboard", "sign out", "logout", "my account")
		}
		if strings.Contains(lowerName, "search") {
			signals = append(signals, "results")
		}
		if strings.Contains(lowerName, "submit") || strings.Contains(lowerName, "apply") || strings.Contains(lowerName, "save") || strings.Contains(lowerName, "continue") || strings.Contains(lowerName, "next") {
			signals = append(signals, "success", "submitted", "saved", "applied", "complete", "result:")
		}
		if len(signals) > 0 {
			hints["successSignals"] = signals
		}
	case "browser_type_by_ref":
		if text, _ := arguments["text"].(string); strings.TrimSpace(text) != "" {
			hints["valueVisible"] = strings.TrimSpace(text)
		}
		if name != "" {
			hints["targetName"] = name
		}
	}
	return hints
}

func buildProposal(toolName string, arguments map[string]any, reason string, confidence string, target map[string]any) map[string]any {
	proposal := map[string]any{
		"type":              "tool",
		"intent":            proposalIntent(toolName),
		"tool":              toolName,
		"arguments":         arguments,
		"reason":            reason,
		"confidence":        confidenceLevel(confidence),
		"expectedOutcome":   expectedOutcomeForTool(toolName, target),
		"needsVerification": true,
		"verificationHints": verificationHintsForTool(toolName, arguments, target),
	}
	if len(target) > 0 {
		proposal["target"] = target
	}
	return proposal
}

func canonicalJSONString(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func proposalSignature(proposal map[string]any) string {
	if len(proposal) == 0 {
		return ""
	}
	toolName, args, err := proposalToolAndArgs(proposal)
	if err != nil {
		return ""
	}
	return toolName + "|" + canonicalJSONString(args)
}

func observationSignature(text string, snapshot string) string {
	text = strings.TrimSpace(text)
	snapshot = strings.TrimSpace(snapshot)
	if text == "" && snapshot == "" {
		return ""
	}
	return text + "\n---\n" + snapshot
}

func mapStringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return value
}

func normalizedContains(haystack string, needle string) bool {
	haystack = strings.ToLower(strings.TrimSpace(haystack))
	needle = strings.ToLower(strings.TrimSpace(needle))
	if haystack == "" || needle == "" {
		return false
	}
	return strings.Contains(haystack, needle)
}

func verificationFailure(reason string, expectedOutcome string) map[string]any {
	result := map[string]any{
		"ok":     false,
		"status": "verification_failed",
		"reason": reason,
	}
	if strings.TrimSpace(expectedOutcome) != "" {
		result["expectedOutcome"] = expectedOutcome
	}
	return result
}

func verificationSuccess(reason string) map[string]any {
	return map[string]any{
		"ok":     true,
		"status": "verified",
		"reason": reason,
	}
}

func combinedObservationText(text string, snapshot string) string {
	return strings.ToLower(strings.TrimSpace(text + "\n" + snapshot))
}

func goalHasAny(goal string, phrases ...string) bool {
	goal = strings.ToLower(strings.TrimSpace(goal))
	if goal == "" {
		return false
	}
	for _, phrase := range phrases {
		if strings.Contains(goal, strings.ToLower(strings.TrimSpace(phrase))) {
			return true
		}
	}
	return false
}

func observationHasAny(text string, snapshot string, phrases ...string) bool {
	combined := combinedObservationText(text, snapshot)
	if combined == "" {
		return false
	}
	for _, phrase := range phrases {
		if strings.Contains(combined, strings.ToLower(strings.TrimSpace(phrase))) {
			return true
		}
	}
	return false
}

func quotedGoalValues(goal string) []string {
	matches := quotedValuePattern.FindAllStringSubmatch(strings.TrimSpace(goal), -1)
	if len(matches) == 0 {
		return nil
	}
	values := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		for _, value := range match[1:] {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			key := strings.ToLower(value)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			values = append(values, value)
		}
	}
	return values
}

func goalSpecificVerification(goal string, toolName string, preText string, preSnapshot string, postText string, postSnapshot string, targetName string) (map[string]any, bool) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil, false
	}

	if strings.TrimSpace(toolName) == "browser_type_by_ref" {
		for _, value := range quotedGoalValues(goal) {
			if observationHasAny(postText, postSnapshot, value) {
				return verificationSuccess(fmt.Sprintf("The post-action page state contains the goal value %q.", value)), true
			}
		}
	}

	if strings.TrimSpace(toolName) == "browser_click_by_ref" {
		if goalHasAny(goal, "log in", "login", "sign in") && observationHasAny(postText, postSnapshot, "welcome", "dashboard", "sign out", "logout", "my account") {
			return verificationSuccess("The post-action page contains common sign-in success signals."), true
		}
		if goalHasAny(goal, "search") {
			for _, value := range quotedGoalValues(goal) {
				if observationHasAny(postText, postSnapshot, value) {
					return verificationSuccess(fmt.Sprintf("The search results appear to reference the requested value %q.", value)), true
				}
			}
		}
		if goalHasAny(goal, "submit", "apply", "save", "continue", "next") && observationHasAny(postText, postSnapshot, "success", "submitted", "saved", "applied", "complete", "result:") {
			return verificationSuccess("The post-action page contains common completion signals."), true
		}
		if strings.TrimSpace(targetName) != "" {
			preHasTarget := normalizedContains(preText, targetName) || normalizedContains(preSnapshot, targetName)
			postHasTarget := normalizedContains(postText, targetName) || normalizedContains(postSnapshot, targetName)
			if preHasTarget && !postHasTarget {
				return verificationSuccess(fmt.Sprintf("The clicked target %q is no longer present in the post-action page state.", targetName)), true
			}
		}
	}

	return nil, false
}

func verificationFromHints(hints map[string]any, preText string, preSnapshot string, postText string, postSnapshot string) (map[string]any, bool) {
	if len(hints) == 0 {
		return nil, false
	}
	if valueVisible, _ := hints["valueVisible"].(string); strings.TrimSpace(valueVisible) != "" {
		if observationHasAny(postText, postSnapshot, valueVisible) {
			return verificationSuccess(fmt.Sprintf("The post-action page state contains the hinted value %q.", valueVisible)), true
		}
	}
	if rawSignals, ok := hints["successSignals"].([]string); ok && len(rawSignals) > 0 {
		if observationHasAny(postText, postSnapshot, rawSignals...) {
			return verificationSuccess("The post-action page contains hinted success signals."), true
		}
	}
	if rawSignals, ok := hints["successSignals"].([]any); ok && len(rawSignals) > 0 {
		signals := make([]string, 0, len(rawSignals))
		for _, signal := range rawSignals {
			if text, ok := signal.(string); ok && strings.TrimSpace(text) != "" {
				signals = append(signals, text)
			}
		}
		if len(signals) > 0 && observationHasAny(postText, postSnapshot, signals...) {
			return verificationSuccess("The post-action page contains hinted success signals."), true
		}
	}
	targetName, _ := hints["targetName"].(string)
	if targetGone, _ := hints["targetGone"].(bool); targetGone && strings.TrimSpace(targetName) != "" {
		preHasTarget := normalizedContains(preText, targetName) || normalizedContains(preSnapshot, targetName)
		postHasTarget := normalizedContains(postText, targetName) || normalizedContains(postSnapshot, targetName)
		if preHasTarget && !postHasTarget {
			return verificationSuccess(fmt.Sprintf("The hinted target %q is no longer present in the post-action page state.", targetName)), true
		}
	}
	return nil, false
}

func verificationResultForApply(proposal map[string]any, applyResult map[string]any) map[string]any {
	needsVerification, _ := proposal["needsVerification"].(bool)
	if !needsVerification {
		return map[string]any{
			"ok":     true,
			"status": "not_required",
			"reason": "The proposal does not require post-action verification.",
		}
	}

	toolName, _ := applyResult["tool"].(string)
	expectedOutcome, _ := proposal["expectedOutcome"].(string)
	postText, _ := applyResult["postActionText"].(string)
	postSnapshot, _ := applyResult["postActionSnapshot"].(string)
	preText, _ := applyResult["preActionText"].(string)
	preSnapshot, _ := applyResult["preActionSnapshot"].(string)
	goal, _ := applyResult["goal"].(string)
	result, _ := applyResult["result"].(map[string]any)
	target, _ := proposal["target"].(map[string]any)
	hints, _ := proposal["verificationHints"].(map[string]any)
	targetName, _ := target["name"].(string)
	targetRole, _ := target["role"].(string)
	postSig := observationSignature(postText, postSnapshot)
	preSig := observationSignature(preText, preSnapshot)

	switch strings.TrimSpace(toolName) {
	case "browser_click_by_ref":
		if verification, ok := verificationFromHints(hints, preText, preSnapshot, postText, postSnapshot); ok {
			return verification
		}
		if verification, ok := goalSpecificVerification(goal, toolName, preText, preSnapshot, postText, postSnapshot, targetName); ok {
			return verification
		}
		if postSig != "" && preSig != "" && postSig != preSig && (normalizedContains(postText, targetName) || normalizedContains(postSnapshot, targetName)) {
			return verificationSuccess(fmt.Sprintf("The post-action page changed and still references the clicked %s target %q.", strings.TrimSpace(targetRole), strings.TrimSpace(targetName)))
		}
		if postSig != "" && preSig != "" && postSig != preSig {
			return verificationSuccess("The click changed the observed page state.")
		}
		return verificationFailure("The click completed, but the observed page state did not show a clear change or target-specific confirmation.", expectedOutcome)
	case "browser_type_by_ref":
		if verification, ok := verificationFromHints(hints, preText, preSnapshot, postText, postSnapshot); ok {
			return verification
		}
		if verification, ok := goalSpecificVerification(goal, toolName, preText, preSnapshot, postText, postSnapshot, targetName); ok {
			return verification
		}
		args, _ := applyResult["arguments"].(map[string]any)
		expectedText, _ := args["text"].(string)
		if strings.TrimSpace(expectedText) != "" && (strings.Contains(postText, expectedText) || strings.Contains(postSnapshot, expectedText)) {
			return verificationSuccess("The typed text is reflected in the post-action page state.")
		}
		if postSig != "" && preSig != "" && postSig != preSig {
			return verificationSuccess("Typing changed the observed page state.")
		}
		return verificationFailure("Typing completed, but the expected value was not visible in the post-action page state.", expectedOutcome)
	case "browser_wait_for_load":
		if ok, _ := result["ok"].(bool); ok {
			return verificationSuccess("The wait operation completed successfully.")
		}
		return verificationFailure("The wait action did not return a successful result.", expectedOutcome)
	case "browser_get_text", "browser_aria_snapshot", "browser_screenshot":
		if ok, _ := result["ok"].(bool); ok {
			return verificationSuccess("The observation tool completed successfully.")
		}
		return verificationFailure("The observation tool did not return a successful result.", expectedOutcome)
	}
	return verificationFailure("The action completed, but no verification rule matched this tool.", expectedOutcome)
}

type snapshotRefCandidate struct {
	Role string
	Name string
	Ref  string
}

type structuredInputCandidate struct {
	Field string
	Value string
	Node  snapshotRefCandidate
}

func parseSnapshotRefCandidates(snapshot string) []snapshotRefCandidate {
	lines := strings.Split(snapshot, "\n")
	out := make([]snapshotRefCandidate, 0, len(lines))
	for _, line := range lines {
		matches := pageAgentSnapshotRefPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(matches) != 4 {
			continue
		}
		out = append(out, snapshotRefCandidate{
			Role: strings.ToLower(strings.TrimSpace(matches[1])),
			Name: strings.TrimSpace(matches[2]),
			Ref:  strings.TrimSpace(matches[3]),
		})
	}
	return out
}

func proposalGoalTokens(goal string) []string {
	raw := strings.Fields(strings.ToLower(goal))
	stop := map[string]struct{}{
		"the": {}, "a": {}, "an": {}, "to": {}, "and": {}, "or": {}, "on": {}, "in": {}, "of": {},
		"for": {}, "with": {}, "page": {}, "button": {}, "link": {}, "tab": {}, "agent": {}, "browser": {},
		"click": {}, "open": {}, "submit": {}, "login": {}, "log": {}, "sign": {}, "select": {}, "go": {},
	}
	tokens := make([]string, 0, len(raw))
	for _, token := range raw {
		token = strings.Trim(token, `"'.,:;!?()[]{}<>`)
		if len(token) < 2 {
			continue
		}
		if _, skip := stop[token]; skip {
			continue
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func findClickCandidate(goal string, candidates []snapshotRefCandidate) *snapshotRefCandidate {
	tokens := proposalGoalTokens(goal)
	if len(candidates) == 0 {
		return nil
	}

	scoreCandidate := func(candidate snapshotRefCandidate) int {
		role := strings.ToLower(candidate.Role)
		if role != "button" && role != "link" {
			return -1
		}
		name := strings.ToLower(candidate.Name)
		score := 0
		for _, token := range tokens {
			if strings.Contains(name, token) {
				score += 3
			}
		}
		if strings.Contains(strings.ToLower(goal), "sign in") && strings.Contains(name, "sign in") {
			score += 4
		}
		if strings.Contains(strings.ToLower(goal), "log in") && strings.Contains(name, "log in") {
			score += 4
		}
		if strings.Contains(strings.ToLower(goal), "submit") && strings.Contains(name, "submit") {
			score += 4
		}
		if strings.Contains(strings.ToLower(goal), "next") && strings.Contains(name, "next") {
			score += 4
		}
		return score
	}

	var best *snapshotRefCandidate
	bestScore := 0
	for _, candidate := range candidates {
		score := scoreCandidate(candidate)
		if score <= 0 {
			continue
		}
		c := candidate
		if best == nil || score > bestScore {
			best = &c
			bestScore = score
		}
	}
	if best != nil {
		return best
	}

	for _, candidate := range candidates {
		role := strings.ToLower(candidate.Role)
		if role == "button" || role == "link" {
			c := candidate
			return &c
		}
	}
	return nil
}

func findPostInputClickCandidate(goal string, candidates []snapshotRefCandidate) *snapshotRefCandidate {
	if len(candidates) == 0 {
		return nil
	}

	lowerGoal := strings.ToLower(strings.TrimSpace(goal))
	preferredPhrases := []string{"search", "submit", "go", "next", "continue", "sign in", "log in", "login"}

	var best *snapshotRefCandidate
	bestScore := 0
	for _, candidate := range candidates {
		role := strings.ToLower(candidate.Role)
		if role != "button" && role != "link" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(candidate.Name))
		score := 1
		for _, phrase := range preferredPhrases {
			if strings.Contains(name, phrase) {
				score += 4
			}
			if strings.Contains(lowerGoal, phrase) && strings.Contains(name, phrase) {
				score += 3
			}
		}
		if best == nil || score > bestScore {
			c := candidate
			best = &c
			bestScore = score
		}
	}
	return best
}

func findTextboxCandidate(goal string, candidates []snapshotRefCandidate) *snapshotRefCandidate {
	tokens := proposalGoalTokens(goal)
	if len(candidates) == 0 {
		return nil
	}

	scoreCandidate := func(candidate snapshotRefCandidate) int {
		role := strings.ToLower(candidate.Role)
		if role != "textbox" && role != "searchbox" && role != "combobox" {
			return -1
		}
		name := strings.ToLower(candidate.Name)
		score := 1
		for _, token := range tokens {
			if strings.Contains(name, token) {
				score += 3
			}
		}
		if strings.Contains(strings.ToLower(goal), "search") && strings.Contains(name, "search") {
			score += 4
		}
		if strings.Contains(strings.ToLower(goal), "email") && strings.Contains(name, "email") {
			score += 4
		}
		if strings.Contains(strings.ToLower(goal), "password") && strings.Contains(name, "password") {
			score += 4
		}
		return score
	}

	var best *snapshotRefCandidate
	bestScore := 0
	for _, candidate := range candidates {
		score := scoreCandidate(candidate)
		if score <= 0 {
			continue
		}
		c := candidate
		if best == nil || score > bestScore {
			best = &c
			bestScore = score
		}
	}
	if best != nil {
		return best
	}

	for _, candidate := range candidates {
		role := strings.ToLower(candidate.Role)
		if role == "textbox" || role == "searchbox" || role == "combobox" {
			c := candidate
			return &c
		}
	}
	return nil
}

func findSelectCandidate(goal string, candidates []snapshotRefCandidate) *snapshotRefCandidate {
	tokens := proposalGoalTokens(goal)
	if len(candidates) == 0 {
		return nil
	}

	scoreCandidate := func(candidate snapshotRefCandidate) int {
		role := strings.ToLower(candidate.Role)
		if role != "combobox" && role != "listbox" {
			return -1
		}
		name := strings.ToLower(candidate.Name)
		score := 1
		for _, token := range tokens {
			if strings.Contains(name, token) {
				score += 3
			}
		}
		if strings.Contains(strings.ToLower(goal), "country") && strings.Contains(name, "country") {
			score += 4
		}
		if strings.Contains(strings.ToLower(goal), "region") && strings.Contains(name, "region") {
			score += 4
		}
		if strings.Contains(strings.ToLower(goal), "state") && strings.Contains(name, "state") {
			score += 4
		}
		return score
	}

	var best *snapshotRefCandidate
	bestScore := 0
	for _, candidate := range candidates {
		score := scoreCandidate(candidate)
		if score <= 0 {
			continue
		}
		c := candidate
		if best == nil || score > bestScore {
			best = &c
			bestScore = score
		}
	}
	if best != nil {
		return best
	}

	for _, candidate := range candidates {
		role := strings.ToLower(candidate.Role)
		if role == "combobox" || role == "listbox" {
			c := candidate
			return &c
		}
	}
	return nil
}

func extractFieldValue(goal string, field string) string {
	pattern := pageAgentFieldValuePatterns[field]
	if pattern == nil {
		return ""
	}
	matches := pattern.FindStringSubmatch(strings.TrimSpace(goal))
	if len(matches) == 0 {
		return ""
	}
	for _, value := range matches[2:] {
		value = strings.TrimSpace(strings.Trim(value, `"'.,:;!?`))
		if value != "" {
			return value
		}
	}
	return ""
}

func extractStructuredInputValues(goal string) map[string]string {
	values := map[string]string{}
	for field := range pageAgentFieldValuePatterns {
		if value := extractFieldValue(goal, field); value != "" {
			values[field] = value
		}
	}
	return values
}

func structuredFieldOrder(goal string, values map[string]string) []string {
	type fieldPos struct {
		Field string
		Pos   int
	}
	goal = strings.ToLower(strings.TrimSpace(goal))
	positions := make([]fieldPos, 0, len(values))
	for field := range values {
		pos := len(goal) + 100
		for _, alias := range pageAgentFieldAliases[field] {
			if idx := strings.Index(goal, alias); idx >= 0 && idx < pos {
				pos = idx
			}
		}
		positions = append(positions, fieldPos{Field: field, Pos: pos})
	}
	sort.SliceStable(positions, func(i, j int) bool {
		if positions[i].Pos == positions[j].Pos {
			return positions[i].Field < positions[j].Field
		}
		return positions[i].Pos < positions[j].Pos
	})
	out := make([]string, 0, len(positions))
	for _, item := range positions {
		out = append(out, item.Field)
	}
	return out
}

func detectCandidateField(candidate snapshotRefCandidate) string {
	name := strings.ToLower(strings.TrimSpace(candidate.Name))
	for field, aliases := range pageAgentFieldAliases {
		for _, alias := range aliases {
			if strings.Contains(name, alias) {
				return field
			}
		}
	}
	return ""
}

func resolveStructuredFieldByRef(candidates []snapshotRefCandidate, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	for _, candidate := range candidates {
		if candidate.Ref != ref {
			continue
		}
		if field := detectCandidateField(candidate); field != "" {
			return field
		}
	}
	return ""
}

func findStructuredInputCandidateForField(goal string, candidates []snapshotRefCandidate, field string, value string, excludeRef string) *structuredInputCandidate {
	if len(candidates) == 0 || strings.TrimSpace(field) == "" || strings.TrimSpace(value) == "" {
		return nil
	}
	lowerGoal := strings.ToLower(strings.TrimSpace(goal))
	var best *snapshotRefCandidate
	bestScore := 0

	for _, candidate := range candidates {
		if candidate.Ref == excludeRef {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(candidate.Role))
		if role != "textbox" && role != "searchbox" && role != "combobox" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(candidate.Name))
		score := 1
		switch field {
		case "password":
			if role == "searchbox" {
				score = -1
			}
		case "email", "username":
			if role == "searchbox" {
				score = -1
			}
		}
		if score < 0 {
			continue
		}
		for _, alias := range pageAgentFieldAliases[field] {
			if strings.Contains(name, alias) {
				score += 5
			}
			if strings.Contains(lowerGoal, alias) && strings.Contains(name, alias) {
				score += 2
			}
		}
		if field == "email" && strings.Contains(value, "@") {
			score += 1
		}
		if field == "password" && strings.Contains(name, "password") {
			score += 2
		}
		if best == nil || score > bestScore {
			c := candidate
			best = &c
			bestScore = score
		}
	}

	if best == nil {
		return nil
	}
	return &structuredInputCandidate{
		Field: field,
		Value: value,
		Node:  *best,
	}
}

func findStructuredInputCandidate(goal string, candidates []snapshotRefCandidate, values map[string]string, excludeRef string, orderedFields []string) *structuredInputCandidate {
	if len(candidates) == 0 || len(values) == 0 {
		return nil
	}
	for _, field := range orderedFields {
		value := values[field]
		if value == "" {
			continue
		}
		if candidate := findStructuredInputCandidateForField(goal, candidates, field, value, excludeRef); candidate != nil {
			return candidate
		}
	}
	return nil
}

func extractInputText(goal string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ""
	}
	if matches := quotedValuePattern.FindStringSubmatch(goal); len(matches) >= 3 {
		if strings.TrimSpace(matches[1]) != "" {
			return strings.TrimSpace(matches[1])
		}
		if strings.TrimSpace(matches[2]) != "" {
			return strings.TrimSpace(matches[2])
		}
	}

	lower := strings.ToLower(goal)
	prefixes := []string{
		"search for ",
		"search ",
		"type ",
		"enter ",
		"fill ",
		"input ",
	}
	for _, prefix := range prefixes {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			value := strings.TrimSpace(goal[idx+len(prefix):])
			value = strings.Trim(value, `"'.,:;!?`)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func extractSelectLabel(goal string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ""
	}
	if matches := quotedValuePattern.FindStringSubmatch(goal); len(matches) >= 3 {
		if strings.TrimSpace(matches[1]) != "" {
			return strings.TrimSpace(matches[1])
		}
		if strings.TrimSpace(matches[2]) != "" {
			return strings.TrimSpace(matches[2])
		}
	}

	lower := strings.ToLower(goal)
	prefixes := []string{
		"select ",
		"choose ",
		"pick ",
	}
	for _, prefix := range prefixes {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			value := strings.TrimSpace(goal[idx+len(prefix):])
			value = strings.Trim(value, `"'.,:;!?`)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func proposeNextActionFromContext(goal string, text string, snapshot string, lastTool string, lastArgs map[string]any) map[string]any {
	normalizedGoal := strings.ToLower(strings.TrimSpace(goal))
	normalizedText := strings.TrimSpace(text)
	normalizedSnapshot := strings.TrimSpace(snapshot)
	normalizedLastTool := strings.TrimSpace(lastTool)
	candidates := parseSnapshotRefCandidates(snapshot)
	structuredValues := extractStructuredInputValues(goal)
	lastRef, _ := lastArgs["ref"].(string)

	if normalizedText == "" && normalizedSnapshot == "" {
		return buildProposal(
			"browser_wait_for_load",
			map[string]any{"waitUntil": "load", "timeoutMs": 30000},
			"The page does not yet have readable text or an accessibility snapshot, so waiting for load is the safest next step.",
			"medium",
			nil,
		)
	}

	if normalizedLastTool == "browser_type_by_ref" || normalizedLastTool == "browser_set_input_value_by_ref" || normalizedLastTool == "browser_type" || normalizedLastTool == "browser_set_input_value" {
		fieldOrder := structuredFieldOrder(goal, structuredValues)
		currentField := resolveStructuredFieldByRef(candidates, lastRef)
		remainingFields := make([]string, 0, len(fieldOrder))
		if currentField != "" {
			include := false
			for _, field := range fieldOrder {
				if include {
					remainingFields = append(remainingFields, field)
				}
				if field == currentField {
					include = true
				}
			}
		} else {
			remainingFields = fieldOrder
		}
		if structured := findStructuredInputCandidate(goal, candidates, structuredValues, strings.TrimSpace(lastRef), remainingFields); structured != nil {
			return buildProposal(
				"browser_type_by_ref",
				map[string]any{"ref": structured.Node.Ref, "text": structured.Value, "clear": true},
				fmt.Sprintf("The goal includes a %s value and the snapshot contains a likely %s field named %q that has not been filled yet.", structured.Field, structured.Node.Role, structured.Node.Name),
				"medium",
				map[string]any{
					"ref":   structured.Node.Ref,
					"role":  structured.Node.Role,
					"name":  structured.Node.Name,
					"field": structured.Field,
				},
			)
		}
		if candidate := findPostInputClickCandidate(goal, candidates); candidate != nil {
			return buildProposal(
				"browser_click_by_ref",
				map[string]any{"ref": candidate.Ref},
				fmt.Sprintf("Text has been entered and the snapshot contains a likely follow-up %s target named %q.", candidate.Role, candidate.Name),
				"medium",
				map[string]any{
					"ref":  candidate.Ref,
					"role": candidate.Role,
					"name": candidate.Name,
				},
			)
		}
	}

	if strings.Contains(normalizedGoal, "extract") || strings.Contains(normalizedGoal, "read") || strings.Contains(normalizedGoal, "summarize") || strings.Contains(normalizedGoal, "inspect") || strings.Contains(normalizedGoal, "analy") {
		return buildProposal(
			"browser_get_text",
			map[string]any{"maxChars": 5000},
			"The goal is information-oriented, so expanding the visible page text is a good next step before making interaction decisions.",
			"high",
			nil,
		)
	}

	if strings.Contains(normalizedGoal, "screenshot") || strings.Contains(normalizedGoal, "capture") || strings.Contains(normalizedGoal, "image") {
		return buildProposal(
			"browser_screenshot",
			map[string]any{"format": "png", "fullPage": true},
			"The goal mentions capture-oriented output, so a screenshot is the most direct next observation.",
			"high",
			nil,
		)
	}

	if len(structuredValues) > 0 || strings.Contains(normalizedGoal, "search") || strings.Contains(normalizedGoal, "input") || strings.Contains(normalizedGoal, "enter") || strings.Contains(normalizedGoal, "fill") || strings.Contains(normalizedGoal, "type ") {
		if structured := findStructuredInputCandidate(goal, candidates, structuredValues, "", structuredFieldOrder(goal, structuredValues)); structured != nil {
			return buildProposal(
				"browser_type_by_ref",
				map[string]any{"ref": structured.Node.Ref, "text": structured.Value, "clear": true},
				fmt.Sprintf("The goal includes a %s value and the snapshot contains a likely %s field named %q.", structured.Field, structured.Node.Role, structured.Node.Name),
				"medium",
				map[string]any{
					"ref":   structured.Node.Ref,
					"role":  structured.Node.Role,
					"name":  structured.Node.Name,
					"field": structured.Field,
				},
			)
		}
		if candidate := findTextboxCandidate(goal, candidates); candidate != nil {
			inputText := extractInputText(goal)
			args := map[string]any{"ref": candidate.Ref}
			reason := fmt.Sprintf("The goal suggests entering text and the snapshot contains a likely %s target named %q.", candidate.Role, candidate.Name)
			if inputText != "" {
				args["text"] = inputText
				args["clear"] = true
				reason = fmt.Sprintf("The goal suggests entering %q and the snapshot contains a likely %s target named %q.", inputText, candidate.Role, candidate.Name)
			}
			return buildProposal(
				"browser_type_by_ref",
				args,
				reason,
				"medium",
				map[string]any{
					"ref":  candidate.Ref,
					"role": candidate.Role,
					"name": candidate.Name,
				},
			)
		}
	}

	if strings.Contains(normalizedGoal, "select") || strings.Contains(normalizedGoal, "choose") || strings.Contains(normalizedGoal, "pick ") {
		if candidate := findSelectCandidate(goal, candidates); candidate != nil {
			label := extractSelectLabel(goal)
			args := map[string]any{"ref": candidate.Ref}
			reason := fmt.Sprintf("The goal suggests choosing an option and the snapshot contains a likely %s target named %q.", candidate.Role, candidate.Name)
			if label != "" {
				args["label"] = label
				reason = fmt.Sprintf("The goal suggests choosing %q and the snapshot contains a likely %s target named %q.", label, candidate.Role, candidate.Name)
			}
			return buildProposal(
				"browser_select_option_by_ref",
				args,
				reason,
				"medium",
				map[string]any{
					"ref":  candidate.Ref,
					"role": candidate.Role,
					"name": candidate.Name,
				},
			)
		}
	}

	if strings.Contains(normalizedGoal, "click") || strings.Contains(normalizedGoal, "open") || strings.Contains(normalizedGoal, "submit") || strings.Contains(normalizedGoal, "login") || strings.Contains(normalizedGoal, "sign in") || strings.Contains(normalizedGoal, "select") {
		if candidate := findClickCandidate(goal, candidates); candidate != nil {
			return buildProposal(
				"browser_click_by_ref",
				map[string]any{"ref": candidate.Ref},
				fmt.Sprintf("The goal suggests an interaction and the snapshot contains a likely %s target named %q.", candidate.Role, candidate.Name),
				"medium",
				map[string]any{
					"ref":  candidate.Ref,
					"role": candidate.Role,
					"name": candidate.Name,
				},
			)
		}
		return buildProposal(
			"browser_aria_snapshot",
			map[string]any{},
			"The goal likely needs interaction, and an accessibility snapshot is the safest way to choose a concrete target before clicking or typing.",
			"medium",
			nil,
		)
	}

	return buildProposal(
		"browser_aria_snapshot",
		map[string]any{},
		"A fresh accessibility snapshot is the best general-purpose next step when the goal does not yet imply a more specific tool.",
		"medium",
		nil,
	)
}

func proposeNextAction(goal string, text string, snapshot string) map[string]any {
	return proposeNextActionFromContext(goal, text, snapshot, "", nil)
}

func (e *Executor) pageRuntimeForAgentLocked(agent *pageAgent) *pageRuntime {
	if agent == nil || e.environments == nil {
		return nil
	}
	env := e.environments[agent.EnvironmentName]
	if env == nil || env.Pages == nil {
		return nil
	}
	return clonePageRuntime(env.Pages[agent.TabID])
}

func (e *Executor) callCreatePageAgent(ctx context.Context, args map[string]any) (map[string]any, error) {
	goal, ok := getStringArg(args, "goal")
	if !ok || strings.TrimSpace(goal) == "" {
		return nil, fmt.Errorf("missing required argument goal")
	}
	goal = strings.TrimSpace(goal)

	if tabID, ok := getStringArg(args, "tabId"); ok && strings.TrimSpace(tabID) != "" {
		if err := e.activateAndConnect(ctx, strings.TrimSpace(tabID)); err != nil {
			return nil, err
		}
	}

	page, err := e.ensureActivePageRuntime(ctx)
	if err != nil {
		return nil, err
	}

	name, _ := getStringArg(args, "name")
	name = strings.TrimSpace(name)

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pageAgents == nil {
		e.pageAgents = make(map[string]*pageAgent)
	}
	agentID := e.nextPageAgentIDLocked()
	if name == "" {
		name = agentID
	}
	now := time.Now().UTC()
	agent := &pageAgent{
		ID:              agentID,
		Name:            name,
		Goal:            goal,
		Status:          "idle",
		EnvironmentName: strings.TrimSpace(e.activeEnvironment),
		TabID:           page.TabID,
		CreatedAt:       now,
		UpdatedAt:       now,
		History:         make([]pageAgentHistoryEntry, 0, 8),
	}
	e.pageAgents[agentID] = agent
	return serializePageAgent(agent, page), nil
}

func (e *Executor) callListPageAgents(ctx context.Context, args map[string]any) (map[string]any, error) {
	_ = ctx
	filterEnv, _ := getStringArg(args, "environment")
	filterEnv = strings.TrimSpace(filterEnv)

	e.mu.Lock()
	defer e.mu.Unlock()
	items := make([]map[string]any, 0, len(e.pageAgents))
	for _, agent := range e.pageAgents {
		if agent == nil {
			continue
		}
		if filterEnv != "" && agent.EnvironmentName != filterEnv {
			continue
		}
		items = append(items, serializePageAgent(agent, e.pageRuntimeForAgentLocked(agent)))
	}
	return map[string]any{
		"pageAgents":        items,
		"activeEnvironment": strings.TrimSpace(e.activeEnvironment),
	}, nil
}

func (e *Executor) callGetPageAgent(ctx context.Context, args map[string]any) (map[string]any, error) {
	_ = ctx
	agentID, ok := getStringArg(args, "agentId")
	if !ok || strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("missing required argument agentId")
	}
	agentID = strings.TrimSpace(agentID)

	e.mu.Lock()
	defer e.mu.Unlock()
	agent := e.pageAgents[agentID]
	if agent == nil {
		return nil, fmt.Errorf("page agent %q not found", agentID)
	}
	return serializePageAgent(agent, e.pageRuntimeForAgentLocked(agent)), nil
}

func (e *Executor) callRemovePageAgent(ctx context.Context, args map[string]any) (map[string]any, error) {
	_ = ctx
	agentID, ok := getStringArg(args, "agentId")
	if !ok || strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("missing required argument agentId")
	}
	agentID = strings.TrimSpace(agentID)

	e.mu.Lock()
	defer e.mu.Unlock()
	agent := e.pageAgents[agentID]
	if agent == nil {
		return nil, fmt.Errorf("page agent %q not found", agentID)
	}
	delete(e.pageAgents, agentID)
	return map[string]any{
		"ok":      true,
		"removed": agentID,
	}, nil
}

func (e *Executor) runWithPageAgentBinding(ctx context.Context, agent *pageAgent, fn func(*pageAgent) (map[string]any, error)) (map[string]any, error) {
	if agent == nil {
		return nil, fmt.Errorf("nil page agent")
	}

	e.mu.Lock()
	originalEnv := strings.TrimSpace(e.activeEnvironment)
	e.snapshotActiveEnvironmentLocked()
	if err := e.loadEnvironmentLocked(agent.EnvironmentName); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.snapshotActiveEnvironmentLocked()
		if originalEnv != "" {
			_ = e.loadEnvironmentLocked(originalEnv)
		}
	}()

	if e.browserClient == nil {
		return fn(agent)
	}
	if err := e.activateAndConnect(ctx, agent.TabID); err != nil {
		return nil, err
	}
	return fn(agent)
}

func (e *Executor) callRunPageAgentStep(ctx context.Context, args map[string]any) (map[string]any, error) {
	agentID, ok := getStringArg(args, "agentId")
	if !ok || strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("missing required argument agentId")
	}
	agentID = strings.TrimSpace(agentID)
	maxChars := getIntArgDefault(args, "maxChars", 2000)
	if maxChars <= 0 {
		maxChars = 2000
	}

	e.mu.Lock()
	agent := e.pageAgents[agentID]
	if agent == nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("page agent %q not found", agentID)
	}
	agent.Status = "running"
	startedAt := time.Now().UTC()
	agent.UpdatedAt = startedAt
	e.mu.Unlock()

	result, err := e.runWithPageAgentBinding(ctx, agent, func(bound *pageAgent) (map[string]any, error) {
		textResult, err := e.callGetText(ctx, map[string]any{"maxChars": maxChars})
		if err != nil {
			return nil, err
		}
		snapshotResult, err := e.callAriaSnapshot(ctx, map[string]any{})
		if err != nil {
			return nil, err
		}
		text, _ := textResult["text"].(string)
		snapshot, _ := snapshotResult["snapshot"].(string)
		proposal, aiErr := e.generateAIProposal(ctx, bound.Goal, text, snapshot, "")
		proposalSource := "ai"
		if aiErr != nil {
			proposal = proposeNextAction(bound.Goal, text, snapshot)
			proposalSource = "rules"
		}
		stepResult := map[string]any{
			"ok":                 true,
			"goal":               bound.Goal,
			"text":               text,
			"snapshot":           snapshot,
			"tabId":              bound.TabID,
			"proposalSource":     proposalSource,
			"nextActionProposal": proposal,
		}

		e.mu.Lock()
		defer e.mu.Unlock()
		current := e.pageAgents[bound.ID]
		if current == nil {
			return nil, fmt.Errorf("page agent %q disappeared during step", bound.ID)
		}
		now := time.Now().UTC()
		step := len(current.History) + 1
		current.Status = "idle"
		current.LastText = text
		current.LastSnapshot = snapshot
		current.LastTool = ""
		current.LastToolArgs = nil
		current.LastResult = stepResult
		current.LastProposal = proposal
		current.LastProposalSource = proposalSource
		current.LastRunAt = now
		current.UpdatedAt = now
		e.updatePageRuntimeObservationLocked(current, text, snapshot, proposal, proposalSource)
		current.History = append(current.History, pageAgentHistoryEntry{
			Step:        step,
			Status:      "ok",
			Summary:     summarizeStepResult(stepResult),
			Result:      stepResult,
			StartedAt:   startedAt,
			CompletedAt: now,
		})
		return map[string]any{
			"agent":      serializePageAgent(current, e.pageRuntimeForAgentLocked(current)),
			"stepResult": stepResult,
		}, nil
	})
	if err != nil {
		e.mu.Lock()
		if current := e.pageAgents[agentID]; current != nil {
			now := time.Now().UTC()
			step := len(current.History) + 1
			current.Status = "error"
			current.LastResult = map[string]any{"ok": false, "error": err.Error()}
			current.LastProposalSource = ""
			current.LastRunAt = now
			current.UpdatedAt = now
			current.History = append(current.History, pageAgentHistoryEntry{
				Step:        step,
				Status:      "error",
				Summary:     err.Error(),
				Result:      current.LastResult,
				StartedAt:   startedAt,
				CompletedAt: now,
			})
		}
		e.mu.Unlock()
		return nil, err
	}
	return result, nil
}

func proposalToolAndArgs(proposal map[string]any) (string, map[string]any, error) {
	if len(proposal) == 0 {
		return "", nil, fmt.Errorf("empty proposal")
	}
	toolName, _ := proposal["tool"].(string)
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return "", nil, fmt.Errorf("proposal missing tool")
	}
	argsRaw, _ := proposal["arguments"].(map[string]any)
	if argsRaw == nil {
		argsRaw = map[string]any{}
	}
	args := make(map[string]any, len(argsRaw))
	for k, v := range argsRaw {
		args[k] = v
	}
	return toolName, args, nil
}

func summarizeApplyResult(toolName string, result map[string]any) string {
	if len(result) == 0 {
		return fmt.Sprintf("applied %s", toolName)
	}
	if verification, _ := result["verification"].(map[string]any); verification != nil {
		if ok, _ := verification["ok"].(bool); !ok {
			return fmt.Sprintf("applied %s but verification failed", toolName)
		}
	}
	if ok, _ := result["ok"].(bool); ok {
		return fmt.Sprintf("applied %s successfully", toolName)
	}
	return fmt.Sprintf("applied %s", toolName)
}

func (e *Executor) callApplyPageAgentProposal(ctx context.Context, args map[string]any) (map[string]any, error) {
	agentID, ok := getStringArg(args, "agentId")
	if !ok || strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("missing required argument agentId")
	}
	agentID = strings.TrimSpace(agentID)

	e.mu.Lock()
	agent := e.pageAgents[agentID]
	if agent == nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("page agent %q not found", agentID)
	}
	proposal := agent.LastProposal
	if len(proposal) == 0 {
		e.mu.Unlock()
		return nil, fmt.Errorf("page agent %q has no proposal to apply", agentID)
	}
	agent.Status = "running"
	startedAt := time.Now().UTC()
	agent.UpdatedAt = startedAt
	e.mu.Unlock()

	result, err := e.runWithPageAgentBinding(ctx, agent, func(bound *pageAgent) (map[string]any, error) {
		toolName, toolArgs, err := proposalToolAndArgs(proposal)
		if err != nil {
			return nil, err
		}
		if _, exists := toolArgs["tabId"]; !exists && strings.TrimSpace(bound.TabID) != "" {
			toolArgs["tabId"] = bound.TabID
		}

		toolResult, err := e.Call(ctx, toolName, toolArgs)
		if err != nil {
			return nil, err
		}

		applyResult := map[string]any{
			"ok":                true,
			"goal":              bound.Goal,
			"tool":              toolName,
			"arguments":         toolArgs,
			"proposal":          proposal,
			"result":            toolResult,
			"tabId":             bound.TabID,
			"preActionText":     bound.LastText,
			"preActionSnapshot": bound.LastSnapshot,
		}

		var nextProposal map[string]any
		nextProposalSource := ""
		if e.browserClient != nil {
			textResult, textErr := e.callGetText(ctx, map[string]any{"maxChars": 2000})
			snapshotResult, snapshotErr := e.callAriaSnapshot(ctx, map[string]any{})
			if textErr == nil && snapshotErr == nil {
				text, _ := textResult["text"].(string)
				snapshot, _ := snapshotResult["snapshot"].(string)
				aiProposal, aiErr := e.generateAIProposal(ctx, bound.Goal, text, snapshot, toolName)
				nextProposalSource = "ai"
				if aiErr == nil {
					nextProposal = aiProposal
				} else {
					nextProposal = proposeNextActionFromContext(bound.Goal, text, snapshot, toolName, toolArgs)
					nextProposalSource = "rules"
				}
				applyResult["nextActionProposal"] = nextProposal
				applyResult["nextActionProposalSource"] = nextProposalSource
				applyResult["postActionText"] = text
				applyResult["postActionSnapshot"] = snapshot
			}
		}
		applyResult["verification"] = verificationResultForApply(proposal, applyResult)

		e.mu.Lock()
		defer e.mu.Unlock()
		current := e.pageAgents[bound.ID]
		if current == nil {
			return nil, fmt.Errorf("page agent %q disappeared during proposal application", bound.ID)
		}
		now := time.Now().UTC()
		step := len(current.History) + 1
		historyStatus := "applied"
		if verification, _ := applyResult["verification"].(map[string]any); verification != nil {
			if ok, _ := verification["ok"].(bool); !ok {
				historyStatus = "verification_failed"
			}
		}
		current.Status = "idle"
		current.LastTool = toolName
		current.LastToolArgs = cloneMap(toolArgs)
		current.LastResult = applyResult
		if len(nextProposal) > 0 {
			current.LastProposal = nextProposal
			current.LastProposalSource = nextProposalSource
		} else {
			current.LastProposal = nil
			current.LastProposalSource = ""
		}
		current.LastRunAt = now
		current.UpdatedAt = now
		e.updatePageRuntimeActionLocked(current, toolName, toolArgs, nextProposal, nextProposalSource)
		if text, _ := applyResult["postActionText"].(string); text != "" {
			current.LastText = text
			if snapshot, _ := applyResult["postActionSnapshot"].(string); snapshot != "" {
				current.LastSnapshot = snapshot
				e.updatePageRuntimeObservationLocked(current, text, snapshot, nextProposal, nextProposalSource)
			}
		} else if snapshot, _ := applyResult["postActionSnapshot"].(string); snapshot != "" {
			current.LastSnapshot = snapshot
			e.updatePageRuntimeObservationLocked(current, current.LastText, snapshot, nextProposal, nextProposalSource)
		}
		current.History = append(current.History, pageAgentHistoryEntry{
			Step:        step,
			Status:      historyStatus,
			Summary:     summarizeApplyResult(toolName, applyResult),
			Result:      applyResult,
			StartedAt:   startedAt,
			CompletedAt: now,
		})
		return map[string]any{
			"agent":       serializePageAgent(current, e.pageRuntimeForAgentLocked(current)),
			"applyResult": applyResult,
		}, nil
	})
	if err != nil {
		e.mu.Lock()
		if current := e.pageAgents[agentID]; current != nil {
			now := time.Now().UTC()
			step := len(current.History) + 1
			current.Status = "error"
			current.LastTool = ""
			current.LastToolArgs = nil
			current.LastResult = map[string]any{"ok": false, "error": err.Error(), "proposal": proposal}
			current.LastProposalSource = ""
			current.LastRunAt = now
			current.UpdatedAt = now
			current.History = append(current.History, pageAgentHistoryEntry{
				Step:        step,
				Status:      "error",
				Summary:     err.Error(),
				Result:      current.LastResult,
				StartedAt:   startedAt,
				CompletedAt: now,
			})
		}
		e.mu.Unlock()
		return nil, err
	}
	return result, nil
}

func (e *Executor) callRunPageAgentLoop(ctx context.Context, args map[string]any) (map[string]any, error) {
	agentID, ok := getStringArg(args, "agentId")
	if !ok || strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("missing required argument agentId")
	}
	agentID = strings.TrimSpace(agentID)

	maxSteps := getIntArgDefault(args, "maxSteps", 3)
	if maxSteps < 1 {
		maxSteps = 1
	}
	if maxSteps > 20 {
		maxSteps = 20
	}

	maxChars := getIntArgDefault(args, "maxChars", 2000)
	if maxChars <= 0 {
		maxChars = 2000
	}
	maxErrors := getIntArgDefault(args, "maxErrors", 1)
	if maxErrors < 1 {
		maxErrors = 1
	}
	requireAI := false
	if v, ok, err := getBoolArg(args, "requireAI"); err == nil && ok {
		requireAI = v
	} else if err != nil {
		return nil, err
	}
	stopOnRepeatedProposal := false
	if v, ok, err := getBoolArg(args, "stopOnRepeatedProposal"); err == nil && ok {
		stopOnRepeatedProposal = v
	} else if err != nil {
		return nil, err
	}
	stopOnNoPageChange := false
	if v, ok, err := getBoolArg(args, "stopOnNoPageChange"); err == nil && ok {
		stopOnNoPageChange = v
	} else if err != nil {
		return nil, err
	}
	stopWhenText, _ := getStringArg(args, "stopWhenText")
	stopWhenText = strings.TrimSpace(stopWhenText)
	stopOnTool, _ := getStringArg(args, "stopOnTool")
	stopOnTool = strings.TrimSpace(stopOnTool)

	steps := make([]map[string]any, 0, maxSteps)
	stopReason := "max_steps_reached"
	errorCount := 0
	lastProposalSig := ""
	lastObservationSig := ""

	for i := 0; i < maxSteps; i++ {
		stepResult, err := e.callRunPageAgentStep(ctx, map[string]any{
			"agentId":  agentID,
			"maxChars": maxChars,
		})
		if err != nil {
			errorCount++
			steps = append(steps, map[string]any{
				"phase": "step_error",
				"data": map[string]any{
					"ok":    false,
					"error": err.Error(),
				},
			})
			if errorCount >= maxErrors {
				stopReason = "max_errors_reached"
				break
			}
			continue
		}
		stepPayload := map[string]any{
			"phase": "step",
			"data":  stepResult,
		}
		steps = append(steps, stepPayload)

		stepBody, _ := stepResult["stepResult"].(map[string]any)
		if requireAI {
			if source, _ := stepBody["proposalSource"].(string); source != "ai" {
				stopReason = "ai_required_but_unavailable"
				break
			}
		}
		if stopWhenText != "" {
			text, _ := stepBody["text"].(string)
			if strings.Contains(text, stopWhenText) {
				stopReason = "stop_when_text_matched"
				break
			}
		}
		proposal, _ := stepBody["nextActionProposal"].(map[string]any)
		if len(proposal) == 0 {
			stopReason = "no_proposal"
			break
		}
		currentProposalSig := proposalSignature(proposal)
		if stopOnRepeatedProposal && currentProposalSig != "" && currentProposalSig == lastProposalSig {
			stopReason = "stop_on_repeated_proposal"
			break
		}

		applyResult, err := e.callApplyPageAgentProposal(ctx, map[string]any{
			"agentId": agentID,
		})
		if err != nil {
			errorCount++
			steps = append(steps, map[string]any{
				"phase": "apply_error",
				"data": map[string]any{
					"ok":    false,
					"error": err.Error(),
				},
			})
			if errorCount >= maxErrors {
				stopReason = "max_errors_reached"
				break
			}
			continue
		}
		steps = append(steps, map[string]any{
			"phase": "apply",
			"data":  applyResult,
		})

		applyBody, _ := applyResult["applyResult"].(map[string]any)
		if stopOnTool != "" {
			if tool, _ := applyBody["tool"].(string); tool == stopOnTool {
				stopReason = "stop_on_tool_matched"
				break
			}
		}
		if verification, _ := applyBody["verification"].(map[string]any); verification != nil {
			if ok, _ := verification["ok"].(bool); !ok {
				stopReason = "verification_failed"
				break
			}
		}
		if stopWhenText != "" {
			if text, _ := applyBody["postActionText"].(string); strings.Contains(text, stopWhenText) {
				stopReason = "stop_when_text_matched"
				break
			}
		}
		if requireAI {
			if src, _ := applyBody["nextActionProposalSource"].(string); src != "" && src != "ai" {
				stopReason = "ai_required_but_unavailable"
				break
			}
		}
		nextProposal, _ := applyBody["nextActionProposal"].(map[string]any)
		if len(nextProposal) == 0 {
			stopReason = "no_followup_proposal"
			break
		}
		if stopOnRepeatedProposal {
			nextProposalSig := proposalSignature(nextProposal)
			if nextProposalSig != "" && nextProposalSig == currentProposalSig {
				stopReason = "stop_on_repeated_proposal"
				break
			}
			lastProposalSig = currentProposalSig
		}
		if stopOnNoPageChange {
			beforeSig := observationSignature(
				mapStringValue(stepBody, "text"),
				mapStringValue(stepBody, "snapshot"),
			)
			afterSig := observationSignature(
				mapStringValue(applyBody, "postActionText"),
				mapStringValue(applyBody, "postActionSnapshot"),
			)
			if beforeSig != "" && afterSig != "" && beforeSig == afterSig {
				stopReason = "stop_on_no_page_change"
				break
			}
			if afterSig != "" && afterSig == lastObservationSig {
				stopReason = "stop_on_no_page_change"
				break
			}
			lastObservationSig = afterSig
		}
	}

	agentResult, err := e.callGetPageAgent(ctx, map[string]any{"agentId": agentID})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ok":                     true,
		"agentId":                agentID,
		"maxSteps":               maxSteps,
		"maxErrors":              maxErrors,
		"errorCount":             errorCount,
		"requireAI":              requireAI,
		"stopOnRepeatedProposal": stopOnRepeatedProposal,
		"stopOnNoPageChange":     stopOnNoPageChange,
		"stopWhenText":           stopWhenText,
		"stopOnTool":             stopOnTool,
		"stopReason":             stopReason,
		"steps":                  steps,
		"agent":                  agentResult,
	}, nil
}
