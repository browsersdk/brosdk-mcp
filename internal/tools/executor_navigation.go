package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"brosdk-mcp/internal/cdp"
)

type navigationHistoryEntry struct {
	ID                int64  `json:"id"`
	URL               string `json:"url"`
	UserTypedURL      string `json:"userTypedURL"`
	Title             string `json:"title"`
	TransitionType    string `json:"transitionType"`
}

func (e *Executor) callNavigate(ctx context.Context, args map[string]any) (map[string]any, error) {
	urlValue, ok := args["url"]
	if !ok {
		return nil, fmt.Errorf("missing required argument url")
	}
	urlStr, ok := urlValue.(string)
	if !ok || strings.TrimSpace(urlStr) == "" {
		return nil, fmt.Errorf("argument url must be a non-empty string")
	}
	urlStr = strings.TrimSpace(urlStr)

	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	pageClient, _, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	waitUntil := "load"
	if v, ok := getStringArg(args, "waitUntil"); ok && strings.TrimSpace(v) != "" {
		waitUntil = v
	}
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)

	params := map[string]any{"url": urlStr}
	raw, err := e.callPageClient(ctx, pageClient, "Page.navigate", params)
	if err != nil {
		return nil, fmt.Errorf("Page.navigate failed: %w", err)
	}
	navTarget, err := decodePageNavigateResult(raw)
	if err != nil {
		return nil, err
	}

	waitForNavigation := func() error { return nil }
	if normalizeWaitUntil(waitUntil) != "none" {
		waitForNavigation = e.prepareNavigationWait(ctx, pageClient, waitUntil, timeoutMs, true, navTarget)
	}

	if err := waitForNavigation(); err != nil {
		return nil, err
	}

	_, currentTabID, _ := e.getCurrentPageClient(ctx)
	e.clearStoredAriaRefMeta(currentTabID)
	return map[string]any{
		"ok":        true,
		"frameId":   navTarget.FrameID,
		"loaderId":  navTarget.LoaderID,
		"url":       urlStr,
		"tabId":     currentTabID,
		"waitUntil": normalizeWaitUntil(waitUntil),
	}, nil
}

func (e *Executor) callReload(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	waitUntil := "load"
	if v, ok := getStringArg(args, "waitUntil"); ok && strings.TrimSpace(v) != "" {
		waitUntil = v
	}
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)
	ignoreCache := false
	if v, ok, err := getBoolArg(args, "ignoreCache"); err != nil {
		return nil, err
	} else if ok {
		ignoreCache = v
	}

	waitForNavigation := func() error { return nil }
	if normalizeWaitUntil(waitUntil) != "none" {
		waitForNavigation = e.prepareNavigationWait(ctx, pageClient, waitUntil, timeoutMs, true, navigationTarget{})
	}

	if _, err := e.callPageClient(ctx, pageClient, "Page.reload", map[string]any{"ignoreCache": ignoreCache}); err != nil {
		return nil, fmt.Errorf("Page.reload failed: %w", err)
	}
	if err := waitForNavigation(); err != nil {
		return nil, err
	}

	e.clearStoredAriaRefMeta(currentTabID)
	return map[string]any{
		"ok":          true,
		"tabId":       currentTabID,
		"ignoreCache": ignoreCache,
		"waitUntil":   normalizeWaitUntil(waitUntil),
	}, nil
}

func (e *Executor) callGoBack(ctx context.Context, args map[string]any) (map[string]any, error) {
	return e.callHistoryNavigation(ctx, args, -1)
}

func (e *Executor) callGoForward(ctx context.Context, args map[string]any) (map[string]any, error) {
	return e.callHistoryNavigation(ctx, args, 1)
}

func (e *Executor) callHistoryNavigation(ctx context.Context, args map[string]any, delta int) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	waitUntil := "load"
	if v, ok := getStringArg(args, "waitUntil"); ok && strings.TrimSpace(v) != "" {
		waitUntil = v
	}
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)

	entry, err := e.resolveHistoryEntry(ctx, pageClient, delta)
	if err != nil {
		return nil, err
	}

	waitForNavigation := func() error { return nil }
	if normalizeWaitUntil(waitUntil) != "none" {
		waitForNavigation = e.prepareNavigationWait(ctx, pageClient, waitUntil, timeoutMs, true, navigationTarget{})
	}

	if _, err := e.callPageClient(ctx, pageClient, "Page.navigateToHistoryEntry", map[string]any{"entryId": entry.ID}); err != nil {
		return nil, fmt.Errorf("Page.navigateToHistoryEntry failed: %w", err)
	}
	if err := waitForNavigation(); err != nil {
		return nil, err
	}

	e.clearStoredAriaRefMeta(currentTabID)
	return map[string]any{
		"ok":        true,
		"tabId":     currentTabID,
		"url":       firstNonEmptyString(entry.URL, entry.UserTypedURL),
		"title":     entry.Title,
		"entryId":   entry.ID,
		"waitUntil": normalizeWaitUntil(waitUntil),
	}, nil
}

