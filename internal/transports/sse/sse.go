package sse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"brosdk-mcp/internal/mcp"
)

type Endpoints struct {
	SSE     string
	Message string
	Session string
	UI      string
}

type Server struct {
	port      int
	logger    *slog.Logger
	handler   *mcp.Handler
	httpSrv   *http.Server
	listener  net.Listener
	serveDone chan error

	nextClientID  atomic.Int64
	nextSessionID atomic.Int64
	mu            sync.Mutex
	clients       map[int64]sseClient
	sessions      map[string]sessionState
}

type sseClient struct {
	sessionID string
	ch        chan string
}

type sessionState struct {
	ID          string
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ClientCount int
}

const sessionTTL = 5 * time.Minute

func NewServer(port int, logger *slog.Logger, handler *mcp.Handler) *Server {
	return &Server{
		port:      port,
		logger:    logger,
		handler:   handler,
		serveDone: make(chan error, 1),
		clients:   make(map[int64]sseClient),
		sessions:  make(map[string]sessionState),
	}
}

func (s *Server) Start() (Endpoints, error) {
	if s.port <= 0 {
		s.port = 0
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/ui", s.handleUI)
	mux.HandleFunc("/sse", s.handleSSE)
	mux.HandleFunc("/message", s.handleMessage)
	mux.HandleFunc("/session", s.handleSession)

	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return Endpoints{}, fmt.Errorf("listen sse: %w", err)
	}
	s.listener = ln

	go func() {
		err := s.httpSrv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.serveDone <- err
			return
		}
		s.serveDone <- nil
	}()

	addr := ln.Addr().String()
	return Endpoints{
		SSE:     fmt.Sprintf("http://%s/sse", addr),
		Message: fmt.Sprintf("http://%s/message", addr),
		Session: fmt.Sprintf("http://%s/session", addr),
		UI:      fmt.Sprintf("http://%s/ui", addr),
	}, nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	if err := s.httpSrv.Shutdown(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	for id, client := range s.clients {
		delete(s.clients, id)
		close(client.ch)
	}
	s.sessions = make(map[string]sessionState)
	s.mu.Unlock()

	select {
	case err := <-s.serveDone:
		return err
	default:
		return nil
	}
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, reused := s.ensureSession(r)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	clientID := s.nextClientID.Add(1)
	ch := make(chan string, 16)
	s.mu.Lock()
	s.clients[clientID] = sseClient{
		sessionID: sessionID,
		ch:        ch,
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		client, ok := s.clients[clientID]
		if ok {
			delete(s.clients, clientID)
			close(client.ch)
		}
		s.touchSessionLocked(sessionID, -1)
		s.cleanupExpiredSessionsLocked(time.Now())
		s.mu.Unlock()
	}()

	readyRaw, _ := json.Marshal(map[string]any{
		"status":    "connected",
		"sessionId": sessionID,
		"reused":    reused,
	})
	_, _ = io.WriteString(w, "event: ready\ndata: "+string(readyRaw)+"\n\n")
	flusher.Flush()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, _ = io.WriteString(w, "event: message\ndata: "+msg+"\n\n")
			flusher.Flush()
		case <-ticker.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, hasSessionID := s.extractRequestedSessionID(r)
	if hasSessionID {
		if !s.touchSession(sessionID, 0) {
			http.Error(w, fmt.Sprintf("unknown sessionId %q", sessionID), http.StatusNotFound)
			return
		}
		w.Header().Set("X-MCP-Session-ID", sessionID)
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read request body failed", http.StatusBadRequest)
		return
	}

	req, err := mcp.ParseRequest(raw)
	var (
		resp      mcp.Response
		writeBody bool
	)
	if err != nil {
		resp = mcp.Response{
			JSONRPC: "2.0",
			Error:   &mcp.Error{Code: -32700, Message: fmt.Sprintf("parse error: %v", err)},
		}
		writeBody = true
	} else {
		if mcp.IsNotification(req) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		resp = s.handler.HandleRequest(r.Context(), req)
		writeBody = true
	}

	if !writeBody {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	respRaw, err := mcp.EncodeResponse(resp)
	if err != nil {
		http.Error(w, "encode response failed", http.StatusInternalServerError)
		return
	}

	if hasSessionID {
		s.broadcast(sessionID, respRaw)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respRaw)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, ok := s.extractRequestedSessionID(r)
	if !ok {
		http.Error(w, "missing sessionId", http.StatusBadRequest)
		return
	}

	if !s.deleteSession(sessionID) {
		http.Error(w, fmt.Sprintf("unknown sessionId %q", sessionID), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) broadcast(sessionID string, raw []byte) {
	msg := string(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, client := range s.clients {
		if client.sessionID != sessionID {
			continue
		}
		select {
		case client.ch <- msg:
		default:
		}
	}
}

func (s *Server) extractRequestedSessionID(r *http.Request) (string, bool) {
	if sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId")); sessionID != "" {
		return sessionID, true
	}
	if sessionID := strings.TrimSpace(r.Header.Get("X-MCP-Session-ID")); sessionID != "" {
		return sessionID, true
	}
	return "", false
}

func (s *Server) deleteSession(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return false
	}

	for id, client := range s.clients {
		if client.sessionID != sessionID {
			continue
		}
		delete(s.clients, id)
		close(client.ch)
	}
	delete(s.sessions, sessionID)
	return true
}

func (s *Server) ensureSession(r *http.Request) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.cleanupExpiredSessionsLocked(now)

	if sessionID, ok := s.extractRequestedSessionID(r); ok {
		st, exists := s.sessions[sessionID]
		if exists {
			st.LastSeenAt = now
			st.ClientCount++
			s.sessions[sessionID] = st
			return sessionID, true
		}
		s.sessions[sessionID] = sessionState{
			ID:          sessionID,
			CreatedAt:   now,
			LastSeenAt:  now,
			ClientCount: 1,
		}
		return sessionID, false
	}

	sessionID := fmt.Sprintf("sse-%d", s.nextSessionID.Add(1))
	s.sessions[sessionID] = sessionState{
		ID:          sessionID,
		CreatedAt:   now,
		LastSeenAt:  now,
		ClientCount: 1,
	}
	return sessionID, false
}

func (s *Server) touchSession(sessionID string, deltaClients int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.touchSessionLocked(sessionID, deltaClients)
}

func (s *Server) touchSessionLocked(sessionID string, deltaClients int) bool {
	now := time.Now()
	s.cleanupExpiredSessionsLocked(now)

	st, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	st.LastSeenAt = now
	st.ClientCount += deltaClients
	if st.ClientCount < 0 {
		st.ClientCount = 0
	}
	s.sessions[sessionID] = st
	return true
}

func (s *Server) cleanupExpiredSessionsLocked(now time.Time) {
	for id, st := range s.sessions {
		if st.ClientCount > 0 {
			continue
		}
		if now.Sub(st.LastSeenAt) < sessionTTL {
			continue
		}
		delete(s.sessions, id)
	}
}
