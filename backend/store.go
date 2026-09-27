package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

var errNotFound = errors.New("not found")

type Thread struct {
	RuntimeID    string `json:"runtimeId"`
	ID           string `json:"id"`
	Title        string `json:"title"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	LastMessage  string `json:"lastMessage"`
	MessageCount int    `json:"messageCount"`
}

type Message struct {
	ID        string `json:"id"`
	ThreadID  string `json:"threadId"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
	Streaming bool   `json:"streaming,omitempty"`
}

type Store struct {
	db *sql.DB
}

func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		PRAGMA foreign_keys = ON;
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 5000;
		CREATE TABLE IF NOT EXISTS threads (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
			role TEXT NOT NULL CHECK(role IN ('user', 'assistant')),
			content TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_messages_thread_created
			ON messages(thread_id, created_at);
		CREATE TABLE IF NOT EXISTS thread_agents (
		 thread_id TEXT PRIMARY KEY REFERENCES threads(id) ON DELETE CASCADE,
		 runtime_id TEXT NOT NULL, session_id TEXT NOT NULL
		);
		INSERT OR IGNORE INTO thread_agents SELECT id, 'crash', '386gpt-' || id FROM threads;
		CREATE TABLE IF NOT EXISTS runs (
		 id TEXT PRIMARY KEY, thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
		 status TEXT NOT NULL, request TEXT NOT NULL, snapshot TEXT NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS runs_one_active ON runs(thread_id)
		 WHERE status IN ('submitting','queued','running','waiting_for_approval','stopping');
		CREATE TABLE IF NOT EXISTS agent_events (
		 seq INTEGER PRIMARY KEY AUTOINCREMENT,
		 thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
		 run_id TEXT NOT NULL, kind TEXT NOT NULL, source_key TEXT,
		 data TEXT NOT NULL, created_at TEXT NOT NULL,
		 UNIQUE(thread_id, source_key)
		);
		CREATE INDEX IF NOT EXISTS agent_events_thread ON agent_events(thread_id,seq);
	`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339Nano) }

const threadSelect = `
	SELECT t.id, t.title, t.created_at,
		COALESCE((SELECT MAX(m.created_at) FROM messages m WHERE m.thread_id = t.id), t.created_at),
		COALESCE((SELECT m.content FROM messages m WHERE m.thread_id = t.id ORDER BY m.created_at DESC LIMIT 1), ''),
		(SELECT COUNT(*) FROM messages m WHERE m.thread_id = t.id)
	, COALESCE((SELECT runtime_id FROM thread_agents WHERE thread_id=t.id), 'crash')
	FROM threads t`

func scanThread(scanner interface{ Scan(...any) error }) (Thread, error) {
	var thread Thread
	err := scanner.Scan(&thread.ID, &thread.Title, &thread.CreatedAt, &thread.UpdatedAt, &thread.LastMessage, &thread.MessageCount, &thread.RuntimeID)
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, errNotFound
	}
	return thread, err
}

func (s *Store) ListThreads() ([]Thread, error) {
	rows, err := s.db.Query(threadSelect + ` ORDER BY 4 DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	threads := make([]Thread, 0)
	for rows.Next() {
		thread, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		threads = append(threads, thread)
	}
	return threads, rows.Err()
}

func (s *Store) GetThread(id string) (Thread, error) {
	return scanThread(s.db.QueryRow(threadSelect+` WHERE t.id = ?`, id))
}

func (s *Store) CreateThread(title string) (Thread, error) {
	return s.CreateThreadRuntime(title, "crash")
}

func (s *Store) CreateThreadRuntime(title, runtime string) (Thread, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New conversation"
	}
	id, err := newID()
	if err != nil {
		return Thread{}, err
	}
	createdAt := nowUTC()
	tx, err := s.db.Begin()
	if err != nil {
		return Thread{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO threads (id, title, created_at) VALUES (?, ?, ?)`, id, title, createdAt); err != nil {
		return Thread{}, err
	}
	if _, err := tx.Exec(`INSERT INTO thread_agents VALUES (?,?,?)`, id, runtime, hermesSessionID(id)); err != nil {
		return Thread{}, err
	}
	if err := tx.Commit(); err != nil {
		return Thread{}, err
	}
	return s.GetThread(id)
}

func (s *Store) RenameThread(id, title string) (Thread, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Thread{}, errors.New("title is required")
	}
	result, err := s.db.Exec(`UPDATE threads SET title = ? WHERE id = ?`, title, id)
	if err != nil {
		return Thread{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return Thread{}, errNotFound
	}
	return s.GetThread(id)
}

func (s *Store) DeleteThread(id string) error {
	result, err := s.db.Exec(`DELETE FROM threads WHERE id = ? AND NOT EXISTS (SELECT 1 FROM runs WHERE thread_id=? AND status IN ('submitting','queued','running','waiting_for_approval','stopping'))`, id, id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		if _, e := s.GetThread(id); e == nil {
			return errors.New("stop the active run before deleting this conversation")
		}
		return errNotFound
	}
	return nil
}

func (s *Store) ListMessages(threadID string) ([]Message, error) {
	if _, err := s.GetThread(threadID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, thread_id, role, content, created_at FROM messages WHERE thread_id = ? ORDER BY created_at`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := make([]Message, 0)
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.ID, &message.ThreadID, &message.Role, &message.Content, &message.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) AddMessage(threadID, role, content string) (Message, error) {
	id, err := newID()
	if err != nil {
		return Message{}, err
	}
	message := Message{ID: id, ThreadID: threadID, Role: role, Content: content, CreatedAt: nowUTC()}
	return message, s.SaveMessage(message)
}

func (s *Store) SaveMessage(message Message) error {
	_, err := s.db.Exec(`INSERT INTO messages (id, thread_id, role, content, created_at) VALUES (?, ?, ?, ?, ?)`, message.ID, message.ThreadID, message.Role, message.Content, message.CreatedAt)
	return err
}

func titleFromMessage(content string) string {
	title := strings.Join(strings.Fields(content), " ")
	if utf8.RuneCountInString(title) <= 38 {
		return title
	}
	runes := []rune(title)
	return strings.TrimSpace(string(runes[:38])) + "…"
}
