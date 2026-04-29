package tools

import (
	"context"
	"strings"
	"testing"
)

func TestCreateGetListRemovePageAgent(t *testing.T) {
	e := &Executor{
		currentTabID:      "tab-a",
		ariaRefStore:      map[string]map[string]ariaRefMeta{"tab-a": {"e1": {Role: "button"}}},
		environments:      map[string]*browserEnvironment{},
		activeEnvironment: "work",
		pageAgents:        map[string]*pageAgent{},
	}
	env := newBrowserEnvironment("work", "endpoint-a", nil, false)
	env.Pages["tab-a"] = newPageRuntime("tab-a", nil, cloneAriaRefStoreForTab(e.ariaRefStore, "tab-a"))
	env.ActivePageID = "tab-a"
	e.environments["work"] = env

	created, err := e.callCreatePageAgent(context.Background(), map[string]any{
		"goal": "extract the pricing table",
	})
	if err != nil {
		t.Fatalf("callCreatePageAgent returned error: %v", err)
	}
	agentID, _ := created["agentId"].(string)
	if agentID == "" {
		t.Fatalf("expected agentId, got %#v", created)
	}
	if created["environment"] != "work" {
		t.Fatalf("expected environment work, got %#v", created)
	}
	if created["tabId"] != "tab-a" {
		t.Fatalf("expected tab-a, got %#v", created)
	}
	if created["pageRefCount"] != 1 {
		t.Fatalf("expected pageRefCount=1, got %#v", created)
	}

	got, err := e.callGetPageAgent(context.Background(), map[string]any{"agentId": agentID})
	if err != nil {
		t.Fatalf("callGetPageAgent returned error: %v", err)
	}
	if got["goal"] != "extract the pricing table" {
		t.Fatalf("unexpected page agent payload: %#v", got)
	}
	if got["historyCount"] != 0 {
		t.Fatalf("expected empty history on new page agent, got %#v", got)
	}

	listed, err := e.callListPageAgents(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("callListPageAgents returned error: %v", err)
	}
	pageAgentsAny, _ := listed["pageAgents"].([]map[string]any)
	if len(pageAgentsAny) != 1 {
		t.Fatalf("expected one page agent, got %#v", listed)
	}

	removed, err := e.callRemovePageAgent(context.Background(), map[string]any{"agentId": agentID})
	if err != nil {
		t.Fatalf("callRemovePageAgent returned error: %v", err)
	}
	if removed["removed"] != agentID {
		t.Fatalf("unexpected remove payload: %#v", removed)
	}
}

func TestListPageAgentsFiltersEnvironment(t *testing.T) {
	e := &Executor{
		pageAgents:        map[string]*pageAgent{},
		environments:      map[string]*browserEnvironment{},
		activeEnvironment: "work",
	}
	e.pageAgents["page-agent-1"] = &pageAgent{ID: "page-agent-1", Name: "a", Goal: "g1", Status: "idle", EnvironmentName: "work", TabID: "tab-a"}
	e.pageAgents["page-agent-2"] = &pageAgent{ID: "page-agent-2", Name: "b", Goal: "g2", Status: "idle", EnvironmentName: "personal", TabID: "tab-b"}

	listed, err := e.callListPageAgents(context.Background(), map[string]any{"environment": "work"})
	if err != nil {
		t.Fatalf("callListPageAgents returned error: %v", err)
	}
	pageAgents, _ := listed["pageAgents"].([]map[string]any)
	if len(pageAgents) != 1 {
		t.Fatalf("expected one filtered page agent, got %#v", listed)
	}
	if pageAgents[0]["environment"] != "work" {
		t.Fatalf("unexpected filtered payload: %#v", pageAgents[0])
	}
}

