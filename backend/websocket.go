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
	Run      *AgentRun       `json:"run,omitempty"`
	Event    *AgentEvent     `json:"event,omitempty"`
	Type     string          `json:"type"`
	Message  *Message        `json:"message,omitempty"`
	Thread   *Thread         `json:"thread,omitempty"`
	Activity *HermesActivity `json:"activity,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type clientCommand struct {
	RequestID string   `json:"requestId,omitempty"`
	Skills    []string `json:"skills,omitempty"`
	Type      string   `json:"type"`
	Content   string   `json:"content"`
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
	CheckOrigin:     func(r *http.Request) bool { return allowedOrigin(r.Header.Get("Origin")) },
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
	if expiry, ok := r.Context().Value(accessExpiryKey{}).(time.Time); ok {
		timer := time.AfterFunc(time.Until(expiry), func() { _ = conn.Close() })
		defer timer.Stop()
	}
	if check, ok := r.Context().Value(sessionCheckKey{}).(func() bool); ok {
		done := make(chan struct{})
		defer close(done)
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					if !check() {
						_ = conn.Close()
						return
					}
				}
			}
		}()
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
		if command.RequestID == "" {
			command.RequestID, _ = newID()
		}
		if _, err := server.submit(context.Background(), c.threadID, command.RequestID, content, command.Skills); err != nil {
			server.hub.broadcast(c.threadID, socketEvent{Type: "error", Error: err.Error()})
		}
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
