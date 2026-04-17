package sse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"brosdk-mcp/internal/mcp"
	"brosdk-mcp/internal/schema"
)

type stubExecutor struct{}

func (s *stubExecutor) Call(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}

func TestMessageEndpointRoutesToMCPHandler(t *testing.T) {
	srv, endpoints := startTestServer(t)

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate","arguments":{"url":"https://example.com"}}}`)
	respRaw, status := postJSON(t, endpoints.Message, reqBody)
	if status != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", status, string(respRaw))
	}

	var payload struct {
		JSONRPC string         `json:"jsonrpc"`
		Result  map[string]any `json:"result"`
		Error   any            `json:"error"`
	}
	if err := json.Unmarshal(respRaw, &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.Error != nil {
		t.Fatalf("unexpected mcp error payload: %#v", payload.Error)
	}
	sc, ok := payload.Result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredContent in mcp result payload: %#v", payload.Result)
	}
	if sc["ok"] != true {
		t.Fatalf("unexpected mcp result payload: %#v", payload.Result)
	}

	shutdownTestServer(t, srv)
}

func TestSSEEndpointEmitsReadyEvent(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	resp, err := http.Get(endpoints.SSE)
	if err != nil {
		t.Fatalf("get sse endpoint failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected sse status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("unexpected content type: %q", got)
	}

	reader := bufio.NewReader(resp.Body)
	event, data := readSSEEvent(t, reader, 5*time.Second)
	if event != "ready" {
		t.Fatalf("expected ready event, got %q", event)
	}
	if !strings.Contains(data, `"status":"connected"`) {
		t.Fatalf("unexpected ready payload: %s", data)
	}
	if !strings.Contains(data, `"sessionId":"`) {
		t.Fatalf("expected ready payload to include sessionId, got %s", data)
	}
	if !strings.Contains(data, `"reused":false`) {
		t.Fatalf("expected first ready payload to mark reused=false, got %s", data)
	}
}

func TestUIEndpointServesHTML(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	resp, err := http.Get(endpoints.UI)
	if err != nil {
		t.Fatalf("get ui endpoint failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected ui status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("unexpected ui content type: %q", got)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read ui body failed: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "PageAgent Test UI") {
		t.Fatalf("expected ui body to contain title, got %s", body)
	}
	if !strings.Contains(body, "browser_create_page_agent") {
		t.Fatalf("expected ui body to reference page agent tools, got %s", body)
	}
}

func TestRootRedirectsToUI(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(strings.TrimSuffix(endpoints.UI, "/ui") + "/")
	if err != nil {
		t.Fatalf("get root endpoint failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/ui" {
		t.Fatalf("unexpected root redirect target: %q", got)
	}
}

func TestMessageEndpointMethodNotAllowed(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	resp, err := http.Get(endpoints.Message)
	if err != nil {
		t.Fatalf("get message endpoint failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 405, got %d body=%s", resp.StatusCode, string(raw))
	}
}

func TestMessageEndpointParseError(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	respRaw, status := postJSON(t, endpoints.Message, []byte(`{"jsonrpc":"2.0",`))
	if status != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", status, string(respRaw))
	}

	var payload struct {
		JSONRPC string `json:"jsonrpc"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respRaw, &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.Error.Code != -32700 {
		t.Fatalf("unexpected parse error code: %d payload=%s", payload.Error.Code, string(respRaw))
	}
}

func TestMessageEndpointNotificationNoContent(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	reqBody := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	respRaw, status := postJSON(t, endpoints.Message, reqBody)
	if status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d, body=%s", status, string(respRaw))
	}
	if len(bytes.TrimSpace(respRaw)) != 0 {
		t.Fatalf("expected empty body for notification, got %q", string(respRaw))
	}
}

func TestMessageEndpointBroadcastsToSSEClients(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	resp, err := http.Get(endpoints.SSE + "?sessionId=test-session")
	if err != nil {
		t.Fatalf("get sse endpoint failed: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	// Consume initial ready event first.
	event, _ := readSSEEvent(t, reader, 5*time.Second)
	if event != "ready" {
		t.Fatalf("expected ready event, got %q", event)
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"browser_navigate","arguments":{"url":"https://example.com"}}}`)
	_, status := postJSON(t, endpoints.Message+"?sessionId=test-session", reqBody)
	if status != http.StatusOK {
		t.Fatalf("unexpected message endpoint status: %d", status)
	}

	event, data := readSSEEvent(t, reader, 5*time.Second)
	if event != "message" {
		t.Fatalf("expected message event, got %q", event)
	}

	var payload struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("decode broadcast payload failed: %v raw=%s", err, data)
	}
	if payload.ID != 11 {
		t.Fatalf("unexpected broadcast id: %d", payload.ID)
	}
	sc, ok := payload.Result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredContent in broadcast payload: %#v", payload.Result)
	}
	if sc["ok"] != true {
		t.Fatalf("unexpected broadcast result: %#v", payload.Result)
	}
}

func TestMessageEndpointBroadcastsOnlyWithinSession(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	respA, err := http.Get(endpoints.SSE + "?sessionId=session-a")
	if err != nil {
		t.Fatalf("get session-a sse endpoint failed: %v", err)
	}
	defer respA.Body.Close()

	respB, err := http.Get(endpoints.SSE + "?sessionId=session-b")
	if err != nil {
		t.Fatalf("get session-b sse endpoint failed: %v", err)
	}
	defer respB.Body.Close()

	readerA := bufio.NewReader(respA.Body)
	readerB := bufio.NewReader(respB.Body)

	event, _ := readSSEEvent(t, readerA, 5*time.Second)
	if event != "ready" {
		t.Fatalf("expected ready event for session-a, got %q", event)
	}
	event, _ = readSSEEvent(t, readerB, 5*time.Second)
	if event != "ready" {
		t.Fatalf("expected ready event for session-b, got %q", event)
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"browser_navigate","arguments":{"url":"https://example.com"}}}`)
	_, status := postJSON(t, endpoints.Message+"?sessionId=session-a", reqBody)
	if status != http.StatusOK {
		t.Fatalf("unexpected message endpoint status: %d", status)
	}

	event, data := readSSEEvent(t, readerA, 5*time.Second)
	if event != "message" {
		t.Fatalf("expected message event for session-a, got %q", event)
	}
	if !strings.Contains(data, `"id":21`) {
		t.Fatalf("unexpected session-a message payload: %s", data)
	}

	assertNoSSEEvent(t, readerB, 400*time.Millisecond)
}

func TestSSEEndpointMarksReusedSessionInReadyEvent(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	respA, err := http.Get(endpoints.SSE + "?sessionId=reuse-me")
	if err != nil {
		t.Fatalf("get first sse endpoint failed: %v", err)
	}
	defer respA.Body.Close()
	readerA := bufio.NewReader(respA.Body)

	event, data := readSSEEvent(t, readerA, 5*time.Second)
	if event != "ready" {
		t.Fatalf("expected ready event, got %q", event)
	}
	if !strings.Contains(data, `"reused":false`) {
		t.Fatalf("expected first ready payload reused=false, got %s", data)
	}

	respB, err := http.Get(endpoints.SSE + "?sessionId=reuse-me")
	if err != nil {
		t.Fatalf("get second sse endpoint failed: %v", err)
	}
	defer respB.Body.Close()
	readerB := bufio.NewReader(respB.Body)

	event, data = readSSEEvent(t, readerB, 5*time.Second)
	if event != "ready" {
		t.Fatalf("expected ready event, got %q", event)
	}
	if !strings.Contains(data, `"reused":true`) {
		t.Fatalf("expected second ready payload reused=true, got %s", data)
	}
}

func TestMessageEndpointReturnsHeaderForKnownSession(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	resp, err := http.Get(endpoints.SSE + "?sessionId=session-header")
	if err != nil {
		t.Fatalf("get sse endpoint failed: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	event, _ := readSSEEvent(t, reader, 5*time.Second)
	if event != "ready" {
		t.Fatalf("expected ready event, got %q", event)
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"browser_navigate","arguments":{"url":"https://example.com"}}}`)
	respRaw, status, headers := postJSONWithHeaders(t, endpoints.Message+"?sessionId=session-header", reqBody, nil)
	if status != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", status, string(respRaw))
	}
	if got := headers.Get("X-MCP-Session-ID"); got != "session-header" {
		t.Fatalf("unexpected session header: %q", got)
	}
}

