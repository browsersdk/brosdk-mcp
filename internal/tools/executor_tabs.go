package tools

import (
	"context"
	"fmt"
	"strings"

	"brosdk-mcp/internal/cdp"
)

func (e *Executor) callListTabs(ctx context.Context, args map[string]any) (map[string]any, error) {
	_ = args
	targets, err := e.listPageTargets(ctx)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	activeTabID := e.currentTabID
	e.mu.Unlock()

	tabs := make([]map[string]any, 0, len(targets))
	for idx, t := range targets {
		tabs = append(tabs, map[string]any{
			"index":  idx,
			"tabId":  t.ID,
			"title":  t.Title,
			"url":    t.URL,
			"active": t.ID == activeTabID,
		})
	}

	return map[string]any{"tabs": tabs}, nil
}

func (e *Executor) callNewTab(ctx context.Context, args map[string]any) (map[string]any, error) {
	url := "about:blank"
	if v, ok := getStringArg(args, "url"); ok && strings.TrimSpace(v) != "" {
		url = strings.TrimSpace(v)
	}

	targetID, err := e.browserClient.CreateTarget(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("Target.createTarget failed: %w", err)
	}
	if err := e.activateAndConnect(ctx, targetID); err != nil {
		return nil, err
	}

	return map[string]any{
		"ok":    true,
		"tabId": targetID,
		"url":   url,
	}, nil
}

func (e *Executor) callSwitchTab(ctx context.Context, args map[string]any) (map[string]any, error) {
	targetID, err := e.resolveTargetID(ctx, args)
	if err != nil {
		return nil, err
	}
	if err := e.activateAndConnect(ctx, targetID); err != nil {
		return nil, err
	}

	return map[string]any{
		"ok":    true,
		"tabId": targetID,
	}, nil
}

func (e *Executor) callCloseTab(ctx context.Context, args map[string]any) (map[string]any, error) {
	targetID, hasTabID := getStringArg(args, "tabId")
	if !hasTabID || strings.TrimSpace(targetID) == "" {
		_, current, err := e.getCurrentPageClient(ctx)
		if err != nil {
			return nil, err
		}
		targetID = current
	}

	if err := e.browserClient.CloseTarget(ctx, targetID); err != nil {
		return nil, fmt.Errorf("Target.closeTarget failed: %w", err)
	}
	e.clearStoredAriaRefMeta(targetID)
	e.mu.Lock()
	if env := e.environments[e.activeEnvironment]; env != nil {
		delete(env.Pages, targetID)
		if env.ActivePageID == targetID {
			env.ActivePageID = ""
		}
	}
	e.mu.Unlock()

	remaining, err := e.listPageTargets(ctx)
	if err != nil {
		return nil, err
	}

	var nextTabID string
	if len(remaining) > 0 {
		nextTabID = remaining[0].ID
		if err := e.activateAndConnect(ctx, nextTabID); err != nil {
			return nil, err
		}
	}

	return map[string]any{
		"ok":          true,
		"closedTabId": targetID,
		"nextTabId":   nextTabID,
	}, nil
}

func (e *Executor) ensureInitialTab(ctx context.Context) (string, error) {
	return ensureInitialTabForBrowser(ctx, e.browserClient)
}

func (e *Executor) listPageTargets(ctx context.Context) ([]cdp.Target, error) {
	if e.browserClient == nil {
		return nil, fmt.Errorf("no active browser environment")
	}
	return listPageTargetsForBrowser(ctx, e.browserClient)
}

func (e *Executor) resolveTargetID(ctx context.Context, args map[string]any) (string, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok && strings.TrimSpace(tabID) != "" {
		return strings.TrimSpace(tabID), nil
	}

	if idx, ok, err := getIndexArg(args, "index"); err != nil {
		return "", err
	} else if ok {
		targets, err := e.listPageTargets(ctx)
		if err != nil {
			return "", err
		}
		if idx < 0 || idx >= len(targets) {
			return "", fmt.Errorf("index %d out of range, total tabs: %d", idx, len(targets))
		}
		return targets[idx].ID, nil
	}

	return "", fmt.Errorf("either tabId or index is required")
}

func (e *Executor) activateAndConnect(ctx context.Context, targetID string) error {
	if e.browserClient == nil {
		return fmt.Errorf("no active browser environment")
	}
	if err := e.browserClient.ActivateTarget(ctx, targetID); err != nil {
		return fmt.Errorf("Target.activateTarget failed: %w", err)
	}
	return e.connectToTab(ctx, targetID)
}

func (e *Executor) connectToTab(ctx context.Context, targetID string) error {
	if e.browserClient == nil {
		return fmt.Errorf("no active browser environment")
	}
	newPageClient, err := e.browserClient.AttachToTarget(ctx, targetID)
	if err != nil {
		return err
	}
	if err := enablePageSession(ctx, newPageClient); err != nil {
		_ = newPageClient.Close()
		return err
	}

	var old *cdp.Client
	var oldTabID string
	e.mu.Lock()
	old = e.pageClient
	oldTabID = e.currentTabID
	e.syncExecutorStateToCurrentPageLocked()
	e.pageClient = newPageClient
	e.currentTabID = targetID
	if strings.TrimSpace(oldTabID) != "" {
		if oldPage := e.ensurePageRuntimeLocked(oldTabID); oldPage != nil {
			oldPage.PageClient = nil
			oldPage.AriaRefStore = cloneAriaRefStoreForTab(e.ariaRefStore, oldTabID)
		}
	}
	if page := e.ensurePageRuntimeLocked(targetID); page != nil {
		page.PageClient = newPageClient
		page.TabID = targetID
	}
	e.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	return nil
}

// enablePageSession enables the CDP domains required for navigation waiting and
// lifecycle event tracking on a freshly attached page session.  It is called
// both on initial connect and after a reconnect so that lifecycle events are
// always available after a connection recovery.
func enablePageSession(ctx context.Context, client *cdp.Client) error {
	if _, err := client.Call(ctx, "Page.enable", nil); err != nil {
		return fmt.Errorf("Page.enable: %w", err)
	}
	if _, err := client.Call(ctx, "Page.setLifecycleEventsEnabled", map[string]any{
		"enabled": true,
	}); err != nil {
		// Non-fatal: lifecycle events improve networkidle detection but are not
		// strictly required for basic navigation.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	_, _ = client.Call(ctx, "Runtime.enable", nil)
	return nil
}

func (e *Executor) getCurrentPageClient(ctx context.Context) (*cdp.Client, string, error) {
	e.mu.Lock()
	client := e.pageClient
	tabID := e.currentTabID
	browserClient := e.browserClient
	e.mu.Unlock()

	if client != nil && strings.TrimSpace(tabID) != "" {
		return client, tabID, nil
	}
	if browserClient == nil {
		return nil, "", fmt.Errorf("no active browser environment")
	}

	initialTabID, err := e.ensureInitialTab(ctx)
	if err != nil {
		return nil, "", err
	}
	if err := e.connectToTab(ctx, initialTabID); err != nil {
		return nil, "", err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.syncExecutorStateToCurrentPageLocked()
	return e.pageClient, e.currentTabID, nil
}
