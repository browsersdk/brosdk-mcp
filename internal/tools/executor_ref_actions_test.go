package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"brosdk-mcp/internal/cdp"
	"github.com/coder/websocket"
)

func TestBuildAXSnapshotIncludesFrameMetadata(t *testing.T) {
	nodes := []axNode{
		{
			NodeID:   "root",
			Role:     &axValue{Value: mustRawJSON(t, `"RootWebArea"`)},
			Name:     &axValue{Value: mustRawJSON(t, `"Page"`)},
			ChildIDs: []string{"frame-btn"},
		},
		{
			NodeID:           "frame-btn",
			Role:             &axValue{Value: mustRawJSON(t, `"button"`)},
			Name:             &axValue{Value: mustRawJSON(t, `"Frame Action"`)},
			BackendDOMNodeID: 321,
			FrameID:          "frame-1",
		},
	}

	_, meta := buildAXSnapshot(nodes, true, true, 24, "Page")
	if meta["e1"]["frameId"] != "frame-1" {
		t.Fatalf("expected frameId metadata, got %#v", meta["e1"])
	}
}

func TestClickByRefUsesFrameSessionFallback(t *testing.T) {
	testByRefFrameAction(t, "click", func(ctx context.Context, e *Executor) error {
		_, err := e.callClickByRef(ctx, map[string]any{"ref": "e7"})
		return err
	})
}

func TestTypeByRefUsesFrameSessionFallback(t *testing.T) {
	testByRefFrameAction(t, "type", func(ctx context.Context, e *Executor) error {
		_, err := e.callTypeByRef(ctx, map[string]any{"ref": "e7", "text": "hello", "clear": true})
		return err
	})
}

func TestSetValueByRefUsesFrameSessionFallback(t *testing.T) {
	testByRefFrameAction(t, "set", func(ctx context.Context, e *Executor) error {
		_, err := e.callSetInputValueByRef(ctx, map[string]any{"ref": "e7", "value": "hello"})
		return err
	})
}

func TestClickByRefUsesFrameMetaFallbackAfterBackendMiss(t *testing.T) {
	testByRefFrameMetaAction(t, "click", func(ctx context.Context, e *Executor) error {
		_, err := e.callClickByRef(ctx, map[string]any{"ref": "e7"})
		return err
	})
}

func TestTypeByRefUsesFrameMetaFallbackAfterBackendMiss(t *testing.T) {
	testByRefFrameMetaAction(t, "type", func(ctx context.Context, e *Executor) error {
		_, err := e.callTypeByRef(ctx, map[string]any{"ref": "e7", "text": "hello", "clear": true})
		return err
	})
}

func TestSetValueByRefUsesFrameMetaFallbackAfterBackendMiss(t *testing.T) {
	testByRefFrameMetaAction(t, "set", func(ctx context.Context, e *Executor) error {
		_, err := e.callSetInputValueByRef(ctx, map[string]any{"ref": "e7", "value": "hello"})
		return err
	})
}

func testByRefFrameAction(t *testing.T, action string, invoke func(context.Context, *Executor) error) {
	t.Helper()

	type cdpRequest struct {
		ID        int64           `json:"id"`
		Method    string          `json:"method"`
		Params    json.RawMessage `json:"params"`
		SessionID string          `json:"sessionId"`
	}

	var sawAttach bool
	var sawResolve bool
	var sawCallFunction bool
	var sawKeyEvent bool
	var sawInsertText bool
	wsURL := startToolsCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")

		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}

			var req cdpRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}

			switch req.Method {
			case "Runtime.evaluate":
				var params struct {
					Expression string `json:"expression"`
				}
				if err := json.Unmarshal(req.Params, &params); err != nil {
					t.Errorf("decode Runtime.evaluate params: %v", err)
					return
				}
				switch {
				case strings.Contains(params.Expression, "resolveRefElement(ref)"):
					writeCDPResult(t, ctx, conn, req.ID, map[string]any{
						"result": map[string]any{"type": "boolean", "value": false},
					})
				case strings.Contains(params.Expression, "window.__ariaRefMeta"):
					writeCDPResult(t, ctx, conn, req.ID, map[string]any{
						"result": map[string]any{
							"type":  "string",
							"value": `{"role":"button","name":"Frame Action","nth":0,"backendNodeId":321,"frameId":"frame-1"}`,
						},
					})
				default:
					t.Errorf("unexpected Runtime.evaluate expression: %s", params.Expression)
					return
				}
			case "Target.getTargets":
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{
					"targetInfos": []map[string]any{
						{"targetId": "frame-1", "type": "iframe"},
					},
				})
			case "Target.attachToTarget":
				sawAttach = true
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{
					"sessionId": "frame-session",
				})
			case "Page.enable", "Page.setLifecycleEventsEnabled", "Runtime.enable":
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{})
			case "DOM.resolveNode":
				if req.SessionID != "frame-session" {
					t.Errorf("DOM.resolveNode should use frame session, got %q", req.SessionID)
				}
				sawResolve = true
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{
					"object": map[string]any{"objectId": "obj-1"},
				})
			case "Runtime.callFunctionOn":
				if req.SessionID != "frame-session" {
					t.Errorf("Runtime.callFunctionOn should use frame session, got %q", req.SessionID)
				}
				sawCallFunction = true
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{
					"result": map[string]any{"type": "boolean", "value": true},
				})
			case "Input.dispatchKeyEvent":
				if action != "type" {
					t.Errorf("unexpected Input.dispatchKeyEvent for action %s", action)
					return
				}
				if req.SessionID != "frame-session" {
					t.Errorf("Input.dispatchKeyEvent should use frame session, got %q", req.SessionID)
				}
				sawKeyEvent = true
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{})
			case "Input.insertText":
				if action != "type" {
					t.Errorf("unexpected Input.insertText for action %s", action)
					return
				}
				if req.SessionID != "frame-session" {
					t.Errorf("Input.insertText should use frame session, got %q", req.SessionID)
				}
				sawInsertText = true
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{})
			case "Target.detachFromTarget":
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{})
			default:
				t.Errorf("unexpected method %s", req.Method)
				return
			}
		}
	})

	rootClient, err := cdp.NewClient(context.Background(), wsURL, testToolsLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer rootClient.Close()

	e := &Executor{
		browserClient: rootClient,
		pageClient:    rootClient,
		currentTabID:  "tab-1",
		logger:        testToolsLogger(),
	}

	if err := invoke(context.Background(), e); err != nil {
		t.Fatalf("%s invoke failed: %v", action, err)
	}
	if !sawAttach || !sawResolve || !sawCallFunction {
		t.Fatalf("frame fallback was incomplete: attach=%v resolve=%v callFunction=%v", sawAttach, sawResolve, sawCallFunction)
	}
	if action == "type" && (!sawKeyEvent || !sawInsertText) {
		t.Fatalf("expected clear keystrokes and Input.insertText to use frame session")
	}
	if action != "type" && (sawKeyEvent || sawInsertText) {
		t.Fatalf("unexpected typing events for action %s", action)
	}
}

