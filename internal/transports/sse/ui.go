package sse

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
)

//go:embed ui.html
var uiHTML string

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
