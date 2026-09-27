package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// An account has its own Store, Hub, run workers and Hermes gateway. No handler
// receives a browser-selected account ID, and no new account uses the old gateway.
type accountSlot struct {
	identity accountIdentity
	legacy   bool
	app      *Server
	handler  http.Handler
	starting bool
	err      error
	retryAt  time.Time
}
type accountManager struct {
	mu               sync.Mutex
	wg               sync.WaitGroup
	ctx              context.Context
	cancel           context.CancelFunc
	registry         *Store
	directory, owner string
	legacy           *HermesClient
	provision        func(context.Context, string) (*HermesClient, error)
	slots            map[string]*accountSlot
}

func newAccountManager(store *Store, legacy *HermesClient, directory, owner string, provision func(context.Context, string) (*HermesClient, error)) (*accountManager, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if _, err := store.db.Exec(`CREATE TABLE IF NOT EXISTS accounts (id TEXT PRIMARY KEY, email TEXT NOT NULL, service INTEGER NOT NULL, legacy INTEGER NOT NULL); CREATE UNIQUE INDEX IF NOT EXISTS one_legacy_account ON accounts(legacy) WHERE legacy=1`); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &accountManager{ctx: ctx, cancel: cancel, registry: store, legacy: legacy, directory: directory, owner: owner, provision: provision, slots: map[string]*accountSlot{}}
	rows, err := store.db.Query(`SELECT id,email,service,legacy FROM accounts`)
	if err != nil {
		cancel()
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		slot := &accountSlot{}
		if err := rows.Scan(&slot.identity.ID, &slot.identity.Email, &slot.identity.Service, &slot.legacy); err != nil {
			cancel()
			return nil, err
		}
		m.slots[slot.identity.ID] = slot
	}
	if err := rows.Err(); err != nil {
		cancel()
		return nil, err
	}
	return m, nil
}
func (m *accountManager) recover() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, slot := range m.slots {
		m.start(slot)
	}
}

// start is called with m.mu held. Provisioning never holds that lock.
func (m *accountManager) start(slot *accountSlot) {
	if slot.starting || slot.app != nil || m.ctx.Err() != nil || time.Now().Before(slot.retryAt) {
		return
	}
	slot.starting = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		llm := m.legacy
		var err error
		if !slot.legacy {
			llm, err = m.provision(m.ctx, slot.identity.ID)
		}
		var store *Store
		if err == nil {
			if slot.legacy {
				store = m.registry
			} else {
				dir := filepath.Join(m.directory, slot.identity.ID)
				if err = os.MkdirAll(dir, 0700); err == nil {
					store, err = openStore(filepath.Join(dir, "386gpt.db"))
				}
			}
		}
		var app *Server
		if err == nil {
			app = newServer(store, llm)
			app.recoverRuns()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		slot.starting = false
		slot.err = err
		slot.retryAt = time.Now().Add(10 * time.Second)
		if err != nil {
			slog.Error("prepare account environment", "account", slot.identity.ID, "error", err)
			return
		}
		slot.app = app
		slot.handler = app.routes()
	}()
}
func (m *accountManager) slot(identity accountIdentity) (*accountSlot, error) {
	if slot := m.slots[identity.ID]; slot != nil {
		return slot, nil
	}
	// Exactly one verified owner identity may claim the pre-account database.
	// Persist that binding before exposing history, so later identities cannot claim it.
	legacy := false
	if !identity.Service && strings.EqualFold(identity.Email, m.owner) {
		var count int
		if err := m.registry.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE legacy=1`).Scan(&count); err != nil {
			return nil, err
		}
		legacy = count == 0
	}
	if _, err := m.registry.db.Exec(`INSERT INTO accounts(id,email,service,legacy) VALUES(?,?,?,?)`, identity.ID, identity.Email, identity.Service, legacy); err != nil {
		return nil, err
	}
	slot := &accountSlot{identity: identity, legacy: legacy}
	m.slots[identity.ID] = slot
	return slot, nil
}
func (m *accountManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	}
	identity, ok := r.Context().Value(identityKey{}).(accountIdentity)
	if !ok || identity.ID == "" {
		writeJSON(w, 401, map[string]string{"error": "Sign in required"})
		return
	}
	// Authentication alone does not authorize cross-origin state changes.
	if !allowedOrigin(r.Header.Get("Origin")) {
		http.Error(w, "Origin denied", 403)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	m.mu.Lock()
	slot, err := m.slot(identity)
	if err != nil {
		m.mu.Unlock()
		writeJSON(w, 503, map[string]string{"error": "Account unavailable"})
		return
	}
	m.start(slot)
	handler := slot.handler
	failed := slot.err != nil && !slot.starting
	m.mu.Unlock()
	if r.URL.Path == "/api/account" && r.Method == "GET" {
		status := "preparing"
		if handler != nil {
			status = "ready"
		} else if failed {
			status = "retrying"
		}
		writeJSON(w, 200, map[string]any{"account": identity, "environment": status, "logoutUrl": "/api/auth/logout"})
		return
	}
	if handler == nil {
		w.Header().Set("Retry-After", "2")
		writeJSON(w, 503, map[string]string{"error": "Your private environment is starting. Please retry shortly."})
		return
	}
	handler.ServeHTTP(w, r)
}
func (m *accountManager) close() {
	m.cancel()
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, slot := range m.slots {
		if slot.app != nil {
			slot.app.close()
			if !slot.legacy {
				slot.app.store.Close()
			}
		}
	}
}
func accountProvisioner(configPath string) (func(context.Context, string) (*HermesClient, error), error) {
	b, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var config hermesConfig
	if err = yaml.Unmarshal(b, &config); err != nil {
		return nil, err
	}
	c := config.Tenants
	if c.BaseURL == "" || c.APIKey == "" {
		return func(context.Context, string) (*HermesClient, error) {
			return nil, errors.New("account environment provisioner is not configured")
		}, nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid account provisioner URL")
	}
	client := &http.Client{Timeout: 150 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, id string) (*HermesClient, error) {
		req, err := http.NewRequestWithContext(ctx, "PUT", strings.TrimRight(c.BaseURL, "/")+"/v1/accounts/"+id, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("account provisioner unreachable")
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("account provisioner returned HTTP %d", res.StatusCode)
		}
		var connection struct {
			ID         string `json:"account_id"`
			BaseURL    string `json:"base_url"`
			APIKey     string `json:"api_key"`
			SessionKey string `json:"session_key"`
		}
		if err = json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&connection); err != nil {
			return nil, errors.New("invalid account provisioner response")
		}
		endpoint, e := url.Parse(connection.BaseURL)
		if e != nil || endpoint.Hostname() != u.Hostname() || endpoint.Scheme != u.Scheme || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || connection.ID != id || connection.APIKey == "" || connection.SessionKey != "account:"+id {
			return nil, errors.New("account provisioner returned mismatched identity or endpoint")
		}
		return &HermesClient{baseURL: connection.BaseURL, apiKey: connection.APIKey, sessionKey: connection.SessionKey, defaultRuntime: "crash", httpClient: &http.Client{}}, nil
	}, nil
}
