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
