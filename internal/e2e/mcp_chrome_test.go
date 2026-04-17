package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestE2EChromeNavigateAndAriaSnapshot(t *testing.T) {
	if os.Getenv("BROSDK_E2E") != "1" {
		t.Skip("set BROSDK_E2E=1 to run e2e test")
	}

	chromePath, ok := findChromeExecutable()
	if !ok {
		t.Skip("chrome executable not found")
	}

	tempDir := t.TempDir()
	chromeCmd, debugPort, chromeStderr, err := startChromeWithDynamicDebugPort(chromePath, tempDir)
	if err != nil {
		t.Fatalf("start chrome failed: %v", err)
	}
	defer func() {
		_ = chromeCmd.Process.Kill()
		_, _ = chromeCmd.Process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var mcpStderr bytes.Buffer
	mcpCmd := exec.CommandContext(
		ctx,
		"go", "run", "./cmd/brosdk-mcp",
		"--mode", "stdio",
		"--cdp", fmt.Sprintf("127.0.0.1:%d", debugPort),
		"--schema", "schemas/browser-tools.schema.json",
	)
	mcpCmd.Dir = repoRootFromTest(t)
	mcpCmd.Stderr = &mcpStderr

	stdin, err := mcpCmd.StdinPipe()
	if err != nil {
		t.Fatalf("get mcp stdin pipe failed: %v", err)
	}
	stdout, err := mcpCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("get mcp stdout pipe failed: %v", err)
	}
	reader := bufio.NewReader(stdout)

	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start mcp failed: %v", err)
	}
	defer func() {
		_ = mcpCmd.Process.Kill()
		_, _ = mcpCmd.Process.Wait()
	}()

	navigateResult := sendToolsCall(t, stdin, reader, 1, "browser_navigate", map[string]any{
		"url": "https://forum.brosdk.com",
	})
	if okValue, ok := navigateResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_navigate unexpected result: %#v", navigateResult)
	}

	waitLoadResult := sendToolsCall(t, stdin, reader, 2, "browser_wait_for_load", map[string]any{
		"waitUntil": "load",
		"timeoutMs": 30000,
	})
	if okValue, ok := waitLoadResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_wait_for_load unexpected result: %#v", waitLoadResult)
	}

	waitSelectorResult := sendToolsCall(t, stdin, reader, 3, "browser_wait_for_selector", map[string]any{
		"selector":  "body",
		"state":     "visible",
		"timeoutMs": 30000,
	})
	if okValue, ok := waitSelectorResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_wait_for_selector unexpected result: %#v", waitSelectorResult)
	}

	waitURLResult := sendToolsCall(t, stdin, reader, 4, "browser_wait_for_url", map[string]any{
		"url":       "*forum.brosdk.com*",
		"timeoutMs": 30000,
	})
	if okValue, ok := waitURLResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_wait_for_url unexpected result: %#v", waitURLResult)
	}

	getTextResult := sendToolsCall(t, stdin, reader, 5, "browser_get_text", map[string]any{
		"maxChars": 1000,
	})
	pageText, ok := getTextResult["text"].(string)
	if !ok || strings.TrimSpace(pageText) == "" {
		t.Fatalf("browser_get_text returned empty text: %#v", getTextResult)
	}

	snapshotResult := sendToolsCall(t, stdin, reader, 6, "browser_aria_snapshot", map[string]any{})
	snapshot, ok := snapshotResult["snapshot"].(string)
	if !ok || strings.TrimSpace(snapshot) == "" {
		t.Fatalf("browser_aria_snapshot returned empty snapshot: %#v", snapshotResult)
	}
	if !strings.Contains(strings.ToLower(snapshot), "document") {
		t.Fatalf("browser_aria_snapshot missing document header: %q", snapshot)
	}

	if strings.TrimSpace(mcpStderr.String()) == "" {
		// keep diagnostics only for failures
		t.Logf("chrome stderr: %s", chromeStderr)
	}
}

