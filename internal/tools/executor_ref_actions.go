package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"brosdk-mcp/internal/cdp"
)

type ariaRefMeta struct {
	Role          string `json:"role"`
	Name          string `json:"name"`
	Nth           int    `json:"nth"`
	BackendNodeID int64  `json:"backendNodeId"`
	FrameID       string `json:"frameId"`
}

func (e *Executor) clickRefWithFallback(ctx context.Context, pageClient *cdp.Client, tabID string, ref string) (bool, error) {
	ref = strings.TrimSpace(ref)
	tabID = strings.TrimSpace(tabID)

	if meta, ok := e.getStoredAriaRefMeta(tabID, ref); ok {
		if okNative, err := e.clickRefNativeByMeta(ctx, pageClient, meta); err == nil && okNative {
			return true, nil
		}
		if e.lowInjection {
			return false, fmt.Errorf("ref %s is stale in low-injection mode; refresh aria snapshot", ref)
		}
		okMeta, metaErr := e.callRefActionWithFallback(ctx, pageClient, meta, buildCallFunctionOnClickExpression(), buildClickMetaExpression(meta))
		if metaErr == nil && okMeta {
			return true, nil
		}
		if metaErr != nil {
			return false, metaErr
		}
	}
	if e.lowInjection {
		return false, fmt.Errorf("ref %s not found in server-side cache; run browser_aria_snapshot first", ref)
	}

	ok, err := e.evaluateBool(ctx, pageClient, buildClickRefExpression(ref))
	if err == nil && ok {
		return true, nil
	}

	meta, metaErr := e.getAriaRefMeta(ctx, pageClient, ref)
	if metaErr != nil {
		if err != nil {
			return false, fmt.Errorf("click ref failed: js path: %w; meta path: %v", err, metaErr)
		}
		return false, metaErr
	}
	if meta == nil || meta.BackendNodeID <= 0 {
		if err != nil {
			return false, err
		}
		return false, nil
	}

	return e.callRefActionWithFallback(ctx, pageClient, meta, buildCallFunctionOnClickExpression(), buildClickMetaExpression(meta))
}

