package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHermesAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := `
agent:
  base_url: http://localhost:8642/
  api_key: test-secret
  session_key: agent:main:386gpt:dm:test
`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := loadHermesAgent(path)
	if err != nil {
		t.Fatal(err)
	}
	if client.baseURL != "http://localhost:8642" || client.apiKey != "test-secret" || client.sessionKey != "agent:main:386gpt:dm:test" {
		t.Fatalf("unexpected client config: %#v", client)
	}
}

func TestStreamHermesSession(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing authorization")
		}
		if r.Header.Get("X-Hermes-Session-Key") != "agent:main:386gpt:dm:test:thread:thread-one" {
			t.Error("missing Hermes session key")
		}
		switch r.URL.Path {
		case "/api/sessions":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"session":{"id":"386gpt-thread-one"}}`)
		case "/api/sessions/386gpt-thread-one/chat/stream":
			assertHermesInstructions(t, r, "Say hello")
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintln(w, "event: run.started")
			fmt.Fprintln(w, `data: {"run_id":"run-one"}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "event: message.started")
			fmt.Fprintln(w, `data: {"message":{"id":"message-one","role":"assistant"}}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "event: tool.started")
			fmt.Fprintln(w, `data: {"tool_name":"web_search","preview":"Looking it up"}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "event: assistant.delta")
			fmt.Fprintln(w, `data: {"delta":"Hello"}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "event: assistant.delta")
			fmt.Fprintln(w, `data: {"delta":" world"}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "event: assistant.completed")
			fmt.Fprintln(w, `data: {"content":"Hello world","completed":true}`)
			fmt.Fprintln(w)
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &HermesClient{baseURL: server.URL, apiKey: "test-secret", sessionKey: "agent:main:386gpt:dm:test", httpClient: server.Client()}
	var completion strings.Builder
	var activities []HermesActivity
	final, err := client.Stream(context.Background(), "thread-one", "Say hello", func(chunk string) {
		completion.WriteString(chunk)
	}, func(activity HermesActivity) {
		activities = append(activities, activity)
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("expected two requests, got %d", requests)
	}
	if completion.String() != "Hello world" || final != "Hello world" {
		t.Fatalf("unexpected completion stream=%q final=%q", completion.String(), final)
	}
	if len(activities) != 1 || activities[0].Tool != "web_search" || activities[0].State != "started" {
		t.Fatalf("unexpected activities: %#v", activities)
	}
}

func TestExistingHermesSessionIsReused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hermes-Session-Key") != "test-key:thread:existing" {
			t.Error("existing thread must use its own stable session key")
		}
		switch r.URL.Path {
		case "/api/sessions":
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"error":{"message":"already exists"}}`)
		case "/api/sessions/386gpt-existing/chat/stream":
			assertHermesInstructions(t, r, "Continue")
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintln(w, "event: assistant.completed")
			fmt.Fprintln(w, `data: {"content":"Reused"}`)
			fmt.Fprintln(w)
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &HermesClient{baseURL: server.URL, apiKey: "test-secret", sessionKey: "test-key", httpClient: server.Client()}
	final, err := client.Stream(context.Background(), "existing", "Continue", func(string) {}, func(HermesActivity) {})
	if err != nil {
		t.Fatal(err)
	}
	if final != "Reused" {
		t.Fatalf("unexpected final response %q", final)
	}
}

func TestHermesThreadKeysAreIsolated(t *testing.T) {
	c := &HermesClient{baseURL: "http://hermes.invalid", sessionKey: "owner"}
	keys := make(map[string]string)
	for _, thread := range []string{"one", "two", "one"} {
		r, err := c.newRequest(context.Background(), thread, http.MethodPost, "/api/sessions", nil)
		if err != nil {
			t.Fatal(err)
		}
		key := r.Header.Get("X-Hermes-Session-Key")
		if previous, ok := keys[thread]; ok && previous != key {
			t.Fatal("thread key changed between requests")
		}
		keys[thread] = key
	}
	if keys["one"] == keys["two"] || keys["one"] == "owner" {
		t.Fatal("different threads must not share terminal state")
	}
}

func assertHermesInstructions(t *testing.T, r *http.Request, message string) {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["message"] != message || body["system_message"] != hermesSystemPrompt {
		t.Errorf("chat request must include user input and runtime instructions: %#v", body)
	}
}