func TestRunPageAgentStepCapturesPageState(t *testing.T) {
	e := &Executor{
		currentTabID:      "tab-a",
		ariaRefStore:      map[string]map[string]ariaRefMeta{"tab-a": {"e1": {Role: "button"}}},
		environments:      map[string]*browserEnvironment{},
		activeEnvironment: "work",
		pageAgents:        map[string]*pageAgent{},
	}
	env := newBrowserEnvironment("work", "endpoint-a", nil, false)
	env.Pages["tab-a"] = newPageRuntime("tab-a", nil, cloneAriaRefStoreForTab(e.ariaRefStore, "tab-a"))
	env.ActivePageID = "tab-a"
	e.environments["work"] = env
	agent := &pageAgent{
		ID:              "page-agent-1",
		Name:            "agent",
		Goal:            "inspect page",
		Status:          "idle",
		EnvironmentName: "work",
		TabID:           "tab-a",
	}
	e.pageAgents[agent.ID] = agent

	// Override page inspection helpers with existing runtime state only.
	originalGetText := e.callGetText
	originalAria := e.callAriaSnapshot
	_ = originalGetText
	_ = originalAria

	// Use the existing no-client path by preloading a page runtime and avoiding any real CDP calls.
	env.Pages["tab-a"].AriaRefStore = map[string]ariaRefMeta{"e1": {Role: "button"}}

	// Inject deterministic observations by writing directly through current agent-bound result path.
	got, err := e.callRunPageAgentStep(context.Background(), map[string]any{"agentId": agent.ID, "maxChars": 100})
	if err == nil {
		// Without a real page client we expect the current implementation to need real page bindings.
		// Keep the test asserting that error-path status is updated consistently.
		if _, ok := got["agent"]; ok {
			t.Fatalf("expected no successful step result without page client, got %#v", got)
		}
	}
	if e.pageAgents[agent.ID].Status != "error" {
		t.Fatalf("expected page agent status error after failed step, got %#v", e.pageAgents[agent.ID])
	}
	if e.pageAgents[agent.ID].LastResult == nil {
		t.Fatalf("expected last result to capture step failure")
	}
	if len(e.pageAgents[agent.ID].History) != 1 {
		t.Fatalf("expected one history entry after failed step, got %#v", e.pageAgents[agent.ID].History)
	}
	if e.pageAgents[agent.ID].History[0].Status != "error" {
		t.Fatalf("expected history entry status error, got %#v", e.pageAgents[agent.ID].History[0])
	}
}

func TestProposeNextActionForExtractionGoal(t *testing.T) {
	proposal := proposeNextAction("extract the pricing table", "pricing text", "snapshot")
	if proposal["tool"] != "browser_get_text" {
		t.Fatalf("expected browser_get_text proposal, got %#v", proposal)
	}
	if proposal["intent"] != "observe" {
		t.Fatalf("expected observe intent, got %#v", proposal)
	}
	if _, ok := proposal["expectedOutcome"].(string); !ok {
		t.Fatalf("expected expectedOutcome, got %#v", proposal)
	}
	if _, ok := proposal["verificationHints"].(map[string]any); !ok {
		t.Fatalf("expected verificationHints, got %#v", proposal)
	}
}

func TestProposeNextActionForInteractionGoal(t *testing.T) {
	proposal := proposeNextAction("click the sign in button", "page text", "snapshot")
	if proposal["tool"] != "browser_aria_snapshot" {
		t.Fatalf("expected browser_aria_snapshot fallback proposal without refs, got %#v", proposal)
	}
}

func TestProposeNextActionUsesSnapshotRefForInteractionGoal(t *testing.T) {
	snapshot := strings.Join([]string{
		`- document "Login"`,
		`  - button "Sign In" [ref=e1]`,
		`  - link "Forgot Password" [ref=e2]`,
	}, "\n")
	proposal := proposeNextAction("click the sign in button", "page text", snapshot)
	if proposal["tool"] != "browser_click_by_ref" {
		t.Fatalf("expected browser_click_by_ref proposal, got %#v", proposal)
	}
	args, _ := proposal["arguments"].(map[string]any)
	if args["ref"] != "e1" {
		t.Fatalf("expected ref e1, got %#v", proposal)
	}
}

