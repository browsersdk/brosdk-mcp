package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"brosdk-mcp/internal/cdp"
)

func (e *Executor) resolveSelectorNodeID(ctx context.Context, pageClient *cdp.Client, selector string) (int64, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return 0, fmt.Errorf("empty selector")
	}

	docRaw, err := e.callPageClient(ctx, pageClient, "DOM.getDocument", map[string]any{"depth": 0})
	if err != nil {
		return 0, fmt.Errorf("DOM.getDocument: %w", err)
	}
	var docPayload struct {
		Root struct {
			NodeID int64 `json:"nodeId"`
		} `json:"root"`
	}
	if err := json.Unmarshal(docRaw, &docPayload); err != nil {
		return 0, fmt.Errorf("decode DOM.getDocument: %w", err)
	}
	if docPayload.Root.NodeID <= 0 {
		return 0, fmt.Errorf("invalid root node id")
	}

	qsRaw, err := e.callPageClient(ctx, pageClient, "DOM.querySelector", map[string]any{
		"nodeId":   docPayload.Root.NodeID,
		"selector": selector,
	})
	if err != nil {
		return 0, fmt.Errorf("DOM.querySelector: %w", err)
	}
	var qsPayload struct {
		NodeID int64 `json:"nodeId"`
	}
	if err := json.Unmarshal(qsRaw, &qsPayload); err != nil {
		return 0, fmt.Errorf("decode DOM.querySelector: %w", err)
	}
	if qsPayload.NodeID <= 0 {
		return 0, fmt.Errorf("selector not found")
	}
	return qsPayload.NodeID, nil
}

func (e *Executor) focusSelectorNative(ctx context.Context, pageClient *cdp.Client, selector string) (bool, error) {
	nodeID, err := e.resolveSelectorNodeID(ctx, pageClient, selector)
	if err != nil {
		return false, nil
	}
	if err := e.focusNodeByID(ctx, pageClient, nodeID); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Executor) clickSelectorNative(ctx context.Context, pageClient *cdp.Client, selector string) (bool, error) {
	nodeID, err := e.resolveSelectorNodeID(ctx, pageClient, selector)
	if err != nil {
		return false, nil
	}
	if err := e.clickNodeByID(ctx, pageClient, nodeID); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Executor) focusNodeByID(ctx context.Context, pageClient *cdp.Client, nodeID int64) error {
	if nodeID <= 0 {
		return fmt.Errorf("invalid node id")
	}
	if _, err := e.callPageClient(ctx, pageClient, "DOM.focus", map[string]any{"nodeId": nodeID}); err != nil {
		return fmt.Errorf("DOM.focus: %w", err)
	}
	return nil
}

func (e *Executor) focusBackendNode(ctx context.Context, pageClient *cdp.Client, backendNodeID int64) error {
	if backendNodeID <= 0 {
		return fmt.Errorf("invalid backend node id")
	}
	if _, err := e.callPageClient(ctx, pageClient, "DOM.focus", map[string]any{"backendNodeId": backendNodeID}); err != nil {
		return fmt.Errorf("DOM.focus: %w", err)
	}
	return nil
}

func (e *Executor) clickNodeByID(ctx context.Context, pageClient *cdp.Client, nodeID int64) error {
	if nodeID <= 0 {
		return fmt.Errorf("invalid node id")
	}
	_, _ = e.callPageClient(ctx, pageClient, "DOM.scrollIntoViewIfNeeded", map[string]any{"nodeId": nodeID})
	x, y, err := e.nodeCenterByNodeID(ctx, pageClient, nodeID)
	if err != nil {
		return err
	}
	return e.dispatchMouseClick(ctx, pageClient, x, y)
}

func (e *Executor) clickBackendNode(ctx context.Context, pageClient *cdp.Client, backendNodeID int64) error {
	if backendNodeID <= 0 {
		return fmt.Errorf("invalid backend node id")
	}
	_, _ = e.callPageClient(ctx, pageClient, "DOM.scrollIntoViewIfNeeded", map[string]any{"backendNodeId": backendNodeID})
	x, y, err := e.nodeCenterByBackendID(ctx, pageClient, backendNodeID)
	if err != nil {
		return err
	}
	return e.dispatchMouseClick(ctx, pageClient, x, y)
}

func (e *Executor) nodeCenterByNodeID(ctx context.Context, pageClient *cdp.Client, nodeID int64) (float64, float64, error) {
	raw, err := e.callPageClient(ctx, pageClient, "DOM.getBoxModel", map[string]any{"nodeId": nodeID})
	if err != nil {
		return 0, 0, fmt.Errorf("DOM.getBoxModel: %w", err)
	}
	return decodeNodeCenter(raw)
}

