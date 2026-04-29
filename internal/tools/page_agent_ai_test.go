package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidateAIProposal(t *testing.T) {
	valid := map[string]any{
		"type":              "tool",
		"intent":            "act",
		"tool":              "browser_click_by_ref",
		"arguments":         map[string]any{"ref": "e1"},
		"reason":            "click the target",
		"confidence":        "medium",
		"expectedOutcome":   "The page reacts after the click.",
		"needsVerification": true,
		"verificationHints": map[string]any{"targetName": "Sign In", "targetGone": true},
	}
	if err := validateAIProposal(valid); err != nil {
		t.Fatalf("expected valid proposal, got %v", err)
	}

	invalid := map[string]any{
		"type":              "tool",
		"intent":            "act",
		"tool":              "browser_click_by_ref",
		"arguments":         map[string]any{},
		"reason":            "click the target",
		"confidence":        "medium",
		"expectedOutcome":   "The page reacts after the click.",
		"needsVerification": true,
		"verificationHints": map[string]any{"targetName": "Sign In", "targetGone": true},
	}
	if err := validateAIProposal(invalid); err == nil {
		t.Fatal("expected missing ref validation error")
	}

	validSelect := map[string]any{
		"type":              "tool",
		"intent":            "act",
		"tool":              "browser_select_option_by_ref",
		"arguments":         map[string]any{"ref": "e5", "label": "Beta"},
		"reason":            "select the requested option",
		"confidence":        "high",
		"expectedOutcome":   "The dropdown shows Beta as the selected value.",
		"needsVerification": true,
		"verificationHints": map[string]any{"valueVisible": "Beta"},
	}
	if err := validateAIProposal(validSelect); err != nil {
		t.Fatalf("expected valid select proposal, got %v", err)
	}
}

func TestGenerateAIProposalLive(t *testing.T) {
	if os.Getenv("BROSDK_PAGEAGENT_AI_E2E") != "1" {
		t.Skip("set BROSDK_PAGEAGENT_AI_E2E=1 to run live AI proposal test")
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("OPENAI_API_KEY, OPENAI_BASE_URL, and OPENAI_MODEL must be set")
	}

	e := &Executor{}
	e.SetPageAgentAIConfig(apiKey, baseURL, model)

	snapshot := strings.Join([]string{
		`- document "Login"`,
		`  - textbox "Email" [ref=e1]`,
		`  - textbox "Password" [ref=e2]`,
		`  - button "Sign In" [ref=e3]`,
	}, "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	proposal, err := e.generateAIProposal(ctx, `click the sign in button`, `Email Password Sign In`, snapshot, "")
	if err != nil {
		t.Fatalf("generateAIProposal returned error: %v", err)
	}
	if err := validateAIProposal(proposal); err != nil {
		t.Fatalf("generated proposal failed validation: %v proposal=%#v", err, proposal)
	}
	tool, _ := proposal["tool"].(string)
	if strings.TrimSpace(tool) == "" {
		t.Fatalf("expected tool in proposal, got %#v", proposal)
	}
}
