package cdp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildVersionURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "host-port",
			input:    "localhost:9222",
			expected: "http://localhost:9222/json/version",
		},
		{
			name:     "http",
			input:    "http://127.0.0.1:9222",
			expected: "http://127.0.0.1:9222/json/version",
		},
		{
			name:     "https-with-path",
			input:    "https://example.com/anything",
			expected: "https://example.com/json/version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildVersionURL(tt.input)
			if err != nil {
				t.Fatalf("buildVersionURL(%q) error: %v", tt.input, err)
			}
			if got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestDiscoverWebSocketURLRetriesTransientStatus(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		attempt := attempts.Add(1)
		if attempt <= 2 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/abc"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := DiscoverWebSocketURL(ctx, srv.URL)
	if err != nil {
		t.Fatalf("DiscoverWebSocketURL failed: %v", err)
	}
	if got != "ws://127.0.0.1:9222/devtools/browser/abc" {
		t.Fatalf("unexpected websocket url: %q", got)
	}
	if gotAttempts := attempts.Load(); gotAttempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", gotAttempts)
	}
}

func TestDiscoverWebSocketURLDoesNotRetryNonRetriableStatus(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		attempts.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := DiscoverWebSocketURL(ctx, srv.URL)
	if err == nil {
		t.Fatalf("expected DiscoverWebSocketURL to fail")
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAttempts := attempts.Load(); gotAttempts != 1 {
		t.Fatalf("expected 1 attempt on non-retriable status, got %d", gotAttempts)
	}
}

func TestListTargetsRetriesTransientStatus(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		attempt := attempts.Add(1)
		if attempt == 1 {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"target-1","type":"page","title":"T","url":"https://example.com","webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/1"}]`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	targets, err := ListTargets(ctx, srv.URL)
	if err != nil {
		t.Fatalf("ListTargets failed: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != "target-1" {
		t.Fatalf("unexpected targets: %#v", targets)
	}
	if gotAttempts := attempts.Load(); gotAttempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", gotAttempts)
	}
}
