package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"syscall"
	"testing"
	"time"
)

func cleanupProcess(t *testing.T, label string, cmd *exec.Cmd, waitTimeout time.Duration) {
	t.Helper()
	if cmd == nil || cmd.Process == nil {
		return
	}

	proc := cmd.Process
	pid := proc.Pid
	exited := make(chan error, 1)
	go func() {
		_, err := cmd.Process.Wait()
		exited <- err
	}()

	select {
	case err := <-exited:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Fatalf("%s process wait failed for pid=%d: %v", label, pid, err)
		}
	case <-time.After(waitTimeout):
		if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Fatalf("%s process kill failed for pid=%d: %v", label, pid, err)
		}
		select {
		case err := <-exited:
			if err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Fatalf("%s process wait after kill failed for pid=%d: %v", label, pid, err)
			}
		case <-time.After(waitTimeout):
			t.Fatalf("%s process did not exit after kill within %s (pid=%d path=%q)", label, waitTimeout, pid, cmd.Path)
		}
	}

	if err := proc.Signal(os.Signal(syscall.Signal(0))); err == nil {
		t.Fatalf("%s process still appears alive after cleanup (pid=%d path=%q)", label, pid, cmd.Path)
	}
}

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
	defer cleanupProcess(t, "chrome", chromeCmd, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	fixtureURL, shutdownFixture := startInteractionFixtureServer(t)
	defer shutdownFixture()

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

	navigateResult := sendToolsCall(t, stdin, reader, 1, "browser_navigate", map[string]any{
		"url": fixtureURL,
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
		"url":       "*interaction*",
		"timeoutMs": 30000,
	})
	if okValue, ok := waitURLResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("browser_wait_for_url unexpected result: %#v", waitURLResult)
	}

	getTextResult := sendToolsCall(t, stdin, reader, 5, "browser_get_text", map[string]any{
		"maxChars": 1000,
	})
	pageText, ok := getTextResult["text"].(string)
	if !ok || !strings.Contains(pageText, "Interaction Fixture") {
		t.Fatalf("browser_get_text returned unexpected text: %#v", getTextResult)
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
	defer cleanupProcess(t, "chrome", chromeCmd, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	fixtureURL, shutdownFixture := startInteractionFixtureServer(t)
	defer shutdownFixture()

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

	navigateResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 1, "browser_navigate", map[string]any{
		"url": fixtureURL,
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
		"url":       "*interaction*",
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
	if !ok || !strings.Contains(pageText, "Interaction Fixture") {
		t.Fatalf("browser_get_text returned unexpected text: %#v", getTextResult)
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
	defer cleanupProcess(t, "chrome", chromeCmd, 5*time.Second)

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

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
	defer cleanupProcess(t, "chrome", chromeCmd, 5*time.Second)

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

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
	defer cleanupProcess(t, "chrome-a", chromeCmdA, 5*time.Second)

	chromeCmdB, debugPortB, _, err := startChromeWithDynamicDebugPort(chromePath, tempDirB)
	if err != nil {
		t.Fatalf("start chrome B failed: %v", err)
	}
	defer cleanupProcess(t, "chrome-b", chromeCmdB, 5*time.Second)

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

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
	defer cleanupProcess(t, "chrome-a", chromeCmdA, 5*time.Second)

	chromeCmdB, debugPortB, _, err := startChromeWithDynamicDebugPort(chromePath, tempDirB)
	if err != nil {
		t.Fatalf("start chrome B failed: %v", err)
	}
	defer cleanupProcess(t, "chrome-b", chromeCmdB, 5*time.Second)

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

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

func TestE2EPageAgentRuleEngineWorkflowSSE(t *testing.T) {
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
	defer cleanupProcess(t, "chrome", chromeCmd, 5*time.Second)

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9601, "browser_navigate", map[string]any{"url": fixtureURL}); err != nil {
		t.Fatalf("navigate failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9602, "browser_wait_for_selector", map[string]any{
		"selector":  "#nameInput",
		"state":     "visible",
		"timeoutMs": 30000,
	}); err != nil {
		t.Fatalf("wait_for_selector nameInput failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}

	createResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9603, "browser_create_page_agent", map[string]any{
		"goal": `type "Alice" into the Name Input textbox and then click the Submit Form button`,
	})
	if err != nil {
		t.Fatalf("create page agent failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	agentID, _ := createResult["agentId"].(string)
	if strings.TrimSpace(agentID) == "" {
		t.Fatalf("expected agentId from create result, got %#v", createResult)
	}

	stepResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9604, "browser_run_page_agent_step", map[string]any{
		"agentId":  agentID,
		"maxChars": 2000,
	})
	if err != nil {
		t.Fatalf("run page agent step failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	stepPayload, _ := stepResult["stepResult"].(map[string]any)
	if stepPayload == nil {
		t.Fatalf("missing stepResult payload: %#v", stepResult)
	}
	if src, _ := stepPayload["proposalSource"].(string); src != "rules" {
		t.Fatalf("expected rules proposal source, got %#v", stepPayload)
	}
	firstProposal, _ := stepPayload["nextActionProposal"].(map[string]any)
	if firstProposal == nil {
		t.Fatalf("missing nextActionProposal: %#v", stepPayload)
	}
	if tool, _ := firstProposal["tool"].(string); tool != "browser_type_by_ref" {
		t.Fatalf("expected first rule-engine proposal to type by ref, got %#v", firstProposal)
	}

	applyOne, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9605, "browser_apply_page_agent_proposal", map[string]any{
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("apply first proposal failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	applyOnePayload, _ := applyOne["applyResult"].(map[string]any)
	if applyOnePayload == nil {
		t.Fatalf("missing first applyResult payload: %#v", applyOne)
	}
	if src, _ := applyOnePayload["nextActionProposalSource"].(string); src != "rules" {
		t.Fatalf("expected rules follow-up proposal source, got %#v", applyOnePayload)
	}
	nextProposal, _ := applyOnePayload["nextActionProposal"].(map[string]any)
	if nextProposal == nil {
		t.Fatalf("missing follow-up proposal after typing: %#v", applyOnePayload)
	}
	if tool, _ := nextProposal["tool"].(string); tool != "browser_click_by_ref" {
		t.Fatalf("expected follow-up click proposal, got %#v", nextProposal)
	}

	applyTwo, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9606, "browser_apply_page_agent_proposal", map[string]any{
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("apply second proposal failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	applyTwoPayload, _ := applyTwo["applyResult"].(map[string]any)
	if applyTwoPayload == nil {
		t.Fatalf("missing second applyResult payload: %#v", applyTwo)
	}
	if tool, _ := applyTwoPayload["tool"].(string); tool != "browser_click_by_ref" {
		t.Fatalf("expected second applied tool to click by ref, got %#v", applyTwoPayload)
	}
	if postActionText, _ := applyTwoPayload["postActionText"].(string); !strings.Contains(postActionText, "result:Alice:submit") {
		t.Fatalf("expected postActionText to reflect Submit result, got %#v", applyTwoPayload)
	}

	agentResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9608, "browser_get_page_agent", map[string]any{
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("get page agent failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if historyCount, _ := agentResult["historyCount"].(float64); historyCount < 3 {
		t.Fatalf("expected at least 3 history entries after rule-engine flow, got %#v", agentResult)
	}
	if status, _ := agentResult["status"].(string); status != "idle" {
		t.Fatalf("expected final page agent status idle, got %#v", agentResult)
	}

	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9610, "browser_navigate", map[string]any{"url": fixtureURL}); err != nil {
		t.Fatalf("navigate before structured form flow failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9611, "browser_wait_for_selector", map[string]any{
		"selector":  "#loginEmail",
		"state":     "visible",
		"timeoutMs": 30000,
	}); err != nil {
		t.Fatalf("wait_for_selector loginEmail failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}

	loginAgentResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9612, "browser_create_page_agent", map[string]any{
		"goal": `log in with email "qa@example.com" and password "secret123"`,
	})
	if err != nil {
		t.Fatalf("create login page agent failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	loginAgentID, _ := loginAgentResult["agentId"].(string)
	if strings.TrimSpace(loginAgentID) == "" {
		t.Fatalf("expected login agentId, got %#v", loginAgentResult)
	}

	loginStep, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9613, "browser_run_page_agent_step", map[string]any{
		"agentId":  loginAgentID,
		"maxChars": 2000,
	})
	if err != nil {
		t.Fatalf("run login page agent step failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	loginStepPayload, _ := loginStep["stepResult"].(map[string]any)
	firstLoginProposal, _ := loginStepPayload["nextActionProposal"].(map[string]any)
	if src, _ := loginStepPayload["proposalSource"].(string); src != "rules" {
		t.Fatalf("expected rules source for login step, got %#v", loginStepPayload)
	}
	if tool, _ := firstLoginProposal["tool"].(string); tool != "browser_type_by_ref" {
		t.Fatalf("expected first login proposal to type by ref, got %#v", firstLoginProposal)
	}
	firstLoginTarget, _ := firstLoginProposal["target"].(map[string]any)
	if field, _ := firstLoginTarget["field"].(string); field != "email" {
		t.Fatalf("expected first login proposal to target email field, got %#v", firstLoginProposal)
	}
	if name, _ := firstLoginTarget["name"].(string); !strings.Contains(strings.ToLower(name), "email") {
		t.Fatalf("expected first login target name to mention email, got %#v", firstLoginProposal)
	}

	loginApplyOne, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9614, "browser_apply_page_agent_proposal", map[string]any{
		"agentId": loginAgentID,
	})
	if err != nil {
		t.Fatalf("apply first login proposal failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	loginApplyOnePayload, _ := loginApplyOne["applyResult"].(map[string]any)
	secondLoginProposal, _ := loginApplyOnePayload["nextActionProposal"].(map[string]any)
	if src, _ := loginApplyOnePayload["nextActionProposalSource"].(string); src != "rules" {
		t.Fatalf("expected rules source for second login proposal, got %#v", loginApplyOnePayload)
	}
	if tool, _ := secondLoginProposal["tool"].(string); tool != "browser_type_by_ref" {
		t.Fatalf("expected second login proposal to type by ref, got %#v", secondLoginProposal)
	}
	secondLoginTarget, _ := secondLoginProposal["target"].(map[string]any)
	if field, _ := secondLoginTarget["field"].(string); field != "password" {
		t.Fatalf("expected second login proposal to target password field, got %#v", secondLoginProposal)
	}
	if name, _ := secondLoginTarget["name"].(string); !strings.Contains(strings.ToLower(name), "password") {
		t.Fatalf("expected second login target name to mention password, got %#v", secondLoginProposal)
	}

	loginApplyTwo, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9615, "browser_apply_page_agent_proposal", map[string]any{
		"agentId": loginAgentID,
	})
	if err != nil {
		t.Fatalf("apply second login proposal failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	loginApplyTwoPayload, _ := loginApplyTwo["applyResult"].(map[string]any)
	thirdLoginProposal, _ := loginApplyTwoPayload["nextActionProposal"].(map[string]any)
	if src, _ := loginApplyTwoPayload["nextActionProposalSource"].(string); src != "rules" {
		t.Fatalf("expected rules source for third login proposal, got %#v", loginApplyTwoPayload)
	}
	if tool, _ := thirdLoginProposal["tool"].(string); tool != "browser_click_by_ref" {
		t.Fatalf("expected third login proposal to click by ref, got %#v", thirdLoginProposal)
	}
	thirdLoginTarget, _ := thirdLoginProposal["target"].(map[string]any)
	if name, _ := thirdLoginTarget["name"].(string); !strings.Contains(strings.ToLower(name), "sign in") {
		t.Fatalf("expected third login target name to mention sign in, got %#v", thirdLoginProposal)
	}

	loginApplyThree, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9616, "browser_apply_page_agent_proposal", map[string]any{
		"agentId": loginAgentID,
	})
	if err != nil {
		t.Fatalf("apply third login proposal failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	loginApplyThreePayload, _ := loginApplyThree["applyResult"].(map[string]any)
	if loginApplyThreePayload == nil {
		t.Fatalf("missing third login applyResult payload: %#v", loginApplyThree)
	}
	if tool, _ := loginApplyThreePayload["tool"].(string); tool != "browser_click_by_ref" {
		t.Fatalf("expected third applied login tool to click by ref, got %#v", loginApplyThreePayload)
	}
	if postActionText, _ := loginApplyThreePayload["postActionText"].(string); !strings.Contains(postActionText, "login:qa@example.com:success") {
		t.Fatalf("expected login postActionText to reflect successful sign-in, got %#v", loginApplyThreePayload)
	}
}

func TestE2EPageAgentControlledLoopSSE(t *testing.T) {
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
	defer cleanupProcess(t, "chrome", chromeCmd, 5*time.Second)

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9651, "browser_navigate", map[string]any{"url": fixtureURL}); err != nil {
		t.Fatalf("navigate failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9652, "browser_wait_for_selector", map[string]any{
		"selector":  "#nameInput",
		"state":     "visible",
		"timeoutMs": 30000,
	}); err != nil {
		t.Fatalf("wait_for_selector nameInput failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}

	stopTextAgentResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9653, "browser_create_page_agent", map[string]any{
		"goal": `inspect the interaction fixture`,
	})
	if err != nil {
		t.Fatalf("create stopWhenText page agent failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	stopTextAgentID, _ := stopTextAgentResult["agentId"].(string)
	if strings.TrimSpace(stopTextAgentID) == "" {
		t.Fatalf("expected stopWhenText agentId, got %#v", stopTextAgentResult)
	}

	stopTextLoop, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9654, "browser_run_page_agent_loop", map[string]any{
		"agentId":      stopTextAgentID,
		"maxSteps":     3,
		"maxChars":     2000,
		"stopWhenText": "Interaction Fixture",
	})
	if err != nil {
		t.Fatalf("run stopWhenText loop failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if reason, _ := stopTextLoop["stopReason"].(string); reason != "stop_when_text_matched" {
		t.Fatalf("expected stop_when_text_matched, got %#v", stopTextLoop)
	}
	if errorCount, _ := stopTextLoop["errorCount"].(float64); errorCount != 0 {
		t.Fatalf("expected stopWhenText errorCount=0, got %#v", stopTextLoop)
	}
	stopTextSteps, _ := stopTextLoop["steps"].([]any)
	if len(stopTextSteps) != 1 {
		t.Fatalf("expected exactly one recorded step for stopWhenText, got %#v", stopTextLoop)
	}
	stopTextAgent, _ := stopTextLoop["agent"].(map[string]any)
	if historyCount, _ := stopTextAgent["historyCount"].(float64); historyCount != 1 {
		t.Fatalf("expected stopWhenText historyCount=1, got %#v", stopTextLoop)
	}

	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9655, "browser_navigate", map[string]any{"url": fixtureURL}); err != nil {
		t.Fatalf("navigate before stopOnTool loop failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	clickAgentResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9656, "browser_create_page_agent", map[string]any{
		"goal": `click the Apply button`,
	})
	if err != nil {
		t.Fatalf("create stopOnTool page agent failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	clickAgentID, _ := clickAgentResult["agentId"].(string)
	if strings.TrimSpace(clickAgentID) == "" {
		t.Fatalf("expected stopOnTool agentId, got %#v", clickAgentResult)
	}

	stopToolLoop, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9657, "browser_run_page_agent_loop", map[string]any{
		"agentId":    clickAgentID,
		"maxSteps":   3,
		"maxChars":   2000,
		"stopOnTool": "browser_click_by_ref",
	})
	if err != nil {
		t.Fatalf("run stopOnTool loop failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if reason, _ := stopToolLoop["stopReason"].(string); reason != "stop_on_tool_matched" {
		t.Fatalf("expected stop_on_tool_matched, got %#v", stopToolLoop)
	}
	if errorCount, _ := stopToolLoop["errorCount"].(float64); errorCount != 0 {
		t.Fatalf("expected stopOnTool errorCount=0, got %#v", stopToolLoop)
	}
	stopToolSteps, _ := stopToolLoop["steps"].([]any)
	if len(stopToolSteps) != 2 {
		t.Fatalf("expected step+apply entries for stopOnTool, got %#v", stopToolLoop)
	}
	stopToolAgent, _ := stopToolLoop["agent"].(map[string]any)
	if historyCount, _ := stopToolAgent["historyCount"].(float64); historyCount != 2 {
		t.Fatalf("expected stopOnTool historyCount=2, got %#v", stopToolLoop)
	}
	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9658, "browser_wait_for_text", map[string]any{
		"text":      "result::apply",
		"timeoutMs": 30000,
	}); err != nil {
		t.Fatalf("wait_for_text stopOnTool result failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}

	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9659, "browser_navigate", map[string]any{"url": fixtureURL}); err != nil {
		t.Fatalf("navigate before requireAI loop failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	requireAIAgentResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9660, "browser_create_page_agent", map[string]any{
		"goal": `type "Alice" into the Name Input textbox and then click the Apply button`,
	})
	if err != nil {
		t.Fatalf("create requireAI page agent failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	requireAIAgentID, _ := requireAIAgentResult["agentId"].(string)
	if strings.TrimSpace(requireAIAgentID) == "" {
		t.Fatalf("expected requireAI agentId, got %#v", requireAIAgentResult)
	}

	requireAILoop, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9661, "browser_run_page_agent_loop", map[string]any{
		"agentId":   requireAIAgentID,
		"maxSteps":  2,
		"maxChars":  2000,
		"requireAI": true,
	})
	if err != nil {
		t.Fatalf("run requireAI loop failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if reason, _ := requireAILoop["stopReason"].(string); reason != "ai_required_but_unavailable" {
		t.Fatalf("expected ai_required_but_unavailable, got %#v", requireAILoop)
	}
	if errorCount, _ := requireAILoop["errorCount"].(float64); errorCount != 0 {
		t.Fatalf("expected requireAI errorCount=0, got %#v", requireAILoop)
	}
	requireAISteps, _ := requireAILoop["steps"].([]any)
	if len(requireAISteps) != 1 {
		t.Fatalf("expected requireAI loop to stop after first step without apply, got %#v", requireAILoop)
	}
	requireAIAgent, _ := requireAILoop["agent"].(map[string]any)
	if historyCount, _ := requireAIAgent["historyCount"].(float64); historyCount != 1 {
		t.Fatalf("expected requireAI historyCount=1, got %#v", requireAILoop)
	}

	resultText, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9662, "browser_get_text", map[string]any{"selector": "#result"})
	if err != nil {
		t.Fatalf("get_text after requireAI loop failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if text, _ := resultText["text"].(string); strings.TrimSpace(text) != "result:init" {
		t.Fatalf("expected requireAI loop to avoid applying actions, got %#v", resultText)
	}
}

func TestE2EPageAgentAIWorkflowSSE(t *testing.T) {
	if os.Getenv("BROSDK_E2E") != "1" {
		t.Skip("set BROSDK_E2E=1 to run e2e test")
	}
	if os.Getenv("BROSDK_PAGEAGENT_AI_E2E") != "1" {
		t.Skip("set BROSDK_PAGEAGENT_AI_E2E=1 to run PageAgent AI e2e test")
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("OPENAI_API_KEY, OPENAI_BASE_URL, and OPENAI_MODEL must be set")
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
	defer cleanupProcess(t, "chrome", chromeCmd, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	ssePort := findFreeTCPPort(t)
	messageEndpoint := fmt.Sprintf("http://127.0.0.1:%d/message", ssePort)
	configEndpoint := fmt.Sprintf("http://127.0.0.1:%d/ui/config", ssePort)

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
	defer cleanupProcess(t, "brosdk-mcp", mcpCmd, 5*time.Second)

	if err := postPageAgentAIConfig(ctx, configEndpoint, apiKey, baseURL, model); err != nil {
		t.Fatalf("configure page agent ai failed: %v", err)
	}

	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9701, "browser_navigate", map[string]any{"url": fixtureURL}); err != nil {
		t.Fatalf("navigate failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9702, "browser_wait_for_selector", map[string]any{
		"selector":  "#nameInput",
		"state":     "visible",
		"timeoutMs": 30000,
	}); err != nil {
		t.Fatalf("wait_for_selector failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}

	createResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9703, "browser_create_page_agent", map[string]any{
		"goal": `type "Alice" into the Name Input textbox and then click the Apply button`,
	})
	if err != nil {
		t.Fatalf("create page agent failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	agentID, _ := createResult["agentId"].(string)
	if strings.TrimSpace(agentID) == "" {
		t.Fatalf("expected agentId from create result, got %#v", createResult)
	}

	stepResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9704, "browser_run_page_agent_step", map[string]any{
		"agentId":  agentID,
		"maxChars": 2000,
	})
	if err != nil {
		t.Fatalf("run page agent step failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	stepPayload, _ := stepResult["stepResult"].(map[string]any)
	if stepPayload == nil {
		t.Fatalf("missing stepResult payload: %#v", stepResult)
	}
	if src, _ := stepPayload["proposalSource"].(string); src != "ai" {
		t.Fatalf("expected AI proposal source, got %#v", stepPayload)
	}
	firstProposal, _ := stepPayload["nextActionProposal"].(map[string]any)
	if firstProposal == nil {
		t.Fatalf("missing nextActionProposal: %#v", stepPayload)
	}
	firstTool, _ := firstProposal["tool"].(string)
	if firstTool != "browser_type_by_ref" {
		t.Fatalf("expected first proposal to type by ref, got %#v", firstProposal)
	}

	applyOne, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9705, "browser_apply_page_agent_proposal", map[string]any{
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("apply first proposal failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	applyOnePayload, _ := applyOne["applyResult"].(map[string]any)
	if applyOnePayload == nil {
		t.Fatalf("missing first applyResult payload: %#v", applyOne)
	}
	if src, _ := applyOnePayload["nextActionProposalSource"].(string); src != "ai" {
		t.Fatalf("expected AI follow-up proposal source, got %#v", applyOnePayload)
	}
	nextProposal, _ := applyOnePayload["nextActionProposal"].(map[string]any)
	if nextProposal == nil {
		t.Fatalf("missing follow-up proposal after typing: %#v", applyOnePayload)
	}
	nextTool, _ := nextProposal["tool"].(string)
	if nextTool != "browser_click_by_ref" {
		t.Fatalf("expected follow-up click proposal, got %#v", nextProposal)
	}

	if _, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9706, "browser_apply_page_agent_proposal", map[string]any{
		"agentId": agentID,
	}); err != nil {
		t.Fatalf("apply second proposal failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}

	waitTextResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9707, "browser_wait_for_text", map[string]any{
		"text":      "result:Alice:apply",
		"timeoutMs": 30000,
	})
	if err != nil {
		t.Fatalf("wait_for_text after page agent apply failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if okValue, ok := waitTextResult["ok"].(bool); !ok || !okValue {
		t.Fatalf("unexpected wait_for_text result: %#v", waitTextResult)
	}

	agentResult, err := sendToolsCallSSEWithRetry(ctx, messageEndpoint, 9708, "browser_get_page_agent", map[string]any{
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("get page agent failed: %v; mcp stderr=%s", err, mcpStderr.String())
	}
	if historyCount, _ := agentResult["historyCount"].(float64); historyCount < 3 {
		t.Fatalf("expected at least 3 history entries, got %#v", agentResult)
	}
	if status, _ := agentResult["status"].(string); status != "idle" {
		t.Fatalf("expected final page agent status idle, got %#v", agentResult)
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
	inputRef := mustExtractRef(t, snapshot, `textbox "Name Input"`)
	applyRef := mustExtractRef(t, snapshot, `button "Apply"`)
	shadowRef := mustExtractRef(t, snapshot, `button "Shadow Action"`)

	uploadDir := t.TempDir()
	uploadPath := filepath.Join(uploadDir, "upload-a.txt")
	if err := os.WriteFile(uploadPath, []byte("upload-a"), 0o644); err != nil {
		t.Fatalf("write upload fixture file: %v", err)
	}

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

	sendToolsCall(t, stdin, reader, 124, "browser_hover", map[string]any{"selector": "#hoverTarget"})
	sendToolsCall(t, stdin, reader, 125, "browser_wait_for_text", map[string]any{"text": "hovered:hover-target", "timeoutMs": 15000})

	sendToolsCall(t, stdin, reader, 126, "browser_select_option", map[string]any{"selector": "#choiceSelect", "value": "beta"})
	sendToolsCall(t, stdin, reader, 127, "browser_wait_for_text", map[string]any{"text": "selected:beta", "timeoutMs": 15000})

	sendToolsCall(t, stdin, reader, 128, "browser_set_file_input_files", map[string]any{"selector": "#fileInput", "paths": []string{uploadPath}})
	sendToolsCall(t, stdin, reader, 129, "browser_wait_for_text", map[string]any{"text": "file:upload-a.txt", "timeoutMs": 15000})

	sendToolsCall(t, stdin, reader, 130, "browser_evaluate", map[string]any{"expression": `setTimeout(function(){ alert('Async hello'); document.getElementById('result').textContent = 'alert:done'; }, 50); true`})
	waitDialog := sendToolsCall(t, stdin, reader, 131, "browser_wait_for_dialog", map[string]any{"timeoutMs": 15000})
	if msg, _ := waitDialog["message"].(string); msg != "Async hello" {
		t.Fatalf("unexpected dialog payload: %#v", waitDialog)
	}
	sendToolsCall(t, stdin, reader, 132, "browser_handle_dialog", map[string]any{"accept": true})
	sendToolsCall(t, stdin, reader, 133, "browser_wait_for_text", map[string]any{"text": "alert:done", "timeoutMs": 15000})

	sendToolsCall(t, stdin, reader, 134, "browser_click", map[string]any{"selector": "#confirmBtn"})
	sendToolsCall(t, stdin, reader, 135, "browser_handle_dialog", map[string]any{"accept": false, "timeoutMs": 15000})
	sendToolsCall(t, stdin, reader, 136, "browser_wait_for_text", map[string]any{"text": "confirm:false", "timeoutMs": 15000})

	sendToolsCall(t, stdin, reader, 137, "browser_click", map[string]any{"selector": "#promptBtn"})
	sendToolsCall(t, stdin, reader, 138, "browser_handle_dialog", map[string]any{"accept": true, "promptText": "typed by mcp", "timeoutMs": 15000})
	sendToolsCall(t, stdin, reader, 139, "browser_wait_for_text", map[string]any{"text": "prompt:typed by mcp", "timeoutMs": 15000})

	resultText := sendToolsCall(t, stdin, reader, 123, "browser_get_text", map[string]any{"selector": "#result"})
	text, _ := resultText["text"].(string)
	if !strings.Contains(text, "prompt:typed by mcp") {
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
	inputRef := mustExtractRef(t, snapshot, `textbox "Name Input"`)
	applyRef := mustExtractRef(t, snapshot, `button "Apply"`)
	shadowRef := mustExtractRef(t, snapshot, `button "Shadow Action"`)

	uploadDir := t.TempDir()
	uploadPath := filepath.Join(uploadDir, "upload-b.txt")
	if err := os.WriteFile(uploadPath, []byte("upload-b"), 0o644); err != nil {
		t.Fatalf("write upload fixture file: %v", err)
	}

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

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 224, "browser_hover", map[string]any{"selector": "#hoverTarget"})
	if err != nil {
		t.Fatalf("hover failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 225, "browser_wait_for_text", map[string]any{"text": "hovered:hover-target", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text hover failed: %v", err)
	}

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 226, "browser_select_option", map[string]any{"selector": "#choiceSelect", "value": "beta"})
	if err != nil {
		t.Fatalf("select_option failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 227, "browser_wait_for_text", map[string]any{"text": "selected:beta", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text select failed: %v", err)
	}

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 228, "browser_set_file_input_files", map[string]any{"selector": "#fileInput", "paths": []string{uploadPath}})
	if err != nil {
		t.Fatalf("set_file_input_files failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 229, "browser_wait_for_text", map[string]any{"text": "file:upload-b.txt", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text upload failed: %v", err)
	}

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 230, "browser_evaluate", map[string]any{"expression": `setTimeout(function(){ alert('Async hello'); document.getElementById('result').textContent = 'alert:done'; }, 50); true`})
	if err != nil {
		t.Fatalf("schedule alert failed: %v", err)
	}
	dialogPayload, err := sendToolsCallSSEWithRetry(ctx, endpoint, 231, "browser_wait_for_dialog", map[string]any{"timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_dialog failed: %v", err)
	}
	if msg, _ := dialogPayload["message"].(string); msg != "Async hello" {
		t.Fatalf("unexpected dialog payload: %#v", dialogPayload)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 232, "browser_handle_dialog", map[string]any{"accept": true})
	if err != nil {
		t.Fatalf("handle_dialog alert failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 233, "browser_wait_for_text", map[string]any{"text": "alert:done", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text alert failed: %v", err)
	}

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 234, "browser_click", map[string]any{"selector": "#confirmBtn"})
	if err != nil {
		t.Fatalf("click confirm failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 235, "browser_handle_dialog", map[string]any{"accept": false, "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("handle_dialog confirm failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 236, "browser_wait_for_text", map[string]any{"text": "confirm:false", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text confirm failed: %v", err)
	}

	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 237, "browser_click", map[string]any{"selector": "#promptBtn"})
	if err != nil {
		t.Fatalf("click prompt failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 238, "browser_handle_dialog", map[string]any{"accept": true, "promptText": "typed by mcp", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("handle_dialog prompt failed: %v", err)
	}
	_, err = sendToolsCallSSEWithRetry(ctx, endpoint, 239, "browser_wait_for_text", map[string]any{"text": "prompt:typed by mcp", "timeoutMs": 15000})
	if err != nil {
		t.Fatalf("wait_for_text prompt failed: %v", err)
	}

	resultText, err := sendToolsCallSSEWithRetry(ctx, endpoint, 240, "browser_get_text", map[string]any{"selector": "#result"})
	if err != nil {
		t.Fatalf("get_text failed: %v", err)
	}
	text, _ := resultText["text"].(string)
	if !strings.Contains(text, "prompt:typed by mcp") {
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

func postPageAgentAIConfig(ctx context.Context, endpoint string, apiKey string, baseURL string, model string) error {
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		err := postPageAgentAIConfigOnce(endpoint, apiKey, baseURL, model)
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			time.Sleep(250 * time.Millisecond)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("unknown ui config error")
	}
	return lastErr
}

func postPageAgentAIConfigOnce(endpoint string, apiKey string, baseURL string, model string) error {
	body, err := json.Marshal(map[string]any{
		"apiKey":  apiKey,
		"baseUrl": baseURL,
		"model":   model,
	})
	if err != nil {
		return err
	}
	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d body=%s", resp.StatusCode, string(raw))
	}
	if !strings.Contains(string(raw), `"configured":true`) {
		return fmt.Errorf("ai config response did not mark configured=true: %s", string(raw))
	}
	return nil
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
  <section aria-label="Login Form">
    <label for="loginEmail">Email Address</label>
    <input id="loginEmail" type="email" aria-label="Email Address" value="" />
    <label for="loginPassword">Password</label>
    <input id="loginPassword" type="password" aria-label="Password" value="" />
    <button id="signInBtn" aria-label="Sign In" onclick="signIn()">Sign In</button>
  </section>
  <button id="hoverTarget" aria-label="Hover Target">Hover Target</button>
  <select id="choiceSelect" aria-label="Choice Select">
    <option value="alpha">Alpha</option>
    <option value="beta">Beta</option>
    <option value="gamma">Gamma</option>
  </select>
  <input id="fileInput" type="file" aria-label="Upload File" />
  <button id="applyBtn" onclick="apply('apply')">Apply</button>
  <button id="submitBtn" onclick="apply('submit')">Submit Form</button>
  <button id="confirmBtn" onclick="setTimeout(function(){ document.getElementById('result').textContent = 'confirm:' + confirm('Please confirm'); }, 0)">Confirm</button>
  <button id="promptBtn" onclick="setTimeout(function(){ document.getElementById('result').textContent = 'prompt:' + (prompt('Enter prompt', 'seed') || ''); }, 0)">Prompt</button>
  <shadow-action></shadow-action>
  <div id="result">result:init</div>
  <script>
    function apply(source) {
      const val = document.getElementById('nameInput').value || '';
      document.getElementById('result').textContent = 'result:' + val + ':' + source;
    }
    function signIn() {
      const email = document.getElementById('loginEmail').value || '';
      const password = document.getElementById('loginPassword').value || '';
      document.getElementById('result').textContent = password ? 'login:' + email + ':success' : 'login:' + email + ':missing-password';
    }
    document.getElementById('hoverTarget').addEventListener('mouseenter', () => {
      document.getElementById('result').textContent = 'hovered:hover-target';
    });
    document.getElementById('choiceSelect').addEventListener('change', (event) => {
      document.getElementById('result').textContent = 'selected:' + event.target.value;
    });
    document.getElementById('fileInput').addEventListener('change', (event) => {
      const names = Array.from(event.target.files || []).map((file) => file.name).join(',');
      document.getElementById('result').textContent = 'file:' + names;
    });
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
