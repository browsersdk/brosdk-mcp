package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"brosdk-mcp/internal/cdp"
	"github.com/coder/websocket"
)

func TestDecodeRuntimeEvaluateResult(t *testing.T) {
	raw := json.RawMessage(`{"result":{"type":"object","subtype":"array","className":"Array","value":["a",1],"description":"Array(2)"}}`)

	got, err := decodeRuntimeEvaluateResult(raw)
	if err != nil {
		t.Fatalf("decodeRuntimeEvaluateResult failed: %v", err)
	}
	if got.Type != "object" {
		t.Fatalf("unexpected type: %q", got.Type)
	}
	if got.Subtype != "array" {
		t.Fatalf("unexpected subtype: %q", got.Subtype)
	}
	if got.ClassName != "Array" {
		t.Fatalf("unexpected className: %q", got.ClassName)
	}
	values, ok := got.Value.([]any)
	if !ok || len(values) != 2 {
		t.Fatalf("unexpected value payload: %#v", got.Value)
	}
}

func TestDecodeScreenshotData(t *testing.T) {
	raw := json.RawMessage(`{"data":"aGVsbG8="}`)

	data, decoded, err := decodeScreenshotData(raw)
	if err != nil {
		t.Fatalf("decodeScreenshotData failed: %v", err)
	}
	if data != "aGVsbG8=" {
		t.Fatalf("unexpected raw data: %q", data)
	}
	if string(decoded) != "hello" {
		t.Fatalf("unexpected decoded bytes: %q", string(decoded))
	}
}

func TestExtractLifecycleEventName(t *testing.T) {
	name, err := extractLifecycleEventName(cdp.Event{
		Method: "Page.lifecycleEvent",
		Params: json.RawMessage(`{"name":"networkIdle"}`),
	})
	if err != nil {
		t.Fatalf("extractLifecycleEventName failed: %v", err)
	}
	if name != "networkidle" {
		t.Fatalf("unexpected lifecycle event name: %q", name)
	}
}

func TestExtractLifecycleEventInfo(t *testing.T) {
	name, frameID, loaderID, err := extractLifecycleEventInfo(cdp.Event{
		Method: "Page.lifecycleEvent",
		Params: json.RawMessage(`{"name":"networkAlmostIdle","frameId":"main","loaderId":"loader-1"}`),
	})
	if err != nil {
		t.Fatalf("extractLifecycleEventInfo failed: %v", err)
	}
	if name != "networkalmostidle" {
		t.Fatalf("unexpected lifecycle name: %q", name)
	}
	if frameID != "main" {
		t.Fatalf("unexpected frameId: %q", frameID)
	}
	if loaderID != "loader-1" {
		t.Fatalf("unexpected loaderId: %q", loaderID)
	}
}

func TestLoadEventSubscriptionForNetworkIdle(t *testing.T) {
	sub := loadEventSubscription("networkidle")
	if sub.method != "Page.lifecycleEvent" {
		t.Fatalf("unexpected method: %q", sub.method)
	}
	if sub.match == nil {
		t.Fatalf("expected networkidle matcher")
	}
	if !sub.match(cdp.Event{Params: json.RawMessage(`{"name":"networkIdle"}`)}) {
		t.Fatalf("expected networkIdle to match")
	}
	if !sub.match(cdp.Event{Params: json.RawMessage(`{"name":"networkAlmostIdle"}`)}) {
		t.Fatalf("expected networkAlmostIdle to match")
	}
	if sub.match(cdp.Event{Params: json.RawMessage(`{"name":"load"}`)}) {
		t.Fatalf("did not expect load to match networkidle")
	}
}

func TestIsMatchingLifecycleEventForNavigation(t *testing.T) {
	match := cdp.Event{
		Method: "Page.lifecycleEvent",
		Params: json.RawMessage(`{"name":"networkIdle","frameId":"main","loaderId":"loader-1"}`),
	}
	if !isMatchingLifecycleEventForNavigation(match, navigationTarget{FrameID: "main", LoaderID: "loader-1"}) {
		t.Fatalf("expected lifecycle event to match navigation target")
	}
	if isMatchingLifecycleEventForNavigation(match, navigationTarget{FrameID: "other"}) {
		t.Fatalf("did not expect lifecycle frame mismatch to match")
	}
	if isMatchingLifecycleEventForNavigation(match, navigationTarget{LoaderID: "loader-2"}) {
		t.Fatalf("did not expect lifecycle loader mismatch to match")
	}
	if isMatchingLifecycleEventForNavigation(cdp.Event{
		Method: "Page.lifecycleEvent",
		Params: json.RawMessage(`{"name":"load","frameId":"main","loaderId":"loader-1"}`),
	}, navigationTarget{FrameID: "main", LoaderID: "loader-1"}) {
		t.Fatalf("did not expect non-networkidle lifecycle event to match")
	}
}