func (e *Executor) resolveHistoryEntry(ctx context.Context, pageClient *cdp.Client, delta int) (navigationHistoryEntry, error) {
	raw, err := e.callPageClient(ctx, pageClient, "Page.getNavigationHistory", nil)
	if err != nil {
		return navigationHistoryEntry{}, fmt.Errorf("Page.getNavigationHistory failed: %w", err)
	}

	var payload struct {
		CurrentIndex int                      `json:"currentIndex"`
		Entries      []navigationHistoryEntry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return navigationHistoryEntry{}, fmt.Errorf("decode Page.getNavigationHistory response: %w", err)
	}

	targetIndex := payload.CurrentIndex + delta
	if targetIndex < 0 || targetIndex >= len(payload.Entries) {
		switch {
		case delta < 0:
			return navigationHistoryEntry{}, fmt.Errorf("cannot go back: already at the first history entry")
		case delta > 0:
			return navigationHistoryEntry{}, fmt.Errorf("cannot go forward: already at the last history entry")
		default:
			return navigationHistoryEntry{}, fmt.Errorf("invalid history navigation delta %d", delta)
		}
	}
	return payload.Entries[targetIndex], nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type navigationTarget struct {
	FrameID  string
	LoaderID string
}

func (e *Executor) prepareNavigationWait(ctx context.Context, pageClient *cdp.Client, waitUntil string, timeoutMs int, requireMainFrameNavigation bool, navTarget navigationTarget) func() error {
	waitUntil = normalizeWaitUntil(waitUntil)
	if waitUntil == "none" {
		return func() error { return nil }
	}

	if timeoutMs <= 0 {
		timeoutMs = 30000
	}

	sub := loadEventSubscription(waitUntil)
	if sub.method == "" {
		return func() error {
			waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
			defer cancel()
			return e.waitForLoadState(waitCtx, pageClient, waitUntil, timeoutMs)
		}
	}

	eventMatch := sub.match
	if normalizeWaitUntil(waitUntil) == "networkidle" {
		eventMatch = func(ev cdp.Event) bool {
			if sub.match != nil && !sub.match(ev) {
				return false
			}
			return isMatchingLifecycleEventForNavigation(ev, navTarget)
		}
	}

	events, unsubscribe := pageClient.SubscribeEvents(sub.method, 8)
	var navEvents <-chan cdp.Event
	var unsubscribeNav func()
	if requireMainFrameNavigation {
		navEvents, unsubscribeNav = pageClient.SubscribeEvents("Page.frameNavigated", 8)
	} else {
		unsubscribeNav = func() {}
	}
	return func() error {
		defer unsubscribe()
		defer unsubscribeNav()

		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()

		mainFrameNavigated := !requireMainFrameNavigation
		mainFrameNavigatedAt := time.Time{}
		if mainFrameNavigated {
			mainFrameNavigatedAt = time.Now()
		}
		requireLoadEvent := normalizeWaitUntil(waitUntil) == "networkidle" && requireMainFrameNavigation
		loadEventMatched := !requireLoadEvent
		sawLoadEvent := false
		for {
			ok, err := e.checkLoadState(waitCtx, e.activePageClient(pageClient), waitUntil)
			if err == nil && ok && mainFrameNavigated && loadEventMatched {
				return nil
			}
			if err == nil && ok && shouldFallbackNetworkIdleWithoutLifecycle(waitUntil, requireMainFrameNavigation, mainFrameNavigated, loadEventMatched, sawLoadEvent, mainFrameNavigatedAt, time.Now()) {
				return nil
			}

			select {
			case <-waitCtx.Done():
				if err != nil {
					return fmt.Errorf("wait for navigation %s timed out: %w", waitUntil, err)
				}
				return waitCtx.Err()
			case ev, ok := <-navEvents:
				if !ok {
					return e.waitForLoadState(waitCtx, e.activePageClient(pageClient), waitUntil, remainingTimeoutMs(waitCtx, timeoutMs))
				}
				if isMatchingMainFrameNavigatedEvent(ev, navTarget) {
					mainFrameNavigated = true
					if mainFrameNavigatedAt.IsZero() {
						mainFrameNavigatedAt = time.Now()
					}
				}
			case ev, ok := <-events:
				if !ok {
					return e.waitForLoadState(waitCtx, e.activePageClient(pageClient), waitUntil, remainingTimeoutMs(waitCtx, timeoutMs))
				}
				sawLoadEvent = true
				if eventMatch != nil && !eventMatch(ev) {
					continue
				}
				loadEventMatched = true
				if mainFrameNavigated {
					ok, err := e.checkLoadState(waitCtx, e.activePageClient(pageClient), waitUntil)
					if err == nil && ok {
						return nil
					}
				}
			}
		}
	}
}

func remainingTimeoutMs(ctx context.Context, defaultMs int) int {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := int(time.Until(deadline).Milliseconds())
		if remaining <= 0 {
			return 1
		}
		return remaining
	}
	if defaultMs > 0 {
		return defaultMs
	}
	return 30000
}

func decodePageNavigateResult(raw json.RawMessage) (navigationTarget, error) {
	var payload struct {
		FrameID   string `json:"frameId"`
		LoaderID  string `json:"loaderId"`
		ErrorText string `json:"errorText"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return navigationTarget{}, fmt.Errorf("decode Page.navigate response: %w", err)
	}
	if strings.TrimSpace(payload.ErrorText) != "" {
		return navigationTarget{}, fmt.Errorf("Page.navigate returned errorText: %s", strings.TrimSpace(payload.ErrorText))
	}
	return navigationTarget{
		FrameID:  strings.TrimSpace(payload.FrameID),
		LoaderID: strings.TrimSpace(payload.LoaderID),
	}, nil
}
