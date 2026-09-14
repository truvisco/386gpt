package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

type Server struct {
	store *Store
	hub   *Hub
	llm   *LLMClient
}

func newServer(store *Store, llm *LLMClient) *Server {
	return &Server{store: store, hub: newHub(), llm: llm}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /api/runtime", s.runtime)
	mux.HandleFunc("GET /api/threads", s.listThreads)
	mux.HandleFunc("POST /api/threads", s.createThread)
	mux.HandleFunc("PATCH /api/threads/{id}", s.renameThread)
	mux.HandleFunc("DELETE /api/threads/{id}", s.deleteThread)
	mux.HandleFunc("GET /api/threads/{id}/messages", s.listMessages)
	mux.HandleFunc("GET /ws", s.serveWebSocket)
	return cors(requestLog(mux))
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, errNotFound) {
		status = http.StatusNotFound
	} else if strings.Contains(err.Error(), "required") {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return false
	}
	return true
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) runtime(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"provider": s.llm.provider, "model": s.llm.model})
}

func (s *Server) listThreads(w http.ResponseWriter, _ *http.Request) {
	threads, err := s.store.ListThreads()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": threads})
}

func (s *Server) createThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	thread, err := s.store.CreateThread(body.Title)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"thread": thread})
}

func (s *Server) renameThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	thread, err := s.store.RenameThread(r.PathValue("id"), body.Title)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": thread})
}

func (s *Server) deleteThread(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteThread(r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	messages, err := s.store.ListMessages(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}
