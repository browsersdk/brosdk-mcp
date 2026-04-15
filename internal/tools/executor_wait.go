package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"brosdk-mcp/internal/cdp"
)

func (e *Executor) callWait(ctx context.Context, args map[string]any) (map[string]any, error) {
	timeoutMs := 0
	if v, ok, err := getIntArg(args, "timeoutMs"); err != nil {
		return nil, err
	} else if ok {
		timeoutMs = v
	}
	if timeoutMs == 0 {
		if v, ok, err := getIntArg(args, "ms"); err != nil {
			return nil, err
		} else if ok {
			timeoutMs = v
		}
	}
	if timeoutMs < 0 {
		timeoutMs = 0
	}
	time.Sleep(time.Duration(timeoutMs) * time.Millisecond)
	return map[string]any{"ok": true, "waitedMs": timeoutMs}, nil
}

func (e *Executor) callWaitForSelector(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	selector, ok := getStringArg(args, "selector")
	if !ok || strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("missing required argument selector")
	}
	selector = strings.TrimSpace(selector)
	state := "visible"
	if v, ok := getStringArg(args, "state"); ok && strings.TrimSpace(v) != "" {
		state = strings.TrimSpace(v)
	}
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)
	pollMs := 100

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	okWait, err := e.waitUntilCondition(ctx, timeoutMs, pollMs, func(c context.Context) (bool, error) {
		expr := buildSelectorStateExpression(selector, state)
		return e.evaluateBool(c, pageClient, expr)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": okWait, "tabId": currentTabID}, nil
}

func (e *Executor) callWaitForText(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	text, ok := getStringArg(args, "text")
	if !ok || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("missing required argument text")
	}
	text = strings.TrimSpace(text)
	exact := false
	if b, ok, err := getBoolArg(args, "exact"); err != nil {
		return nil, err
	} else if ok {
		exact = b
	}
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)
	pollMs := 100

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	okWait, err := e.waitUntilCondition(ctx, timeoutMs, pollMs, func(c context.Context) (bool, error) {
		expr := buildWaitTextExpression(text, exact)
		return e.evaluateBool(c, pageClient, expr)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": okWait, "tabId": currentTabID}, nil
}

func (e *Executor) callWaitForURL(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	pattern, ok := getStringArg(args, "url")
	if !ok || strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("missing required argument url")
	}
	pattern = strings.TrimSpace(pattern)
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)
	pollMs := 100

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	okWait, err := e.waitUntilCondition(ctx, timeoutMs, pollMs, func(c context.Context) (bool, error) {
		currentURL, err := e.evaluateString(c, pageClient, "location.href")
		if err != nil {
			return false, err
		}
		return matchURLPattern(pattern, currentURL), nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": okWait, "tabId": currentTabID}, nil
}

func (e *Executor) callWaitForLoadTool(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	waitUntil := ""
	if v, ok := getStringArg(args, "waitUntil"); ok {
		waitUntil = strings.TrimSpace(v)
	}
	if waitUntil == "" {
		if v, ok := getStringArg(args, "load"); ok {
			waitUntil = strings.TrimSpace(v)
		}
	}
	if waitUntil == "" {
		waitUntil = "load"
	}

	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	if err := e.waitForLoadState(ctx, pageClient, waitUntil, timeoutMs); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "tabId": currentTabID, "waitUntil": waitUntil}, nil
}