func (e *Executor) nodeCenterByBackendID(ctx context.Context, pageClient *cdp.Client, backendNodeID int64) (float64, float64, error) {
	raw, err := e.callPageClient(ctx, pageClient, "DOM.getBoxModel", map[string]any{"backendNodeId": backendNodeID})
	if err != nil {
		return 0, 0, fmt.Errorf("DOM.getBoxModel: %w", err)
	}
	return decodeNodeCenter(raw)
}

func decodeNodeCenter(raw json.RawMessage) (float64, float64, error) {
	var payload struct {
		Model struct {
			Border  []float64 `json:"border"`
			Content []float64 `json:"content"`
		} `json:"model"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, 0, fmt.Errorf("decode DOM.getBoxModel: %w", err)
	}

	quad := payload.Model.Border
	if len(quad) < 8 {
		quad = payload.Model.Content
	}
	if len(quad) < 8 {
		return 0, 0, fmt.Errorf("box model has no valid quad")
	}

	x := (quad[0] + quad[2] + quad[4] + quad[6]) / 4
	y := (quad[1] + quad[3] + quad[5] + quad[7]) / 4
	return x, y, nil
}

func (e *Executor) dispatchMouseClick(ctx context.Context, pageClient *cdp.Client, x float64, y float64) error {
	_, err := e.callPageClient(ctx, pageClient, "Input.dispatchMouseEvent", map[string]any{
		"type":       "mousePressed",
		"x":          x,
		"y":          y,
		"button":     "left",
		"clickCount": 1,
	})
	if err != nil {
		return fmt.Errorf("mousePressed: %w", err)
	}
	_, err = e.callPageClient(ctx, pageClient, "Input.dispatchMouseEvent", map[string]any{
		"type":       "mouseReleased",
		"x":          x,
		"y":          y,
		"button":     "left",
		"clickCount": 1,
	})
	if err != nil {
		return fmt.Errorf("mouseReleased: %w", err)
	}
	return nil
}

func (e *Executor) clearActiveElementNative(ctx context.Context, pageClient *cdp.Client) error {
	// Try both Ctrl+A and Meta+A, then clear with Backspace.
	_ = e.dispatchSelectAll(ctx, pageClient, "Control", "ControlLeft", 17, 2)
	_ = e.dispatchSelectAll(ctx, pageClient, "Meta", "MetaLeft", 91, 4)

	if err := e.dispatchKeyStroke(ctx, pageClient, "Backspace", "Backspace", 8, 0); err != nil {
		return err
	}
	return nil
}

func (e *Executor) dispatchSelectAll(ctx context.Context, pageClient *cdp.Client, modifierKey string, modifierCode string, modifierVK int, modifierMask int) error {
	if err := e.dispatchKeyEvent(ctx, pageClient, "keyDown", modifierKey, modifierCode, modifierVK, 0); err != nil {
		return err
	}
	if err := e.dispatchKeyEvent(ctx, pageClient, "keyDown", "a", "KeyA", 65, modifierMask); err != nil {
		_ = e.dispatchKeyEvent(ctx, pageClient, "keyUp", modifierKey, modifierCode, modifierVK, 0)
		return err
	}
	if err := e.dispatchKeyEvent(ctx, pageClient, "keyUp", "a", "KeyA", 65, modifierMask); err != nil {
		_ = e.dispatchKeyEvent(ctx, pageClient, "keyUp", modifierKey, modifierCode, modifierVK, 0)
		return err
	}
	if err := e.dispatchKeyEvent(ctx, pageClient, "keyUp", modifierKey, modifierCode, modifierVK, 0); err != nil {
		return err
	}
	return nil
}

func (e *Executor) dispatchKeyStroke(ctx context.Context, pageClient *cdp.Client, key string, code string, windowsVK int, modifiers int) error {
	if err := e.dispatchKeyEvent(ctx, pageClient, "keyDown", key, code, windowsVK, modifiers); err != nil {
		return err
	}
	if err := e.dispatchKeyEvent(ctx, pageClient, "keyUp", key, code, windowsVK, modifiers); err != nil {
		return err
	}
	return nil
}

func (e *Executor) dispatchKeyEvent(ctx context.Context, pageClient *cdp.Client, eventType string, key string, code string, windowsVK int, modifiers int) error {
	_, err := e.callPageClient(ctx, pageClient, "Input.dispatchKeyEvent", map[string]any{
		"type":                  eventType,
		"key":                   key,
		"code":                  code,
		"windowsVirtualKeyCode": windowsVK,
		"nativeVirtualKeyCode":  windowsVK,
		"modifiers":             modifiers,
	})
	if err != nil {
		return fmt.Errorf("Input.dispatchKeyEvent(%s %s): %w", eventType, key, err)
	}
	return nil
}
