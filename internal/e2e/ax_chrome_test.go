package e2e

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestE2EAXSnapshot validates the AX-priority path of browser_aria_snapshot
// against a controlled local fixture page.
//
// Run with: BROSDK_E2E=1 go test ./internal/e2e -run TestE2EAXSnapshot -v
func TestE2EAXSnapshot(t *testing.T) {
	if os.Getenv("BROSDK_E2E") != "1" {
		t.Skip("set BROSDK_E2E=1 to run e2e test")
	}

	chromePath, ok := findChromeExecutable()
	if !ok {
		t.Skip("chrome executable not found")
	}

	fixtureURL, shutdown := startAXFixtureServer(t)
	defer shutdown()

	tempDir := t.TempDir()
	chromeCmd, debugPort, _, err := startChromeWithDynamicDebugPort(chromePath, tempDir)
	if err != nil {
		t.Fatalf("start chrome: %v", err)
	}
	defer func() {
		_ = chromeCmd.Process.Kill()
		_, _ = chromeCmd.Process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var mcpStderr bytes.Buffer
	mcpCmd := exec.CommandContext(ctx,
		"go", "run", "./cmd/brosdk-mcp",
		"--mode", "stdio",
		"--cdp", fmt.Sprintf("127.0.0.1:%d", debugPort),
		"--schema", "schemas/browser-tools.schema.json",
	)
	mcpCmd.Dir = repoRootFromTest(t)
	mcpCmd.Stderr = &mcpStderr

	stdin, err := mcpCmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := mcpCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	reader := bufio.NewReader(stdout)

	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start mcp: %v", err)
	}
	defer func() {
		_ = mcpCmd.Process.Kill()
		_, _ = mcpCmd.Process.Wait()
	}()

	// Navigate to fixture and wait for it to be ready.
	navResult := sendToolsCall(t, stdin, reader, 1, "browser_navigate", map[string]any{"url": fixtureURL})
	if ok, _ := navResult["ok"].(bool); !ok {
		t.Fatalf("navigate failed: %#v\nstderr: %s", navResult, mcpStderr.String())
	}
	sendToolsCall(t, stdin, reader, 2, "browser_wait_for_selector", map[string]any{
		"selector": "#ready", "state": "visible", "timeoutMs": 15000,
	})

	t.Run("full_snapshot_structure", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 10, "browser_aria_snapshot", map[string]any{
			"interactive": false,
		})
		snapshot := mustGetSnapshot(t, result)
		mustHaveAXSource(t, result)

		// Document header must be present.
		if !strings.Contains(snapshot, `- document`) {
			t.Fatalf("missing document header:\n%s", snapshot)
		}
		// Heading must appear.
		if !strings.Contains(snapshot, `heading`) {
			t.Fatalf("missing heading in snapshot:\n%s", snapshot)
		}
		// Interactive elements must have refs.
		if !strings.Contains(snapshot, "[ref=") {
			t.Fatalf("no refs in snapshot:\n%s", snapshot)
		}
		// Ref format must be eN.
		re := regexp.MustCompile(`\[ref=(e\d+)\]`)
		if !re.MatchString(snapshot) {
			t.Fatalf("unexpected ref format in snapshot:\n%s", snapshot)
		}
		// Named button must appear.
		if !strings.Contains(snapshot, `"AX Button"`) {
			t.Fatalf("missing AX Button in snapshot:\n%s", snapshot)
		}
		// Named input must appear.
		if !strings.Contains(snapshot, `"AX Input"`) {
			t.Fatalf("missing AX Input in snapshot:\n%s", snapshot)
		}
		assertRefCountMatches(t, result, snapshot)
		t.Logf("full snapshot (%d chars, %d refs):\n%s",
			len(snapshot), axRefCount(snapshot), snapshot)
	})

	t.Run("interactive_only_filter", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 11, "browser_aria_snapshot", map[string]any{
			"interactive": true,
		})
		snapshot := mustGetSnapshot(t, result)

		// Heading is semantic, not interactive – must be absent.
		if strings.Contains(snapshot, "heading") {
			t.Fatalf("interactive-only snapshot should not contain heading:\n%s", snapshot)
		}
		// Button and input must still be present.
		if !strings.Contains(snapshot, `"AX Button"`) {
			t.Fatalf("interactive-only snapshot missing AX Button:\n%s", snapshot)
		}
		if !strings.Contains(snapshot, `"AX Input"`) {
			t.Fatalf("interactive-only snapshot missing AX Input:\n%s", snapshot)
		}
		t.Logf("interactive snapshot (%d refs):\n%s", axRefCount(snapshot), snapshot)
	})

	t.Run("compact_flag", func(t *testing.T) {
		verbose := mustGetSnapshot(t, sendToolsCall(t, stdin, reader, 12, "browser_aria_snapshot", map[string]any{
			"compact": false,
		}))
		compact := mustGetSnapshot(t, sendToolsCall(t, stdin, reader, 13, "browser_aria_snapshot", map[string]any{
			"compact": true,
		}))

		// Compact must not contain backendNodeId extras.
		if strings.Contains(compact, "backendNodeId") {
			t.Fatalf("compact snapshot should not contain backendNodeId:\n%s", compact)
		}
		// Verbose should contain backendNodeId for at least one node.
		if !strings.Contains(verbose, "backendNodeId") {
			t.Logf("verbose snapshot (no backendNodeId found – may be browser-version dependent):\n%s", verbose)
		}
		// Both must have refs.
		if axRefCount(compact) == 0 {
			t.Fatalf("compact snapshot has no refs:\n%s", compact)
		}
	})

	t.Run("max_depth_truncation", func(t *testing.T) {
		shallow := mustGetSnapshot(t, sendToolsCall(t, stdin, reader, 14, "browser_aria_snapshot", map[string]any{
			"maxDepth": 2,
		}))
		deep := mustGetSnapshot(t, sendToolsCall(t, stdin, reader, 15, "browser_aria_snapshot", map[string]any{
			"maxDepth": 20,
		}))

		shallowRefs := axRefCount(shallow)
		deepRefs := axRefCount(deep)
		if shallowRefs > deepRefs {
			t.Fatalf("shallow (%d refs) should have <= refs than deep (%d refs)", shallowRefs, deepRefs)
		}
		t.Logf("shallow refs=%d deep refs=%d", shallowRefs, deepRefs)
	})

	t.Run("shadow_dom_pierce", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 16, "browser_aria_snapshot", map[string]any{})
		snapshot := mustGetSnapshot(t, result)

		// Shadow DOM button must appear in the AX tree (pierce=true).
		if !strings.Contains(snapshot, `"AX Shadow Button"`) {
			t.Fatalf("shadow DOM button not found in AX snapshot (pierce may not be working):\n%s", snapshot)
		}
		t.Logf("shadow DOM button found in snapshot")
	})

	t.Run("ref_click_by_ref", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 20, "browser_aria_snapshot", map[string]any{})
		snapshot := mustGetSnapshot(t, result)

		btnRef := mustExtractRef(t, snapshot, `"AX Button"`)

		clickResult := sendToolsCall(t, stdin, reader, 21, "browser_click_by_ref", map[string]any{"ref": btnRef})
		if ok, _ := clickResult["ok"].(bool); !ok {
			t.Fatalf("click_by_ref failed: %#v", clickResult)
		}
		sendToolsCall(t, stdin, reader, 22, "browser_wait_for_text", map[string]any{
			"text": "clicked:ax-button", "timeoutMs": 10000,
		})
	})

	t.Run("ref_type_by_ref", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 30, "browser_aria_snapshot", map[string]any{})
		snapshot := mustGetSnapshot(t, result)

		inputRef := mustExtractRef(t, snapshot, `"AX Input"`)

		sendToolsCall(t, stdin, reader, 31, "browser_type_by_ref", map[string]any{
			"ref": inputRef, "text": "hello-ax", "clear": true,
		})
		sendToolsCall(t, stdin, reader, 32, "browser_wait_for_text", map[string]any{
			"text": "typed:hello-ax", "timeoutMs": 10000,
		})
	})

	t.Run("ref_set_input_value_by_ref", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 40, "browser_aria_snapshot", map[string]any{})
		snapshot := mustGetSnapshot(t, result)

		inputRef := mustExtractRef(t, snapshot, `"AX Input"`)

		sendToolsCall(t, stdin, reader, 41, "browser_set_input_value_by_ref", map[string]any{
			"ref": inputRef, "value": "set-ax",
		})
		// Trigger change event so the fixture updates the status.
		sendToolsCall(t, stdin, reader, 42, "browser_evaluate", map[string]any{
			"expression": `document.getElementById('axInput').dispatchEvent(new Event('change'))`,
		})
		sendToolsCall(t, stdin, reader, 43, "browser_wait_for_text", map[string]any{
			"text": "changed:set-ax", "timeoutMs": 10000,
		})
	})

	t.Run("shadow_ref_click_by_ref", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 50, "browser_aria_snapshot", map[string]any{})
		snapshot := mustGetSnapshot(t, result)

		shadowRef := mustExtractRef(t, snapshot, `"AX Shadow Button"`)

		clickResult := sendToolsCall(t, stdin, reader, 51, "browser_click_by_ref", map[string]any{"ref": shadowRef})
		if ok, _ := clickResult["ok"].(bool); !ok {
			t.Fatalf("shadow click_by_ref failed: %#v", clickResult)
		}
		sendToolsCall(t, stdin, reader, 52, "browser_wait_for_text", map[string]any{
			"text": "clicked:shadow-button", "timeoutMs": 10000,
		})
	})

	t.Run("selector_scoped_ax_snapshot", func(t *testing.T) {
		// Request a snapshot scoped to the #axForm container.
		// The AX path should use Accessibility.queryAXTree; the DOM fallback
		// also works, so we just verify the result is non-empty and contains
		// only elements inside the form.
		result := sendToolsCall(t, stdin, reader, 60, "browser_aria_snapshot", map[string]any{
			"selector": "#axForm",
		})
		snapshot := mustGetSnapshot(t, result)
		mustHaveAXSource(t, result)

		// The form's button must appear.
		if !strings.Contains(snapshot, `"AX Form Button"`) {
			t.Fatalf("selector-scoped snapshot missing AX Form Button:\n%s", snapshot)
		}
		// The top-level AX Button (outside the form) must NOT appear.
		if strings.Contains(snapshot, `"AX Button"`) {
			t.Fatalf("selector-scoped snapshot leaked out-of-scope AX Button:\n%s", snapshot)
		}
		assertRefCountMatches(t, result, snapshot)
		t.Logf("selector-scoped snapshot (%d refs):\n%s", axRefCount(snapshot), snapshot)
	})

	t.Run("iframe_ax_snapshot", func(t *testing.T) {
		// The fixture page embeds an iframe with a button inside it.
		// With pierce/iframe merging the button should appear in the full snapshot.
		result := sendToolsCall(t, stdin, reader, 70, "browser_aria_snapshot", map[string]any{})
		snapshot := mustGetSnapshot(t, result)
		mustHaveAXSource(t, result)

		if strings.Contains(snapshot, `"AX IFrame Button"`) {
			t.Logf("iframe button found in AX snapshot (cross-frame merge working)")
		} else {
			// Cross-origin or sandboxed iframes may not be accessible; log but
			// do not fail – the test documents the expected behaviour.
			t.Logf("iframe button NOT found in AX snapshot (may be cross-origin or browser version limitation):\n%s", snapshot)
		}
	})

	t.Run("iframe_ref_click_by_ref", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 80, "browser_aria_snapshot", map[string]any{})
		snapshot := mustGetSnapshot(t, result)
		if !strings.Contains(snapshot, `"AX IFrame Button"`) {
			t.Fatalf("iframe button not found in AX snapshot:\n%s", snapshot)
		}
		iframeButtonRef := mustExtractRef(t, snapshot, `"AX IFrame Button"`)

		clickResult := sendToolsCall(t, stdin, reader, 81, "browser_click_by_ref", map[string]any{"ref": iframeButtonRef})
		if ok, _ := clickResult["ok"].(bool); !ok {
			t.Fatalf("iframe click_by_ref failed: %#v", clickResult)
		}
		sendToolsCall(t, stdin, reader, 82, "browser_wait_for_text", map[string]any{
			"text": "clicked:iframe-button", "timeoutMs": 10000,
		})
	})

	t.Run("iframe_ref_type_by_ref", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 90, "browser_aria_snapshot", map[string]any{})
		snapshot := mustGetSnapshot(t, result)
		if !strings.Contains(snapshot, `"AX IFrame Input"`) {
			t.Fatalf("iframe input not found in AX snapshot:\n%s", snapshot)
		}
		iframeInputRef := mustExtractRef(t, snapshot, `"AX IFrame Input"`)

		sendToolsCall(t, stdin, reader, 91, "browser_type_by_ref", map[string]any{
			"ref": iframeInputRef, "text": "iframe-typed", "clear": true,
		})
		sendToolsCall(t, stdin, reader, 92, "browser_wait_for_text", map[string]any{
			"text": "iframe-typed:iframe-typed", "timeoutMs": 10000,
		})
	})

	t.Run("iframe_ref_set_input_value_by_ref", func(t *testing.T) {
		result := sendToolsCall(t, stdin, reader, 100, "browser_aria_snapshot", map[string]any{})
		snapshot := mustGetSnapshot(t, result)
		if !strings.Contains(snapshot, `"AX IFrame Input"`) {
			t.Fatalf("iframe input not found in AX snapshot:\n%s", snapshot)
		}
		iframeInputRef := mustExtractRef(t, snapshot, `"AX IFrame Input"`)

		sendToolsCall(t, stdin, reader, 101, "browser_set_input_value_by_ref", map[string]any{
			"ref": iframeInputRef, "value": "iframe-set",
		})
		sendToolsCall(t, stdin, reader, 102, "browser_wait_for_text", map[string]any{
			"text": "iframe-changed:iframe-set", "timeoutMs": 10000,
		})
	})

	if t.Failed() {
		t.Logf("mcp stderr:\n%s", mcpStderr.String())
	}
}