func TestMessageEndpointRejectsUnknownSession(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	reqBody := []byte(`{"jsonrpc":"2.0","id":41,"method":"tools/call","params":{"name":"browser_navigate","arguments":{"url":"https://example.com"}}}`)
	respRaw, status, _ := postJSONWithHeaders(t, endpoints.Message+"?sessionId=missing-session", reqBody, nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", status, string(respRaw))
	}
	if !strings.Contains(string(respRaw), `unknown sessionId "missing-session"`) {
		t.Fatalf("unexpected body: %s", string(respRaw))
	}
}

func TestSessionEndpointMethodNotAllowed(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	resp, err := http.Get(endpoints.Session)
	if err != nil {
		t.Fatalf("get session endpoint failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 405, got %d body=%s", resp.StatusCode, string(raw))
	}
}

func TestSessionEndpointDeletesSessionAndRejectsFurtherMessages(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	resp, err := http.Get(endpoints.SSE + "?sessionId=cleanup-me")
	if err != nil {
		t.Fatalf("get sse endpoint failed: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	event, _ := readSSEEvent(t, reader, 5*time.Second)
	if event != "ready" {
		t.Fatalf("expected ready event, got %q", event)
	}

	status, body := deleteSession(t, endpoints.Session+"?sessionId=cleanup-me", nil)
	if status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", status, body)
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":51,"method":"tools/call","params":{"name":"browser_navigate","arguments":{"url":"https://example.com"}}}`)
	respRaw, msgStatus, _ := postJSONWithHeaders(t, endpoints.Message+"?sessionId=cleanup-me", reqBody, nil)
	if msgStatus != http.StatusNotFound {
		t.Fatalf("expected 404 after session cleanup, got %d body=%s", msgStatus, string(respRaw))
	}
	if !strings.Contains(string(respRaw), `unknown sessionId "cleanup-me"`) {
		t.Fatalf("unexpected body: %s", string(respRaw))
	}
}

func TestSessionEndpointDeletesByHeaderSessionID(t *testing.T) {
	srv, endpoints := startTestServer(t)
	defer shutdownTestServer(t, srv)

	resp, err := http.Get(endpoints.SSE + "?sessionId=cleanup-header")
	if err != nil {
		t.Fatalf("get sse endpoint failed: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	event, _ := readSSEEvent(t, reader, 5*time.Second)
	if event != "ready" {
		t.Fatalf("expected ready event, got %q", event)
	}

	status, body := deleteSession(t, endpoints.Session, map[string]string{
		"X-MCP-Session-ID": "cleanup-header",
	})
	if status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", status, body)
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":61,"method":"tools/call","params":{"name":"browser_navigate","arguments":{"url":"https://example.com"}}}`)
	respRaw, msgStatus, _ := postJSONWithHeaders(t, endpoints.Message+"?sessionId=cleanup-header", reqBody, nil)
	if msgStatus != http.StatusNotFound {
		t.Fatalf("expected 404 after header cleanup, got %d body=%s", msgStatus, string(respRaw))
	}
}

func startTestServer(t *testing.T) (*Server, Endpoints) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
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

	handler := mcp.NewHandler(mcp.NewRouter(reg), &stubExecutor{}, "brosdk-mcp", "test-version")
	srv := NewServer(0, logger, handler)
	endpoints, err := srv.Start()
	if err != nil {
		t.Fatalf("start sse server failed: %v", err)
	}
	return srv, endpoints
}

func shutdownTestServer(t *testing.T, srv *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown sse server failed: %v", err)
	}
}

func postJSON(t *testing.T, endpoint string, body []byte) ([]byte, int) {
	t.Helper()
	raw, status, _ := postJSONWithHeaders(t, endpoint, body, nil)
	return raw, status
}

func postJSONWithHeaders(t *testing.T, endpoint string, body []byte, headers map[string]string) ([]byte, int, http.Header) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post json failed: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body failed: %v", err)
	}
	return raw, resp.StatusCode, resp.Header.Clone()
}

