package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type responseEnvelope struct {
	ID        *int64          `json:"id,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *rpcError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type eventEnvelope struct {
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type requestEnvelope struct {
	ID        int64  `json:"id"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("cdp rpc error %d: %s", e.Code, e.Message)
}

type BrowserVersion struct {
	ProtocolVersion string `json:"protocolVersion"`
	Product         string `json:"product"`
	Revision        string `json:"revision"`
	UserAgent       string `json:"userAgent"`
	JSVersion       string `json:"jsVersion"`
}

type Target struct {
	ID       string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Attached bool   `json:"attached"`
}

type pendingResponse struct {
	result json.RawMessage
	err    error
}

type Event struct {
	Method    string
	Params    json.RawMessage
	SessionID string
}

type eventSubscriber struct {
	method    string
	sessionID string
	ch        chan Event
}

type clientCore struct {
	conn   *websocket.Conn
	logger *slog.Logger

	bgCtx    context.Context
	bgCancel context.CancelFunc

	nextID  atomic.Int64
	pending map[int64]chan pendingResponse
	nextSub atomic.Int64

	subscribers map[int64]eventSubscriber
	mu          sync.Mutex
}

type Client struct {
	core      *clientCore
	sessionID string

	closeOnce sync.Once
	closeFn   func() error
}

const (
	cdpDialAttempts   = 4
	cdpDialBackoffMin = 100 * time.Millisecond
)

func NewClient(ctx context.Context, wsURL string, logger *slog.Logger) (*Client, error) {
	conn, err := dialWithRetry(ctx, wsURL)
	if err != nil {
		return nil, err
	}

	bgCtx, cancel := context.WithCancel(context.Background())
	core := &clientCore{
		conn:        conn,
		logger:      logger,
		bgCtx:       bgCtx,
		bgCancel:    cancel,
		pending:     make(map[int64]chan pendingResponse),
		subscribers: make(map[int64]eventSubscriber),
	}
	client := &Client{
		core: core,
		closeFn: func() error {
			core.bgCancel()
			return core.conn.Close(websocket.StatusNormalClosure, "shutdown")
		},
	}
	go core.readLoop()
	return client, nil
}

func dialWithRetry(ctx context.Context, wsURL string) (*websocket.Conn, error) {
	var lastErr error
	for attempt := 1; attempt <= cdpDialAttempts; attempt++ {
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if attempt >= cdpDialAttempts {
			break
		}
		if sleepErr := sleepWithContext(ctx, retryBackoff(attempt, cdpDialBackoffMin)); sleepErr != nil {
			return nil, fmt.Errorf("dial cdp websocket %q: %w", wsURL, lastErr)
		}
	}
	return nil, fmt.Errorf("dial cdp websocket %q: %w", wsURL, lastErr)
}

func retryBackoff(attempt int, base time.Duration) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	if base <= 0 {
		base = 50 * time.Millisecond
	}

	backoff := base
	for i := 1; i < attempt; i++ {
		backoff *= 2
		if backoff > 2*time.Second {
			return 2 * time.Second
		}
	}
	return backoff
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) SessionID() string {
	return c.sessionID
}

func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		if c.closeFn != nil {
			err = c.closeFn()
		}
	})
	return err
}

func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.core.nextID.Add(1)
	respCh := make(chan pendingResponse, 1)

	c.core.mu.Lock()
	c.core.pending[id] = respCh
	c.core.mu.Unlock()

	req := requestEnvelope{
		ID:        id,
		Method:    method,
		Params:    params,
		SessionID: c.sessionID,
	}

	raw, err := json.Marshal(req)
	if err != nil {
		c.core.removePending(id)
		return nil, fmt.Errorf("marshal cdp request %q: %w", method, err)
	}

	if err := c.core.conn.Write(ctx, websocket.MessageText, raw); err != nil {
		c.core.removePending(id)
		return nil, fmt.Errorf("write cdp request %q: %w", method, err)
	}

	select {
	case msg := <-respCh:
		if msg.err != nil {
			return nil, msg.err
		}
		return msg.result, nil
	case <-ctx.Done():
		c.core.removePending(id)
		return nil, ctx.Err()
	}
}

func (c *Client) SubscribeEvents(method string, buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 16
	}

	id := c.core.nextSub.Add(1)
	sub := eventSubscriber{
		method:    strings.TrimSpace(method),
		sessionID: c.sessionID,
		ch:        make(chan Event, buffer),
	}

	c.core.mu.Lock()
	c.core.subscribers[id] = sub
	c.core.mu.Unlock()

	unsubscribe := func() {
		c.core.mu.Lock()
		delete(c.core.subscribers, id)
		c.core.mu.Unlock()
	}
	return sub.ch, unsubscribe
}