func TestE2EChromeNavigateAndAriaSnapshotSSE(t *testing.T) {
	if os.Getenv("BROSDK_E2E") != "1" {
		t.Skip("set BROSDK_E2E=1 to run e2e test")
	}

	chromePath, ok := findChromeExecutable()
	if !ok {
		t.Skip("chrome executable not found")
	}

	tempDir := t.TempDir()
	chromeCmd, debugPort, _, err := startChromeWithDynamicDebugPort(chromePath, tempDir)
	if err != nil {
		t.Fatalf("start chrome failed: %v", err)
	}
	defer func() {
		_ = chromeCmd.Process.Kill()
		_, _ = chromeCmd.Process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	ssePort := findFreeTCPPort(t)
	messageEndpoint := fmt.Sprintf("http://127.0.0.1:%d/message", ssePort)

	var mcpStderr bytes.Buffer
	mcpCmd := exec.CommandContext(
		ctx,
		"go", "run", "./cmd/brosdk-mcp",
		"--mode", "sse",
		"--cdp", fmt.Sprintf("127.0.0.1:%d", debugPort),
		"--schema", "schemas/browser-tools.schema.json",
		"--port", strconv.Itoa(ssePort),
	)
	mcpCmd.Dir = repoRootFromTest(t)
	mcpCmd.Stdout = io.Discard
	mcpCmd.Stderr = &mcpStderr

	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start mcp failed: %v", err)
	}
	defer func() {
		_ = mcpCmd.Process.Kill()
		_, _ = mcpCmd.Process.Wait()
	}()

	navigateResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 1, "browser_navigate", map[string]any{
		"url": "https://forum.brosdk.com",
	})
	if err != nil {
		t.Fatalf("browser_navigate via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if okValue, ok := navigateResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_navigate unexpected result: %#v", navigateResult)
	}

	waitLoadResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 2, "browser_wait_for_load", map[string]any{
		"waitUntil": "load",
		"timeoutMs": 30000,
	})
	if err != nil {
		t.Fatalf("browser_wait_for_load via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if okValue, ok := waitLoadResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_wait_for_load unexpected result: %#v", waitLoadResult)
	}

	waitSelectorResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 3, "browser_wait_for_selector", map[string]any{
		"selector":  "body",
		"state":     "visible",
		"timeoutMs": 30000,
	})
	if err != nil {
		t.Fatalf("browser_wait_for_selector via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if okValue, ok := waitSelectorResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_wait_for_selector unexpected result: %#v", waitSelectorResult)
	}

	waitURLResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 4, "browser_wait_for_url", map[string]any{
		"url":       "*forum.brosdk.com*",
		"timeoutMs": 30000,
	})
	if err != nil {
		t.Fatalf("browser_wait_for_url via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if okValue, ok := waitURLResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_wait_for_url unexpected result: %#v", waitURLResult)
	}

	getTextResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 5, "browser_get_text", map[string]any{
		"maxChars": 1000,
	})
	if err != nil {
		t.Fatalf("browser_get_text via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	pageText, ok := getTextResult["text"].(string)
	if !ok || strings.TrimSpace(pageText) == "" {
		t.Fatalf("browser_get_text returned empty text: %#v", getTextResult)
	}

	snapshotResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 6, "browser_aria_snapshot", map[string]any{})
	if err != nil {
		t.Fatalf("browser_aria_snapshot via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}

	snapshot, ok := snapshotResult["snapshot"].(string)
	if !ok || strings.TrimSpace(snapshot) == "" {
		t.Fatalf("browser_aria_snapshot returned empty snapshot: %#v", snapshotResult)
	}
	if !strings.Contains(strings.ToLower(snapshot), "document") {
		t.Fatalf("browser_aria_snapshot missing document header: %q", snapshot)
	}
}

func TestE2EInteractionWorkflowStdio(t *testing.T) {
	if os.Getenv("BROSDK_E2E") != "1" {
		t.Skip("set BROSDK_E2E=1 to run e2e test")
	}

	chromePath, ok := findChromeExecutable()
	if !ok {
		t.Skip("chrome executable not found")
	}

	fixtureURL, shutdownFixture := startInteractionFixtureServer(t)
	defer shutdownFixture()

	tempDir := t.TempDir()
	chromeCmd, debugPort, _, err := startChromeWithDynamicDebugPort(chromePath, tempDir)
	if err != nil {
		t.Fatalf("start chrome failed: %v", err)
	}
	defer func() {
		_ = chromeCmd.Process.Kill()
		_, _ = chromeCmd.Process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var mcpStderr bytes.Buffer
	mcpCmd := exec.CommandContext(
		ctx,
		"go", "run", "./cmd/brosdk-mcp",
		"--mode", "stdio",
		"--cdp", fmt.Sprintf("127.0.0.1:%d", debugPort),
		"--schema", "schemas/browser-tools.schema.json",
	)
	mcpCmd.Dir = repoRootFromTest(t)
	mcpCmd.Stderr = &mcpStderr

	stdin, err := mcpCmd.StdinPipe()
	if err != nil {
		t.Fatalf("get mcp stdin pipe failed: %v", err)
	}
	stdout, err := mcpCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("get mcp stdout pipe failed: %v", err)
	}
	reader := bufio.NewReader(stdout)

	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start mcp failed: %v", err)
	}
	defer func() {
		_ = mcpCmd.Process.Kill()
		_, _ = mcpCmd.Process.Wait()
	}()

	runInteractionWorkflowStdio(t, stdin, reader, fixtureURL)
}

func TestE2EInteractionWorkflowSSE(t *testing.T) {
	if os.Getenv("BROSDK_E2E") != "1" {
		t.Skip("set BROSDK_E2E=1 to run e2e test")
	}

	chromePath, ok := findChromeExecutable()
	if !ok {
		t.Skip("chrome executable not found")
	}

	fixtureURL, shutdownFixture := startInteractionFixtureServer(t)
	defer shutdownFixture()

	tempDir := t.TempDir()
	chromeCmd, debugPort, _, err := startChromeWithDynamicDebugPort(chromePath, tempDir)
	if err != nil {
		t.Fatalf("start chrome failed: %v", err)
	}
	defer func() {
		_ = chromeCmd.Process.Kill()
		_, _ = chromeCmd.Process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	ssePort := findFreeTCPPort(t)
	messageEndpoint := fmt.Sprintf("http://127.0.0.1:%d/message", ssePort)

	var mcpStderr bytes.Buffer
	mcpCmd := exec.CommandContext(
		ctx,
		"go", "run", "./cmd/brosdk-mcp",
		"--mode", "sse",
		"--cdp", fmt.Sprintf("127.0.0.1:%d", debugPort),
		"--schema", "schemas/browser-tools.schema.json",
		"--port", strconv.Itoa(ssePort),
	)
	mcpCmd.Dir = repoRootFromTest(t)
	mcpCmd.Stdout = io.Discard
	mcpCmd.Stderr = &mcpStderr

	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start mcp failed: %v", err)
	}
	defer func() {
		_ = mcpCmd.Process.Kill()
		_, _ = mcpCmd.Process.Wait()
	}()

	runInteractionWorkflowSSE(t, ctx, messageEndpoint, fixtureURL)
}

func TestE2EMultiEnvironmentWorkflowStdio(t *testing.T) {
	if os.Getenv("BROSDK_E2E") != "1" {
		t.Skip("set BROSDK_E2E=1 to run e2e test")
	}

	chromePath, ok := findChromeExecutable()
	if !ok {
		t.Skip("chrome executable not found")
	}

	fixtureBaseURL, shutdownFixture := startMultiEnvironmentFixtureServer(t)
	defer shutdownFixture()

	tempDirA := filepath.Join(t.TempDir(), "chrome-a")
	tempDirB := filepath.Join(t.TempDir(), "chrome-b")
	if err := os.MkdirAll(tempDirA, 0o755); err != nil {
		t.Fatalf("mkdir tempDirA failed: %v", err)
	}
	if err := os.MkdirAll(tempDirB, 0o755); err != nil {
		t.Fatalf("mkdir tempDirB failed: %v", err)
	}

	chromeCmdA, debugPortA, _, err := startChromeWithDynamicDebugPort(chromePath, tempDirA)
	if err != nil {
		t.Fatalf("start chrome A failed: %v", err)
	}
	defer func() {
		_ = chromeCmdA.Process.Kill()
		_, _ = chromeCmdA.Process.Wait()
	}()

	chromeCmdB, debugPortB, _, err := startChromeWithDynamicDebugPort(chromePath, tempDirB)
	if err != nil {
		t.Fatalf("start chrome B failed: %v", err)
	}
	defer func() {
		_ = chromeCmdB.Process.Kill()
		_, _ = chromeCmdB.Process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var mcpStderr bytes.Buffer
	mcpCmd := exec.CommandContext(
		ctx,
		"go", "run", "./cmd/brosdk-mcp",
		"--mode", "stdio",
		"--cdp", fmt.Sprintf("127.0.0.1:%d", debugPortA),
		"--schema", "schemas/browser-tools.schema.json",
	)
	mcpCmd.Dir = repoRootFromTest(t)
	mcpCmd.Stderr = &mcpStderr

	stdin, err := mcpCmd.StdinPipe()
	if err != nil {
		t.Fatalf("get mcp stdin pipe failed: %v", err)
	}
	stdout, err := mcpCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("get mcp stdout pipe failed: %v", err)
	}
	reader := bufio.NewReader(stdout)

	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start mcp failed: %v", err)
	}
	defer func() {
		_ = mcpCmd.Process.Kill()
		_, _ = mcpCmd.Process.Wait()
	}()

	addEnvResult := sendToolsCall(t, stdin, reader, 9001, "browser_connect_environment", map[string]any{
		"name":         "second",
		"cdp_endpoint": fmt.Sprintf("127.0.0.1:%d", debugPortB),
		"set_active":   false,
	})
	if okValue, ok := addEnvResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_connect_environment unexpected result: %#v", addEnvResult)
	}

	listResult := sendToolsCall(t, stdin, reader, 9002, "browser_list_environments", map[string]any{})
	if active, _ := listResult["activeEnvironment"].(string); active != "default" {
		t.Fatalf("expected active environment default, got %#v", listResult)
	}

	sendToolsCall(t, stdin, reader, 9003, "browser_navigate", map[string]any{
		"url":         fixtureBaseURL + "/env-a",
		"environment": "default",
	})
	sendToolsCall(t, stdin, reader, 9004, "browser_navigate", map[string]any{
		"url":         fixtureBaseURL + "/env-b",
		"environment": "second",
	})

	textA := sendToolsCall(t, stdin, reader, 9005, "browser_get_text", map[string]any{
		"environment": "default",
		"maxChars":    1000,
	})
	if text, _ := textA["text"].(string); !strings.Contains(text, "Environment A Marker") {
		t.Fatalf("default environment text mismatch: %#v", textA)
	}

	textB := sendToolsCall(t, stdin, reader, 9006, "browser_get_text", map[string]any{
		"environment": "second",
		"maxChars":    1000,
	})
	if text, _ := textB["text"].(string); !strings.Contains(text, "Environment B Marker") {
		t.Fatalf("second environment text mismatch: %#v", textB)
	}

	useEnvResult := sendToolsCall(t, stdin, reader, 9007, "browser_switch_environment", map[string]any{"name": "second"})
	if okValue, ok := useEnvResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_switch_environment unexpected result: %#v", useEnvResult)
	}

	listAfterUse := sendToolsCall(t, stdin, reader, 9008, "browser_list_environments", map[string]any{})
	if active, _ := listAfterUse["activeEnvironment"].(string); active != "second" {
		t.Fatalf("expected active environment second after use, got %#v", listAfterUse)
	}

	closeResult := sendToolsCall(t, stdin, reader, 9009, "browser_close_environment", map[string]any{"name": "second"})
	if okValue, ok := closeResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_close_environment unexpected result: %#v", closeResult)
	}
	if active, _ := closeResult["activeEnvironment"].(string); active != "default" {
		t.Fatalf("expected fallback active environment default, got %#v", closeResult)
	}

	textAfterClose := sendToolsCall(t, stdin, reader, 9010, "browser_get_text", map[string]any{
		"maxChars": 1000,
	})
	if text, _ := textAfterClose["text"].(string); !strings.Contains(text, "Environment A Marker") {
		t.Fatalf("active environment after close mismatch: %#v", textAfterClose)
	}
}

