package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type pageAgent struct {
	ID              string
	Name            string
	Goal            string
	Status          string
	EnvironmentName string
	TabID           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
		"createdAt":     agent.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     agent.UpdatedAt.UTC().Format(time.RFC3339),
	}
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