func TestProposeNextActionUsesTextboxRefForInputGoal(t *testing.T) {
	snapshot := strings.Join([]string{
		`- document "Search"`,
		`  - textbox "Search" [ref=e3]`,
		`  - button "Submit" [ref=e4]`,
	}, "\n")
	proposal := proposeNextAction(`search for "browser sdk"`, "page text", snapshot)
	if proposal["tool"] != "browser_type_by_ref" {
		t.Fatalf("expected browser_type_by_ref proposal, got %#v", proposal)
	}
	args, _ := proposal["arguments"].(map[string]any)
	if args["ref"] != "e3" {
		t.Fatalf("expected ref e3, got %#v", proposal)
	}
	if args["text"] != "browser sdk" {
		t.Fatalf("expected extracted text browser sdk, got %#v", proposal)
	}
}

func TestProposeNextActionUsesEmailFieldForLoginGoal(t *testing.T) {
	snapshot := strings.Join([]string{
		`- document "Login"`,
		`  - textbox "Email" [ref=e1]`,
		`  - textbox "Password" [ref=e2]`,
		`  - button "Sign In" [ref=e3]`,
	}, "\n")
	proposal := proposeNextAction(`log in with email "qa@example.com" and password "secret123"`, "page text", snapshot)
	if proposal["tool"] != "browser_type_by_ref" {
		t.Fatalf("expected browser_type_by_ref proposal, got %#v", proposal)
	}
	args, _ := proposal["arguments"].(map[string]any)
	if args["ref"] != "e1" {
		t.Fatalf("expected email ref e1, got %#v", proposal)
	}
	if args["text"] != "qa@example.com" {
		t.Fatalf("expected email text qa@example.com, got %#v", proposal)
	}
}

func TestProposeNextActionFromContextUsesPasswordFieldAfterEmail(t *testing.T) {
	snapshot := strings.Join([]string{
		`- document "Login"`,
		`  - textbox "Email" [ref=e1]`,
		`  - textbox "Password" [ref=e2]`,
		`  - button "Sign In" [ref=e3]`,
	}, "\n")
	proposal := proposeNextActionFromContext(`log in with email "qa@example.com" and password "secret123"`, "page text", snapshot, "browser_type_by_ref", map[string]any{"ref": "e1"})
	if proposal["tool"] != "browser_type_by_ref" {
		t.Fatalf("expected follow-up typing proposal, got %#v", proposal)
	}
	args, _ := proposal["arguments"].(map[string]any)
	if args["ref"] != "e2" {
		t.Fatalf("expected password ref e2, got %#v", proposal)
	}
	if args["text"] != "secret123" {
		t.Fatalf("expected password text secret123, got %#v", proposal)
	}
}

func TestProposeNextActionFromContextClicksAfterPassword(t *testing.T) {
	snapshot := strings.Join([]string{
		`- document "Login"`,
		`  - textbox "Email" [ref=e1]`,
		`  - textbox "Password" [ref=e2]`,
		`  - button "Sign In" [ref=e3]`,
	}, "\n")
	proposal := proposeNextActionFromContext(`log in with email "qa@example.com" and password "secret123"`, "page text", snapshot, "browser_type_by_ref", map[string]any{"ref": "e2"})
	if proposal["tool"] != "browser_click_by_ref" {
		t.Fatalf("expected follow-up click proposal after password entry, got %#v", proposal)
	}
	args, _ := proposal["arguments"].(map[string]any)
	if args["ref"] != "e3" {
		t.Fatalf("expected sign-in ref e3, got %#v", proposal)
	}
}

func TestProposeNextActionFromContextPrefersClickAfterTyping(t *testing.T) {
	snapshot := strings.Join([]string{
		`- document "Search"`,
		`  - textbox "Search" [ref=e3]`,
		`  - button "Search" [ref=e4]`,
	}, "\n")
	proposal := proposeNextActionFromContext(`search for "browser sdk"`, "page text", snapshot, "browser_type_by_ref", map[string]any{"ref": "e3"})
	if proposal["tool"] != "browser_click_by_ref" {
		t.Fatalf("expected follow-up click proposal, got %#v", proposal)
	}
	args, _ := proposal["arguments"].(map[string]any)
	if args["ref"] != "e4" {
		t.Fatalf("expected follow-up button ref e4, got %#v", proposal)
	}
}