func TestE2EMultiEnvironmentWorkflowSSE(t *testing.T) {
	if os.Getenv("BROSDK_E2E") != "1" {
		t.Skip("set BROSDK_E2E=1 to run e2e test")
	}

	chromePath, ok := findChromeExecutable()
	if !ok {
		t.Skip("chrome executable not found")
	}

	fixtureBaseURL, shutdownFixture := startMultiEnvironmentFixtureServer(t)
	defer shutdownFixture()

	tempDirA := filepath.Join(t.TempDir(), "chrome-a")
	tempDirB := filepath.Join(t.TempDir(), "chrome-b")
	if err := os.MkdirAll(tempDirA, 0o755); err != nil {
		t.Fatalf("mkdir tempDirA failed: %v", err)
	}
	if err := os.MkdirAll(tempDirB, 0o755); err != nil {
		t.Fatalf("mkdir tempDirB failed: %v", err)
	}

	chromeCmdA, debugPortA, _, err := startChromeWithDynamicDebugPort(chromePath, tempDirA)
	if err != nil {
		t.Fatalf("start chrome A failed: %v", err)
	}
	defer func() {
		_ = chromeCmdA.Process.Kill()
		_, _ = chromeCmdA.Process.Wait()
	}()

	chromeCmdB, debugPortB, _, err := startChromeWithDynamicDebugPort(chromePath, tempDirB)
	if err != nil {
		t.Fatalf("start chrome B failed: %v", err)
	}
	defer func() {
		_ = chromeCmdB.Process.Kill()
		_, _ = chromeCmdB.Process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	ssePort := findFreeTCPPort(t)
	messageEndpoint := fmt.Sprintf("http://127.0.0.1:%d/message", ssePort)

	var mcpStderr bytes.Buffer
	mcpCmd := exec.CommandContext(
		ctx,
		"go", "run", "./cmd/brosdk-mcp",
		"--mode", "sse",
		"--cdp", fmt.Sprintf("127.0.0.1:%d", debugPortA),
		"--schema", "schemas/browser-tools.schema.json",
		"--port", strconv.Itoa(ssePort),
	)
	mcpCmd.Dir = repoRootFromTest(t)
	mcpCmd.Stdout = io.Discard
	mcpCmd.Stderr = &mcpStderr

	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start mcp failed: %v", err)
	}
	defer func() {
		_ = mcpCmd.Process.Kill()
		_, _ = mcpCmd.Process.Wait()
	}()

	addEnvResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9101, "browser_connect_environment", map[string]any{
		"name":         "second",
		"cdp_endpoint": fmt.Sprintf("127.0.0.1:%d", debugPortB),
		"set_active":   false,
	})
	if err != nil {
		t.Fatalf("browser_connect_environment via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if okValue, ok := addEnvResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_connect_environment unexpected result: %#v", addEnvResult)
	}

	listResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9102, "browser_list_environments", map[string]any{})
	if err != nil {
		t.Fatalf("browser_list_environments via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if active, _ := listResult["activeEnvironment"].(string); active != "default" {
		t.Fatalf("expected active environment default, got %#v", listResult)
	}

	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9103, "browser_navigate", map[string]any{
		"url":         fixtureBaseURL + "/env-a",
		"environment": "default",
	}); err != nil {
		t.Fatalf("navigate default via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9104, "browser_navigate", map[string]any{
		"url":         fixtureBaseURL + "/env-b",
		"environment": "second",
	}); err != nil {
		t.Fatalf("navigate second via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}

	textA, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9105, "browser_get_text", map[string]any{
		"environment": "default",
		"maxChars":    1000,
	})
	if err != nil {
		t.Fatalf("get_text default via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if text, _ := textA["text"].(string); !strings.Contains(text, "Environment A Marker") {
		t.Fatalf("default environment text mismatch: %#v", textA)
	}

	textB, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9106, "browser_get_text", map[string]any{
		"environment": "second",
		"maxChars":    1000,
	})
	if err != nil {
		t.Fatalf("get_text second via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if text, _ := textB["text"].(string); !strings.Contains(text, "Environment B Marker") {
		t.Fatalf("second environment text mismatch: %#v", textB)
	}

	useEnvResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9107, "browser_switch_environment", map[string]any{"name": "second"})
	if err != nil {
		t.Fatalf("browser_switch_environment via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if okValue, ok := useEnvResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_switch_environment unexpected result: %#v", useEnvResult)
	}

	listAfterUse, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9108, "browser_list_environments", map[string]any{})
	if err != nil {
		t.Fatalf("browser_list_environments after use via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if active, _ := listAfterUse["activeEnvironment"].(string); active != "second" {
		t.Fatalf("expected active environment second after use, got %#v", listAfterUse)
	}

	closeResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9109, "browser_close_environment", map[string]any{"name": "second"})
	if err != nil {
		t.Fatalf("browser_close_environment via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if okValue, ok := closeResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_close_environment unexpected result: %#v", closeResult)
	}
	if active, _ := closeResult["activeEnvironment"].(string); active != "default" {
		t.Fatalf("expected fallback active environment default, got %#v", closeResult)
	}

	textAfterClose, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9110, "browser_get_text", map[string]any{
		"maxChars": 1000,
	})
	if err != nil {
		t.Fatalf("browser_get_text after close via sse failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if text, _ := textAfterClose["text"].(string); !strings.Contains(text, "Environment A Marker") {
		t.Fatalf("active environment after close mismatch: %#v", textAfterClose)
	}
}

