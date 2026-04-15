package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"brosdk-mcp/internal/cdp"
)

func (e *Executor) callClick(ctx context.Context, args map[string]any) (map[string]any, error) {
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

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	waitUntil := resolveWaitUntil(args)
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)
	waitForNavigation := func() error { return nil }
	if waitUntil != "none" {
		waitForNavigation = e.prepareNavigationWait(ctx, pageClient, waitUntil, timeoutMs, false, navigationTarget{})
	}

	okClick, nativeErr := e.clickSelectorNative(ctx, pageClient, selector)
	if !okClick {
		if e.lowInjection {
			if nativeErr != nil {
				return nil, fmt.Errorf("click selector failed in low-injection mode: %w", nativeErr)
			}
			return nil, fmt.Errorf("click selector failed in low-injection mode: selector not found or not clickable")
		}
		okClick, err = e.evaluateBool(ctx, pageClient, buildClickSelectorExpression(selector))
		if err != nil {
			if nativeErr != nil {
				return nil, fmt.Errorf("click selector failed: cdp path: %v; js path: %w", nativeErr, err)
			}
			return nil, fmt.Errorf("click selector failed: %w", err)
		}
		if !okClick {
			return nil, fmt.Errorf("click selector did not find clickable element: %s", selector)
		}
	}

	if err := waitForNavigation(); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callClickByRef(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	ref, ok := getStringArg(args, "ref")
	if !ok || strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("missing required argument ref")
	}
	ref = strings.TrimSpace(ref)

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	waitUntil := resolveWaitUntil(args)
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)
	waitForNavigation := func() error { return nil }
	if waitUntil != "none" {
		waitForNavigation = e.prepareNavigationWait(ctx, pageClient, waitUntil, timeoutMs, false, navigationTarget{})
	}

	okClick, err := e.clickRefWithFallback(ctx, pageClient, currentTabID, ref)
	if err != nil {
		return nil, fmt.Errorf("click ref failed: %w", err)
	}
	if !okClick {
		return nil, fmt.Errorf("click ref did not find element: %s", ref)
	}

	if err := waitForNavigation(); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callType(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	selector, ok := getStringArg(args, "selector")
	if !ok || strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("missing required argument selector")
	}
	text, ok := getStringArg(args, "text")
	if !ok {
		return nil, fmt.Errorf("missing required argument text")
	}

	clear := false
	if b, ok, err := getBoolArg(args, "clear"); err != nil {
		return nil, err
	} else if ok {
		clear = b
	}
	humanDelayMs := getIntArgDefault(args, "humanDelayMs", 0)
	if humanDelayMs < 0 {
		humanDelayMs = 0
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	okFocus, err := e.focusSelectorNative(ctx, pageClient, strings.TrimSpace(selector))
	if err != nil && e.logger != nil {
		e.logger.Debug("type selector native focus failed, fallback to js", "selector", selector, "error", err)
	}
	if !okFocus {
		if e.lowInjection {
			return nil, fmt.Errorf("focus selector failed in low-injection mode: %s", selector)
		}
		okFocus, err = e.evaluateBool(ctx, pageClient, buildFocusSelectorExpression(strings.TrimSpace(selector), clear))
		if err != nil {
			return nil, fmt.Errorf("focus selector failed: %w", err)
		}
		if !okFocus {
			return nil, fmt.Errorf("focus selector not found: %s", selector)
		}
	}
	if clear {
		if err := e.clearActiveElementNative(ctx, pageClient); err != nil {
			if e.lowInjection {
				return nil, fmt.Errorf("clear active element failed in low-injection mode: %w", err)
			}
			okClear, clearErr := e.evaluateBool(ctx, pageClient, buildFocusSelectorExpression(strings.TrimSpace(selector), true))
			if clearErr != nil {
				return nil, fmt.Errorf("clear selector failed: native=%v js=%w", err, clearErr)
			}
			if !okClear {
				return nil, fmt.Errorf("clear selector failed: %s", selector)
			}
		}
	}

	if err := e.insertText(ctx, pageClient, text, humanDelayMs); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callTypeByRef(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	ref, ok := getStringArg(args, "ref")
	if !ok || strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("missing required argument ref")
	}
	text, ok := getStringArg(args, "text")
	if !ok {
		return nil, fmt.Errorf("missing required argument text")
	}

	clear := false
	if b, ok, err := getBoolArg(args, "clear"); err != nil {
		return nil, err
	} else if ok {
		clear = b
	}
	humanDelayMs := getIntArgDefault(args, "humanDelayMs", 0)
	if humanDelayMs < 0 {
		humanDelayMs = 0
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	actionClient, okFocus, err := e.focusRefWithFallback(ctx, pageClient, currentTabID, strings.TrimSpace(ref), false)
	if err != nil {
		return nil, fmt.Errorf("focus ref failed: %w", err)
	}
	if !okFocus {
		return nil, fmt.Errorf("focus ref not found: %s", ref)
	}
	if actionClient == nil {
		actionClient = pageClient
	}
	if actionClient != pageClient {
		defer actionClient.Close()
	}
	if clear {
		if err := e.clearActiveElementNative(ctx, actionClient); err != nil {
			if e.lowInjection {
				return nil, fmt.Errorf("clear ref failed in low-injection mode: %w", err)
			}
			okClear, clearErr := e.evaluateBool(ctx, actionClient, buildFocusRefExpression(strings.TrimSpace(ref), true))
			if clearErr != nil {
				return nil, fmt.Errorf("clear ref failed: native=%v js=%w", err, clearErr)
			}
			if !okClear {
				return nil, fmt.Errorf("clear ref failed: %s", ref)
			}
		}
	}

	if err := e.insertText(ctx, actionClient, text, humanDelayMs); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callSetInputValue(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	selector, ok := getStringArg(args, "selector")
	if !ok || strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("missing required argument selector")
	}
	value, ok := getStringArg(args, "value")
	if !ok {
		return nil, fmt.Errorf("missing required argument value")
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	okSet, err := e.evaluateBool(ctx, pageClient, buildSetValueSelectorExpression(strings.TrimSpace(selector), value))
	if err != nil {
		return nil, fmt.Errorf("set input value failed: %w", err)
	}
	if !okSet {
		return nil, fmt.Errorf("selector not found: %s", selector)
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callSetInputValueByRef(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	ref, ok := getStringArg(args, "ref")
	if !ok || strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("missing required argument ref")
	}
	value, ok := getStringArg(args, "value")
	if !ok {
		return nil, fmt.Errorf("missing required argument value")
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	okSet, err := e.setValueRefWithFallback(ctx, pageClient, currentTabID, strings.TrimSpace(ref), value)
	if err != nil {
		return nil, fmt.Errorf("set input value by ref failed: %w", err)
	}
	if !okSet {
		return nil, fmt.Errorf("ref not found: %s", ref)
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callFindAndClickText(ctx context.Context, args map[string]any) (map[string]any, error) {
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

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}
	if e.lowInjection {
		return nil, fmt.Errorf("browser_find_and_click_text is disabled in low-injection mode")
	}

	timeoutMs := getIntArgDefault(args, "timeoutMs", 15000)
	waitUntil := resolveWaitUntil(args)
	waitForNavigation := func() error { return nil }
	if waitUntil != "none" {
		waitForNavigation = e.prepareNavigationWait(ctx, pageClient, waitUntil, timeoutMs, false, navigationTarget{})
	}

	okClick, err := e.waitUntilCondition(ctx, timeoutMs, 150, func(c context.Context) (bool, error) {
		return e.evaluateBool(c, pageClient, buildFindAndClickTextExpression(text, exact))
	})
	if err != nil {
		return nil, err
	}
	if !okClick {
		return nil, fmt.Errorf("text not found for click: %s", text)
	}

	if err := waitForNavigation(); err != nil {
		return nil, err
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callGetText(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	selector := ""
	if s, ok := getStringArg(args, "selector"); ok {
		selector = strings.TrimSpace(s)
	}
	maxChars := getIntArgDefault(args, "maxChars", 50000)
	if maxChars <= 0 {
		maxChars = 50000
	}

	expression := `(function(sel, limit) {
  ` + buildDOMSearchHelpers() + `
  function pickText(el) {
    if (!el) return '';
    const raw = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
    if (raw.length > limit) return raw.slice(0, limit);
    return raw;
  }
  if (sel && sel.trim()) {
    return pickText(findFirstDeep(sel, document));
  }
  return pickText(document.body);
})`

	raw, err := e.callPageClient(ctx, pageClient, "Runtime.callFunctionOn", map[string]any{
		"functionDeclaration": expression,
		"executionContextId":  0,
		"arguments": []map[string]any{
			{"value": selector},
			{"value": maxChars},
		},
		"returnByValue": true,
	})
	if err != nil {
		// Fallback to evaluate for compatibility.
		text, evalErr := e.evaluateString(ctx, pageClient, buildGetTextExpression(selector, maxChars))
		if evalErr != nil {
			return nil, fmt.Errorf("get text failed (callFunctionOn: %v, evaluate: %w)", err, evalErr)
		}
		return map[string]any{"text": text, "tabId": currentTabID}, nil
	}

	text, err := extractRuntimeString(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{"text": text, "tabId": currentTabID}, nil
}

func (e *Executor) callAriaSnapshot(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	selector := ""
	if v, ok := getStringArg(args, "selector"); ok {
		selector = strings.TrimSpace(v)
	}

	interactive := true
	if v, ok, err := getBoolArg(args, "interactive"); err != nil {
		return nil, err
	} else if ok {
		interactive = v
	}

	compact := true
	if v, ok, err := getBoolArg(args, "compact"); err != nil {
		return nil, err
	} else if ok {
		compact = v
	}

	maxDepth := getIntArgDefault(args, "maxDepth", 24)
	if maxDepth < 1 {
		maxDepth = 1
	}
	if maxDepth > 64 {
		maxDepth = 64
	}

	source := "dom"
	var snapshot string
	refCount := 0

	// AX-tree-first path: prefer CDP Accessibility tree for stronger semantics.
	// For unscoped snapshots use getFullAXTree (with iframe merging).
	// For selector-scoped snapshots use queryAXTree via backendNodeId.
	if selector == "" {
		if axSnapshot, axMeta, err := e.tryBuildAXSnapshot(ctx, pageClient, interactive, compact, maxDepth); err == nil {
			snapshot = axSnapshot
			refCount = len(axMeta)
			source = "ax"
			e.storeAriaRefMeta(currentTabID, axMeta)
		} else if e.logger != nil {
			e.logger.Debug("aria snapshot: ax path failed, fallback to dom", "error", err)
		}
	} else {
		if axSnapshot, axMeta, err := e.tryBuildAXSnapshotForSelector(ctx, pageClient, selector, interactive, compact, maxDepth); err == nil {
			snapshot = axSnapshot
			refCount = len(axMeta)
			source = "ax"
			e.storeAriaRefMeta(currentTabID, axMeta)
		} else if e.logger != nil {
			e.logger.Debug("aria snapshot selector: ax path failed, fallback to dom", "error", err)
		}
	}

	if strings.TrimSpace(snapshot) == "" {
		if e.lowInjection {
			return nil, fmt.Errorf("aria snapshot AX path unavailable in low-injection mode; DOM JS fallback is disabled")
		}
		e.clearStoredAriaRefMeta(currentTabID)
		expression := buildAriaSnapshotExpression(selector, interactive, compact, maxDepth)
		value, err := e.evaluateString(ctx, pageClient, expression)
		if err != nil {
			return nil, fmt.Errorf("Runtime.evaluate aria snapshot failed: %w", err)
		}
		snapshot = value
		refCount = estimateRefCount(snapshot)
	}

	return map[string]any{
		"ok":          true,
		"snapshot":    snapshot,
		"tabId":       currentTabID,
		"interactive": interactive,
		"compact":     compact,
		"maxDepth":    maxDepth,
		"source":      source,
		"refCount":    refCount,
	}, nil
}

func (e *Executor) insertText(ctx context.Context, pageClient *cdp.Client, text string, humanDelayMs int) error {
	if humanDelayMs <= 0 {
		if _, err := e.callPageClient(ctx, pageClient, "Input.insertText", map[string]any{"text": text}); err == nil {
			return nil
		}
		_, err := e.evaluateBool(ctx, pageClient, buildAppendToActiveElementExpression(text))
		return err
	}

	for _, r := range text {
		if _, err := e.callPageClient(ctx, pageClient, "Input.insertText", map[string]any{"text": string(r)}); err != nil {
			_, fallbackErr := e.evaluateBool(ctx, pageClient, buildAppendToActiveElementExpression(string(r)))
			if fallbackErr != nil {
				return fmt.Errorf("Input.insertText failed: %w", err)
			}
		}
		time.Sleep(time.Duration(humanDelayMs) * time.Millisecond)
	}
	return nil
}