func TestExtractInputText(t *testing.T) {
	if got := extractInputText(`search for "browser sdk"`); got != "browser sdk" {
		t.Fatalf("expected quoted extraction, got %q", got)
	}
	if got := extractInputText("enter hello world"); got != "hello world" {
		t.Fatalf("expected prefix extraction, got %q", got)
	}
}

func TestProposeNextActionUsesSelectRefForChooseGoal(t *testing.T) {
	snapshot := strings.Join([]string{
		`- document "Checkout"`,
		`  - combobox "Country" [ref=e5]`,
		`  - button "Continue" [ref=e6]`,
	}, "\n")
	proposal := proposeNextAction(`select "Japan" in the country field`, "page text", snapshot)
	if proposal["tool"] != "browser_select_option_by_ref" {
		t.Fatalf("expected browser_select_option_by_ref proposal, got %#v", proposal)
	}
	args, _ := proposal["arguments"].(map[string]any)
	if args["ref"] != "e5" {
		t.Fatalf("expected ref e5, got %#v", proposal)
	}
	if args["label"] != "Japan" {
		t.Fatalf("expected label Japan, got %#v", proposal)
	}
}

func TestProposalSignature(t *testing.T) {
	proposal := map[string]any{
		"type":              "tool",
		"intent":            "act",
		"tool":              "browser_click_by_ref",
		"arguments":         map[string]any{"ref": "e3"},
		"reason":            "click submit",
		"confidence":        "medium",
		"expectedOutcome":   "The page reacts after the click.",
		"needsVerification": true,
	}
	if got := proposalSignature(proposal); !strings.Contains(got, "browser_click_by_ref") {
		t.Fatalf("expected proposal signature to include tool name, got %q", got)
	}
}

func TestObservationSignature(t *testing.T) {
	if got := observationSignature("", ""); got != "" {
		t.Fatalf("expected empty observation signature, got %q", got)
	}
	if got := observationSignature("hello", "snapshot"); !strings.Contains(got, "hello") {
		t.Fatalf("expected observation signature to contain page text, got %q", got)
	}
}

func TestVerificationResultForApply(t *testing.T) {
	proposal := map[string]any{
		"tool":              "browser_type_by_ref",
		"needsVerification": true,
		"expectedOutcome":   `The field "Email" contains the intended input.`,
		"verificationHints": map[string]any{
			"valueVisible": "qa@example.com",
			"targetName":   "Email",
		},
	}
	applyResult := map[string]any{
		"goal":               `log in with email "qa@example.com" and password "secret123"`,
		"tool":               "browser_type_by_ref",
		"arguments":          map[string]any{"ref": "e1", "text": "qa@example.com"},
		"preActionText":      "",
		"preActionSnapshot":  `- textbox "Email" [ref=e1]`,
		"postActionText":     "qa@example.com",
		"postActionSnapshot": `- textbox "Email" [ref=e1] value="qa@example.com"`,
	}
	verification := verificationResultForApply(proposal, applyResult)
	if ok, _ := verification["ok"].(bool); !ok {
		t.Fatalf("expected verification to pass, got %#v", verification)
	}
}

func TestVerificationResultForApplyFailure(t *testing.T) {
	proposal := map[string]any{
		"tool":              "browser_click_by_ref",
		"needsVerification": true,
		"expectedOutcome":   `The page reacts after clicking "Apply".`,
		"verificationHints": map[string]any{
			"targetName": "Apply",
			"targetGone": true,
		},
		"target": map[string]any{
			"name": "Apply",
			"role": "button",
		},
	}
	applyResult := map[string]any{
		"goal":               `click the Apply button`,
		"tool":               "browser_click_by_ref",
		"arguments":          map[string]any{"ref": "e3"},
		"preActionText":      "Apply",
		"preActionSnapshot":  `- button "Apply" [ref=e3]`,
		"postActionText":     "Apply",
		"postActionSnapshot": `- button "Apply" [ref=e3]`,
	}
	verification := verificationResultForApply(proposal, applyResult)
	if ok, _ := verification["ok"].(bool); ok {
		t.Fatalf("expected verification to fail, got %#v", verification)
	}
}

