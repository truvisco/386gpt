package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func accountRequest(m *accountManager, identity accountIdentity, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), identityKey{}, identity))
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	return w
}
func awaitAccount(t *testing.T, m *accountManager, identity accountIdentity) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w := accountRequest(m, identity, "GET", "/api/account", "")
		if w.Code != 200 {
			t.Fatalf("account: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"environment":"ready"`) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("account not ready")
}
func TestAccountIsolationAndPersistentBinding(t *testing.T) {
	root := t.TempDir()
	store, err := openStore(filepath.Join(root, "386gpt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old, err := store.CreateThread("Owner history")
	if err != nil {
		t.Fatal(err)
	}
	owner := accountIdentity{ID: identityID("issuer", "user", "owner"), Email: "owner@example.com"}
	other := accountIdentity{ID: identityID("issuer", "user", "other"), Email: "other@example.com"}
	ci := accountIdentity{ID: identityID("issuer", "service", "ci"), Service: true}
	var mu sync.Mutex
	calls := map[string]int{}
	provision := func(_ context.Context, id string) (*HermesClient, error) {
		mu.Lock()
		calls[id]++
		mu.Unlock()
		return &HermesClient{baseURL: "http://account-" + id, apiKey: id, sessionKey: "account:" + id, httpClient: &http.Client{}}, nil
	}
	legacy := &HermesClient{baseURL: "http://legacy", apiKey: "legacy", sessionKey: "legacy", httpClient: &http.Client{}}
	m, err := newAccountManager(store, legacy, filepath.Join(root, "accounts"), owner.Email, provision)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []accountIdentity{other, ci, owner} {
		awaitAccount(t, m, a)
	}
	if !strings.Contains(accountRequest(m, owner, "GET", "/api/threads", "").Body.String(), old.ID) {
		t.Fatal("owner lost legacy history")
	}
	for _, a := range []accountIdentity{other, ci} {
		if strings.Contains(accountRequest(m, a, "GET", "/api/threads", "").Body.String(), old.ID) {
			t.Fatal("legacy history leaked")
		}
		for _, path := range []string{"/api/threads/" + old.ID + "/messages", "/api/threads/" + old.ID + "/runs", "/api/threads/" + old.ID + "/activity", "/api/runtime?thread_id=" + old.ID, "/ws?thread_id=" + old.ID} {
			if w := accountRequest(m, a, "GET", path, ""); w.Code != 404 {
				t.Errorf("cross-account read %s returned %d", path, w.Code)
			}
		}
		for _, method := range []string{"PATCH", "DELETE"} {
			if w := accountRequest(m, a, method, "/api/threads/"+old.ID, `{"title":"stolen"}`); w.Code != 404 {
				t.Errorf("cross-account %s returned %d", method, w.Code)
			}
		}
	}
	w := accountRequest(m, other, "POST", "/api/threads", `{"title":"Other private chat"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var created struct {
		Thread Thread `json:"thread"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if strings.Contains(accountRequest(m, owner, "GET", "/api/threads", "").Body.String(), created.Thread.ID) {
		t.Fatal("other history leaked")
	}
	// Same browser-supplied run ID and thread IDs cannot bridge account stores.
	for _, a := range []accountIdentity{owner, ci} {
		if w := accountRequest(m, a, "POST", "/api/threads/"+created.Thread.ID+"/runs", `{"requestId":"shared-run-id","message":"hello"}`); w.Code != 404 {
			t.Errorf("cross-account run returned %d", w.Code)
		}
		if w := accountRequest(m, a, "POST", "/api/threads/"+created.Thread.ID+"/runs/shared-run-id/stop", `{}`); w.Code != 404 {
			t.Errorf("cross-account control returned %d", w.Code)
		}
	}
	m.mu.Lock()
	otherClient := m.slots[other.ID].app.llm
	ciClient := m.slots[ci.ID].app.llm
	m.mu.Unlock()
	for _, c := range []*HermesClient{otherClient, ciClient} {
		r, _ := c.newRequest(context.Background(), "same-thread", "GET", "/v1/capabilities", nil)
		if !strings.HasPrefix(r.Header.Get("X-Hermes-Session-Key"), c.sessionKey+":thread:") {
			t.Fatal("session namespace missing")
		}
	}
	if otherClient.apiKey == ciClient.apiKey || otherClient.sessionKey == ciClient.sessionKey {
		t.Fatal("shared credentials or sessions")
	}
	m.close()
	m, err = newAccountManager(store, legacy, filepath.Join(root, "accounts"), owner.Email, provision)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	m.recover()
	awaitAccount(t, m, other)
	if !strings.Contains(accountRequest(m, other, "GET", "/api/threads", "").Body.String(), created.Thread.ID) {
		t.Fatal("history did not persist")
	}
	replacement := accountIdentity{ID: identityID("issuer", "user", "replacement"), Email: owner.Email}
	awaitAccount(t, m, replacement)
	if strings.Contains(accountRequest(m, replacement, "GET", "/api/threads", "").Body.String(), old.ID) {
		t.Fatal("new subject claimed owner history")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls[owner.ID] != 0 || calls[ci.ID] == 0 {
		t.Fatal("owner/CI provision routing incorrect")
	}
}
func TestAccountFailsClosedDuringProvisioning(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := newAccountManager(s, &HermesClient{}, t.TempDir(), "owner@example.com", func(context.Context, string) (*HermesClient, error) { return nil, fmt.Errorf("unavailable") })
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	a := accountIdentity{ID: identityID("issuer", "user", "other"), Email: "other@example.com"}
	if w := accountRequest(m, a, "GET", "/api/threads", ""); w.Code != 503 {
		t.Fatal("failed provisioner exposed shared store")
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/api/threads?account=owner", nil))
	if w.Code != 401 {
		t.Fatal("client selected identity")
	}
}

func TestSignInRechecksCachedEnvironment(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			store, err := openStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			identity := accountIdentity{ID: identityID("google", "user", "subject"), Email: "user@example.com"}
			owner := "owner@example.com"
			if legacy {
				owner = identity.Email
			}
			calls := make(chan string, 10)
			release := make(chan struct{}, 10)
			connection := &HermesClient{baseURL: "http://account", apiKey: "key", sessionKey: "account", httpClient: &http.Client{}}
			provision := func(ctx context.Context, id string) (*HermesClient, error) {
				calls <- id
				select {
				case <-release:
					return connection, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			m, err := newAccountManager(store, connection, t.TempDir(), owner, provision)
			if err != nil {
				t.Fatal(err)
			}
			defer m.close()
			release <- struct{}{}
			awaitAccount(t, m, identity)
			expected := identity.ID
			if legacy {
				expected = "owner"
			}
			if id := <-calls; id != expected {
				t.Fatal("wrong environment", id)
			}
			m.mu.Lock()
			original := m.slots[identity.ID].app
			m.mu.Unlock()
			thread, err := original.store.CreateThread("Preserved history")
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				accountRequest(m, identity, "GET", "/api/account", "")
			}
			select {
			case <-calls:
				t.Fatal("ordinary request caused repeated image check")
			default:
			}
			m.signedIn(identity)
			if id := <-calls; id != expected {
				t.Fatal("wrong environment refreshed", id)
			}
			if w := accountRequest(m, identity, "GET", "/api/threads", ""); w.Code != 503 {
				t.Fatal("served account before image check completed")
			}
			// A second login during an update must not be lost behind the cached result.
			m.signedIn(identity)
			release <- struct{}{}
			release <- struct{}{}
			awaitAccount(t, m, identity)
			if id := <-calls; id != expected {
				t.Fatal("overlapping sign-in lost")
			}
			m.mu.Lock()
			same := m.slots[identity.ID].app == original
			m.mu.Unlock()
			if !same {
				t.Fatal("replaced account store/hub during image check")
			}
			if w := accountRequest(m, identity, "GET", "/api/threads", ""); !strings.Contains(w.Body.String(), thread.ID) {
				t.Fatal("history lost")
			}
		})
	}
}
