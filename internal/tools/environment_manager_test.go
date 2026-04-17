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
		browserClient:     nil,
		cdpEndpoint:       "endpoint-a",
		currentTabID:      "tab-a",
		ariaRefStore:      map[string]map[string]ariaRefMeta{"tab-a": {"e1": {Role: "button"}}},
		environments:      map[string]*browserEnvironment{},
		activeEnvironment: "work",
	}
	e.environments["work"] = newBrowserEnvironment("work", "endpoint-a", nil, false)
	e.environments["work"].Pages["tab-a"] = newPageRuntime("tab-a", nil, cloneAriaRefStoreForTab(e.ariaRefStore, "tab-a"))
	e.environments["work"].ActivePageID = "tab-a"
	e.environments["personal"] = newBrowserEnvironment("personal", "endpoint-b", nil, false)
	e.environments["personal"].Pages["tab-b"] = newPageRuntime("tab-b", nil, nil)
	e.environments["personal"].ActivePageID = "tab-b"

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
	if got := e.environments["personal"].ActivePageID; got != "tab-b-2" {
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

func TestAllocateEnvironmentNameLocked(t *testing.T) {
	e := &Executor{
		environments: map[string]*browserEnvironment{
			"local":   newBrowserEnvironment("local", "", nil, false),
			"local-1": newBrowserEnvironment("local-1", "", nil, false),
			"work":    newBrowserEnvironment("work", "", nil, false),
		},
	}

	if got := e.allocateEnvironmentNameLocked("local"); got != "local-2" {
		t.Fatalf("expected local-2, got %q", got)
	}
	if got := e.allocateEnvironmentNameLocked(""); got != "local-2" {
		t.Fatalf("expected default prefix to allocate local-2, got %q", got)
	}
	if got := e.allocateEnvironmentNameLocked("sandbox"); got != "sandbox" {
		t.Fatalf("expected sandbox, got %q", got)
	}
}

func TestLoadEnvironmentLockedExportsPageRuntimeState(t *testing.T) {
	e := &Executor{
		environments: map[string]*browserEnvironment{},
	}
	env := newBrowserEnvironment("work", "endpoint-a", nil, false)
	env.Pages["tab-a"] = newPageRuntime("tab-a", nil, map[string]ariaRefMeta{
		"e1": {Role: "button"},
	})
	env.ActivePageID = "tab-a"
	e.environments["work"] = env

	if err := e.loadEnvironmentLocked("work"); err != nil {
		t.Fatalf("loadEnvironmentLocked returned error: %v", err)
	}
	if e.currentTabID != "tab-a" {
		t.Fatalf("expected currentTabID tab-a, got %q", e.currentTabID)
	}
	meta, ok := e.getStoredAriaRefMeta("tab-a", "e1")
	if !ok || meta.Role != "button" {
		t.Fatalf("expected aria ref meta to be exported from page runtime, got %#v ok=%v", meta, ok)
	}
}
