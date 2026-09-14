package main

import (
	"path/filepath"
	"testing"
)

func TestStoreThreadLifecycle(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	thread, err := store.CreateThread("")
	if err != nil {
		t.Fatal(err)
	}
	if thread.Title != "New conversation" {
		t.Fatalf("unexpected title %q", thread.Title)
	}

	message, err := store.AddMessage(thread.ID, "user", "hello from the test")
	if err != nil {
		t.Fatal(err)
	}
	if message.ThreadID != thread.ID {
		t.Fatal("message belongs to the wrong thread")
	}

	messages, err := store.ListMessages(thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "hello from the test" {
		t.Fatalf("unexpected messages: %#v", messages)
	}

	if err := store.DeleteThread(thread.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListMessages(thread.ID); err != errNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestTitleFromMessage(t *testing.T) {
	got := titleFromMessage("  a   useful conversation title  ")
	if got != "a useful conversation title" {
		t.Fatalf("unexpected title %q", got)
	}
}
