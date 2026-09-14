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

func TestLoadHermesLLM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := `
model:
  default: auto
  provider: local-test
custom_providers:
  - name: local-test
    base_url: http://localhost:9000/v1/
    api_key: test-secret
    api_mode: chat_completions
`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := loadHermesLLM(path)
	if err != nil {
		t.Fatal(err)
	}
	if client.provider != "local-test" || client.model != "auto" || client.baseURL != "http://localhost:9000/v1" {
		t.Fatalf("unexpected client config: provider=%q model=%q baseURL=%q", client.provider, client.model, client.baseURL)
	}
}

func TestStreamCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing authorization")
		}
		var request completionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "auto" || !request.Stream {
			t.Errorf("unexpected request: %#v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Hello"}}]}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":" world"}}]}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer server.Close()

	client := &LLMClient{provider: "test", model: "auto", baseURL: server.URL + "/v1", apiKey: "test-secret", httpClient: server.Client()}
	var completion strings.Builder
	err := client.Stream(context.Background(), []Message{{Role: "user", Content: "Say hello"}}, func(chunk string) { completion.WriteString(chunk) })
	if err != nil {
		t.Fatal(err)
	}
	if completion.String() != "Hello world" {
		t.Fatalf("unexpected completion %q", completion.String())
	}
}
