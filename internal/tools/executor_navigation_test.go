package tools

import (
    "context"
    "encoding/json"
    "strings"
    "testing"

    "brosdk-mcp/internal/cdp"
    "github.com/coder/websocket"
)

func TestResolveHistoryEntryBackAndForward(t *testing.T) {
    type cdpRequest struct {
        ID        int64           `json:"id"`
        Method    string          `json:"method"`
        Params    json.RawMessage `json:"params"`
        SessionID string          `json:"sessionId"`
    }

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
            case "Page.getNavigationHistory":
                writeCDPResult(t, ctx, conn, req.ID, map[string]any{
                    "currentIndex": 1,
                    "entries": []map[string]any{
                        {"id": 11, "url": "https://a.example"},
                        {"id": 12, "url": "https://b.example"},
                        {"id": 13, "url": "https://c.example"},
                    },
                })
            default:
                t.Errorf("unexpected method %s", req.Method)
                return
            }
        }
    })

    client, err := cdp.NewClient(context.Background(), wsURL, testToolsLogger())
    if err != nil {
        t.Fatalf("NewClient failed: %v", err)
    }
    defer client.Close()

    e := &Executor{pageClient: client}

    back, err := e.resolveHistoryEntry(context.Background(), client, -1)
    if err != nil {
        t.Fatalf("resolveHistoryEntry(-1) failed: %v", err)
    }
    if back.ID != 11 || back.URL != "https://a.example" {
        t.Fatalf("unexpected back entry: %#v", back)
    }

    forward, err := e.resolveHistoryEntry(context.Background(), client, 1)
    if err != nil {
        t.Fatalf("resolveHistoryEntry(1) failed: %v", err)
    }
    if forward.ID != 13 || forward.URL != "https://c.example" {
        t.Fatalf("unexpected forward entry: %#v", forward)
    }
}

func TestResolveHistoryEntryOutOfRange(t *testing.T) {
    type cdpRequest struct {
        ID        int64           `json:"id"`
        Method    string          `json:"method"`
        Params    json.RawMessage `json:"params"`
        SessionID string          `json:"sessionId"`
    }

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
            case "Page.getNavigationHistory":
                writeCDPResult(t, ctx, conn, req.ID, map[string]any{
                    "currentIndex": 0,
                    "entries": []map[string]any{
                        {"id": 11, "url": "https://a.example"},
                    },
                })
            default:
                t.Errorf("unexpected method %s", req.Method)
                return
            }
        }
    })

    client, err := cdp.NewClient(context.Background(), wsURL, testToolsLogger())
    if err != nil {
        t.Fatalf("NewClient failed: %v", err)
    }
    defer client.Close()

    e := &Executor{pageClient: client}

    if _, err := e.resolveHistoryEntry(context.Background(), client, -1); err == nil || !strings.Contains(err.Error(), "cannot go back") {
        t.Fatalf("expected cannot go back error, got %v", err)
    }
    if _, err := e.resolveHistoryEntry(context.Background(), client, 1); err == nil || !strings.Contains(err.Error(), "cannot go forward") {
        t.Fatalf("expected cannot go forward error, got %v", err)
    }
}