func deleteSession(t *testing.T, endpoint string, headers map[string]string) (int, string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
	if err != nil {
		t.Fatalf("build delete request failed: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete session failed: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read delete response failed: %v", err)
	}
	return resp.StatusCode, string(raw)
}

func readSSEEvent(t *testing.T, reader *bufio.Reader, timeout time.Duration) (string, string) {
	t.Helper()

	type result struct {
		event string
		data  string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		var event, data string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if event != "" || data != "" {
					ch <- result{event: event, data: data}
					return
				}
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				continue
			}
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
				continue
			}
		}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read sse event failed: %v", r.err)
		}
		return r.event, r.data
	case <-time.After(timeout):
		t.Fatalf("timeout waiting sse event after %s", timeout)
		return "", ""
	}
}

func assertNoSSEEvent(t *testing.T, reader *bufio.Reader, timeout time.Duration) {
	t.Helper()

	type result struct {
		event string
		data  string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		event, data := readSSEEventAllowTimeout(reader, timeout)
		ch <- result{event: event, data: data}
	}()

	select {
	case r := <-ch:
		if r.event != "" || r.data != "" || r.err != nil {
			t.Fatalf("unexpected sse event: event=%q data=%q err=%v", r.event, r.data, r.err)
		}
	case <-time.After(timeout + 200*time.Millisecond):
	}
}

func readSSEEventAllowTimeout(reader *bufio.Reader, timeout time.Duration) (string, string) {
	type result struct {
		event string
		data  string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		var event, data string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if event != "" || data != "" {
					ch <- result{event: event, data: data}
					return
				}
				continue
			}
			if strings.HasPrefix(line, ":") {
				event, data = "", ""
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				continue
			}
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
				continue
			}
		}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return "", ""
		}
		return r.event, r.data
	case <-time.After(timeout):
		return "", ""
	}
}
