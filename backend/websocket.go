package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type socketEvent struct {
	Type    string   `json:"type"`
	Message *Message `json:"message,omitempty"`
	Thread  *Thread  `json:"thread,omitempty"`
	Error   string   `json:"error,omitempty"`
}

type clientCommand struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type Client struct {
	threadID string
	conn     *websocket.Conn
	send     chan []byte
}

type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*Client]struct{}
}

func newHub() *Hub { return &Hub{clients: make(map[string]map[*Client]struct{})} }

func (h *Hub) register(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[client.threadID] == nil {
		h.clients[client.threadID] = make(map[*Client]struct{})
	}
	h.clients[client.threadID][client] = struct{}{}
}

func (h *Hub) unregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	clients := h.clients[client.threadID]
	if _, ok := clients[client]; ok {
		delete(clients, client)
		close(client.send)
	}
	if len(clients) == 0 {
		delete(h.clients, client.threadID)
	}
}

func (h *Hub) broadcast(threadID string, event socketEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("encode websocket event", "error", err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients[threadID] {
		select {
		case client.send <- payload:
		default:
			slog.Warn("dropping event for slow websocket client", "thread_id", threadID)
		}
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

func (s *Server) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	threadID := r.URL.Query().Get("thread_id")
	if threadID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "thread_id is required"})
		return
	}
	if _, err := s.store.GetThread(threadID); err != nil {
		writeError(w, err)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("upgrade websocket", "error", err)
		return
	}
	client := &Client{threadID: threadID, conn: conn, send: make(chan []byte, 32)}
	s.hub.register(client)
	go client.writeLoop()
	ready, _ := json.Marshal(socketEvent{Type: "ready"})
	client.send <- ready
	client.readLoop(s)
}

func (c *Client) readLoop(server *Server) {
	defer func() {
		server.hub.unregister(c)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(32 << 10)
	for {
		var command clientCommand
		if err := c.conn.ReadJSON(&command); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Debug("websocket closed", "error", err)
			}
			return
		}
		content := strings.TrimSpace(command.Content)
		if command.Type != "user_message" || content == "" {
			server.hub.broadcast(c.threadID, socketEvent{Type: "error", Error: "message content is required"})
			continue
		}
		message, err := server.store.AddMessage(c.threadID, "user", content)
		if err != nil {
			server.hub.broadcast(c.threadID, socketEvent{Type: "error", Error: "could not save message"})
			continue
		}
		server.hub.broadcast(c.threadID, socketEvent{Type: "message", Message: &message})

		thread, err := server.store.GetThread(c.threadID)
		if err == nil && thread.Title == "New conversation" {
			thread, err = server.store.RenameThread(c.threadID, titleFromMessage(content))
			if err == nil {
				server.hub.broadcast(c.threadID, socketEvent{Type: "thread_updated", Thread: &thread})
			}
		}
		go server.respond(c.threadID)
	}
}

func (c *Client) writeLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case payload, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) respond(threadID string) {
	id, err := newID()
	if err != nil {
		return
	}
	message := Message{ID: id, ThreadID: threadID, Role: "assistant", CreatedAt: nowUTC(), Streaming: true}
	s.hub.broadcast(threadID, socketEvent{Type: "message", Message: &message})
	history, err := s.store.ListMessages(threadID)
	if err != nil {
		s.hub.broadcast(threadID, socketEvent{Type: "error", Error: "could not load conversation history"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	err = s.llm.Stream(ctx, history, func(chunk string) {
		message.Content += chunk
		s.hub.broadcast(threadID, socketEvent{Type: "message", Message: &message})
	})
	if err != nil {
		slog.Error("stream completion", "provider", s.llm.provider, "model", s.llm.model, "error", err)
		if message.Content == "" {
			s.hub.broadcast(threadID, socketEvent{Type: "error", Error: "the Hermes provider could not complete this request"})
			return
		}
		message.Content += "\n\n[UPLINK INTERRUPTED]"
	}
	message.Streaming = false
	if err := s.store.SaveMessage(message); err != nil {
		s.hub.broadcast(threadID, socketEvent{Type: "error", Error: "could not save assistant message"})
		return
	}
	s.hub.broadcast(threadID, socketEvent{Type: "message", Message: &message})
	if thread, err := s.store.GetThread(threadID); err == nil {
		s.hub.broadcast(threadID, socketEvent{Type: "thread_updated", Thread: &thread})
	}
}
