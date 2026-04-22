package sse

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
)

//go:embed ui.html
var uiHTML string

const interactionFixtureHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Built-in PageAgent Fixture</title>
  <style>
    :root {
      --bg: #f6f3ea;
      --panel: #fffdf8;
      --ink: #1f2430;
      --muted: #6a6f7a;
      --line: #d9d2c3;
      --accent: #1d6b57;
      --accent-2: #c96b32;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Segoe UI", "PingFang SC", "Noto Sans", sans-serif;
      background: linear-gradient(180deg, #faf7ef 0%, var(--bg) 100%);
      color: var(--ink);
    }
    main {
      max-width: 1080px;
      margin: 0 auto;
      padding: 28px 20px 40px;
    }
    h1, h2 {
      margin-top: 0;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
      gap: 18px;
    }
    section {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 18px;
      padding: 18px;
      box-shadow: 0 14px 40px rgba(40, 33, 20, 0.10);
    }
    label {
      display: block;
      margin: 10px 0 6px;
      font-size: 13px;
      font-weight: 600;
      color: var(--muted);
    }
    input, button {
      font: inherit;
    }
    input {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 12px;
      background: #fff;
      color: var(--ink);
      padding: 11px 12px;
    }
    button {
      margin-top: 12px;
      border: 0;
      border-radius: 999px;
      padding: 10px 16px;
      font-weight: 600;
      color: #fff;
      background: var(--accent);
      cursor: pointer;
    }
    button.secondary {
      background: var(--accent-2);
    }
    .result {
      margin-top: 18px;
      padding: 14px;
      border-radius: 14px;
      background: #191d24;
      color: #eef2f7;
      font-family: "Cascadia Code", "Consolas", monospace;
      white-space: pre-wrap;
      word-break: break-word;
    }
    .eyebrow {
      margin-bottom: 8px;
      font-size: 12px;
      letter-spacing: 0.14em;
      text-transform: uppercase;
      color: var(--muted);
    }
    .copy {
      color: var(--muted);
      line-height: 1.5;
    }
  </style>
</head>
<body>
  <main>
    <p class="eyebrow">Built-in UI Fixture</p>
    <h1>Built-in PageAgent Fixture</h1>
    <p class="copy">This local page is meant for quick PageAgent checks inside the built-in UI. It exposes search, login, inspect, and simple form actions without depending on any external site.</p>
    <div class="grid">
      <section aria-label="Search Workspace">
        <h2>Search Workspace</h2>
        <label for="searchQuery">Search Query</label>
        <input id="searchQuery" type="search" aria-label="Search Query" value="" />
        <button id="searchBtn" aria-label="Search" onclick="runSearch()">Search</button>
        <div id="searchStatus" class="copy">search:idle</div>
      </section>

      <section aria-label="Login Form">
        <h2>Login Form</h2>
        <label for="loginEmail">Email Address</label>
        <input id="loginEmail" type="email" aria-label="Email Address" value="" />
        <label for="loginPassword">Password</label>
        <input id="loginPassword" type="password" aria-label="Password" value="" />
        <button id="signInBtn" aria-label="Sign In" onclick="signIn()">Sign In</button>
      </section>

      <section aria-label="Payment Details Panel">
        <h2>Checkout Summary</h2>
        <p class="copy">This area intentionally includes stable copy for stop conditions and inspection flows.</p>
        <div id="paymentDetails" aria-label="Payment Details">Payment Details</div>
        <div class="copy">Order review, billing address, and shipping estimate are visible on this fixture page.</div>
      </section>

      <section aria-label="Form Actions">
        <h2>Form Actions</h2>
        <label for="nameInput">Name Input</label>
        <input id="nameInput" aria-label="Name Input" value="" />
        <button id="applyBtn" onclick="applyAction('apply')">Apply</button>
        <button id="submitBtn" class="secondary" onclick="applyAction('submit')">Submit Form</button>
      </section>
    </div>

    <div id="result" class="result">result:init</div>
  </main>
  <script>
    function setResult(value) {
      document.getElementById('result').textContent = value;
    }
    function runSearch() {
      const query = document.getElementById('searchQuery').value || '';
      document.getElementById('searchStatus').textContent = 'search:' + query + ':submitted';
      setResult('search:' + query + ':submitted');
    }
    function signIn() {
      const email = document.getElementById('loginEmail').value || '';
      const password = document.getElementById('loginPassword').value || '';
      setResult(password ? 'login:' + email + ':success' : 'login:' + email + ':missing-password');
    }
    function applyAction(source) {
      const value = document.getElementById('nameInput').value || '';
      setResult('result:' + value + ':' + source);
    }
  </script>
</body>
</html>`

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui", http.StatusTemporaryRedirect)
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(uiHTML))
}

func (s *Server) handleUIFixtureInteraction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(interactionFixtureHTML))
}

func (s *Server) handleUIConfig(w http.ResponseWriter, r *http.Request) {
	exec, ok := s.handler.Executor().(pageAgentAIConfigManager)
	if !ok {
		http.Error(w, "page agent ai config unsupported", http.StatusNotImplemented)
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(exec.PageAgentAIConfigInfo())
		return

	case http.MethodPost:
		var payload struct {
			APIKey  string `json:"apiKey"`
			BaseURL string `json:"baseUrl"`
			Model   string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		exec.SetPageAgentAIConfig(strings.TrimSpace(payload.APIKey), strings.TrimSpace(payload.BaseURL), strings.TrimSpace(payload.Model))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(exec.PageAgentAIConfigInfo())
		return

	case http.MethodDelete:
		exec.ClearPageAgentAIConfig()
		w.WriteHeader(http.StatusNoContent)
		return

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}