// ---------------------------------------------------------------------------
// AX fixture server
// ---------------------------------------------------------------------------

// startAXFixtureServer serves a minimal HTML page designed to exercise the
// AX snapshot path:
//   - heading, link, named button, named input (interactive elements)
//   - a shadow DOM component with a named button
//   - a status div that reflects interactions (for wait_for_text assertions)
func startAXFixtureServer(t *testing.T) (string, func()) {
	t.Helper()

	mux := http.NewServeMux()
	var baseURL string
	mux.HandleFunc("/ax", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, strings.ReplaceAll(axFixtureHTML, "__IFRAME_SRC__", baseURL+"/ax-frame"))
	})
	mux.HandleFunc("/ax-frame", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, axFrameHTML)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start ax fixture listener: %v", err)
	}

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	baseURL = fmt.Sprintf("http://%s", ln.Addr().String())
	url := baseURL + "/ax"
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return url, shutdown
}

const axFixtureHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <title>AX Fixture</title>
</head>
<body>
  <h1>AX Fixture Heading</h1>
  <a href="#" id="axLink" aria-label="AX Link">AX Link</a>

  <button id="axBtn" aria-label="AX Button"
    onclick="setStatus('clicked:ax-button')">AX Button</button>

  <input id="axInput" aria-label="AX Input" value=""
    oninput="setStatus('typed:' + this.value)"
    onchange="setStatus('changed:' + this.value)" />

  <ax-shadow-widget></ax-shadow-widget>

  <!-- Scoped snapshot target: only AX Form Button lives inside this form -->
  <form id="axForm">
    <button id="axFormBtn" aria-label="AX Form Button" type="button"
      onclick="setStatus('clicked:form-button')">AX Form Button</button>
  </form>

  <!-- Same-origin iframe for cross-frame AX merge test -->
  <iframe id="axIframe" src="__IFRAME_SRC__"
    style="width:260px;height:120px;border:none">
  </iframe>

  <div id="status">status:init</div>
  <div id="ready">ready</div>

  <script>
    function setStatus(msg) {
      document.getElementById('status').textContent = msg;
    }

    customElements.define('ax-shadow-widget', class extends HTMLElement {
      connectedCallback() {
        const root = this.attachShadow({ mode: 'open' });
        root.innerHTML =
          '<button id="shadowBtn" aria-label="AX Shadow Button"' +
          ' onclick="document.getElementById(\'status\').textContent=\'clicked:shadow-button\'">' +
          'AX Shadow Button</button>';
      }
    });
  </script>
