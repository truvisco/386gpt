package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var errRunBusy = errors.New("conversation already has an active run")
var errRunConflict = errors.New("request ID was already used for different input")

type AgentRun struct {
	ID           string          `json:"id"`
	ThreadID     string          `json:"threadId"`
	RuntimeID    string          `json:"runtimeId"`
	UpstreamID   string          `json:"upstreamId,omitempty"`
	SessionID    string          `json:"sessionId"`
	Status       string          `json:"status"`
	Input        string          `json:"input"`
	Skills       []string        `json:"skills"`
	CreatedAt    string          `json:"createdAt"`
	UpdatedAt    string          `json:"updatedAt"`
	Output       string          `json:"output,omitempty"`
	Error        string          `json:"error,omitempty"`
	Approval     json.RawMessage `json:"approval,omitempty"`
	PendingSteer json.RawMessage `json:"pendingSteer,omitempty"`
	Request      string          `json:"-"`
}

func (r AgentRun) active() bool {
	switch r.Status {
	case "submitting", "queued", "running", "waiting_for_approval", "stopping":
		return true
	}
	return false
}

type AgentEvent struct {
	Seq       int64           `json:"seq"`
	ThreadID  string          `json:"threadId"`
	RunID     string          `json:"runId"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	CreatedAt string          `json:"createdAt"`
}

func (s *Store) run(id string) (AgentRun, error) {
	var raw, request string
	err := s.db.QueryRow(`SELECT snapshot,request FROM runs WHERE id=?`, id).Scan(&raw, &request)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRun{}, errNotFound
	}
	var r AgentRun
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(raw), &r)
	r.Request = request
	return r, err
}

func (s *Store) stopRequested(id string) bool {
	var count int
	_ = s.db.QueryRow(`SELECT count(*) FROM agent_events WHERE source_key=?`, "stop:"+id).Scan(&count)
	return count > 0
}

func (s *Store) runs(thread string, activeOnly bool) ([]AgentRun, error) {
	query := `SELECT snapshot,request FROM runs WHERE (?='' OR thread_id=?)`
	if activeOnly {
		query += ` AND status IN ('submitting','queued','running','waiting_for_approval','stopping')`
	}
	query += ` ORDER BY rowid DESC`
	if !activeOnly {
		query += ` LIMIT 100`
	}
	rows, err := s.db.Query(query, thread, thread)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentRun{}
	for rows.Next() {
		var r AgentRun
		var raw string
		if err = rows.Scan(&raw, &r.Request); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) reserveRun(r AgentRun) (AgentRun, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return r, false, err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRow(`SELECT snapshot FROM runs WHERE id=?`, r.ID).Scan(&raw)
	if err == nil {
		var existing AgentRun
		if err = json.Unmarshal([]byte(raw), &existing); err != nil {
			return r, false, err
		}
		a, _ := json.Marshal(existing.Skills)
		b, _ := json.Marshal(r.Skills)
		if existing.ThreadID != r.ThreadID || existing.Input != r.Input || string(a) != string(b) {
			return r, false, errRunConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return r, false, err
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM runs WHERE thread_id=? AND status IN ('submitting','queued','running','waiting_for_approval','stopping')`, r.ThreadID).Scan(&count); err != nil {
		return r, false, err
	}
	if count > 0 {
		return r, false, errRunBusy
	}
	data, _ := json.Marshal(r)
	if _, err = tx.Exec(`INSERT INTO runs VALUES (?,?,?,?,?)`, r.ID, r.ThreadID, r.Status, r.Request, string(data)); err != nil {
		return r, false, err
	}
	if _, err = tx.Exec(`INSERT INTO messages VALUES (?,?,?,?,?)`, r.ID+"-user", r.ThreadID, "user", r.Input, r.CreatedAt); err != nil {
		return r, false, err
	}
	if err = tx.Commit(); err != nil {
		return r, false, err
	}
	return r, true, nil
}

func (s *Store) saveRun(r AgentRun) error {
	r.UpdatedAt = nowUTC()
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE runs SET status=?,snapshot=? WHERE id=?`, r.Status, string(raw), r.ID); err != nil {
		return err
	}
	if !r.active() && r.Output != "" {
		_, err = tx.Exec(`INSERT INTO messages VALUES (?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET content=excluded.content`, r.ID+"-assistant", r.ThreadID, "assistant", r.Output, r.UpdatedAt)
		if err != nil {
			return err
		}
	}
	if r.SessionID != "" {
		if _, err = tx.Exec(`UPDATE thread_agents SET session_id=? WHERE thread_id=?`, r.SessionID, r.ThreadID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) addEvent(thread, run, kind, key string, data any) (AgentEvent, bool, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return AgentEvent{}, false, err
	}
	e := AgentEvent{ThreadID: thread, RunID: run, Kind: kind, Data: raw, CreatedAt: nowUTC()}
	var source any
	if key != "" {
		source = key
	}
	result, err := s.db.Exec(`INSERT OR IGNORE INTO agent_events(thread_id,run_id,kind,source_key,data,created_at) VALUES (?,?,?,?,?,?)`, thread, run, kind, source, string(raw), e.CreatedAt)
	if err != nil {
		return e, false, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return e, false, nil
	}
	e.Seq, err = result.LastInsertId()
	return e, true, err
}

func (s *Store) events(thread string, after int64) ([]AgentEvent, error) {
	rows, err := s.db.Query(`SELECT seq,thread_id,run_id,kind,data,created_at FROM agent_events WHERE thread_id=? AND seq>? ORDER BY seq LIMIT 200`, thread, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentEvent{}
	for rows.Next() {
		var e AgentEvent
		var data string
		if err = rows.Scan(&e.Seq, &e.ThreadID, &e.RunID, &e.Kind, &data, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) sessionID(thread string) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT session_id FROM thread_agents WHERE thread_id=?`, thread).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("conversation agent mapping missing: %w", errNotFound)
	}
	return id, err
}
