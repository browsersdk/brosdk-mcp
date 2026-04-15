package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"brosdk-mcp/internal/schema"
)

type stubExecutor struct {
	result map[string]any
	err    error
}

func (s *stubExecutor) Call(context.Context, string, map[string]any) (map[string]any, error) {
	return s.result, s.err
}

func TestHandleToolsList(t *testing.T) {
	reg := &schema.Registry{
		ToolCount: 1,
		Tools: []schema.Tool{
			{
				Name:         "browser_navigate",
				Description:  "Navigate current tab to URL.",
				InputSchema:  map[string]any{"type": "object"},
				OutputSchema: map[string]any{"type": "object"},
			},
		},
	}
	router := NewRouter(reg)
	handler := NewHandler(router, &stubExecutor{result: map[string]any{"ok": true}}, "brosdk-mcp", "test-version")

	req := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tools/list",
	}
	resp := handler.HandleRequest(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}

	toolsResult, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %#v", resp.Result)
	}
	raw, err := json.Marshal(toolsResult["tools"])
	if err != nil {
		t.Fatalf("marshal tools failed: %v", err)
	}
	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatalf("decode tools failed: %v", err)
	}
	if len(tools) != 1 || tools[0]["name"] != "browser_navigate" {
		t.Fatalf("unexpected tools payload: %#v", tools)
	}
}

func TestHandleToolsCallUnknownTool(t *testing.T) {
	reg := &schema.Registry{
		ToolCount: 0,
		Tools:     nil,
	}
	router := NewRouter(reg)
	handler := NewHandler(router, &stubExecutor{result: map[string]any{"ok": true}}, "brosdk-mcp", "test-version")

	req := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"browser_navigate","arguments":{"url":"https://example.com"}}`),
	}
	resp := handler.HandleRequest(context.Background(), req)
	if resp.Error == nil {
		t.Fatalf("expected error, got response: %#v", resp)
	}
	if resp.Error.Code != -32601 {
		t.Fatalf("unexpected error code: %#v", resp.Error)
	}
}

func TestHandleToolsCallWrapsStructuredContent(t *testing.T) {
	reg := &schema.Registry{
		ToolCount: 1,
		Tools: []schema.Tool{
			{
				Name:         "browser_navigate",
				Description:  "Navigate current tab to URL.",
				InputSchema:  map[string]any{"type": "object"},
				OutputSchema: map[string]any{"type": "object"},
			},
		},
	}
	router := NewRouter(reg)
	handler := NewHandler(router, &stubExecutor{result: map[string]any{"ok": true}}, "brosdk-mcp", "test-version")

	req := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"browser_navigate","arguments":{"url":"https://example.com"}}`),
	}
	resp := handler.HandleRequest(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %#v", resp.Result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredContent: %#v", result)
	}
	if structured["ok"] != true {
		t.Fatalf("unexpected structuredContent payload: %#v", structured)
	}
	if isError, ok := result["isError"].(bool); !ok || isError {
		t.Fatalf("expected isError=false, got %#v", result["isError"])
	}
}

func TestHandleInitialize(t *testing.T) {
	reg := &schema.Registry{
		ToolCount: 0,
		Tools:     nil,
	}
	router := NewRouter(reg)
	handler := NewHandler(router, &stubExecutor{result: map[string]any{"ok": true}}, "brosdk-mcp", "1.2.3")

	req := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"tester","version":"0.0.1"}}`),
	}
	resp := handler.HandleRequest(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %#v", resp.Result)
	}

	if got, _ := result["protocolVersion"].(string); got != "2025-03-26" {
		t.Fatalf("unexpected protocolVersion: %q", got)
	}

	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("missing serverInfo: %#v", result)
	}
	if serverInfo["name"] != "brosdk-mcp" || serverInfo["version"] != "1.2.3" {
		t.Fatalf("unexpected serverInfo: %#v", serverInfo)
	}

	capabilities, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("missing capabilities: %#v", result)
	}
	tools, ok := capabilities["tools"].(map[string]any)
	if !ok {
		t.Fatalf("missing tools capabilities: %#v", capabilities)
	}
	if tools["listChanged"] != false {
		t.Fatalf("unexpected tools.listChanged: %#v", tools["listChanged"])
	}
}

func TestIsNotification(t *testing.T) {
	if !IsNotification(Request{Method: "notifications/initialized"}) {
		t.Fatalf("request without id must be notification")
	}
	if IsNotification(Request{Method: "tools/list", ID: json.RawMessage("1")}) {
		t.Fatalf("request with id must not be notification")
	}
}
