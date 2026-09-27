package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunPersistenceAndConversationBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.db")
	db, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.CreateThread("old")
	if err != nil {
		t.Fatal(err)
	}
	// Emulate a conversation created by the previous schema.
	if _, err = db.db.Exec(`DELETE FROM thread_agents WHERE thread_id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacy, _ := db.GetThread(old.ID)
	if legacy.RuntimeID != "crash" {
		t.Fatal("legacy runtime changed")
	}
	local, err := db.CreateThreadRuntime("new", "local")
	if err != nil {
		t.Fatal(err)
	}
	if local.RuntimeID != "local" {
		t.Fatal("new runtime not bound")
	}
	r := AgentRun{ID: "request-one", ThreadID: local.ID, RuntimeID: "local", SessionID: hermesSessionID(local.ID), Status: "submitting", Input: "hello", CreatedAt: nowUTC(), Request: `{"input":"hello"}`}
	if _, created, err := db.reserveRun(r); err != nil || !created {
		t.Fatalf("reserve: %v %v", created, err)
	}
	if _, created, err := db.reserveRun(r); err != nil || created {
		t.Fatalf("replay: %v %v", created, err)
	}
	other := r
	other.ID = "request-two"
	if _, _, err := db.reserveRun(other); !errors.Is(err, errRunBusy) {
		t.Fatalf("overlap accepted: %v", err)
	}
	other = r
	other.Input = "different"
	if _, _, err := db.reserveRun(other); !errors.Is(err, errRunConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	if err := db.DeleteThread(local.ID); err == nil {
		t.Fatal("deleted active conversation")
	}
	for i := 0; i < 2; i++ {
		if _, _, err := db.addEvent(local.ID, r.ID, "tool.result", "result:one", map[string]int{"exit_code": 1}); err != nil {
			t.Fatal(err)
		}
	}
	events, _ := db.events(local.ID, 0)
	if len(events) != 1 {
		t.Fatalf("duplicate events: %d", len(events))
	}
	after, _ := db.events(local.ID, events[0].Seq)
	if len(after) != 0 {
		t.Fatal("cursor replayed old events")
	}
	r.Status = "completed"
	r.Output = "done"
	r.SessionID = "rotated-session"
	if err := db.saveRun(r); err != nil {
		t.Fatal(err)
	}
	if err := db.saveRun(r); err != nil {
		t.Fatal(err)
	}
	messages, _ := db.ListMessages(local.ID)
	if len(messages) != 2 {
		t.Fatalf("duplicate final: %d", len(messages))
	}
	session, _ := db.sessionID(local.ID)
	if session != "rotated-session" {
		t.Fatal("session rotation lost")
	}
	if err := db.DeleteThread(local.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRunLostAcceptanceAndEvidence(t *testing.T) {
	var mu sync.Mutex
	posts := 0
	var firstBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/runs" && r.Method == "POST":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["instructions"] != hermesSystemPrompt {
				t.Error("instructions omitted")
			}
			if r.Header.Get("Idempotency-Key") != "request-live" {
				t.Error("missing stable idempotency key")
			}
			encoded, _ := json.Marshal(body)
			mu.Lock()
			posts++
			n := posts
			if firstBody == "" {
				firstBody = string(encoded)
			} else if firstBody != string(encoded) {
				t.Error("retry payload changed")
			}
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"error":"lost acceptance response"}`)
				return
			}
			fmt.Fprint(w, `{"run_id":"upstream-one","status":"started"}`)
		case strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: tool.completed\ndata: {\"event\":\"tool.completed\",\"tool\":\"terminal\",\"error\":true}\n\nevent: run.completed\ndata: {\"event\":\"run.completed\"}\n\n")
		case r.URL.Path == "/v1/runs/upstream-one":
			fmt.Fprint(w, `{"status":"completed","output":"Agent reply","session_id":"rotated-one"}`)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			fmt.Fprint(w, `{"session_id":"rotated-one","data":[{"id":1,"role":"assistant","tool_calls":[{"id":"call-one","function":{"name":"terminal","arguments":"{\"command\":\"false\"}"}}]},{"id":2,"role":"tool","tool_call_id":"call-one","tool_name":"terminal","content":"{\"exit_code\":1,\"output\":\"test-secret\"}"}]}`)
		default:
			t.Errorf("unexpected upstream route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	store, _ := openStore(filepath.Join(t.TempDir(), "app.db"))
	defer store.Close()
	client := &HermesClient{baseURL: upstream.URL, apiKey: "test-secret", sessionKey: "owner", httpClient: upstream.Client()}
	app := newServer(store, client)
	defer app.close()
	thread, _ := store.CreateThread("test")
	run, err := app.submit(context.Background(), thread.ID, "request-live", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		run, _ = store.run(run.ID)
		if !run.active() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if run.Status != "completed" || run.Output != "Agent reply" {
		t.Fatalf("run not reconciled: %+v", run)
	}
	events, _ := store.events(thread.ID, 0)
	found := false
	for _, event := range events {
		if strings.Contains(string(event.Data), "test-secret") {
			t.Fatal("credential exposed")
		}
		if event.Kind == "tool.result" && strings.Contains(string(event.Data), `exit_code`) {
			found = true
		}
	}
	if !found {
		t.Fatal("tool failure evidence missing")
	}
	messages, _ := store.ListMessages(thread.ID)
	if len(messages) != 2 {
		t.Fatalf("unexpected message count %d", len(messages))
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 2 {
		t.Fatalf("submission attempts %d", posts)
	}
}

func TestRunApprovalOwnershipAndStaleChoice(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{"ok":true}`) }))
	defer upstream.Close()
	store, _ := openStore(filepath.Join(t.TempDir(), "app.db"))
	defer store.Close()
	app := newServer(store, &HermesClient{baseURL: upstream.URL, sessionKey: "owner", httpClient: upstream.Client()})
	defer app.close()
	thread, _ := store.CreateThread("test")
	r := AgentRun{ID: "request-approve", ThreadID: thread.ID, Status: "waiting_for_approval", RuntimeID: "crash", UpstreamID: "remote", CreatedAt: nowUTC(), Approval: json.RawMessage(`{"request_id":"approval-one","choices":["once","deny"]}`)}
	if _, _, err := store.reserveRun(r); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		thread, body string
		status       int
	}{
		{"other", `{"choice":"once","request_id":"approval-one"}`, 404},
		{thread.ID, `{"choice":"once","request_id":"expired"}`, 409},
		{thread.ID, `{"choice":"permanent","request_id":"approval-one"}`, 409},
		{thread.ID, `{"choice":"once","request_id":"approval-one"}`, 200},
	} {
		req := httptest.NewRequest("POST", "/api/threads/"+test.thread+"/runs/"+r.ID+"/approval", strings.NewReader(test.body))
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, req)
		if w.Code != test.status {
			t.Fatalf("got %d want %d: %s", w.Code, test.status, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("invalid approvals forwarded: %d", calls)
	}
}

func TestRecoveryStopsAcceptedRunWithoutResubmitting(t *testing.T) {
	var mu sync.Mutex
	posts, stops := 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/runs":
			mu.Lock()
			posts++
			mu.Unlock()
			t.Error("accepted run resubmitted")
			w.WriteHeader(500)
		case strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream") // Simulate a disconnected event stream.
		case strings.HasSuffix(r.URL.Path, "/stop"):
			mu.Lock()
			stops++
			mu.Unlock()
			fmt.Fprint(w, `{"status":"stopping"}`)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			fmt.Fprint(w, `{"data":[]}`)
		default:
			fmt.Fprint(w, `{"status":"cancelled","pending_steer":["unfinished update"]}`)
		}
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "app.db")
	store, _ := openStore(path)
	thread, _ := store.CreateThread("recovery")
	run := AgentRun{ID: "request-recover", ThreadID: thread.ID, RuntimeID: "crash", UpstreamID: "accepted", SessionID: hermesSessionID(thread.ID), Status: "running", CreatedAt: nowUTC()}
	if _, _, err := store.reserveRun(run); err != nil {
		t.Fatal(err)
	}
	store.addEvent(thread.ID, run.ID, "control.stop.requested", "stop:"+run.ID, map[string]string{"status": "requested"})
	store.Close()
	store, _ = openStore(path)
	defer store.Close()
	app := newServer(store, &HermesClient{baseURL: upstream.URL, sessionKey: "owner", httpClient: upstream.Client()})
	defer app.close()
	app.recoverRuns()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, _ = store.run(run.ID)
		if !run.active() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if run.Status != "cancelled" || !strings.Contains(string(run.PendingSteer), "unfinished update") {
		t.Fatalf("recovery lost state: %+v", run)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 0 || stops != 1 {
		t.Fatalf("posts=%d stops=%d", posts, stops)
	}
}