func TestIsMainFrameNavigatedEvent(t *testing.T) {
	if !isMainFrameNavigatedEvent(cdp.Event{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{"frame":{"id":"main","url":"https://example.com"}}`),
	}) {
		t.Fatalf("expected main frame event to match")
	}

	if isMainFrameNavigatedEvent(cdp.Event{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{"frame":{"id":"child","parentId":"main","url":"https://example.com/frame"}}`),
	}) {
		t.Fatalf("did not expect child frame event to match")
	}
}

func TestIsMatchingMainFrameNavigatedEvent(t *testing.T) {
	ev := cdp.Event{
		Method: "Page.frameNavigated",
		Params: json.RawMessage(`{"frame":{"id":"main","loaderId":"loader-1","url":"https://example.com"}}`),
	}
	if !isMatchingMainFrameNavigatedEvent(ev, navigationTarget{FrameID: "main", LoaderID: "loader-1"}) {
		t.Fatalf("expected exact frame/loader match")
	}
	if isMatchingMainFrameNavigatedEvent(ev, navigationTarget{FrameID: "main", LoaderID: "loader-2"}) {
		t.Fatalf("did not expect mismatched loader to match")
	}
	if isMatchingMainFrameNavigatedEvent(ev, navigationTarget{FrameID: "other"}) {
		t.Fatalf("did not expect mismatched frame to match")
	}
}

func TestShouldFallbackNetworkIdleWithoutLifecycle(t *testing.T) {
	now := time.Now()
	base := now.Add(-2 * time.Second)

	if !shouldFallbackNetworkIdleWithoutLifecycle("networkidle", true, true, false, false, base, now) {
		t.Fatalf("expected networkidle fallback to be enabled")
	}
	if shouldFallbackNetworkIdleWithoutLifecycle("load", true, true, false, false, base, now) {
		t.Fatalf("did not expect non-networkidle fallback")
	}
	if shouldFallbackNetworkIdleWithoutLifecycle("networkidle", false, true, false, false, base, now) {
		t.Fatalf("did not expect fallback without main-frame requirement")
	}
	if shouldFallbackNetworkIdleWithoutLifecycle("networkidle", true, false, false, false, base, now) {
		t.Fatalf("did not expect fallback before main-frame navigation")
	}
	if shouldFallbackNetworkIdleWithoutLifecycle("networkidle", true, true, true, false, base, now) {
		t.Fatalf("did not expect fallback after load event matched")
	}
	if shouldFallbackNetworkIdleWithoutLifecycle("networkidle", true, true, false, true, base, now) {
		t.Fatalf("did not expect fallback after observing lifecycle event")
	}
	if shouldFallbackNetworkIdleWithoutLifecycle("networkidle", true, true, false, false, now.Add(-200*time.Millisecond), now) {
		t.Fatalf("did not expect fallback before grace delay")
	}
}

