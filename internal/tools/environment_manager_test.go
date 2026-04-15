package tools

import "testing"

func TestInitializeEnvironmentStateWithoutBrowserClient(t *testing.T) {
	e := &Executor{
		ariaRefStore: make(map[string]map[string]ariaRefMeta),
	}
	e.initializeEnvironmentState("work")
	if e.activeEnvironment != "" {
		t.Fatalf("expected no active environment, got %q", e.activeEnvironment)
	}
	if len(e.environments) != 0 {
		t.Fatalf("expected no default environment without browser client, got %#v", e.environments)
	}
}

func TestWithEnvironmentRestoresActiveEnvironment(t *testing.T) {
	e := &Executor{
		browserClient:      nil,
		cdpEndpoint:       "endpoint-a",
		currentTabID:      "tab-a",
		ariaRefStore:      map[string]map[string]ariaRefMeta{"tab-a": {"e1": {Role: "button"}}},
		environments:      map[string]*browserEnvironment{},
		activeEnvironment: "work",
	}
	e.environments["work"] = newBrowserEnvironment("work", "endpoint-a", nil, false)
	e.environments["work"].CurrentTabID = "tab-a"
	e.environments["work"].AriaRefStore = cloneAriaRefStore(e.ariaRefStore)
	e.environments["personal"] = newBrowserEnvironment("personal", "endpoint-b", nil, false)
	e.environments["personal"].CurrentTabID = "tab-b"

	seen := ""
	_, err := e.withEnvironment(map[string]any{"environment": "personal"}, func() (map[string]any, error) {
		seen = e.activeEnvironmentName()
		if e.currentTabID != "tab-b" {
			t.Fatalf("expected personal tab context, got %q", e.currentTabID)
		}
		e.currentTabID = "tab-b-2"
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		t.Fatalf("withEnvironment returned error: %v", err)
	}
	if seen != "personal" {
		t.Fatalf("expected temporary environment personal, got %q", seen)
	}
	if e.activeEnvironmentName() != "work" {
		t.Fatalf("expected active environment restored to work, got %q", e.activeEnvironmentName())
	}
	if e.currentTabID != "tab-a" {
		t.Fatalf("expected work tab restored, got %q", e.currentTabID)
	}
	if got := e.environments["personal"].CurrentTabID; got != "tab-b-2" {
		t.Fatalf("expected temporary environment state to persist, got %q", got)
	}
}

func TestCloseEnvironmentFallsBackToNextActive(t *testing.T) {
	e := &Executor{
		ariaRefStore:      make(map[string]map[string]ariaRefMeta),
		environments:      map[string]*browserEnvironment{},
		activeEnvironment: "alpha",
	}
	e.environments["alpha"] = newBrowserEnvironment("alpha", "endpoint-a", nil, false)
	e.environments["beta"] = newBrowserEnvironment("beta", "endpoint-b", nil, false)
	if err := e.loadEnvironmentLocked("alpha"); err != nil {
		t.Fatalf("loadEnvironmentLocked alpha: %v", err)
	}

	_, next, err := e.closeEnvironment("alpha")
	if err != nil {
		t.Fatalf("closeEnvironment returned error: %v", err)
	}
	if next != "beta" {
		t.Fatalf("expected fallback active environment beta, got %q", next)
	}
	if e.activeEnvironmentName() != "beta" {
		t.Fatalf("expected executor active environment beta, got %q", e.activeEnvironmentName())
	}
}
