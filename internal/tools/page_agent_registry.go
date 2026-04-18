package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var pageAgentSnapshotRefPattern = regexp.MustCompile(`- ([^"]+?) "([^"]*)" \[ref=(e[0-9]+)\]`)
var quotedValuePattern = regexp.MustCompile(`"([^"]+)"|'([^']+)'`)

type pageAgent struct {
	ID              string
	Name            string
	Goal            string
	Status          string
	EnvironmentName string
	TabID           string
	LastText        string
	LastSnapshot    string
	LastResult      map[string]any
	LastProposal    map[string]any
	LastRunAt       time.Time
	History         []pageAgentHistoryEntry
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
		"agentId":       agent.ID,
		"name":          agent.Name,
		"goal":          agent.Goal,
		"status":        agent.Status,
		"environment":   agent.EnvironmentName,
		"tabId":         agent.TabID,
		"pageConnected": connected,
		"pageRefCount":  refCount,
		"lastText":      agent.LastText,
		"lastSnapshot":  agent.LastSnapshot,
		"lastResult":    agent.LastResult,
		"lastProposal":  agent.LastProposal,
		"lastRunAt":     formatAgentTime(agent.LastRunAt),
		"history":       serializePageAgentHistory(agent.History),
		"historyCount":  len(agent.History),
		"createdAt":     agent.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     agent.UpdatedAt.UTC().Format(time.RFC3339),
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

type snapshotRefCandidate struct {
	Role string
	Name string
	Ref  string
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

func proposeNextActionFromContext(goal string, text string, snapshot string, lastTool string) map[string]any {
	normalizedGoal := strings.ToLower(strings.TrimSpace(goal))
	normalizedText := strings.TrimSpace(text)
	normalizedSnapshot := strings.TrimSpace(snapshot)
	normalizedLastTool := strings.TrimSpace(lastTool)
	candidates := parseSnapshotRefCandidates(snapshot)

	if normalizedText == "" && normalizedSnapshot == "" {
		return map[string]any{
			"type":       "tool",
			"tool":       "browser_wait_for_load",
			"arguments":  map[string]any{"waitUntil": "load", "timeoutMs": 30000},
			"reason":     "The page does not yet have readable text or an accessibility snapshot, so waiting for load is the safest next step.",
			"confidence": "medium",
		}
	}

	if normalizedLastTool == "browser_type_by_ref" || normalizedLastTool == "browser_set_input_value_by_ref" || normalizedLastTool == "browser_type" || normalizedLastTool == "browser_set_input_value" {
		if candidate := findPostInputClickCandidate(goal, candidates); candidate != nil {
			return map[string]any{
				"type":       "tool",
				"tool":       "browser_click_by_ref",
				"arguments":  map[string]any{"ref": candidate.Ref},
				"reason":     fmt.Sprintf("Text has been entered and the snapshot contains a likely follow-up %s target named %q.", candidate.Role, candidate.Name),
				"confidence": "medium",
				"target": map[string]any{
					"ref":  candidate.Ref,
					"role": candidate.Role,
					"name": candidate.Name,
				},
			}
		}
	}

	if strings.Contains(normalizedGoal, "extract") || strings.Contains(normalizedGoal, "read") || strings.Contains(normalizedGoal, "summarize") || strings.Contains(normalizedGoal, "inspect") || strings.Contains(normalizedGoal, "analy") {
		return map[string]any{
			"type":       "tool",
			"tool":       "browser_get_text",
			"arguments":  map[string]any{"maxChars": 5000},
			"reason":     "The goal is information-oriented, so expanding the visible page text is a good next step before making interaction decisions.",
			"confidence": "high",
		}
	}

	if strings.Contains(normalizedGoal, "screenshot") || strings.Contains(normalizedGoal, "capture") || strings.Contains(normalizedGoal, "image") {
		return map[string]any{
			"type":       "tool",
			"tool":       "browser_screenshot",
			"arguments":  map[string]any{"format": "png", "fullPage": true},
			"reason":     "The goal mentions capture-oriented output, so a screenshot is the most direct next observation.",
			"confidence": "high",
		}
	}

	if strings.Contains(normalizedGoal, "search") || strings.Contains(normalizedGoal, "input") || strings.Contains(normalizedGoal, "enter") || strings.Contains(normalizedGoal, "fill") || strings.Contains(normalizedGoal, "type ") {
		if candidate := findTextboxCandidate(goal, candidates); candidate != nil {
			inputText := extractInputText(goal)
			args := map[string]any{"ref": candidate.Ref}
			reason := fmt.Sprintf("The goal suggests entering text and the snapshot contains a likely %s target named %q.", candidate.Role, candidate.Name)
			if inputText != "" {
				args["text"] = inputText
				args["clear"] = true
				reason = fmt.Sprintf("The goal suggests entering %q and the snapshot contains a likely %s target named %q.", inputText, candidate.Role, candidate.Name)
			}
			return map[string]any{
				"type":       "tool",
				"tool":       "browser_type_by_ref",
				"arguments":  args,
				"reason":     reason,
				"confidence": "medium",
				"target": map[string]any{
					"ref":  candidate.Ref,
					"role": candidate.Role,
					"name": candidate.Name,
				},
			}
		}
	}

	if strings.Contains(normalizedGoal, "click") || strings.Contains(normalizedGoal, "open") || strings.Contains(normalizedGoal, "submit") || strings.Contains(normalizedGoal, "login") || strings.Contains(normalizedGoal, "sign in") || strings.Contains(normalizedGoal, "select") {
		if candidate := findClickCandidate(goal, candidates); candidate != nil {
			return map[string]any{
				"type":       "tool",
				"tool":       "browser_click_by_ref",
				"arguments":  map[string]any{"ref": candidate.Ref},
				"reason":     fmt.Sprintf("The goal suggests an interaction and the snapshot contains a likely %s target named %q.", candidate.Role, candidate.Name),
				"confidence": "medium",
				"target": map[string]any{
					"ref":  candidate.Ref,
					"role": candidate.Role,
					"name": candidate.Name,
				},
			}
		}
		return map[string]any{
			"type":       "tool",
			"tool":       "browser_aria_snapshot",
			"arguments":  map[string]any{},
			"reason":     "The goal likely needs interaction, and an accessibility snapshot is the safest way to choose a concrete target before clicking or typing.",
			"confidence": "medium",
		}
	}

	return map[string]any{
		"type":       "tool",
		"tool":       "browser_aria_snapshot",
		"arguments":  map[string]any{},
		"reason":     "A fresh accessibility snapshot is the best general-purpose next step when the goal does not yet imply a more specific tool.",
		"confidence": "medium",
	}
}

func proposeNextAction(goal string, text string, snapshot string) map[string]any {
	return proposeNextActionFromContext(goal, text, snapshot, "")
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
		current.LastResult = stepResult
		current.LastProposal = proposal
		current.LastRunAt = now
		current.UpdatedAt = now
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
			"ok":        true,
			"tool":      toolName,
			"arguments": toolArgs,
			"proposal":  proposal,
			"result":    toolResult,
			"tabId":     bound.TabID,
		}

		var nextProposal map[string]any
		if e.browserClient != nil {
			textResult, textErr := e.callGetText(ctx, map[string]any{"maxChars": 2000})
			snapshotResult, snapshotErr := e.callAriaSnapshot(ctx, map[string]any{})
			if textErr == nil && snapshotErr == nil {
				text, _ := textResult["text"].(string)
				snapshot, _ := snapshotResult["snapshot"].(string)
				aiProposal, aiErr := e.generateAIProposal(ctx, bound.Goal, text, snapshot, toolName)
				nextProposalSource := "ai"
				if aiErr == nil {
					nextProposal = aiProposal
				} else {
					nextProposal = proposeNextActionFromContext(bound.Goal, text, snapshot, toolName)
					nextProposalSource = "rules"
				}
				applyResult["nextActionProposal"] = nextProposal
				applyResult["nextActionProposalSource"] = nextProposalSource
				applyResult["postActionText"] = text
				applyResult["postActionSnapshot"] = snapshot
			}
		}

		e.mu.Lock()
		defer e.mu.Unlock()
		current := e.pageAgents[bound.ID]
		if current == nil {
			return nil, fmt.Errorf("page agent %q disappeared during proposal application", bound.ID)
		}
		now := time.Now().UTC()
		step := len(current.History) + 1
		current.Status = "idle"
		current.LastResult = applyResult
		if len(nextProposal) > 0 {
			current.LastProposal = nextProposal
		}
		current.LastRunAt = now
		current.UpdatedAt = now
		current.History = append(current.History, pageAgentHistoryEntry{
			Step:        step,
			Status:      "applied",
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
			current.LastResult = map[string]any{"ok": false, "error": err.Error(), "proposal": proposal}
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
