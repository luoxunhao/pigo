package sessionstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/compaction"
	"github.com/smallnest/pigo/internal/session"
)

func TestCompactionPersistsAndReopens(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "compact-sess", CreatedAt: now, UpdatedAt: now, Model: "m", Provider: "p", Cwd: ws}
	cfg := &session.LaneConfig{Model: "m", Provider: "p", ThinkingLevel: "medium"}
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("q1")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("a1")}},
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("q2")}},
	}
	if err := st.CreateWithLaneConfig(NewMetadata(header.ID, "S", "pigo", "m", ws), header, msgs, cfg); err != nil {
		t.Fatal(err)
	}
	res := &compaction.CompactionResult{
		Summary:      "compacted",
		RetainedTail: msgs[2:],
		TokensBefore: 100,
		Details:      compaction.CompactionDetails{},
	}
	leaf, err := st.AppendCompaction(header.ID, header, res)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	proj, err := st2.Projection(header.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if proj.LeafID != leaf {
		t.Fatalf("leaf after reopen = %q, want %q", proj.LeafID, leaf)
	}
	if len(proj.Messages) != 2 {
		t.Fatalf("projection messages = %d, want 2 (compaction + retained tail)", len(proj.Messages))
	}
	if _, ok := proj.Messages[0].(agentcore.CompactionMessage); !ok {
		t.Fatalf("first projected message = %T, want CompactionMessage", proj.Messages[0])
	}
}

func TestImportV4RejectsLegacyVersion(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(home, "legacy.jsonl")
	content := "{\"type\":\"session\",\"version\":1,\"id\":\"old\"}\n{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"hi\"}]}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := st.ImportV4(path, time.Now().UTC()); err == nil {
		t.Fatal("ImportV4 must reject v1 input")
	}
}

func TestLaneMovePersistsAcrossStoreOpen(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "lane-sess", CreatedAt: now, UpdatedAt: now, Model: "m", Provider: "p", Cwd: ws}
	cfg := &session.LaneConfig{Model: "m", Provider: "p", ThinkingLevel: "medium"}
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("q1")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("a1")}},
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("q2")}},
	}
	if err := st.CreateWithLaneConfig(NewMetadata(header.ID, "S", "pigo", "m", ws), header, msgs, cfg); err != nil {
		t.Fatal(err)
	}
	entries, err := st.Entries(header.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := entries[0].ID
	if err := st.MoveLane(header.ID, "main", &target); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	proj, err := st2.Projection(header.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if proj.LeafID != target {
		t.Fatalf("reopened leaf = %q, want %q", proj.LeafID, target)
	}
	if len(proj.Messages) != 1 {
		t.Fatalf("projection messages = %d, want 1", len(proj.Messages))
	}
}

func TestQuarantineScriptsExist(t *testing.T) {
	for _, name := range []string{
		filepath.Join("..", "..", "scripts", "quarantine-legacy-sessions.ps1"),
		filepath.Join("..", "..", "scripts", "quarantine-legacy-sessions.sh"),
	} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}