func (e *Executor) callWaitForFunction(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	fn, ok := getStringArg(args, "fn")
	if !ok || strings.TrimSpace(fn) == "" {
		return nil, fmt.Errorf("missing required argument fn")
	}
	fn = strings.TrimSpace(fn)
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)
	pollMs := getIntArgDefault(args, "pollingMs", 100)
	if pollMs <= 0 {
		pollMs = 100
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	okWait, err := e.waitUntilCondition(ctx, timeoutMs, pollMs, func(c context.Context) (bool, error) {
		expr := buildWaitFunctionExpression(fn)
		return e.evaluateBool(c, pageClient, expr)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": okWait, "tabId": currentTabID}, nil
}

func (e *Executor) waitForLoad(ctx context.Context, pageClient *cdp.Client) error {
	return e.waitForLoadState(ctx, pageClient, "load", 30000)
}

func (e *Executor) waitForLoadState(ctx context.Context, pageClient *cdp.Client, waitUntil string, timeoutMs int) error {
	waitUntil = normalizeWaitUntil(waitUntil)
	if waitUntil == "none" {
		return nil
	}

	if timeoutMs <= 0 {
		timeoutMs = 30000
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	pageClient = e.activePageClient(pageClient)
	if pageClient == nil {
		return fmt.Errorf("nil page client for waitUntil=%s", waitUntil)
	}

	ok, err := e.checkLoadState(waitCtx, pageClient, waitUntil)
	if err == nil && ok {
		return nil
	}

	sub := loadEventSubscription(waitUntil)
	if sub.method != "" {
		_, waitErr := pageClient.WaitForEvent(waitCtx, sub.method, sub.match)
		switch {
		case waitErr == nil:
			// Event received – verify readyState as a sanity check.
			if ok, checkErr := e.checkLoadState(waitCtx, e.activePageClient(pageClient), waitUntil); checkErr == nil && ok {
				return nil
			}
			// readyState not yet settled; fall through to polling.
		case errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled):
			// Hard timeout or caller cancellation – do not fall through to polling.
			return waitErr
		default:
			// Event stream closed (e.g. connection lost) – fall through to
			// polling so the caller gets a best-effort result rather than an
			// immediate error.
		}
	}

	_, err = e.waitUntilCondition(waitCtx, remainingTimeoutMs(waitCtx, timeoutMs), 100, func(c context.Context) (bool, error) {
		return e.checkLoadState(c, e.activePageClient(pageClient), waitUntil)
	})
	return err
}

func (e *Executor) waitUntilCondition(ctx context.Context, timeoutMs int, pollMs int, fn func(context.Context) (bool, error)) (bool, error) {
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	if pollMs <= 0 {
		pollMs = 100
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	ticker := time.NewTicker(time.Duration(pollMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		ok, err := fn(waitCtx)
		if err == nil && ok {
			return true, nil
		}

		select {
		case <-waitCtx.Done():
			if err != nil {
				return false, fmt.Errorf("wait condition timed out: %w", err)
			}
			return false, waitCtx.Err()
		case <-ticker.C:
		}
	}
}

func (e *Executor) checkLoadState(ctx context.Context, pageClient *cdp.Client, waitUntil string) (bool, error) {
	pageClient = e.activePageClient(pageClient)
	state, err := e.evaluateString(ctx, pageClient, "document.readyState")
	if err != nil {
		return false, err
	}
	switch normalizeWaitUntil(waitUntil) {
	case "domcontentloaded":
		return state == "interactive" || state == "complete", nil
	case "networkidle":
		// P2 fallback: use readyState complete until event-driven network idle is implemented.
		return state == "complete", nil
	case "load":
		return state == "complete", nil
	case "none":
		return true, nil
	default:
		return state == "complete", nil
	}
}

func normalizeWaitUntil(waitUntil string) string {
	switch strings.ToLower(strings.TrimSpace(waitUntil)) {
	case "", "load":
		return "load"
	case "domcontentloaded":
		return "domcontentloaded"
	case "networkidle":
		return "networkidle"
	case "none":
		return "none"
	default:
		return "load"
	}
}

const networkIdleLifecycleFallbackDelay = 1200 * time.Millisecond

func shouldFallbackNetworkIdleWithoutLifecycle(waitUntil string, requireMainFrameNavigation bool, mainFrameNavigated bool, loadEventMatched bool, sawLoadEvent bool, mainFrameNavigatedAt time.Time, now time.Time) bool {
	if normalizeWaitUntil(waitUntil) != "networkidle" {
		return false
	}
	if !requireMainFrameNavigation {
		return false
	}
	if !mainFrameNavigated || mainFrameNavigatedAt.IsZero() {
		return false
	}
	if loadEventMatched || sawLoadEvent {
		return false
	}
	if now.Before(mainFrameNavigatedAt) {
		return false
	}
	return now.Sub(mainFrameNavigatedAt) >= networkIdleLifecycleFallbackDelay
}

func loadEventMethod(waitUntil string) string {
	return loadEventSubscription(waitUntil).method
}

type eventSubscription struct {
	method string
	match  func(cdp.Event) bool
}

func loadEventSubscription(waitUntil string) eventSubscription {
	switch normalizeWaitUntil(waitUntil) {
	case "domcontentloaded":
		return eventSubscription{method: "Page.domContentEventFired"}
	case "load":
		return eventSubscription{method: "Page.loadEventFired"}
	case "networkidle":
		return eventSubscription{
			method: "Page.lifecycleEvent",
			match: func(ev cdp.Event) bool {
				name, err := extractLifecycleEventName(ev)
				if err != nil {
					return false
				}
				return name == "networkidle" || name == "networkalmostidle"
			},
		}
	default:
		return eventSubscription{}
	}
}

func extractLifecycleEventName(ev cdp.Event) (string, error) {
	name, _, _, err := extractLifecycleEventInfo(ev)
	return name, err
}

func extractLifecycleEventInfo(ev cdp.Event) (string, string, string, error) {
	var payload struct {
		Name     string `json:"name"`
		FrameID  string `json:"frameId"`
		LoaderID string `json:"loaderId"`
	}
	if err := json.Unmarshal(ev.Params, &payload); err != nil {
		return "", "", "", err
	}
	return strings.ToLower(strings.TrimSpace(payload.Name)), strings.TrimSpace(payload.FrameID), strings.TrimSpace(payload.LoaderID), nil
}

func isMatchingLifecycleEventForNavigation(ev cdp.Event, target navigationTarget) bool {
	name, frameID, loaderID, err := extractLifecycleEventInfo(ev)
	if err != nil {
		return false
	}
	if name != "networkidle" && name != "networkalmostidle" {
		return false
	}
	if target.FrameID != "" && frameID != "" && target.FrameID != frameID {
		return false
	}
	if target.LoaderID != "" && loaderID != "" && target.LoaderID != loaderID {
		return false
	}
	return true
}

func isMainFrameNavigatedEvent(ev cdp.Event) bool {
	return isMatchingMainFrameNavigatedEvent(ev, navigationTarget{})
}

func isMatchingMainFrameNavigatedEvent(ev cdp.Event, target navigationTarget) bool {
	var payload struct {
		Frame struct {
			ID       string `json:"id"`
			ParentID string `json:"parentId"`
			LoaderID string `json:"loaderId"`
		} `json:"frame"`
	}
	if err := json.Unmarshal(ev.Params, &payload); err != nil {
		return false
	}
	frameID := strings.TrimSpace(payload.Frame.ID)
	parentID := strings.TrimSpace(payload.Frame.ParentID)
	loaderID := strings.TrimSpace(payload.Frame.LoaderID)
	if frameID == "" || parentID != "" {
		return false
	}
	if target.FrameID != "" && target.FrameID != frameID {
		return false
	}
	if target.LoaderID != "" && loaderID != "" && target.LoaderID != loaderID {
		return false
	}
	return true
}
