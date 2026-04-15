package cdp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestNewClientRetriesDialOnTransientUpgradeFailure(t *testing.T) {
	var attempts atomic.Int32
	wsURL := startCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")
		<-ctx.Done()
	}, func(w http.ResponseWriter, _ *http.Request) bool {
		attempt := attempts.Add(1)
		if attempt <= 2 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return true
		}
		return false
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewClient(ctx, wsURL, testLogger())
	if err != nil {
		t.Fatalf("NewClient failed after retries: %v", err)
	}
	defer client.Close()

	if got := attempts.Load(); got < 3 {
		t.Fatalf("expected at least 3 dial attempts, got %d", got)
	}
}

func TestClientSubscribeEventsFiltersByMethod(t *testing.T) {
	wsURL := startCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")

		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var req requestEnvelope
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}

		resp, _ := json.Marshal(map[string]any{
			"id":     req.ID,
			"result": map[string]any{},
		})
		if err := conn.Write(ctx, websocket.MessageText, resp); err != nil {
			return
		}

		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"Runtime.consoleAPICalled","params":{"type":"log"}}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"Page.loadEventFired","params":{"ts":1}}`))

		<-ctx.Done()
	})

	client, err := NewClient(context.Background(), wsURL, testLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	events, unsubscribe := client.SubscribeEvents("Page.loadEventFired", 4)
	defer unsubscribe()

	callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.Call(callCtx, "Runtime.enable", nil); err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Method != "Page.loadEventFired" {
			t.Fatalf("unexpected event method: %q", ev.Method)
		}
		var payload map[string]any
		if err := json.Unmarshal(ev.Params, &payload); err != nil {
			t.Fatalf("decode event params failed: %v", err)
		}
		if payload["ts"] != float64(1) {
			t.Fatalf("unexpected event params: %#v", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting event")
	}
}

func TestClientWaitForEventWithPredicate(t *testing.T) {
	wsURL := startCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")

		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var req requestEnvelope
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}

		resp, _ := json.Marshal(map[string]any{
			"id":     req.ID,
			"result": map[string]any{},
		})
		if err := conn.Write(ctx, websocket.MessageText, resp); err != nil {
			return
		}

		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"Page.frameNavigated","params":{"frame":{"id":"sub"}}}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"Page.frameNavigated","params":{"frame":{"id":"main"}}}`))

		<-ctx.Done()
	})

	client, err := NewClient(context.Background(), wsURL, testLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelWait()

	type waitResult struct {
		event Event
		err   error
	}
	waitCh := make(chan waitResult, 1)
	go func() {
		ev, err := client.WaitForEvent(waitCtx, "Page.frameNavigated", func(ev Event) bool {
			var payload struct {
				Frame struct {
					ID string `json:"id"`
				} `json:"frame"`
			}
			if err := json.Unmarshal(ev.Params, &payload); err != nil {
				return false
			}
			return payload.Frame.ID == "main"
		})
		waitCh <- waitResult{event: ev, err: err}
	}()

	callCtx, cancelCall := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelCall()
	if _, err := client.Call(callCtx, "Page.enable", nil); err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	select {
	case got := <-waitCh:
		if got.err != nil {
			t.Fatalf("WaitForEvent failed: %v", got.err)
		}
		var payload struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		}
		if err := json.Unmarshal(got.event.Params, &payload); err != nil {
			t.Fatalf("decode matched event failed: %v", err)
		}
		if payload.Frame.ID != "main" {
			t.Fatalf("unexpected frame id: %q", payload.Frame.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting WaitForEvent result")
	}
}

func TestClientCloseClosesEventStream(t *testing.T) {
	wsURL := startCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")
		<-ctx.Done()
	})

	client, err := NewClient(context.Background(), wsURL, testLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	events, unsubscribe := client.SubscribeEvents("", 1)
	defer unsubscribe()

	_ = client.Close()

	select {
	case _, ok := <-events:
		if ok {
			t.Fatalf("expected closed events channel")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting events channel close")
	}
}

func TestClientUnsubscribeStopsDelivery(t *testing.T) {
	wsURL := startCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")

		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var req requestEnvelope
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}

		resp, _ := json.Marshal(map[string]any{
			"id":     req.ID,
			"result": map[string]any{},
		})
		if err := conn.Write(ctx, websocket.MessageText, resp); err != nil {
			return
		}

		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"Page.loadEventFired","params":{"ts":2}}`))
		<-ctx.Done()
	})

	client, err := NewClient(context.Background(), wsURL, testLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	events, unsubscribe := client.SubscribeEvents("Page.loadEventFired", 1)
	unsubscribe()

	callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.Call(callCtx, "Runtime.enable", nil); err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	select {
	case ev := <-events:
		t.Fatalf("unexpected event after unsubscribe: %#v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestClientAttachToTargetCreatesSessionClient(t *testing.T) {
	wsURL := startCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")

		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}

			var req requestEnvelope
			if err := json.Unmarshal(raw, &req); err != nil {
				return
			}

			switch req.Method {
			case "Target.attachToTarget":
				if req.SessionID != "" {
					t.Errorf("attach request should not include sessionId, got %q", req.SessionID)
				}
				resp, _ := json.Marshal(map[string]any{
					"id":     req.ID,
					"result": map[string]any{"sessionId": "session-1"},
				})
				if err := conn.Write(ctx, websocket.MessageText, resp); err != nil {
					return
				}
			case "Runtime.evaluate":
				if req.SessionID != "session-1" {
					t.Errorf("expected session Runtime.evaluate request, got %q", req.SessionID)
				}
				resp, _ := json.Marshal(map[string]any{
					"id":        req.ID,
					"sessionId": "session-1",
					"result":    map[string]any{"result": map[string]any{"type": "string", "value": "ok"}},
				})
				if err := conn.Write(ctx, websocket.MessageText, resp); err != nil {
					return
				}
				_ = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"Page.loadEventFired","sessionId":"session-1","params":{"ts":3}}`))
				_ = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"Page.loadEventFired","params":{"ts":9}}`))
			case "Target.detachFromTarget":
				var params struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(mustJSONMarshal(t, req.Params), &params); err != nil {
					t.Errorf("decode detach params failed: %v", err)
				}
				if params.SessionID != "session-1" {
					t.Errorf("unexpected detach session id: %q", params.SessionID)
				}
				resp, _ := json.Marshal(map[string]any{
					"id":     req.ID,
					"result": map[string]any{},
				})
				if err := conn.Write(ctx, websocket.MessageText, resp); err != nil {
					return
				}
				return
			default:
				t.Errorf("unexpected method %q", req.Method)
				return
			}
		}
	})

	client, err := NewClient(context.Background(), wsURL, testLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	sessionClient, err := client.AttachToTarget(context.Background(), "target-1")
	if err != nil {
		t.Fatalf("AttachToTarget failed: %v", err)
	}

	if got := sessionClient.SessionID(); got != "session-1" {
		t.Fatalf("unexpected session id: %q", got)
	}

	events, unsubscribe := sessionClient.SubscribeEvents("Page.loadEventFired", 1)
	defer unsubscribe()

	callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := sessionClient.Call(callCtx, "Runtime.evaluate", map[string]any{"expression": "1+1"}); err != nil {
		t.Fatalf("session Call failed: %v", err)
	}

	select {
	case ev := <-events:
		if ev.SessionID != "session-1" {
			t.Fatalf("unexpected event session id: %q", ev.SessionID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting session event")
	}

	if err := sessionClient.Close(); err != nil {
		t.Fatalf("session Close failed: %v", err)
	}
}

func TestBrowserClientDoesNotReceiveSessionScopedEvents(t *testing.T) {
	wsURL := startCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")

		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var req requestEnvelope
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}

		resp, _ := json.Marshal(map[string]any{
			"id":     req.ID,
			"result": map[string]any{},
		})
		if err := conn.Write(ctx, websocket.MessageText, resp); err != nil {
			return
		}

		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"Page.loadEventFired","sessionId":"session-1","params":{"ts":1}}`))
		<-ctx.Done()
	})

	client, err := NewClient(context.Background(), wsURL, testLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	events, unsubscribe := client.SubscribeEvents("Page.loadEventFired", 1)
	defer unsubscribe()

	callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.Call(callCtx, "Runtime.enable", nil); err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	select {
	case ev := <-events:
		t.Fatalf("unexpected browser-scoped event delivery: %#v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func mustJSONMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	return raw
}

func startCDPTestServer(t *testing.T, onConnect func(context.Context, *websocket.Conn), preAccept ...func(http.ResponseWriter, *http.Request) bool) string {
	t.Helper()

	var gate func(http.ResponseWriter, *http.Request) bool
	if len(preAccept) > 0 {
		gate = preAccept[0]
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gate != nil && gate(w, r) {
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("websocket accept failed: %v", err)
			return
		}
		onConnect(r.Context(), conn)
	}))
	t.Cleanup(srv.Close)

	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