func TestE2ELaunchLocalEnvironmentStdio(t *testing.T) {
	if os.Getenv("BROSDK_E2E") != "1" {
		t.Skip("set BROSDK_E2E=1 to run e2e test")
	}

	chromePath, ok := findChromeExecutable()
	if !ok {
		t.Skip("chrome executable not found")
	}

	fixtureURL, shutdownFixture := startInteractionFixtureServer(t)
	defer shutdownFixture()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var mcpStderr bytes.Buffer
	mcpCmd := exec.CommandContext(
		ctx,
		"go", "run", "./cmd/brosdk-mcp",
		"--mode", "stdio",
		"--schema", "schemas/browser-tools.schema.json",
	)
	mcpCmd.Dir = repoRootFromTest(t)
	mcpCmd.Stderr = &mcpStderr

	stdin, err := mcpCmd.StdinPipe()
	if err != nil {
		t.Fatalf("get mcp stdin pipe failed: %v", err)
	}
	stdout, err := mcpCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("get mcp stdout pipe failed: %v", err)
	}
	reader := bufio.NewReader(stdout)

	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start mcp failed: %v", err)
	}
	defer func() {
		_ = mcpCmd.Process.Kill()
		_, _ = mcpCmd.Process.Wait()
	}()

	launchResult := sendToolsCall(t, stdin, reader, 9501, "browser_launch_environment", map[string]any{
		"executable_path": chromePath,
		"initial_url":     fixtureURL,
		"headless":        true,
	})
	if okValue, ok := launchResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_launch_environment unexpected result: %#v", launchResult)
	}
	if name, _ := launchResult["name"].(string); !strings.HasPrefix(name, "local") {
		t.Fatalf("expected auto-assigned local environment name, got %#v", launchResult)
	}
	if active, _ := launchResult["active"].(bool); !active {
		t.Fatalf("expected launched environment to be active, got %#v", launchResult)
	}

	waitSelectorResult := sendToolsCall(t, stdin, reader, 9502, "browser_wait_for_selector", map[string]any{
		"selector":  "#nameInput",
		"state":     "visible",
		"timeoutMs": 30000,
	})
	if okValue, ok := waitSelectorResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_wait_for_selector unexpected result: %#v", waitSelectorResult)
	}

	getTextResult := sendToolsCall(t, stdin, reader, 9503, "browser_get_text", map[string]any{
		"maxChars": 1000,
	})
	pageText, ok := getTextResult["text"].(string)
	if !ok || !strings.Contains(pageText, "Interaction Fixture") {
		t.Fatalf("browser_get_text returned unexpected text: %#v", getTextResult)
	}
}

