package sessionstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/session"
)

func TestWorkspaceSlugStableAndDeterministic(t *testing.T) {
	a := WorkspaceSlug(`E:\Project\Pigo`)
	b := WorkspaceSlug(`E:\Project\Pigo`)
	if a != b {
		t.Fatalf("slug not deterministic: %q vs %q", a, b)
	}
	if a == "" || strings.ContainsAny(a, `/\:`) {
		t.Fatalf("slug contains unsafe chars: %q", a)
	}
	if strings.ToLower(a) != a {
		t.Fatalf("slug not lowercased: %q", a)
	}
}

func TestWorkspaceSlugEmptyFallsBack(t *testing.T) {
	if got := WorkspaceSlug(""); got != "workspace" {
		t.Fatalf("empty slug = %q, want workspace", got)
	}
}

func TestWorkspaceSlugLongPathUsesSuffix(t *testing.T) {
	long := filepath.Join(strings.Repeat("very-long-directory-name-", 30), "repo")
	slug := WorkspaceSlug(long)
	if len(slug) > maxSlugLen {
		t.Fatalf("slug too long: %d", len(slug))
	}
	if !strings.Contains(slug, "-") {
		t.Fatalf("long slug should carry a suffix separator: %q", slug)
	}
	if WorkspaceSlug(long) != slug {
		t.Fatalf("long slug not deterministic")
	}
}

func TestCreateLoadListAppendDelete(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}

	meta := NewMetadata("sess-1", "Fix bug", "agentic", "openrouter/free", ws)
	header := session.SessionHeader{
		ID:           "sess-1",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
		Model:        "openrouter/free",
		Provider:     "openrouter",
		SystemPrompt: "you are pigo",
		Cwd:          ws,
	}
	messages := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hi")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("hello")}},
	}
	if err := store.Create(meta, header, messages); err != nil {
		t.Fatal(err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].SessionID != "sess-1" {
		t.Fatalf("list = %+v, want one sess-1", list)
	}
	if list[0].MessageCount != 0 {
		t.Fatalf("fresh session message count = %d, want 0", list[0].MessageCount)
	}

	loadedMeta, loadedHeader, loadedMsgs, err := store.Load("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if loadedMeta.WorkspacePath != ws || loadedMeta.WorkspaceHost != DefaultWorkspaceHost {
		t.Fatalf("metadata workspace mismatch: %+v", loadedMeta)
	}
	if loadedHeader.ID != "sess-1" || loadedHeader.SystemPrompt != "you are pigo" {
		t.Fatalf("header mismatch: %+v", loadedHeader)
	}
	if len(loadedMsgs) != 2 {
		t.Fatalf("loaded messages = %d, want 2", len(loadedMsgs))
	}

	tail := agentcore.MessageList{
		agentcore.ToolResultMessage{RoleField: agentcore.RoleToolResult, ToolCallID: "call-1", Content: agentcore.ContentList{agentcore.NewTextContent("ok")}},
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("again")}},
	}
	now := time.Now().UTC()
	if err := store.Append("sess-1", now, tail); err != nil {
		t.Fatal(err)
	}
	after, _, _, err := store.Load("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if after.MessageCount != 2 || after.TurnCount != 1 || after.ToolCallCount != 1 {
		t.Fatalf("counts after append = %+v", after)
	}

	if err := store.Delete("sess-1"); err != nil {
		t.Fatal(err)
	}
	list, err = store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("list after delete = %+v, want empty", list)
	}
	if _, err := store.LoadMetadata("sess-1"); !os.IsNotExist(err) {
		t.Fatalf("metadata still readable after delete: %v", err)
	}
}