func (e *Executor) focusRefWithFallback(ctx context.Context, pageClient *cdp.Client, tabID string, ref string, clear bool) (*cdp.Client, bool, error) {
	ref = strings.TrimSpace(ref)
	tabID = strings.TrimSpace(tabID)

	if meta, ok := e.getStoredAriaRefMeta(tabID, ref); ok {
		if clientUsed, okNative, nativeErr := e.focusRefNativeByMeta(ctx, pageClient, meta); nativeErr == nil && okNative {
			return clientUsed, true, nil
		}
		if e.lowInjection {
			return nil, false, fmt.Errorf("ref %s is stale in low-injection mode; refresh aria snapshot", ref)
		}
		clientUsed, okMeta, metaErr := e.callRefActionWithFallbackWithClient(ctx, pageClient, meta, buildCallFunctionOnFocusExpression(clear), buildFocusMetaExpression(meta, clear))
		if metaErr == nil && okMeta {
			return clientUsed, true, nil
		}
		if metaErr != nil {
			return nil, false, metaErr
		}
	}
	if e.lowInjection {
		return nil, false, fmt.Errorf("ref %s not found in server-side cache; run browser_aria_snapshot first", ref)
	}

	ok, err := e.evaluateBool(ctx, pageClient, buildFocusRefExpression(ref, clear))
	if err == nil && ok {
		return pageClient, true, nil
	}

	meta, metaErr := e.getAriaRefMeta(ctx, pageClient, ref)
	if metaErr != nil {
		if err != nil {
			return nil, false, fmt.Errorf("focus ref failed: js path: %w; meta path: %v", err, metaErr)
		}
		return nil, false, metaErr
	}
	if meta == nil || meta.BackendNodeID <= 0 {
		if err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	clientUsed, ok, callErr := e.callRefActionWithFallbackWithClient(ctx, pageClient, meta, buildCallFunctionOnFocusExpression(clear), buildFocusMetaExpression(meta, clear))
	if callErr != nil {
		return nil, false, callErr
	}
	return clientUsed, ok, nil
}

func (e *Executor) setValueRefWithFallback(ctx context.Context, pageClient *cdp.Client, tabID string, ref string, value string) (bool, error) {
	ref = strings.TrimSpace(ref)
	tabID = strings.TrimSpace(tabID)
	if e.lowInjection {
		return false, fmt.Errorf("browser_set_input_value_by_ref is disabled in low-injection mode")
	}

	if meta, ok := e.getStoredAriaRefMeta(tabID, ref); ok {
		okMeta, metaErr := e.callRefActionWithFallback(ctx, pageClient, meta, buildCallFunctionOnSetValueExpression(value), buildSetValueMetaExpression(meta, value))
		if metaErr == nil && okMeta {
			return true, nil
		}
		if metaErr != nil {
			return false, metaErr
		}
	}

	ok, err := e.evaluateBool(ctx, pageClient, buildSetValueRefExpression(ref, value))
	if err == nil && ok {
		return true, nil
	}

	meta, metaErr := e.getAriaRefMeta(ctx, pageClient, ref)
	if metaErr != nil {
		if err != nil {
			return false, fmt.Errorf("set value by ref failed: js path: %w; meta path: %v", err, metaErr)
		}
		return false, metaErr
	}
	if meta == nil || meta.BackendNodeID <= 0 {
		if err != nil {
			return false, err
		}
		return false, nil
	}

	return e.callRefActionWithFallback(ctx, pageClient, meta, buildCallFunctionOnSetValueExpression(value), buildSetValueMetaExpression(meta, value))
}

func (e *Executor) clickRefNativeByMeta(ctx context.Context, pageClient *cdp.Client, meta *ariaRefMeta) (bool, error) {
	if meta == nil || meta.BackendNodeID <= 0 {
		return false, nil
	}
	candidates, closers, err := e.refActionClients(ctx, pageClient, meta)
	if err != nil {
		return false, err
	}
	defer closeRefActionClients(closers, nil)

	for _, client := range candidates {
		if err := e.clickBackendNode(ctx, client, meta.BackendNodeID); err == nil {
			return true, nil
		}
	}
	return false, nil
}

func (e *Executor) focusRefNativeByMeta(ctx context.Context, pageClient *cdp.Client, meta *ariaRefMeta) (*cdp.Client, bool, error) {
	if meta == nil || meta.BackendNodeID <= 0 {
		return nil, false, nil
	}

	candidates, closers, err := e.refActionClients(ctx, pageClient, meta)
	if err != nil {
		return nil, false, err
	}

	for _, client := range candidates {
		if err := e.focusBackendNode(ctx, client, meta.BackendNodeID); err == nil {
			closeRefActionClients(closers, client)
			return client, true, nil
		}
	}
	closeRefActionClients(closers, nil)
	return nil, false, nil
}

func (e *Executor) getAriaRefMeta(ctx context.Context, pageClient *cdp.Client, ref string) (*ariaRefMeta, error) {
	refJSON, _ := json.Marshal(strings.TrimSpace(ref))
	expr := `(function(ref){
  var root = window.__ariaRefMeta || {};
  var meta = root[ref];
  if (!meta) return "";
  try { return JSON.stringify(meta); } catch (e) { return ""; }
})(` + string(refJSON) + `)`

	raw, err := e.evaluateString(ctx, pageClient, expr)
	if err != nil {
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}

	var meta ariaRefMeta
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil, fmt.Errorf("decode aria ref meta for %s: %w", ref, err)
	}
	meta.FrameID = strings.TrimSpace(meta.FrameID)
	return &meta, nil
}

func (e *Executor) callRefActionByBackendNode(ctx context.Context, pageClient *cdp.Client, meta *ariaRefMeta, functionDeclaration string) (bool, error) {
	_, ok, err := e.callRefActionByBackendNodeWithClient(ctx, pageClient, meta, functionDeclaration)
	return ok, err
}

func (e *Executor) callRefActionWithFallback(ctx context.Context, pageClient *cdp.Client, meta *ariaRefMeta, functionDeclaration string, evaluateExpression string) (bool, error) {
	_, ok, err := e.callRefActionWithFallbackWithClient(ctx, pageClient, meta, functionDeclaration, evaluateExpression)
	return ok, err
}

func (e *Executor) callRefActionWithFallbackWithClient(ctx context.Context, pageClient *cdp.Client, meta *ariaRefMeta, functionDeclaration string, evaluateExpression string) (*cdp.Client, bool, error) {
	clientUsed, ok, err := e.callRefActionByBackendNodeWithClient(ctx, pageClient, meta, functionDeclaration)
	if err != nil || ok {
		return clientUsed, ok, err
	}
	return e.callRefActionByMetaWithClient(ctx, pageClient, meta, evaluateExpression)
}