func sendToolsCall(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int, name string, args map[string]any) map[string]any {
	t.Helper()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	if _, err := stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write request failed: %v", err)
	}

	line, err := readLineWithTimeout(reader, 40*time.Second)
	if err != nil {
		t.Fatalf("read response failed: %v", err)
	}

	var resp struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("decode response failed: %v; raw=%s", err, line)
	}
	if len(resp.Error) > 0 {
		t.Fatalf("mcp returned error: %#v", resp.Error)
	}
	if resp.ID != id {
		t.Fatalf("unexpected response id: got %d want %d", resp.ID, id)
	}
	return extractStructuredContentOrResult(t, resp.Result)
}

func sendToolsCallSSEWithRetry(ctx context.Context, endpoint string, id int, name string, args map[string]any) (map[string]any, error) {
	deadline := time.Now().Add(40 * time.Second)
	var lastErr error

	for time.Now().Before(deadline) {
		result, err := sendToolsCallSSE(endpoint, id, name, args)
		if err == nil {
			return result, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			time.Sleep(250 * time.Millisecond)
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("unknown timeout waiting for sse endpoint")
	}
	return nil, lastErr
}

func sendToolsCallSSE(endpoint string, id int, name string, args map[string]any) (map[string]any, error) {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}

	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d body=%s", resp.StatusCode, string(body))
	}

	var rpcResp struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode response failed: %w raw=%s", err, string(body))
	}
	if len(rpcResp.Error) > 0 {
		return nil, fmt.Errorf("mcp error response: %#v", rpcResp.Error)
	}
	if rpcResp.ID != id {
		return nil, fmt.Errorf("unexpected response id: got %d want %d", rpcResp.ID, id)
	}
	result, err := extractStructuredContentOrResultE(rpcResp.Result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func extractStructuredContentOrResult(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	out, err := extractStructuredContentOrResultE(result)
	if err != nil {
		t.Fatalf("invalid tools/call result payload: %v; raw=%#v", err, result)
	}
	return out
}

func extractStructuredContentOrResultE(result map[string]any) (map[string]any, error) {
	if structured, ok := result["structuredContent"]; ok {
		contentMap, ok := structured.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("structuredContent must be object, got %T", structured)
		}
		return contentMap, nil
	}
	return result, nil
}

