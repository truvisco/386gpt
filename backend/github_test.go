package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubSettingsAccountRuntimeAndSecrets(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer account-key" {
			t.Error("missing gateway authentication")
		}
		if r.URL.Path == "/v1/github/token" {
			var body struct {
				Token string `json:"token"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Token != "github_pat_private_fixture" {
				t.Error("token not forwarded privately")
			}
		}
		fmt.Fprint(w, `{"state":"connected","login":"fixture","token":"must-never-return","debug":"must-never-return"}`)
	}))
	defer upstream.Close()
	store, err := openStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app := newServer(store, &HermesClient{baseURL: upstream.URL, apiKey: "account-key", sessionKey: "account:one", httpClient: upstream.Client()})
	defer app.close()
	thread, err := store.CreateThread("owned")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, httptest.NewRequest("POST", "/api/settings/github/token?thread_id="+thread.ID, strings.NewReader(`{"token":"github_pat_private_fixture"}`)))
	if w.Code != 200 || strings.Contains(w.Body.String(), "private_fixture") || strings.Contains(w.Body.String(), "must-never-return") {
		t.Fatalf("unsafe settings response: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	app.routes().ServeHTTP(w, httptest.NewRequest("GET", "/api/settings/github?thread_id=another-account", nil))
	if w.Code != 404 || calls != 1 {
		t.Fatal("foreign conversation reached gateway")
	}
	w = httptest.NewRecorder()
	app.routes().ServeHTTP(w, httptest.NewRequest("POST", "/api/settings/github/arbitrary-command", nil))
	if w.Code != 404 || calls != 1 {
		t.Fatal("unrecognized action reached gateway")
	}
	var n int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM runs").Scan(&n); err != nil || n != 0 {
		t.Fatal("credentials must not become runs")
	}
}