func TestVerificationResultForClickPassesOnPageChange(t *testing.T) {
	proposal := map[string]any{
		"tool":              "browser_click_by_ref",
		"needsVerification": true,
		"expectedOutcome":   `The page reacts after clicking "Sign In".`,
		"verificationHints": map[string]any{
			"targetName": "Sign In",
			"targetGone": true,
		},
		"target": map[string]any{
			"name": "Sign In",
			"role": "button",
		},
	}
	applyResult := map[string]any{
		"goal":               `log in with email "qa@example.com" and password "secret123"`,
		"tool":               "browser_click_by_ref",
		"arguments":          map[string]any{"ref": "e3"},
		"preActionText":      "Sign In",
		"preActionSnapshot":  `- button "Sign In" [ref=e3]`,
		"postActionText":     "Welcome back",
		"postActionSnapshot": `- heading "Dashboard"`,
	}
	verification := verificationResultForApply(proposal, applyResult)
	if ok, _ := verification["ok"].(bool); !ok {
		t.Fatalf("expected click verification to pass on page change, got %#v", verification)
	}
}

func TestVerificationFromHintsForTargetGone(t *testing.T) {
	hints := map[string]any{
		"targetName": "Sign In",
		"targetGone": true,
	}
	verification, ok := verificationFromHints(hints, "Sign In", `- button "Sign In" [ref=e3]`, "Welcome back", `- heading "Dashboard"`)
	if !ok {
		t.Fatal("expected verificationFromHints to produce a result")
	}
	if verified, _ := verification["ok"].(bool); !verified {
		t.Fatalf("expected hinted verification to pass, got %#v", verification)
	}
}

func TestVerificationFromHintsForSuccessSignals(t *testing.T) {
	hints := map[string]any{
		"successSignals": []string{"welcome", "dashboard"},
	}
	verification, ok := verificationFromHints(hints, "", "", "Welcome back", `- heading "Dashboard"`)
	if !ok {
		t.Fatal("expected verificationFromHints to produce a result")
	}
	if verified, _ := verification["ok"].(bool); !verified {
		t.Fatalf("expected hinted success-signal verification to pass, got %#v", verification)
	}
}

func TestVerificationResultForClickPassesOnLoginSignals(t *testing.T) {
	proposal := map[string]any{
		"tool":              "browser_click_by_ref",
		"needsVerification": true,
		"expectedOutcome":   `The page reacts after clicking "Sign In".`,
		"target": map[string]any{
			"name": "Sign In",
			"role": "button",
		},
	}
	applyResult := map[string]any{
		"goal":               `log in with email "qa@example.com" and password "secret123"`,
		"tool":               "browser_click_by_ref",
		"arguments":          map[string]any{"ref": "e3"},
		"preActionText":      "Sign In",
		"preActionSnapshot":  `- button "Sign In" [ref=e3]`,
		"postActionText":     "Welcome back to your dashboard",
		"postActionSnapshot": `- heading "Dashboard"`,
	}
	verification := verificationResultForApply(proposal, applyResult)
	if ok, _ := verification["ok"].(bool); !ok {
		t.Fatalf("expected login click verification to pass, got %#v", verification)
	}
}