func TestIsCDPConnectionLost(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "read loop eof",
			err:  fmt.Errorf("read cdp websocket: failed to get reader: failed to read frame header: EOF"),
			want: true,
		},
		{
			name: "write closed connection",
			err:  fmt.Errorf("write cdp request \"Runtime.evaluate\": write tcp: use of closed network connection"),
			want: true,
		},
		{
			name: "remote host closed",
			err:  fmt.Errorf("read cdp websocket: wsarecv: An existing connection was forcibly closed by the remote host."),
			want: true,
		},
		{
			name: "regular validation error",
			err:  fmt.Errorf("missing required argument selector"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isCDPConnectionLost(tc.err)
			if got != tc.want {
				t.Fatalf("isCDPConnectionLost(%v)=%v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestReconnectCircuitOpensAndRecovers(t *testing.T) {
	e := &Executor{}
	base := time.Now()

	for i := 0; i < reconnectAttemptLimit; i++ {
		if err := e.beginReconnectAttempt(base.Add(time.Duration(i) * time.Second)); err != nil {
			t.Fatalf("unexpected reconnect attempt error at %d: %v", i, err)
		}
	}

	openErr := e.beginReconnectAttempt(base.Add(6 * time.Second))
	if openErr == nil || !strings.Contains(openErr.Error(), "circuit opened") {
		t.Fatalf("expected circuit-open error, got %v", openErr)
	}

	blockedErr := e.beginReconnectAttempt(base.Add(7 * time.Second))
	if blockedErr == nil || !strings.Contains(blockedErr.Error(), "temporarily blocked") {
		t.Fatalf("expected blocked error, got %v", blockedErr)
	}

	recoverAt := base.Add(6*time.Second + reconnectBlockDuration + time.Millisecond)
	if err := e.beginReconnectAttempt(recoverAt); err != nil {
		t.Fatalf("expected reconnect to recover after block window, got %v", err)
	}
}

func TestMarkReconnectSuccessResetsWindow(t *testing.T) {
	e := &Executor{}
	base := time.Now()

	if err := e.beginReconnectAttempt(base); err != nil {
		t.Fatalf("unexpected first reconnect attempt error: %v", err)
	}
	if err := e.beginReconnectAttempt(base.Add(time.Second)); err != nil {
		t.Fatalf("unexpected second reconnect attempt error: %v", err)
	}

	e.markReconnectSuccess(base.Add(2 * time.Second))

	for i := 0; i < reconnectAttemptLimit; i++ {
		if err := e.beginReconnectAttempt(base.Add(3*time.Second + time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("unexpected reconnect attempt after success reset at %d: %v", i, err)
		}
	}
}

func TestRemainingTimeoutMs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	got := remainingTimeoutMs(ctx, 9999)
	if got <= 0 || got > 1200 {
		t.Fatalf("unexpected remaining timeout with deadline: %d", got)
	}

	noDeadline := remainingTimeoutMs(context.Background(), 4321)
	if noDeadline != 4321 {
		t.Fatalf("unexpected remaining timeout without deadline: %d", noDeadline)
	}

	noDeadlineDefault := remainingTimeoutMs(context.Background(), 0)
	if noDeadlineDefault != 30000 {
		t.Fatalf("unexpected fallback timeout: %d", noDeadlineDefault)
	}
}

func TestNormalizeEvaluateExpression(t *testing.T) {
	got, err := normalizeEvaluateExpression("  document.title  ")
	if err != nil {
		t.Fatalf("normalizeEvaluateExpression failed: %v", err)
	}
	if got != "document.title" {
		t.Fatalf("unexpected normalized expression: %q", got)
	}

	_, err = normalizeEvaluateExpression(strings.Repeat("x", maxEvaluateExpressionLen+1))
	if err == nil {
		t.Fatalf("expected oversized expression to fail")
	}
}

func TestResolveScreenshotOptions(t *testing.T) {
	opts, err := resolveScreenshotOptions(map[string]any{
		"format":   "jpeg",
		"quality":  float64(80),
		"fullPage": true,
		"path":     "shots\\page.jpg",
	})
	if err != nil {
		t.Fatalf("resolveScreenshotOptions failed: %v", err)
	}
	if opts.format != "jpeg" || opts.quality != 80 || !opts.fullPage || !opts.hasPath {
		t.Fatalf("unexpected screenshot options: %#v", opts)
	}

	_, err = resolveScreenshotOptions(map[string]any{
		"format":  "png",
		"quality": float64(50),
	})
	if err == nil {
		t.Fatalf("expected png quality validation to fail")
	}
}

func TestBuildAriaSnapshotExpressionIncludesParameters(t *testing.T) {
	expr := buildAriaSnapshotExpression("#app", false, false, 12)
	if !strings.Contains(expr, `var sel = "#app";`) {
		t.Fatalf("selector not encoded in aria snapshot expression: %s", expr)
	}
	if !strings.Contains(expr, `var interactiveOnly = false;`) {
		t.Fatalf("interactive flag not encoded in aria snapshot expression")
	}
	if !strings.Contains(expr, `var compact = false;`) {
		t.Fatalf("compact flag not encoded in aria snapshot expression")
	}
	if !strings.Contains(expr, `var maxDepth = 12;`) {
		t.Fatalf("maxDepth not encoded in aria snapshot expression")
	}
	if !strings.Contains(expr, `window.__ariaRefMeta = { __url: location.href, __createdAt: Date.now() };`) {
		t.Fatalf("aria ref metadata root not initialized")
	}
	if !strings.Contains(expr, `window.__ariaRefMeta[ref] = {`) {
		t.Fatalf("aria ref metadata entry not assigned")
	}
}

func TestSelectorExpressionsUseDeepSearch(t *testing.T) {
	clickExpr := buildClickSelectorExpression("#shadowBtn")
	if !strings.Contains(clickExpr, "findFirstDeep(sel, document)") {
		t.Fatalf("click selector expression did not use deep search")
	}
	if strings.Contains(clickExpr, "document.querySelector(sel)") {
		t.Fatalf("click selector expression still uses direct querySelector")
	}

	waitExpr := buildSelectorStateExpression("#shadowBtn", "visible")
	if !strings.Contains(waitExpr, "findFirstDeep(sel, document)") {
		t.Fatalf("wait selector expression did not use deep search")
	}
}

func TestRefExpressionsUseResolveRefElementFallback(t *testing.T) {
	clickExpr := buildClickRefExpression("e7")
	if !strings.Contains(clickExpr, "resolveRefElement(ref)") {
		t.Fatalf("click by ref expression did not use resolveRefElement fallback")
	}
	if !strings.Contains(clickExpr, "var metaRoot = window.__ariaRefMeta || {};") {
		t.Fatalf("click by ref expression missing ref metadata fallback helpers")
	}

	typeExpr := buildFocusRefExpression("e9", true)
	if !strings.Contains(typeExpr, "resolveRefElement(ref)") {
		t.Fatalf("focus by ref expression did not use resolveRefElement fallback")
	}

	setExpr := buildSetValueRefExpression("e11", "abc")
	if !strings.Contains(setExpr, "resolveRefElement(ref)") {
		t.Fatalf("set value by ref expression did not use resolveRefElement fallback")
	}
	if !strings.Contains(setExpr, "metaRoot.__url !== location.href") {
		t.Fatalf("set value by ref expression missing same-url safeguard for fallback")
	}
}

func TestWaitTextExpressionUsesDeepTextCollection(t *testing.T) {
	expr := buildWaitTextExpression("Shadow Action", false)
	if !strings.Contains(expr, "collectDeepText(document.body, 200000)") {
		t.Fatalf("wait text expression did not use deep text collection")
	}
	if strings.Contains(expr, "document.body.innerText") {
		t.Fatalf("wait text expression still uses plain body.innerText")
	}
}

func TestDecodePageNavigateResult(t *testing.T) {
	got, err := decodePageNavigateResult(json.RawMessage(`{"frameId":"main","loaderId":"loader-1"}`))
	if err != nil {
		t.Fatalf("decodePageNavigateResult failed: %v", err)
	}
	if got.FrameID != "main" || got.LoaderID != "loader-1" {
		t.Fatalf("unexpected navigate result: %#v", got)
	}

	_, err = decodePageNavigateResult(json.RawMessage(`{"errorText":"net::ERR_ABORTED"}`))
	if err == nil {
		t.Fatalf("expected errorText to fail")
	}
}

func TestActivePageClientPrefersExecutorCurrent(t *testing.T) {
	wsURL := startWaitCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			payload, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{}})
			if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
				t.Errorf("write response: %v", err)
				return
			}
		}
	})

	currentClient, err := cdp.NewClient(context.Background(), wsURL, testWaitLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer currentClient.Close()

	e := &Executor{pageClient: currentClient}
	if got := e.activePageClient(nil); got != currentClient {
		t.Fatalf("expected active page client to use executor current client")
	}
}

func TestWaitForLoadStateUsesExecutorCurrentClient(t *testing.T) {
	wsURL := startWaitCDPTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}

			var req struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}

			switch req.Method {
			case "Runtime.evaluate":
				payload, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"result": map[string]any{"type": "string", "value": "complete"},
					},
				})
				if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
					t.Errorf("write response: %v", err)
					return
				}
			default:
				payload, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{}})
				if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
					t.Errorf("write response: %v", err)
					return
				}
			}
		}
	})

	currentClient, err := cdp.NewClient(context.Background(), wsURL, testWaitLogger())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer currentClient.Close()

	e := &Executor{pageClient: currentClient}
	if err := e.waitForLoadState(context.Background(), nil, "load", 1000); err != nil {
		t.Fatalf("waitForLoadState should use executor current client, got %v", err)
	}
}

func startWaitCDPTestServer(t *testing.T, onConnect func(context.Context, *websocket.Conn)) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func testWaitLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
