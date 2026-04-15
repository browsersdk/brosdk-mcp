package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"brosdk-mcp/internal/cdp"
)

type runtimeRemoteObject struct {
	Type                string `json:"type"`
	Subtype             string `json:"subtype"`
	ClassName           string `json:"className"`
	Value               any    `json:"value"`
	Description         string `json:"description"`
	ObjectID            string `json:"objectId"`
	UnserializableValue string `json:"unserializableValue"`
}

const (
	reconnectAttemptWindow = 15 * time.Second
	reconnectAttemptLimit  = 5
	reconnectBlockDuration = 5 * time.Second
)

var snapshotRefPattern = regexp.MustCompile(`\[ref=e[0-9]+\]`)

func (e *Executor) evaluateString(ctx context.Context, pageClient *cdp.Client, expression string) (string, error) {
	params := map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	}

	raw, err := e.callPageClient(ctx, pageClient, "Runtime.evaluate", params)
	if err != nil {
		return "", err
	}

	result, err := decodeRuntimeEvaluateResult(raw)
	if err != nil {
		return "", err
	}

	switch v := result.Value.(type) {
	case string:
		return v, nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func (e *Executor) evaluateBool(ctx context.Context, pageClient *cdp.Client, expression string) (bool, error) {
	params := map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	}

	raw, err := e.callPageClient(ctx, pageClient, "Runtime.evaluate", params)
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

func decodeRuntimeEvaluateResult(raw json.RawMessage) (runtimeRemoteObject, error) {
	var payload struct {
		Result runtimeRemoteObject `json:"result"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return runtimeRemoteObject{}, fmt.Errorf("decode Runtime.evaluate response: %w", err)
	}
	return payload.Result, nil
}

func (e *Executor) activePageClient(preferred *cdp.Client) *cdp.Client {
	if preferred != nil {
		return preferred
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pageClient
}

func (e *Executor) callPageClient(ctx context.Context, pageClient *cdp.Client, method string, params any) (json.RawMessage, error) {
	pageClient = e.activePageClient(pageClient)
	if pageClient == nil {
		return nil, fmt.Errorf("nil page client for method %s", method)
	}

	raw, err := pageClient.Call(ctx, method, params)
	if err == nil {
		return raw, nil
	}
	if !isCDPConnectionLost(err) {
		return nil, err
	}

	if reconnectErr := e.reconnectCurrentTab(ctx); reconnectErr != nil {
		return nil, fmt.Errorf("%s failed: %w (reconnect failed: %v)", method, err, reconnectErr)
	}

	e.mu.Lock()
	retryClient := e.pageClient
	e.mu.Unlock()
	if retryClient == nil {
		return nil, fmt.Errorf("%s failed: %w (reconnect produced nil page client)", method, err)
	}

	raw, retryErr := retryClient.Call(ctx, method, params)
	if retryErr != nil {
		return nil, fmt.Errorf("%s failed after reconnect: %w (original: %v)", method, retryErr, err)
	}
	return raw, nil
}

func (e *Executor) reconnectCurrentTab(ctx context.Context) error {
	if e.browserClient == nil {
		return fmt.Errorf("cannot reconnect without browser client")
	}

	now := time.Now()
	if err := e.beginReconnectAttempt(now); err != nil {
		return err
	}

	e.mu.Lock()
	targetID := strings.TrimSpace(e.currentTabID)
	e.mu.Unlock()

	if targetID == "" {
		initialTabID, err := e.ensureInitialTab(ctx)
		if err != nil {
			return err
		}
		targetID = strings.TrimSpace(initialTabID)
	}
	if targetID == "" {
		return fmt.Errorf("cannot reconnect without target tab id")
	}

	if err := e.connectToTab(ctx, targetID); err != nil {
		return err
	}

	e.markReconnectSuccess(time.Now())
	return nil
}

func isCDPConnectionLost(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}

	markers := []string{
		"read cdp websocket",
		"write cdp request",
		"failed to get reader",
		"forcibly closed by the remote host",
		"connection reset by peer",
		"broken pipe",
		"closed network connection",
		"websocket closed",
		"statusnormalclosure",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func (e *Executor) beginReconnectAttempt(now time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.reconnectBlockedUntil.IsZero() && now.Before(e.reconnectBlockedUntil) {
		return fmt.Errorf("cdp reconnect temporarily blocked until %s", e.reconnectBlockedUntil.Format(time.RFC3339Nano))
	}
	if !e.reconnectBlockedUntil.IsZero() && !now.Before(e.reconnectBlockedUntil) {
		e.reconnectBlockedUntil = time.Time{}
		e.reconnectWindowStart = now
		e.reconnectAttempts = 0
	}
	if e.reconnectWindowStart.IsZero() || now.Sub(e.reconnectWindowStart) > reconnectAttemptWindow {
		e.reconnectWindowStart = now
		e.reconnectAttempts = 0
	}

	e.reconnectAttempts++
	if e.reconnectAttempts > reconnectAttemptLimit {
		e.reconnectBlockedUntil = now.Add(reconnectBlockDuration)
		return fmt.Errorf("cdp reconnect circuit opened after %d attempts in %s", reconnectAttemptLimit, reconnectAttemptWindow)
	}
	return nil
}

func (e *Executor) markReconnectSuccess(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.reconnectWindowStart = now
	e.reconnectAttempts = 0
	e.reconnectBlockedUntil = time.Time{}
}

func decodeScreenshotData(raw json.RawMessage) (string, []byte, error) {
	var payload struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", nil, fmt.Errorf("decode Page.captureScreenshot response: %w", err)
	}
	if strings.TrimSpace(payload.Data) == "" {
		return "", nil, fmt.Errorf("Page.captureScreenshot returned empty data")
	}

	decoded, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		return "", nil, fmt.Errorf("decode screenshot data: %w", err)
	}
	return payload.Data, decoded, nil
}

func extractRuntimeString(raw json.RawMessage) (string, error) {
	var payload struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decode Runtime.callFunctionOn response: %w", err)
	}

	switch v := payload.Result.Value.(type) {
	case string:
		return v, nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func getStringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

func getBoolArg(args map[string]any, key string) (bool, bool, error) {
	v, ok := args[key]
	if !ok {
		return false, false, nil
	}
	switch x := v.(type) {
	case bool:
		return x, true, nil
	default:
		return false, true, fmt.Errorf("%s must be boolean", key)
	}
}

func getIndexArg(args map[string]any, key string) (int, bool, error) {
	v, ok := args[key]
	if !ok {
		return 0, false, nil
	}
	switch x := v.(type) {
	case float64:
		return int(x), true, nil
	case int:
		return x, true, nil
	case int64:
		return int(x), true, nil
	case json.Number:
		n, err := strconv.Atoi(x.String())
		if err != nil {
			return 0, true, fmt.Errorf("invalid %s value: %w", key, err)
		}
		return n, true, nil
	default:
		return 0, true, fmt.Errorf("%s must be number", key)
	}
}

func getIntArg(args map[string]any, key string) (int, bool, error) {
	v, ok := args[key]
	if !ok {
		return 0, false, nil
	}
	switch x := v.(type) {
	case float64:
		return int(x), true, nil
	case int:
		return x, true, nil
	case int64:
		return int(x), true, nil
	case json.Number:
		n, err := strconv.Atoi(x.String())
		if err != nil {
			return 0, true, fmt.Errorf("invalid %s value: %w", key, err)
		}
		return n, true, nil
	default:
		return 0, true, fmt.Errorf("%s must be number", key)
	}
}

func getIntArgDefault(args map[string]any, key string, defaultValue int) int {
	v, ok, err := getIntArg(args, key)
	if err != nil || !ok {
		return defaultValue
	}
	return v
}

func resolveWaitUntil(args map[string]any) string {
	waitUntil := "none"
	if v, ok := getStringArg(args, "waitUntil"); ok && strings.TrimSpace(v) != "" {
		waitUntil = strings.ToLower(strings.TrimSpace(v))
	}
	if waitUntil == "none" {
		if waitNav, ok, _ := getBoolArg(args, "waitNav"); ok && waitNav {
			waitUntil = "load"
		}
	}
	return waitUntil
}

func matchURLPattern(pattern string, url string) bool {
	p := strings.TrimSpace(pattern)
	u := strings.TrimSpace(url)
	if p == "" {
		return false
	}
	if p == u {
		return true
	}
	if strings.ContainsAny(p, "*?[]") {
		ok, err := path.Match(p, u)
		if err == nil && ok {
			return true
		}
	}

	re, err := globToRegexp(p)
	if err == nil && re.MatchString(u) {
		return true
	}

	return strings.Contains(u, p)
}

func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '\\':
			b.WriteString("\\")
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func estimateRefCount(snapshot string) int {
	if strings.TrimSpace(snapshot) == "" {
		return 0
	}
	return len(snapshotRefPattern.FindAllString(snapshot, -1))
}
