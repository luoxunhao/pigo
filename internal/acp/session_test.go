package acp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smallnest/pigo/internal/sessionstore"
)

// TestSessionManagerPersistsBranchTree verifies that ACP runs append each turn
// as a branch below the previous leaf on the single project-scoped store, so
// the on-disk tree (and therefore /tree navigation) survives the ACP path.
func TestSessionManagerPersistsBranchTree(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewSessionManager(&fakeRunner{})
	sess, err := mgr.New(ws, "m", "sys", store)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Run(context.Background(), sess, "first", nil, nil, nil, TurnHooks{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	firstLeaf := sess.CurLeaf
	if firstLeaf == "" {
		t.Fatal("first run must record a leaf")
	}

	if _, err := mgr.Run(context.Background(), sess, "second", nil, nil, nil, TurnHooks{}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if sess.CurLeaf == firstLeaf {
		t.Fatal("second run must advance the leaf")
	}

	_, entries, err := store.TranscriptStore().LoadEntries(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("transcript has %d entries, want 4", len(entries))
	}
	if entries[2].ParentID != entries[1].ID {
		t.Errorf("second turn parent = %q, want first turn leaf %q", entries[2].ParentID, entries[1].ID)
	}
}

// TestRunReleasesTurnSlotWhenPersistenceFails verifies that a failed
// sessionstore write still releases the single-turn slot, so the next prompt
// is not queued forever behind a broken store.
func TestRunReleasesTurnSlotWhenPersistenceFails(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewSessionManager(&fakeRunner{})
	sess, err := mgr.New(ws, "m", "sys", store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(store.Dir()); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Run(context.Background(), sess, "first", nil, nil, nil, TurnHooks{}); err == nil {
		t.Fatal("expected persistence failure after removing sessions dir")
	}
	p := &queuedPrompt{text: "second", done: make(chan struct{})}
	if !sess.tryRun(p) {
		t.Fatal("turn slot not released after persistence failure")
	}
}