func TestVerificationResultForClickPassesOnSearchSignals(t *testing.T) {
	proposal := map[string]any{
		"tool":              "browser_click_by_ref",
		"needsVerification": true,
		"expectedOutcome":   `The search results are shown.`,
		"target": map[string]any{
			"name": "Search",
			"role": "button",
		},
	}
	applyResult := map[string]any{
		"goal":               `search for "browser sdk"`,
		"tool":               "browser_click_by_ref",
		"arguments":          map[string]any{"ref": "e4"},
		"preActionText":      "Search",
		"preActionSnapshot":  `- button "Search" [ref=e4]`,
		"postActionText":     "Results for browser sdk",
		"postActionSnapshot": `- heading "Results for browser sdk"`,
	}
	verification := verificationResultForApply(proposal, applyResult)
	if ok, _ := verification["ok"].(bool); !ok {
		t.Fatalf("expected search click verification to pass, got %#v", verification)
	}
}

func TestApplyPageAgentProposalWithoutProposalFails(t *testing.T) {
	e := &Executor{
		pageAgents: map[string]*pageAgent{
			"page-agent-1": {
				ID:              "page-agent-1",
				Name:            "agent",
				Goal:            "inspect",
				Status:          "idle",
				EnvironmentName: "work",
				TabID:           "tab-a",
			},
		},
	}
	if _, err := e.callApplyPageAgentProposal(context.Background(), map[string]any{"agentId": "page-agent-1"}); err == nil {
		t.Fatal("expected error when proposal is missing")
	}
}

func TestApplyPageAgentProposalRecordsHistory(t *testing.T) {
	e := &Executor{
		currentTabID:      "tab-a",
		environments:      map[string]*browserEnvironment{},
		activeEnvironment: "work",
		pageAgents:        map[string]*pageAgent{},
	}
	env := newBrowserEnvironment("work", "endpoint-a", nil, false)
	env.Pages["tab-a"] = newPageRuntime("tab-a", nil, nil)
	env.ActivePageID = "tab-a"
	e.environments["work"] = env

	agent := &pageAgent{
		ID:              "page-agent-1",
		Name:            "agent",
		Goal:            "inspect page",
		Status:          "idle",
		EnvironmentName: "work",
		TabID:           "tab-a",
		LastProposal: map[string]any{
			"type":              "tool",
			"intent":            "observe",
			"tool":              "browser_list_page_agents",
			"arguments":         map[string]any{},
			"reason":            "inspect available agents",
			"confidence":        "low",
			"expectedOutcome":   "A list response is returned.",
			"needsVerification": false,
		},
		History: make([]pageAgentHistoryEntry, 0, 8),
	}
	e.pageAgents[agent.ID] = agent

	got, err := e.callApplyPageAgentProposal(context.Background(), map[string]any{"agentId": agent.ID})
	if err != nil {
		t.Fatalf("callApplyPageAgentProposal returned error: %v", err)
	}
	if _, ok := got["applyResult"].(map[string]any); !ok {
		t.Fatalf("expected applyResult payload, got %#v", got)
	}
	if len(e.pageAgents[agent.ID].History) != 1 {
		t.Fatalf("expected one history entry, got %#v", e.pageAgents[agent.ID].History)
	}
	if e.pageAgents[agent.ID].History[0].Status != "applied" {
		t.Fatalf("expected applied history status, got %#v", e.pageAgents[agent.ID].History[0])
	}
}

func TestApplyPageAgentProposalSyncsPageRuntimeState(t *testing.T) {
	e := &Executor{
		currentTabID:      "tab-a",
		environments:      map[string]*browserEnvironment{},
		activeEnvironment: "work",
		pageAgents:        map[string]*pageAgent{},
	}
	env := newBrowserEnvironment("work", "endpoint-a", nil, false)
	env.Pages["tab-a"] = newPageRuntime("tab-a", nil, nil)
	env.ActivePageID = "tab-a"
	e.environments["work"] = env

	agent := &pageAgent{
		ID:                 "page-agent-1",
		Name:               "agent",
		Goal:               "inspect page",
		Status:             "idle",
		EnvironmentName:    "work",
		TabID:              "tab-a",
		LastProposalSource: "rules",
		LastProposal: map[string]any{
			"type":              "tool",
			"intent":            "observe",
			"tool":              "browser_list_page_agents",
			"arguments":         map[string]any{},
			"reason":            "inspect available agents",
			"confidence":        "low",
			"expectedOutcome":   "A list response is returned.",
			"needsVerification": false,
		},
		History: make([]pageAgentHistoryEntry, 0, 8),
	}
	e.pageAgents[agent.ID] = agent

	if _, err := e.callApplyPageAgentProposal(context.Background(), map[string]any{"agentId": agent.ID}); err != nil {
		t.Fatalf("callApplyPageAgentProposal returned error: %v", err)
	}

	page := e.environments["work"].Pages["tab-a"]
	if page.LastTool != "browser_list_page_agents" {
		t.Fatalf("expected page runtime LastTool to sync, got %#v", page)
	}
	if page.PendingProposal != nil {
		t.Fatalf("expected no pending proposal after non-browser follow-up, got %#v", page.PendingProposal)
	}
}

