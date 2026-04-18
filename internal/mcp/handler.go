package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ToolExecutor interface {
	Call(ctx context.Context, name string, args map[string]any) (map[string]any, error)
}

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	ClientInfo      map[string]any `json:"clientInfo,omitempty"`
}

const defaultProtocolVersion = "2024-11-05"

type Handler struct {
	router        *Router
	exec          ToolExecutor
	serverName    string
	serverVersion string
}

func NewHandler(router *Router, exec ToolExecutor, serverName, serverVersion string) *Handler {
	if strings.TrimSpace(serverName) == "" {
		serverName = "brosdk-mcp"
	}
	if strings.TrimSpace(serverVersion) == "" {
		serverVersion = "dev"
	}

	return &Handler{
		router:        router,
		exec:          exec,
		serverName:    serverName,
		serverVersion: serverVersion,
	}
}

func (h *Handler) Executor() ToolExecutor {
	return h.exec
}

func IsNotification(req Request) bool {
	return len(bytes.TrimSpace(req.ID)) == 0
}

func ParseRequest(raw []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Request{}, err
	}
	return req, nil
}

func EncodeResponse(resp Response) ([]byte, error) {
	return json.Marshal(resp)
}

func (h *Handler) HandleRequest(ctx context.Context, req Request) Response {
	resp := Response{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		var p initializeParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				resp.Error = &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)}
				return resp
			}
		}

		protocolVersion := defaultProtocolVersion
		if v := strings.TrimSpace(p.ProtocolVersion); v != "" {
			protocolVersion = v
		}

		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]any{
				"name":    h.serverName,
				"version": h.serverVersion,
			},
		}
		return resp

	case "notifications/initialized":
		resp.Result = map[string]any{}
		return resp

	case "ping":
		resp.Result = map[string]any{}
		return resp

	case "tools/list":
		toolsList := h.router.ToolList()
		items := make([]map[string]any, 0, len(toolsList))
		for _, tool := range toolsList {
			items = append(items, map[string]any{
				"name":         tool.Name,
				"description":  tool.Description,
				"inputSchema":  tool.InputSchema,
				"outputSchema": tool.OutputSchema,
			})
		}
		resp.Result = map[string]any{"tools": items}
		return resp

	case "tools/call":
		var p toolsCallParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)}
			return resp
		}

		if !h.router.HasTool(p.Name) {
			resp.Error = &Error{Code: -32601, Message: fmt.Sprintf("unknown tool %q", p.Name)}
			return resp
		}

		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}

		result, err := h.exec.Call(ctx, p.Name, p.Arguments)
		if err != nil {
			resp.Error = &Error{Code: -32000, Message: err.Error()}
			return resp
		}
		resp.Result = map[string]any{
			"structuredContent": result,
			"content": []map[string]any{
				{
					"type": "text",
					"text": fmt.Sprintf("Tool %s executed.", p.Name),
				},
			},
			"isError": false,
		}
		return resp

	default:
		resp.Error = &Error{Code: -32601, Message: fmt.Sprintf("method %q not found", req.Method)}
		return resp
	}
}
