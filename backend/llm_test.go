package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
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

func TestHermesThreadKeysAreIsolated(t *testing.T) {
	c := &HermesClient{baseURL: "http://hermes.invalid", sessionKey: "owner"}
	keys := make(map[string]string)
	for _, thread := range []string{"one", "two", "one"} {
		r, err := c.newRequest(context.Background(), thread, http.MethodPost, "/v1/runs", nil)
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
