package tools

import (
	"context"
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
