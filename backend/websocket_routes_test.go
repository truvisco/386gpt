package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWebSocketRoutes(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app := newServer(store, &HermesClient{})
	defer app.close()
	thread, err := store.CreateThread("websocket")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.routes())
	defer server.Close()
	for _, path := range []string{"/ws", "/ws/"} {
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path+"?thread_id="+thread.ID, nil)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var event socketEvent
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		conn.Close()
		if event.Type != "ready" {
			t.Fatalf("%s: missing ready event", path)
		}
	}
}