</body>
</html>`

const axFrameHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <title>AX Frame</title>
</head>
<body>
  <button aria-label="AX IFrame Button"
    onclick="parent.document.getElementById('status').textContent='clicked:iframe-button'">
    AX IFrame Button
  </button>
  <input aria-label="AX IFrame Input"
    oninput="parent.document.getElementById('status').textContent='iframe-typed:' + this.value"
    onchange="parent.document.getElementById('status').textContent='iframe-changed:' + this.value" />
</body>
</html>`

// ---------------------------------------------------------------------------
// Helpers specific to AX tests
// ---------------------------------------------------------------------------

func mustGetSnapshot(t *testing.T, result map[string]any) string {
	t.Helper()
	snapshot, ok := result["snapshot"].(string)
	if !ok || strings.TrimSpace(snapshot) == "" {
		t.Fatalf("browser_aria_snapshot returned no snapshot: %#v", result)
	}
	return snapshot
}

func axRefCount(snapshot string) int {
	re := regexp.MustCompile(`\[ref=e\d+\]`)
	return len(re.FindAllString(snapshot, -1))
}

func mustHaveAXSource(t *testing.T, result map[string]any) {
	t.Helper()
	if got, _ := result["source"].(string); got != "ax" {
		t.Fatalf("expected AX snapshot source, got %#v", result["source"])
	}
}

func assertRefCountMatches(t *testing.T, result map[string]any, snapshot string) {
	t.Helper()
	want := axRefCount(snapshot)
	got := intResultField(result, "refCount")
	if got != want {
		t.Fatalf("refCount mismatch: result=%d snapshot=%d result=%#v", got, want, result)
	}
}

func intResultField(result map[string]any, key string) int {
	switch v := result[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}