func (c *Client) WaitForEvent(ctx context.Context, method string, match func(Event) bool) (Event, error) {
	events, unsubscribe := c.SubscribeEvents(method, 16)
	defer unsubscribe()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return Event{}, errors.New("cdp event stream closed")
			}
			if match == nil || match(ev) {
				return ev, nil
			}
		case <-ctx.Done():
			return Event{}, ctx.Err()
		}
	}
}

func (c *Client) AttachToTarget(ctx context.Context, targetID string) (*Client, error) {
	raw, err := c.Call(ctx, "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	})
	if err != nil {
		return nil, err
	}

	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode Target.attachToTarget response: %w", err)
	}
	payload.SessionID = strings.TrimSpace(payload.SessionID)
	if payload.SessionID == "" {
		return nil, fmt.Errorf("Target.attachToTarget returned empty sessionId")
	}

	return &Client{
		core:      c.core,
		sessionID: payload.SessionID,
		closeFn: func() error {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return c.DetachFromTarget(closeCtx, payload.SessionID)
		},
	}, nil
}

func (c *Client) DetachFromTarget(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	_, err := c.Call(ctx, "Target.detachFromTarget", map[string]any{
		"sessionId": sessionID,
	})
	return err
}

func (c *Client) GetBrowserVersion(ctx context.Context) (BrowserVersion, error) {
	raw, err := c.Call(ctx, "Browser.getVersion", nil)
	if err != nil {
		return BrowserVersion{}, err
	}

	var v BrowserVersion
	if err := json.Unmarshal(raw, &v); err != nil {
		return BrowserVersion{}, fmt.Errorf("decode Browser.getVersion response: %w", err)
	}
	return v, nil
}

func (c *Client) GetTargets(ctx context.Context) ([]Target, error) {
	raw, err := c.Call(ctx, "Target.getTargets", nil)
	if err != nil {
		return nil, err
	}

	var payload struct {
		TargetInfos []Target `json:"targetInfos"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode Target.getTargets response: %w", err)
	}
	return payload.TargetInfos, nil
}

func (c *Client) CreateTarget(ctx context.Context, url string) (string, error) {
	raw, err := c.Call(ctx, "Target.createTarget", map[string]any{
		"url": url,
	})
	if err != nil {
		return "", err
	}

	var payload struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decode Target.createTarget response: %w", err)
	}
	if payload.TargetID == "" {
		return "", fmt.Errorf("Target.createTarget returned empty targetId")
	}
	return payload.TargetID, nil
}

func (c *Client) ActivateTarget(ctx context.Context, targetID string) error {
	_, err := c.Call(ctx, "Target.activateTarget", map[string]any{
		"targetId": targetID,
	})
	return err
}

func (c *Client) CloseTarget(ctx context.Context, targetID string) error {
	_, err := c.Call(ctx, "Target.closeTarget", map[string]any{
		"targetId": targetID,
	})
	return err
}

func (c *clientCore) readLoop() {
	for {
		_, raw, err := c.conn.Read(c.bgCtx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				c.failAllPending(err)
				c.closeAllSubscribers()
				return
			}
			c.failAllPending(fmt.Errorf("read cdp websocket: %w", err))
			c.closeAllSubscribers()
			return
		}

		var env responseEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			c.logger.Debug("ignore non-response payload", "error", err)
			continue
		}

		if env.ID == nil {
			c.dispatchEvent(raw)
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[*env.ID]
		if ok {
			delete(c.pending, *env.ID)
		}
		c.mu.Unlock()

		if !ok {
			continue
		}

		if env.Error != nil {
			ch <- pendingResponse{err: env.Error}
			continue
		}

		ch <- pendingResponse{result: env.Result}
	}
}

func (c *clientCore) removePending(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}

func (c *clientCore) failAllPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- pendingResponse{err: err}
	}
}

func (c *clientCore) dispatchEvent(raw json.RawMessage) {
	var env eventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		c.logger.Debug("ignore invalid event payload", "error", err)
		return
	}

	method := strings.TrimSpace(env.Method)
	if method == "" {
		return
	}

	event := Event{
		Method:    method,
		Params:    env.Params,
		SessionID: strings.TrimSpace(env.SessionID),
	}

	c.mu.Lock()
	subs := make([]eventSubscriber, 0, len(c.subscribers))
	for _, sub := range c.subscribers {
		if sub.method != "" && sub.method != method {
			continue
		}
		if sub.sessionID != event.SessionID {
			continue
		}
		subs = append(subs, sub)
	}
	c.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.ch <- event:
		default:
			c.logger.Debug("drop cdp event for slow subscriber", "method", method, "sessionId", event.SessionID)
		}
	}
}

func (c *clientCore) closeAllSubscribers() {
	c.mu.Lock()
	subs := c.subscribers
	c.subscribers = make(map[int64]eventSubscriber)
	c.mu.Unlock()

	for _, sub := range subs {
		close(sub.ch)
	}
}