func TestIndexRebuildAfterStaleEntry(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	header := session.SessionHeader{ID: "a", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.Create(NewMetadata("a", "A", "agentic", "m", ws), header, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(NewMetadata("b", "B", "agentic", "m", ws), session.SessionHeader{ID: "b", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.metadataPath("a")); err != nil {
		t.Fatal(err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].SessionID != "b" {
		t.Fatalf("rebuild list = %+v, want only b", list)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	header := session.SessionHeader{ID: "x", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.Create(NewMetadata("x", "X", "agentic", "m", ws), header, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(NewMetadata("x", "X2", "agentic", "m", ws), header, nil); err == nil {
		t.Fatal("duplicate create should fail")
	}
}

func TestSeparateProjectsIsolateSessions(t *testing.T) {
	home := t.TempDir()
	wsA := filepath.Join(home, "a")
	wsB := filepath.Join(home, "b")
	for _, ws := range []string{wsA, wsB} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	storeA, err := OpenForWorkspace(home, wsA)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := OpenForWorkspace(home, wsB)
	if err != nil {
		t.Fatal(err)
	}
	headerA := session.SessionHeader{ID: "s", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := storeA.Create(NewMetadata("s", "S", "agentic", "m", wsA), headerA, nil); err != nil {
		t.Fatal(err)
	}
	lb, err := storeB.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(lb) != 0 {
		t.Fatalf("project B leaked sessions: %+v", lb)
	}
}

func TestAppendBranchUpdatesMetadataAndPreservesTree(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	meta := NewMetadata("sess-branch", "Branch", "pigo", "m", ws)
	header := session.SessionHeader{ID: "sess-branch", CreatedAt: now, UpdatedAt: now, Model: "m", Provider: "p", Cwd: ws}
	first := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("q1")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("a1")}},
	}
	if err := store.Create(meta, header, first); err != nil {
		t.Fatal(err)
	}
	_, entries, err := store.TranscriptStore().LoadEntries("sess-branch")
	if err != nil {
		t.Fatal(err)
	}
	leaf := entries[len(entries)-1].ID

	header.UpdatedAt = now.Add(time.Hour)
	second := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("q2")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("a2")}},
	}
	newLeaf, err := store.AppendBranch("sess-branch", header, leaf, second)
	if err != nil {
		t.Fatal(err)
	}
	if newLeaf == "" || newLeaf == leaf {
		t.Fatalf("AppendBranch returned leaf %q, want a new id", newLeaf)
	}
	meta2, err := store.LoadMetadata("sess-branch")
	if err != nil {
		t.Fatal(err)
	}
	// Create seeds zero counts; AppendBranch counts only the appended batch, so
	// the metadata reflects the branch write (2 messages, 1 user turn).
	if meta2.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", meta2.MessageCount)
	}
	if meta2.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", meta2.TurnCount)
	}
	if !meta2.LastActiveAt.Equal(header.UpdatedAt) {
		t.Errorf("LastActiveAt = %v, want %v", meta2.LastActiveAt, header.UpdatedAt)
	}
	// The tree survives: the second chain descends from the first leaf, so both
	// branches are still present in the single transcript file.
	_, all, err := store.TranscriptStore().LoadEntries("sess-branch")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("transcript has %d entries, want 4", len(all))
	}
	if all[2].ParentID != leaf {
		t.Errorf("branch entry parent = %q, want %q", all[2].ParentID, leaf)
	}
}

func TestImportEntriesRoundTripsTree(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}

	src, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "legacy-sess", CreatedAt: now, UpdatedAt: now, Model: "m", Provider: "p", Cwd: ws}
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("q")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("a")}},
	}
	if err := src.Save(header, msgs); err != nil {
		t.Fatal(err)
	}
	_, entries, err := src.LoadEntries(header.ID)
	if err != nil {
		t.Fatal(err)
	}

	meta := NewMetadata(header.ID, "Imported", "pigo", header.Model, ws)
	meta.CreatedAt = header.CreatedAt
	meta.LastActiveAt = header.UpdatedAt
	meta.MessageCount = len(entries)
	if err := store.ImportEntries(meta, header, entries); err != nil {
		t.Fatal(err)
	}

	_, h2, msgs2, err := store.Load(header.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h2.ID != header.ID || len(msgs2) != 2 {
		t.Fatalf("imported session mismatch: id=%q msgs=%d", h2.ID, len(msgs2))
	}
	_, imported, err := store.TranscriptStore().LoadEntries(header.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range imported {
		if e.ID != entries[i].ID || e.ParentID != entries[i].ParentID {
			t.Errorf("entry[%d] tree metadata not preserved: got (%q,%q) want (%q,%q)",
				i, e.ID, e.ParentID, entries[i].ID, entries[i].ParentID)
		}
	}
	if err := store.ImportEntries(meta, header, entries); err == nil {
		t.Fatal("ImportEntries must reject an existing session id")
	}
}

func TestListAllScansProjects(t *testing.T) {
	home := t.TempDir()
	wsA := filepath.Join(home, "project-a")
	wsB := filepath.Join(home, "project-b")
	if err := os.MkdirAll(wsA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wsB, 0o755); err != nil {
		t.Fatal(err)
	}
	storeA, err := OpenForWorkspace(home, wsA)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := OpenForWorkspace(home, wsB)
	if err != nil {
		t.Fatal(err)
	}
	older := time.Now().UTC().Add(-2 * time.Hour)
	newer := time.Now().UTC()
	metaA := NewMetadata("sess-a", "A", "pigo", "m", wsA)
	metaA.CreatedAt = older
	metaA.LastActiveAt = older
	metaB := NewMetadata("sess-b", "B", "pigo", "m", wsB)
	metaB.CreatedAt = newer
	metaB.LastActiveAt = newer
	headerA := session.SessionHeader{ID: "sess-a", CreatedAt: older, UpdatedAt: older, Cwd: wsA}
	headerB := session.SessionHeader{ID: "sess-b", CreatedAt: newer, UpdatedAt: newer, Cwd: wsB}
	if err := storeA.Create(metaA, headerA, nil); err != nil {
		t.Fatal(err)
	}
	if err := storeB.Create(metaB, headerB, nil); err != nil {
		t.Fatal(err)
	}

	all, err := ListAll(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll returned %d sessions, want 2", len(all))
	}
	if all[0].SessionID != "sess-b" || all[1].SessionID != "sess-a" {
		t.Errorf("ListAll order = [%s, %s], want [sess-b, sess-a]", all[0].SessionID, all[1].SessionID)
	}

	empty, err := ListAll(filepath.Join(home, "empty"))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListAll on empty home returned %d sessions, want 0", len(empty))
	}
}
