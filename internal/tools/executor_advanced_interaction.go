package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"brosdk-mcp/internal/cdp"
)

type selectOptionCriteria struct {
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
	Index *int   `json:"index,omitempty"`
}

type dialogEventPayload struct {
	URL               string `json:"url"`
	Message           string `json:"message"`
	Type              string `json:"type"`
	DefaultPrompt     string `json:"defaultPrompt"`
	HasBrowserHandler bool   `json:"hasBrowserHandler"`
}

func (e *Executor) callHover(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	selector, ok := getStringArg(args, "selector")
	if !ok || strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("missing required argument selector")
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	nodeID, err := e.resolveSelectorNodeID(ctx, pageClient, strings.TrimSpace(selector))
	if err != nil {
		return nil, err
	}
	if err := e.hoverNodeByID(ctx, pageClient, nodeID); err != nil {
		return nil, fmt.Errorf("hover selector failed: %w", err)
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callHoverByRef(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	ref, ok := getStringArg(args, "ref")
	if !ok || strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("missing required argument ref")
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	okHover, err := e.hoverRefWithMeta(ctx, pageClient, currentTabID, strings.TrimSpace(ref))
	if err != nil {
		return nil, fmt.Errorf("hover ref failed: %w", err)
	}
	if !okHover {
		return nil, fmt.Errorf("hover ref did not find element: %s", strings.TrimSpace(ref))
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callSelectOption(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	selector, ok := getStringArg(args, "selector")
	if !ok || strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("missing required argument selector")
	}
	criteria, err := resolveSelectOptionCriteria(args)
	if err != nil {
		return nil, err
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	nodeID, err := e.resolveSelectorNodeID(ctx, pageClient, strings.TrimSpace(selector))
	if err != nil {
		return nil, err
	}
	fn, err := buildSelectOptionFunction(criteria)
	if err != nil {
		return nil, err
	}
	okSelect, err := e.callFunctionOnNodeID(ctx, pageClient, nodeID, fn)
	if err != nil {
		return nil, fmt.Errorf("select option failed: %w", err)
	}
	if !okSelect {
		return nil, fmt.Errorf("select option did not match any option for selector %s", strings.TrimSpace(selector))
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callSelectOptionByRef(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	ref, ok := getStringArg(args, "ref")
	if !ok || strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("missing required argument ref")
	}
	criteria, err := resolveSelectOptionCriteria(args)
	if err != nil {
		return nil, err
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	meta, err := e.resolveRefMetaForAction(ctx, pageClient, currentTabID, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	fn, err := buildSelectOptionFunction(criteria)
	if err != nil {
		return nil, err
	}
	okSelect, err := e.callRefActionByBackendNode(ctx, pageClient, meta, fn)
	if err != nil {
		return nil, fmt.Errorf("select option by ref failed: %w", err)
	}
	if !okSelect {
		return nil, fmt.Errorf("select option by ref did not match any option for ref %s", strings.TrimSpace(ref))
	}

	return map[string]any{"ok": true, "tabId": currentTabID}, nil
}

func (e *Executor) callSetFileInputFiles(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	selector, ok := getStringArg(args, "selector")
	if !ok || strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("missing required argument selector")
	}
	paths, err := resolveFileInputPaths(args)
	if err != nil {
		return nil, err
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	nodeID, err := e.resolveSelectorNodeID(ctx, pageClient, strings.TrimSpace(selector))
	if err != nil {
		return nil, err
	}
	if err := e.setNodeFileInputFiles(ctx, pageClient, nodeID, paths); err != nil {
		return nil, fmt.Errorf("set file input files failed: %w", err)
	}

	return map[string]any{
		"ok":        true,
		"tabId":     currentTabID,
		"fileCount": len(paths),
		"paths":     paths,
	}, nil
}

func (e *Executor) callSetFileInputFilesByRef(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	ref, ok := getStringArg(args, "ref")
	if !ok || strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("missing required argument ref")
	}
	paths, err := resolveFileInputPaths(args)
	if err != nil {
		return nil, err
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	meta, err := e.resolveRefMetaForAction(ctx, pageClient, currentTabID, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	if err := e.setRefFileInputFiles(ctx, pageClient, meta, paths); err != nil {
		return nil, fmt.Errorf("set file input files by ref failed: %w", err)
	}

	return map[string]any{
		"ok":        true,
		"tabId":     currentTabID,
		"fileCount": len(paths),
		"paths":     paths,
	}, nil
}

func (e *Executor) callWaitForDialog(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)
	payload, err := waitForDialogEvent(ctx, pageClient, timeoutMs)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ok":            true,
		"tabId":         currentTabID,
		"type":          payload.Type,
		"message":       payload.Message,
		"defaultPrompt": payload.DefaultPrompt,
		"url":           payload.URL,
	}, nil
}

func (e *Executor) callHandleDialog(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	accept := true
	if v, ok, err := getBoolArg(args, "accept"); err != nil {
		return nil, err
	} else if ok {
		accept = v
	}
	promptText, _ := getStringArg(args, "promptText")
	timeoutMs := getIntArgDefault(args, "timeoutMs", 30000)

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	err = e.handleDialogNow(ctx, pageClient, accept, promptText)
	if err == nil {
		return map[string]any{
			"ok":         true,
			"tabId":      currentTabID,
			"accept":     accept,
			"promptText": promptText,
			"handled":    true,
			"waited":     false,
		}, nil
	}
	if !isNoDialogOpenError(err) {
		return nil, err
	}

	payload, waitErr := waitForDialogEvent(ctx, pageClient, timeoutMs)
	if waitErr != nil {
		return nil, waitErr
	}
	if err := e.handleDialogNow(ctx, pageClient, accept, promptText); err != nil {
		return nil, err
	}

	return map[string]any{
		"ok":            true,
		"tabId":         currentTabID,
		"accept":        accept,
		"promptText":    promptText,
		"handled":       true,
		"waited":        true,
		"type":          payload.Type,
		"message":       payload.Message,
		"defaultPrompt": payload.DefaultPrompt,
		"url":           payload.URL,
	}, nil
}

func resolveSelectOptionCriteria(args map[string]any) (selectOptionCriteria, error) {
	var criteria selectOptionCriteria
	if value, ok := getStringArg(args, "value"); ok {
		criteria.Value = strings.TrimSpace(value)
	}
	if label, ok := getStringArg(args, "label"); ok {
		criteria.Label = strings.TrimSpace(label)
	}
	if index, ok, err := getIntArg(args, "index"); err != nil {
		return selectOptionCriteria{}, err
	} else if ok {
		if index < 0 {
			return selectOptionCriteria{}, fmt.Errorf("index must be >= 0")
		}
		criteria.Index = &index
	}
	if criteria.Value == "" && criteria.Label == "" && criteria.Index == nil {
		return selectOptionCriteria{}, fmt.Errorf("one of value, label, or index is required")
	}
	return criteria, nil
}

func buildSelectOptionFunction(criteria selectOptionCriteria) (string, error) {
	payload, err := json.Marshal(criteria)
	if err != nil {
		return "", fmt.Errorf("marshal select option criteria: %w", err)
	}
	return `(function() {
  var criteria = ` + string(payload) + `;
  var el = this;
  if (!el || String(el.tagName || '').toLowerCase() !== 'select') return false;
  var opts = Array.prototype.slice.call(el.options || []);
  var targetIndex = -1;
  if (criteria.index !== null && criteria.index !== undefined) {
    if (criteria.index < 0 || criteria.index >= opts.length) return false;
    targetIndex = criteria.index;
  } else if (criteria.value) {
    targetIndex = opts.findIndex(function(opt) { return opt.value === criteria.value; });
  } else if (criteria.label) {
    targetIndex = opts.findIndex(function(opt) { return (opt.label || opt.text || '').trim() === criteria.label; });
  }
  if (targetIndex < 0) return false;
  el.selectedIndex = targetIndex;
  if (opts[targetIndex]) {
    opts[targetIndex].selected = true;
  }
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return true;
})`, nil
}

func resolveFileInputPaths(args map[string]any) ([]string, error) {
	if path, ok := getStringArg(args, "path"); ok && strings.TrimSpace(path) != "" {
		return normalizeFileInputPaths([]string{path})
	}
	paths, ok, err := getStringSliceArg(args, "paths")
	if err != nil {
		return nil, err
	}
	if !ok || len(paths) == 0 {
		return nil, fmt.Errorf("one of path or paths is required")
	}
	return normalizeFileInputPaths(paths)
}

func normalizeFileInputPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one file path is required")
	}
	out := make([]string, 0, len(paths))
	for _, item := range paths {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("file paths must not be empty")
		}
		abs, err := filepath.Abs(item)
		if err != nil {
			return nil, fmt.Errorf("resolve file path %q: %w", item, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("stat file path %q: %w", abs, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("file path %q is a directory", abs)
		}
		out = append(out, abs)
	}
	return out, nil
}

func (e *Executor) resolveNodeObject(ctx context.Context, pageClient *cdp.Client, nodeID int64) (string, error) {
	raw, err := e.callPageClient(ctx, pageClient, "DOM.resolveNode", map[string]any{
		"nodeId": nodeID,
	})
	if err != nil {
		return "", err
	}

	var payload struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decode DOM.resolveNode response: %w", err)
	}
	if strings.TrimSpace(payload.Object.ObjectID) == "" {
		return "", fmt.Errorf("DOM.resolveNode returned empty objectId for nodeId %d", nodeID)
	}
	return strings.TrimSpace(payload.Object.ObjectID), nil
}

func (e *Executor) callFunctionOnNodeID(ctx context.Context, pageClient *cdp.Client, nodeID int64, functionDeclaration string) (bool, error) {
	objectID, err := e.resolveNodeObject(ctx, pageClient, nodeID)
	if err != nil {
		return false, err
	}

	raw, err := e.callPageClient(ctx, pageClient, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": functionDeclaration,
		"returnByValue":       true,
		"awaitPromise":        true,
	})
	if err != nil {
		return false, err
	}

	result, err := decodeRuntimeEvaluateResult(raw)
	if err != nil {
		return false, err
	}
	switch v := result.Value.(type) {
	case bool:
		return v, nil
	case string:
		return strings.EqualFold(v, "true"), nil
	case float64:
		return v != 0, nil
	default:
		return false, nil
	}
}

func (e *Executor) hoverNodeByID(ctx context.Context, pageClient *cdp.Client, nodeID int64) error {
	if nodeID <= 0 {
		return fmt.Errorf("invalid node id")
	}
	_, _ = e.callPageClient(ctx, pageClient, "DOM.scrollIntoViewIfNeeded", map[string]any{"nodeId": nodeID})
	x, y, err := e.nodeCenterByNodeID(ctx, pageClient, nodeID)
	if err != nil {
		return err
	}
	return e.dispatchMouseMove(ctx, pageClient, x, y)
}

func (e *Executor) hoverBackendNode(ctx context.Context, pageClient *cdp.Client, backendNodeID int64) error {
	if backendNodeID <= 0 {
		return fmt.Errorf("invalid backend node id")
	}
	_, _ = e.callPageClient(ctx, pageClient, "DOM.scrollIntoViewIfNeeded", map[string]any{"backendNodeId": backendNodeID})
	x, y, err := e.nodeCenterByBackendID(ctx, pageClient, backendNodeID)
	if err != nil {
		return err
	}
	return e.dispatchMouseMove(ctx, pageClient, x, y)
}

func (e *Executor) dispatchMouseMove(ctx context.Context, pageClient *cdp.Client, x float64, y float64) error {
	_, err := e.callPageClient(ctx, pageClient, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved",
		"x":    x,
		"y":    y,
	})
	if err != nil {
		return fmt.Errorf("mouseMoved: %w", err)
	}
	return nil
}

func (e *Executor) hoverRefWithMeta(ctx context.Context, pageClient *cdp.Client, tabID string, ref string) (bool, error) {
	meta, err := e.resolveRefMetaForAction(ctx, pageClient, tabID, ref)
	if err != nil {
		return false, err
	}
	candidates, closers, err := e.refActionClients(ctx, pageClient, meta)
	if err != nil {
		return false, err
	}
	defer closeRefActionClients(closers, nil)

	for _, client := range candidates {
		if err := e.hoverBackendNode(ctx, client, meta.BackendNodeID); err == nil {
			return true, nil
		}
	}
	return false, nil
}

func (e *Executor) resolveRefMetaForAction(ctx context.Context, pageClient *cdp.Client, tabID string, ref string) (*ariaRefMeta, error) {
	if meta, ok := e.getStoredAriaRefMeta(strings.TrimSpace(tabID), strings.TrimSpace(ref)); ok && meta != nil && meta.BackendNodeID > 0 {
		return meta, nil
	}
	if e.lowInjection {
		return nil, fmt.Errorf("ref %s not found in server-side cache; run browser_aria_snapshot first", strings.TrimSpace(ref))
	}
	meta, err := e.getAriaRefMeta(ctx, pageClient, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	if meta == nil || meta.BackendNodeID <= 0 {
		return nil, fmt.Errorf("ref %s did not resolve to an actionable backend node", strings.TrimSpace(ref))
	}
	return meta, nil
}

func (e *Executor) setNodeFileInputFiles(ctx context.Context, pageClient *cdp.Client, nodeID int64, paths []string) error {
	_, err := e.callPageClient(ctx, pageClient, "DOM.setFileInputFiles", map[string]any{
		"nodeId": nodeID,
		"files":  paths,
	})
	if err != nil {
		return err
	}
	return nil
}

func (e *Executor) setRefFileInputFiles(ctx context.Context, pageClient *cdp.Client, meta *ariaRefMeta, paths []string) error {
	if meta == nil || meta.BackendNodeID <= 0 {
		return fmt.Errorf("ref metadata missing backend node id")
	}
	candidates, closers, err := e.refActionClients(ctx, pageClient, meta)
	if err != nil {
		return err
	}
	defer closeRefActionClients(closers, nil)

	for _, client := range candidates {
		if _, callErr := e.callPageClient(ctx, client, "DOM.setFileInputFiles", map[string]any{
			"backendNodeId": meta.BackendNodeID,
			"files":         paths,
		}); callErr == nil {
			return nil
		}
	}
	return fmt.Errorf("DOM.setFileInputFiles failed for backend node %d", meta.BackendNodeID)
}

func waitForDialogEvent(ctx context.Context, pageClient *cdp.Client, timeoutMs int) (*dialogEventPayload, error) {
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	ev, err := pageClient.WaitForEvent(waitCtx, "Page.javascriptDialogOpening", nil)
	if err != nil {
		return nil, fmt.Errorf("wait for dialog: %w", err)
	}
	var payload dialogEventPayload
	if err := json.Unmarshal(ev.Params, &payload); err != nil {
		return nil, fmt.Errorf("decode dialog event: %w", err)
	}
	return &payload, nil
}

func (e *Executor) handleDialogNow(ctx context.Context, pageClient *cdp.Client, accept bool, promptText string) error {
	_, err := e.callPageClient(ctx, pageClient, "Page.handleJavaScriptDialog", map[string]any{
		"accept":     accept,
		"promptText": promptText,
	})
	return err
}

func isNoDialogOpenError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no dialog is showing") || strings.Contains(msg, "javascript dialog") || strings.Contains(msg, "dialog is not open")
}