func (e *Executor) callRefActionByBackendNodeWithClient(ctx context.Context, pageClient *cdp.Client, meta *ariaRefMeta, functionDeclaration string) (*cdp.Client, bool, error) {
	if meta == nil || meta.BackendNodeID <= 0 {
		return nil, false, nil
	}

	candidates, closers, err := e.refActionClients(ctx, pageClient, meta)
	if err != nil {
		return nil, false, err
	}

	for _, client := range candidates {
		ok, callErr := e.callFunctionOnBackendNode(ctx, client, meta.BackendNodeID, functionDeclaration)
		if callErr == nil && ok {
			closeRefActionClients(closers, client)
			return client, true, nil
		}
		if e.logger != nil && callErr != nil {
			e.logger.Debug("ref backend action failed on candidate client", "frameId", meta.FrameID, "backendNodeId", meta.BackendNodeID, "error", callErr)
		}
	}
	closeRefActionClients(closers, nil)
	return nil, false, nil
}

func (e *Executor) callRefActionByMetaWithClient(ctx context.Context, pageClient *cdp.Client, meta *ariaRefMeta, evaluateExpression string) (*cdp.Client, bool, error) {
	if meta == nil || strings.TrimSpace(evaluateExpression) == "" {
		return nil, false, nil
	}

	candidates, closers, err := e.refActionClients(ctx, pageClient, meta)
	if err != nil {
		return nil, false, err
	}

	for _, client := range candidates {
		ok, callErr := e.evaluateBool(ctx, client, evaluateExpression)
		if callErr == nil && ok {
			closeRefActionClients(closers, client)
			return client, true, nil
		}
		if e.logger != nil && callErr != nil {
			e.logger.Debug("ref meta action failed on candidate client", "frameId", meta.FrameID, "backendNodeId", meta.BackendNodeID, "error", callErr)
		}
	}
	closeRefActionClients(closers, nil)
	return nil, false, nil
}

func (e *Executor) refActionClients(ctx context.Context, pageClient *cdp.Client, meta *ariaRefMeta) ([]*cdp.Client, []*cdp.Client, error) {
	candidates := make([]*cdp.Client, 0, 2)
	var closers []*cdp.Client

	addCandidate := func(client *cdp.Client) {
		if client == nil {
			return
		}
		for _, existing := range candidates {
			if existing == client {
				return
			}
		}
		candidates = append(candidates, client)
	}

	if meta != nil && strings.TrimSpace(meta.FrameID) != "" {
		frameClient, err := e.attachFrameClient(ctx, strings.TrimSpace(meta.FrameID))
		if err != nil {
			return nil, nil, err
		}
		if frameClient != nil {
			closers = append(closers, frameClient)
			addCandidate(frameClient)
		}
	}

	addCandidate(pageClient)
	return candidates, closers, nil
}

func closeRefActionClients(closers []*cdp.Client, keep *cdp.Client) {
	for _, client := range closers {
		if client == nil || client == keep {
			continue
		}
		_ = client.Close()
	}
}

func (e *Executor) attachFrameClient(ctx context.Context, frameID string) (*cdp.Client, error) {
	frameID = strings.TrimSpace(frameID)
	if frameID == "" || e.browserClient == nil {
		return nil, nil
	}

	targets, err := e.browserClient.GetTargets(ctx)
	if err != nil {
		return nil, fmt.Errorf("Target.getTargets for frame %s: %w", frameID, err)
	}
	for _, t := range targets {
		if strings.TrimSpace(t.ID) != frameID {
			continue
		}
		frameClient, err := e.browserClient.AttachToTarget(ctx, t.ID)
		if err != nil {
			return nil, fmt.Errorf("attach frame target %s: %w", frameID, err)
		}
		if err := enablePageSession(ctx, frameClient); err != nil {
			_ = frameClient.Close()
			return nil, err
		}
		return frameClient, nil
	}
	return nil, nil
}

func (e *Executor) callFunctionOnBackendNode(ctx context.Context, pageClient *cdp.Client, backendNodeID int64, functionDeclaration string) (bool, error) {
	objectID, err := e.resolveBackendNodeObject(ctx, pageClient, backendNodeID)
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

func (e *Executor) resolveBackendNodeObject(ctx context.Context, pageClient *cdp.Client, backendNodeID int64) (string, error) {
	raw, err := e.callPageClient(ctx, pageClient, "DOM.resolveNode", map[string]any{
		"backendNodeId": backendNodeID,
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
		return "", fmt.Errorf("DOM.resolveNode returned empty objectId for backendNodeId %d", backendNodeID)
	}
	return strings.TrimSpace(payload.Object.ObjectID), nil
}