func testByRefFrameMetaAction(t *testing.T, action string, invoke func(context.Context, *Executor) error) {
	t.Helper()

	type cdpRequest struct {
		ID        int64           `json:"id"`
		Method    string          `json:"method"`
		Params    json.RawMessage `json:"params"`
		SessionID string          `json:"sessionId"`
	}

	var sawAttach bool
	var sawResolve bool
	var sawMetaEvaluate bool
	var sawKeyEvent bool
	var sawInsertText bool
	wsURL := startToolsCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")

		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}

			var req cdpRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}

			switch req.Method {
			case "Runtime.evaluate":
				var params struct {
					Expression string `json:"expression"`
				}
				if err := json.Unmarshal(req.Params, &params); err != nil {
					t.Errorf("decode Runtime.evaluate params: %v", err)
					return
				}
				switch {
				case req.SessionID == "" && strings.Contains(params.Expression, "resolveRefElement(ref)"):
					writeCDPResult(t, ctx, conn, req.ID, map[string]any{
						"result": map[string]any{"type": "boolean", "value": false},
					})
				case req.SessionID == "" && strings.Contains(params.Expression, "window.__ariaRefMeta"):
					writeCDPResult(t, ctx, conn, req.ID, map[string]any{
						"result": map[string]any{
							"type":  "string",
							"value": `{"role":"button","name":"Frame Action","nth":0,"backendNodeId":321,"frameId":"frame-1"}`,
						},
					})
				case req.SessionID == "frame-session" && strings.Contains(params.Expression, `"frameId":"frame-1"`):
					sawMetaEvaluate = true
					writeCDPResult(t, ctx, conn, req.ID, map[string]any{
						"result": map[string]any{"type": "boolean", "value": true},
					})
				default:
					t.Errorf("unexpected Runtime.evaluate expression: %s", params.Expression)
					return
				}
			case "Target.getTargets":
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{
					"targetInfos": []map[string]any{
						{"targetId": "frame-1", "type": "iframe"},
					},
				})
			case "Target.attachToTarget":
				sawAttach = true
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{
					"sessionId": "frame-session",
				})
			case "Page.enable", "Page.setLifecycleEventsEnabled", "Runtime.enable":
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{})
			case "DOM.resolveNode":
				sawResolve = true
				writeCDPError(t, ctx, conn, req.ID, -32000, "No node with given id found")
			case "Runtime.callFunctionOn":
				t.Errorf("Runtime.callFunctionOn should not run after backend miss for action %s", action)
				return
			case "Input.dispatchKeyEvent":
				if action != "type" {
					t.Errorf("unexpected Input.dispatchKeyEvent for action %s", action)
					return
				}
				if req.SessionID != "frame-session" {
					t.Errorf("Input.dispatchKeyEvent should use frame session, got %q", req.SessionID)
				}
				sawKeyEvent = true
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{})
			case "Input.insertText":
				if action != "type" {
					t.Errorf("unexpected Input.insertText for action %s", action)
					return
				}
				if req.SessionID != "frame-session" {
					t.Errorf("Input.insertText should use frame session, got %q", req.SessionID)
				}
				sawInsertText = true
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{})
			case "Target.detachFromTarget":
				writeCDPResult(t, ctx, conn, req.ID, map[string]any{})
			default:
				t.Errorf("unexpected method %s", req.Method)
				return
			}
		}
	})

	rootClient, err := cdp.NewClient(context.Background(), wsURL, testToolsLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer rootClient.Close()

	e := &Executor{
		browserClient: rootClient,
		pageClient:    rootClient,
		currentTabID:  "tab-1",
		logger:        testToolsLogger(),
	}

	if err := invoke(context.Background(), e); err != nil {
		t.Fatalf("%s invoke failed: %v", action, err)
	}
	if !sawAttach || !sawResolve || !sawMetaEvaluate {
		t.Fatalf("frame meta fallback was incomplete: attach=%v resolve=%v metaEvaluate=%v", sawAttach, sawResolve, sawMetaEvaluate)
	}
	if action == "type" && (!sawKeyEvent || !sawInsertText) {
		t.Fatalf("expected clear keystrokes and Input.insertText to use frame session")
	}
	if action != "type" && (sawKeyEvent || sawInsertText) {
		t.Fatalf("unexpected typing events for action %s", action)
	}
}