func runInteractionWorkflowStdio(t *testing.T, stdin io.Writer, reader *bufio.Reader, fixtureURL string) {
	t.Helper()

	sendToolsCall(t, stdin, reader, 101, "browser_navigate", map[string]any{"url": fixtureURL})
	sendToolsCall(t, stdin, reader, 102, "browser_wait_for_selector", map[string]any{"selector": "#nameInput", "state": "visible", "timeoutMs": 15000})

	evalResult := sendToolsCall(t, stdin, reader, 1021, "browser_evaluate", map[string]any{"expression": "document.title"})
	if title, _ := evalResult["result"].(string); title != "Interaction Fixture" {
		t.Fatalf("unexpected evaluate result: %#v", evalResult)
	}

	screenshotResult := sendToolsCall(t, stdin, reader, 1022, "browser_screenshot", map[string]any{"format": "png"})
	if ok, _ := screenshotResult["ok"].(bool); !ok {
		t.Fatalf("unexpected screenshot result: %#v", screenshotResult)
	}
	if data, _ := screenshotResult["data"].(string); strings.TrimSpace(data) == "" {
		t.Fatalf("empty screenshot data: %#v", screenshotResult)
	}

	snapshotResult := sendToolsCall(t, stdin, reader, 103, "browser_aria_snapshot", map[string]any{})
	snapshot, _ := snapshotResult["snapshot"].(string)
	inputRef := mustExtractRef(t, snapshot, `input "Name Input"`)
	applyRef := mustExtractRef(t, snapshot, `button "Apply"`)
	shadowRef := mustExtractRef(t, snapshot, `button "Shadow Action"`)

	sendToolsCall(t, stdin, reader, 104, "browser_type", map[string]any{"selector": "#nameInput", "text": "Alice", "clear": true})
	sendToolsCall(t, stdin, reader, 105, "browser_click", map[string]any{"selector": "#applyBtn"})
	sendToolsCall(t, stdin, reader, 106, "browser_wait_for_text", map[string]any{"text": "result:Alice:apply", "timeoutMs": 15000})

	sendToolsCall(t, stdin, reader, 107, "browser_set_input_value", map[string]any{"selector": "#nameInput", "value": "Bob"})
	sendToolsCall(t, stdin, reader, 108, "browser_click_by_ref", map[string]any{"ref": applyRef})
	sendToolsCall(t, stdin, reader, 109, "browser_wait_for_text", map[string]any{"text": "result:Bob:apply", "timeoutMs": 15000})

	sendToolsCall(t, stdin, reader, 110, "browser_type_by_ref", map[string]any{"ref": inputRef, "text": "Carol", "clear": true})
	sendToolsCall(t, stdin, reader, 111, "browser_find_and_click_text", map[string]any{"text": "Submit Form", "exact": true, "timeoutMs": 15000})
	sendToolsCall(t, stdin, reader, 112, "browser_wait_for_text", map[string]any{"text": "result:Carol:submit", "timeoutMs": 15000})

	sendToolsCall(t, stdin, reader, 113, "browser_set_input_value_by_ref", map[string]any{"ref": inputRef, "value": "Dave"})
	sendToolsCall(t, stdin, reader, 114, "browser_find_and_click_text", map[string]any{"text": "Apply", "exact": true, "timeoutMs": 15000})
	sendToolsCall(t, stdin, reader, 115, "browser_wait_for_text", map[string]any{"text": "result:Dave:apply", "timeoutMs": 15000})

	sendToolsCall(t, stdin, reader, 117, "browser_click_by_ref", map[string]any{"ref": shadowRef})
	sendToolsCall(t, stdin, reader, 118, "browser_wait_for_text", map[string]any{"text": "result:Dave:shadow", "timeoutMs": 15000})
	sendToolsCall(t, stdin, reader, 120, "browser_wait_for_selector", map[string]any{"selector": "#shadowBtn", "state": "visible", "timeoutMs": 15000})
	sendToolsCall(t, stdin, reader, 121, "browser_click", map[string]any{"selector": "#shadowBtn"})
	sendToolsCall(t, stdin, reader, 122, "browser_wait_for_text", map[string]any{"text": "result:Dave:shadow", "timeoutMs": 15000})

	resultText := sendToolsCall(t, stdin, reader, 123, "browser_get_text", map[string]any{"selector": "#result"})
	text, _ := resultText["text"].(string)
	if !strings.Contains(text, "result:Dave:shadow") {
		t.Fatalf("unexpected result text: %q", text)
	}
}