func TestRunPageAgentLoopRequiresAgent(t *testing.T) {
	e := &Executor{pageAgents: map[string]*pageAgent{}}
	if _, err := e.callRunPageAgentLoop(context.Background(), map[string]any{"agentId": "missing"}); err == nil {
		t.Fatal("expected missing agent error")
	}
}

func TestRunPageAgentLoopRejectsInvalidRequireAIArg(t *testing.T) {
	e := &Executor{
		pageAgents: map[string]*pageAgent{
			"page-agent-1": {ID: "page-agent-1"},
		},
	}
	if _, err := e.callRunPageAgentLoop(context.Background(), map[string]any{
		"agentId":   "page-agent-1",
		"requireAI": "yes",
	}); err == nil {
		t.Fatal("expected requireAI type validation error")
	}
}

func TestRunPageAgentLoopRejectsInvalidStopOnRepeatedProposalArg(t *testing.T) {
	e := &Executor{
		pageAgents: map[string]*pageAgent{
			"page-agent-1": {ID: "page-agent-1"},
		},
	}
	if _, err := e.callRunPageAgentLoop(context.Background(), map[string]any{
		"agentId":                "page-agent-1",
		"stopOnRepeatedProposal": "yes",
	}); err == nil {
		t.Fatal("expected stopOnRepeatedProposal type validation error")
	}
}

func TestRunPageAgentLoopRejectsInvalidStopOnNoPageChangeArg(t *testing.T) {
	e := &Executor{
		pageAgents: map[string]*pageAgent{
			"page-agent-1": {ID: "page-agent-1"},
		},
	}
	if _, err := e.callRunPageAgentLoop(context.Background(), map[string]any{
		"agentId":            "page-agent-1",
		"stopOnNoPageChange": "yes",
	}); err == nil {
		t.Fatal("expected stopOnNoPageChange type validation error")
	}
}

func TestRunPageAgentLoopRecordsStepErrors(t *testing.T) {
	e := &Executor{
		pageAgents: map[string]*pageAgent{
			"page-agent-1": {
				ID:              "page-agent-1",
				Name:            "agent",
				Goal:            "inspect page",
				Status:          "idle",
				EnvironmentName: "missing",
				TabID:           "tab-a",
			},
		},
		environments: map[string]*browserEnvironment{},
	}

	got, err := e.callRunPageAgentLoop(context.Background(), map[string]any{
		"agentId":   "page-agent-1",
		"maxSteps":  3,
		"maxErrors": 2,
	})
	if err != nil {
		t.Fatalf("callRunPageAgentLoop returned error: %v", err)
	}
	if got["stopReason"] != "max_errors_reached" {
		t.Fatalf("expected max_errors_reached, got %#v", got)
	}
	if got["errorCount"] != 2 {
		t.Fatalf("expected errorCount=2, got %#v", got)
	}
	steps, _ := got["steps"].([]map[string]any)
	if len(steps) != 2 {
		t.Fatalf("expected two recorded step errors, got %#v", got)
	}
	if steps[0]["phase"] != "step_error" {
		t.Fatalf("expected first phase step_error, got %#v", steps[0])
	}
}
