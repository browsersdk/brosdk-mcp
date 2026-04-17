package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"brosdk-mcp/internal/cdp"
)

type Executor struct {
	browserClient *cdp.Client
	cdpEndpoint   string
	logger        *slog.Logger
	lowInjection  bool

	mu           sync.Mutex
	currentTabID string
	pageClient   *cdp.Client
	ariaRefStore map[string]map[string]ariaRefMeta

	pageAgents      map[string]*pageAgent
	nextPageAgentID int

	environments      map[string]*browserEnvironment
	activeEnvironment string

	reconnectWindowStart  time.Time
	reconnectAttempts     int
	reconnectBlockedUntil time.Time
}

func NewExecutor(ctx context.Context, browserClient *cdp.Client, cdpEndpoint string, environmentName string, logger *slog.Logger, lowInjection bool) (*Executor, error) {
	e := &Executor{
		browserClient: browserClient,
		cdpEndpoint:   strings.TrimSpace(cdpEndpoint),
		logger:        logger,
		lowInjection:  lowInjection,
		ariaRefStore:  make(map[string]map[string]ariaRefMeta),
		pageAgents:    make(map[string]*pageAgent),
	}

	if browserClient != nil {
		initialTabID, err := e.ensureInitialTab(ctx)
		if err != nil {
			return nil, err
		}
		if err := e.connectToTab(ctx, initialTabID); err != nil {
			return nil, err
		}
	}
	e.initializeEnvironmentState(environmentName)

	return e, nil
}

func (e *Executor) Close() error {
	e.mu.Lock()
	e.snapshotActiveEnvironmentLocked()
	envs := make([]*browserEnvironment, 0, len(e.environments))
	for _, env := range e.environments {
		envs = append(envs, env)
	}
	e.pageClient = nil
	e.currentTabID = ""
	e.browserClient = nil
	e.activeEnvironment = ""
	e.mu.Unlock()

	for _, env := range envs {
		if env == nil {
			continue
		}
		for _, page := range env.Pages {
			if page == nil || page.PageClient == nil {
				continue
			}
			_ = page.PageClient.Close()
			page.PageClient = nil
		}
		if env.OwnsBrowser && env.BrowserClient != nil {
			_ = env.BrowserClient.Close()
			env.BrowserClient = nil
		}
	}
	return nil
}

func (e *Executor) Call(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "browser_create_page_agent":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callCreatePageAgent(ctx, args) })
	case "browser_list_page_agents":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callListPageAgents(ctx, args) })
	case "browser_get_page_agent":
		return e.callGetPageAgent(ctx, args)
	case "browser_remove_page_agent":
		return e.callRemovePageAgent(ctx, args)
	case "browser_connect_environment":
		return e.callAddEnvironment(ctx, args)
	case "browser_launch_environment":
		return e.callLaunchLocalEnvironment(ctx, args)
	case "browser_list_environments":
		return e.callListEnvironments(ctx, args)
	case "browser_switch_environment":
		return e.callUseEnvironment(ctx, args)
	case "browser_close_environment":
		return e.callCloseEnvironment(ctx, args)
	case "browser_navigate":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callNavigate(ctx, args) })
	case "browser_reload":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callReload(ctx, args) })
	case "browser_go_back":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callGoBack(ctx, args) })
	case "browser_go_forward":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callGoForward(ctx, args) })
	case "browser_aria_snapshot":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callAriaSnapshot(ctx, args) })
	case "browser_screenshot":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callScreenshot(ctx, args) })
	case "browser_click":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callClick(ctx, args) })
	case "browser_click_by_ref":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callClickByRef(ctx, args) })
	case "browser_type":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callType(ctx, args) })
	case "browser_type_by_ref":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callTypeByRef(ctx, args) })
	case "browser_set_input_value":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callSetInputValue(ctx, args) })
	case "browser_set_input_value_by_ref":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callSetInputValueByRef(ctx, args) })
	case "browser_find_and_click_text":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callFindAndClickText(ctx, args) })
	case "browser_get_text":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callGetText(ctx, args) })
	case "browser_evaluate":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callEvaluate(ctx, args) })
	case "browser_wait":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callWait(ctx, args) })
	case "browser_wait_for_selector":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callWaitForSelector(ctx, args) })
	case "browser_wait_for_text":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callWaitForText(ctx, args) })
	case "browser_wait_for_url":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callWaitForURL(ctx, args) })
	case "browser_wait_for_load":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callWaitForLoadTool(ctx, args) })
	case "browser_wait_for_function":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callWaitForFunction(ctx, args) })
	case "browser_list_tabs":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callListTabs(ctx, args) })
	case "browser_new_tab":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callNewTab(ctx, args) })
	case "browser_switch_tab":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callSwitchTab(ctx, args) })
	case "browser_close_tab":
		return e.withEnvironment(args, func() (map[string]any, error) { return e.callCloseTab(ctx, args) })
	default:
		return nil, fmt.Errorf("tool %q not implemented in P2", name)
	}
}
