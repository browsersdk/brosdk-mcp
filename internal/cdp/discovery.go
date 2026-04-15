package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type versionResponse struct {
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type TargetInfo struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

const (
	discoveryAttempts   = 3
	discoveryBackoffMin = 100 * time.Millisecond
)

func DiscoverWebSocketURL(ctx context.Context, endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("empty cdp endpoint")
	}

	if strings.HasPrefix(endpoint, "ws://") || strings.HasPrefix(endpoint, "wss://") {
		return endpoint, nil
	}

	versionURL, err := buildVersionURL(endpoint)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return "", fmt.Errorf("build version request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	respBody, err := doRequestWithRetry(ctx, client, req, versionURL, "version")
	if err != nil {
		return "", err
	}

	var payload versionResponse
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return "", fmt.Errorf("decode cdp version response: %w", err)
	}
	if strings.TrimSpace(payload.WebSocketDebuggerURL) == "" {
		return "", fmt.Errorf("cdp version response missing webSocketDebuggerUrl")
	}

	return payload.WebSocketDebuggerURL, nil
}

func buildVersionURL(endpoint string) (string, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return "", fmt.Errorf("empty endpoint")
	}

	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse cdp endpoint %q: %w", endpoint, err)
	}

	switch u.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported cdp endpoint scheme %q", u.Scheme)
	}

	if u.Host == "" {
		return "", fmt.Errorf("cdp endpoint %q missing host", endpoint)
	}

	u.Path = "/json/version"
	u.RawQuery = ""
	u.Fragment = ""

	return u.String(), nil
}

func DiscoverPageWebSocketURL(ctx context.Context, endpoint string) (string, error) {
	targets, err := ListTargets(ctx, endpoint)
	if err != nil {
		return "", err
	}

	for _, t := range targets {
		if t.Type == "page" && strings.TrimSpace(t.WebSocketDebuggerURL) != "" {
			return t.WebSocketDebuggerURL, nil
		}
	}

	return "", fmt.Errorf("no page websocket target found from %q", endpoint)
}

func DiscoverPageWebSocketURLByTargetID(ctx context.Context, endpoint string, targetID string) (string, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return "", fmt.Errorf("empty targetID")
	}

	targets, err := ListTargets(ctx, endpoint)
	if err != nil {
		return "", err
	}

	for _, t := range targets {
		if t.ID == targetID && t.Type == "page" && strings.TrimSpace(t.WebSocketDebuggerURL) != "" {
			return t.WebSocketDebuggerURL, nil
		}
	}

	return "", fmt.Errorf("page websocket target %q not found", targetID)
}

func ListTargets(ctx context.Context, endpoint string) ([]TargetInfo, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("empty cdp endpoint")
	}
	if strings.HasPrefix(endpoint, "ws://") || strings.HasPrefix(endpoint, "wss://") {
		return nil, fmt.Errorf("target discovery requires host:port or http(s) endpoint, got websocket endpoint")
	}

	listURL, err := buildListURL(endpoint)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build list request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	respBody, err := doRequestWithRetry(ctx, client, req, listURL, "list")
	if err != nil {
		return nil, err
	}

	var targets []TargetInfo
	if err := json.Unmarshal(respBody, &targets); err != nil {
		return nil, fmt.Errorf("decode cdp list response: %w", err)
	}
	return targets, nil
}

func doRequestWithRetry(ctx context.Context, client *http.Client, req *http.Request, endpointURL string, endpointName string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	var lastErr error
	for attempt := 1; attempt <= discoveryAttempts; attempt++ {
		reqCopy := req.Clone(ctx)

		resp, err := client.Do(reqCopy)
		if err != nil {
			lastErr = fmt.Errorf("request cdp %s endpoint %q: %w", endpointName, endpointURL, err)
			if attempt >= discoveryAttempts {
				break
			}
			if sleepErr := sleepWithContext(ctx, retryBackoff(attempt, discoveryBackoffMin)); sleepErr != nil {
				break
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read cdp %s endpoint %q: %w", endpointName, endpointURL, readErr)
			if attempt >= discoveryAttempts {
				break
			}
			if sleepErr := sleepWithContext(ctx, retryBackoff(attempt, discoveryBackoffMin)); sleepErr != nil {
				break
			}
			continue
		}

		if resp.StatusCode == http.StatusOK {
			return body, nil
		}

		lastErr = fmt.Errorf("cdp %s endpoint %q returned status %d", endpointName, endpointURL, resp.StatusCode)
		if !isRetriableDiscoveryStatus(resp.StatusCode) || attempt >= discoveryAttempts {
			break
		}
		if sleepErr := sleepWithContext(ctx, retryBackoff(attempt, discoveryBackoffMin)); sleepErr != nil {
			break
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("request cdp %s endpoint %q failed", endpointName, endpointURL)
}

func isRetriableDiscoveryStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func buildListURL(endpoint string) (string, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return "", fmt.Errorf("empty endpoint")
	}

	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse cdp endpoint %q: %w", endpoint, err)
	}

	switch u.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported cdp endpoint scheme %q", u.Scheme)
	}

	if u.Host == "" {
		return "", fmt.Errorf("cdp endpoint %q missing host", endpoint)
	}

	u.Path = "/json/list"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