func runInteractionWorkflowSSE(t *testing.T, ctx context.Context, endpoint string, fixtureURL string) {
	t.Helper()

	_, err := sendToolsCallSSEWithRetry(ctx, endpoint, 201, "browser_navigate", map[string]any{"url": fixtureURL})
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 202, "browser_wait_for_selector", map[string]any{"selector": "#nameInput", "state": "visible", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_selector failed: %v", err)
	}

	evalResult, err := sendToolsCallSSEWithRetry(ctx, endpoint, 2021, "browser_evaluate", map[string]any{"expression": "document.title"})
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}
	if title, _ := evalResult["result"].(string); title != "Interaction Fixture" {
		t.Fatalf("unexpected evaluate result: %#v", evalResult)
	}

	screenshotResult, err := sendToolsCallSSEWithRetry(ctx, endpoint, 2022, "browser_screenshot", map[string]any{"format": "png"})
	if err != nil {
		t.Fatalf("screenshot failed: %v", err)
	}
	if ok, _ := screenshotResult["ok"].(bool); !ok {
		t.Fatalf("unexpected screenshot result: %#v", screenshotResult)
	}
	if data, _ := screenshotResult["data"].(string); strings.TrimSpace(data) == "" {
		t.Fatalf("empty screenshot data: %#v", screenshotResult)
	}

	snapshotResult, err := sendToolsCallSSEWithRetry(ctx, endpoint, 203, "browser_aria_snapshot", map[string]any{})
	if err != nil {
		t.Fatalf("aria_snapshot failed: %v", err)
	}
	snapshot, _ := snapshotResult["snapshot"].(string)
	inputRef := mustExtractRef(t, snapshot, `input "Name Input"`)
	applyRef := mustExtractRef(t, snapshot, `button "Apply"`)
	shadowRef := mustExtractRef(t, snapshot, `button "Shadow Action"`)

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 204, "browser_type", map[string]any{"selector": "#nameInput", "text": "Alice", "clear": true})
	if err != nil {
		t.Fatalf("type failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 205, "browser_click", map[string]any{"selector": "#applyBtn"})
	if err != nil {
		t.Fatalf("click failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 206, "browser_wait_for_text", map[string]any{"text": "result:Alice:apply", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text alice failed: %v", err)
	}

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 207, "browser_set_input_value", map[string]any{"selector": "#nameInput", "value": "Bob"})
	if err != nil {
		t.Fatalf("set_input_value failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 208, "browser_click_by_ref", map[string]any{"ref": applyRef})
	if err != nil {
		t.Fatalf("click_by_ref failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 209, "browser_wait_for_text", map[string]any{"text": "result:Bob:apply", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text bob failed: %v", err)
	}

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 210, "browser_type_by_ref", map[string]any{"ref": inputRef, "text": "Carol", "clear": true})
	if err != nil {
		t.Fatalf("type_by_ref failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 211, "browser_find_and_click_text", map[string]any{"text": "Submit Form", "exact": true, "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("find_and_click_text submit failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 212, "browser_wait_for_text", map[string]any{"text": "result:Carol:submit", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text carol failed: %v", err)
	}

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 213, "browser_set_input_value_by_ref", map[string]any{"ref": inputRef, "value": "Dave"})
	if err != nil {
		t.Fatalf("set_input_value_by_ref failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 214, "browser_find_and_click_text", map[string]any{"text": "Apply", "exact": true, "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("find_and_click_text apply failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 215, "browser_wait_for_text", map[string]any{"text": "result:Dave:apply", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text dave failed: %v", err)
	}

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 217, "browser_click_by_ref", map[string]any{"ref": shadowRef})
	if err != nil {
		t.Fatalf("click_by_ref shadow failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 218, "browser_wait_for_text", map[string]any{"text": "result:Dave:shadow", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text shadow failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 220, "browser_wait_for_selector", map[string]any{"selector": "#shadowBtn", "state": "visible", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_selector shadow failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 221, "browser_click", map[string]any{"selector": "#shadowBtn"})
	if err != nil {
		t.Fatalf("click shadow selector failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 222, "browser_wait_for_text", map[string]any{"text": "result:Dave:shadow", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text shadow selector failed: %v", err)
	}

	resultText, err := sendToolsCallSSEWithRetry(ctx, endpoint, 223, "browser_get_text", map[string]any{"selector": "#result"})
	if err != nil {
		t.Fatalf("get_text failed: %v", err)
	}
	text, _ := resultText["text"].(string)
	if !strings.Contains(text, "result:Dave:shadow") {
		t.Fatalf("unexpected result text: %q", text)
	}
}

func mustExtractRef(t *testing.T, snapshot string, lineContains string) string {
	t.Helper()
	lines := strings.Split(snapshot, "\n")
	re := regexp.MustCompile(`\[ref=(e[0-9]+)\]`)
	for _, line := range lines {
		if !strings.Contains(line, lineContains) {
			continue
		}
		m := re.FindStringSubmatch(line)
		if len(m) == 2 {
			return m[1]
		}
	}
	t.Fatalf("ref not found for %q in snapshot:\n%s", lineContains, snapshot)
	return ""
}

func readLineWithTimeout(reader *bufio.Reader, timeout time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := reader.ReadString('\n')
		ch <- result{line: line, err: err}
	}()

	select {
	case r := <-ch:
		return strings.TrimSpace(r.line), r.err
	case <-time.After(timeout):
		return "", fmt.Errorf("read timeout after %s", timeout)
	}
}

func findFreeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port failed: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startInteractionFixtureServer(t *testing.T) (string, func()) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/interaction", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <title>Interaction Fixture</title>
</head>
<body>
  <h1>Interaction Fixture</h1>
  <input id="nameInput" aria-label="Name Input" value="" />
  <button id="applyBtn" onclick="apply('apply')">Apply</button>
  <button id="submitBtn" onclick="apply('submit')">Submit Form</button>
  <shadow-action></shadow-action>
  <div id="result">result:init</div>
  <script>
    function apply(source) {
      const val = document.getElementById('nameInput').value || '';
      document.getElementById('result').textContent = 'result:' + val + ':' + source;
    }
    customElements.define('shadow-action', class extends HTMLElement {
      connectedCallback() {
        const root = this.attachShadow({mode: 'open'});
        root.innerHTML = '<button id="shadowBtn" aria-label="Shadow Action">Shadow Action</button>';
        root.getElementById('shadowBtn').addEventListener('click', () => apply('shadow'));
      }
    });
  </script>
</body>
</html>`)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start fixture listener failed: %v", err)
	}

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(ln)
	}()

	url := fmt.Sprintf("http://%s/interaction", ln.Addr().String())
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return url, shutdown
}

func startMultiEnvironmentFixtureServer(t *testing.T) (string, func()) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/env-a", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html>
<html>
<head><meta charset="utf-8" /><title>Environment A</title></head>
<body>
  <h1>Environment A Marker</h1>
  <p>Only browser environment A should show this content.</p>
</body>
</html>`)
	})
	mux.HandleFunc("/env-b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html>
<html>
<head><meta charset="utf-8" /><title>Environment B</title></head>
<body>
  <h1>Environment B Marker</h1>
  <p>Only browser environment B should show this content.</p>
</body>
</html>`)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start multi-environment fixture listener failed: %v", err)
	}

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(ln)
	}()

	baseURL := fmt.Sprintf("http://%s", ln.Addr().String())
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return baseURL, shutdown
}

func startChromeWithDynamicDebugPort(chromePath string, userDataDir string) (*exec.Cmd, int, string, error) {
	var stderr bytes.Buffer
	cmd := exec.Command(
		chromePath,
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--remote-debugging-port=0",
		"about:blank",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, 0, "", err
	}

	portFile := filepath.Join(userDataDir, "DevToolsActivePort")
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(portFile)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) >= 1 {
				port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
				if err == nil && port > 0 {
					return cmd, port, stderr.String(), nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	return nil, 0, stderr.String(), fmt.Errorf("DevToolsActivePort not ready in %s", userDataDir)
}

func findChromeExecutable() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("BROSDK_CHROME_PATH")); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, true
		}
	}
	if v := strings.TrimSpace(os.Getenv("CHROME_PATH")); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, true
		}
	}

	candidates := []string{
		"chrome",
		"chrome.exe",
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, true
		}
	}

	// Common Windows install paths.
	winCandidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
	}
	for _, p := range winCandidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}

	return "", false
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	// internal/e2e -> repo root
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
