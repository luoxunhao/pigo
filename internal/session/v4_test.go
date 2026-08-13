package session

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
)

func TestV4JSONLRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	leaf := "00000002"
	header := V4Header{
		Type: "session", Version: 4, ID: "sess-1", CreatedAt: now, UpdatedAt: now,
		Cwd: "E:/project/pigo", Model: "deepseek/deepseek-v4-pro", Provider: "deepseek",
		LeafID: &leaf,
	}
	u := agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hi")}, Timestamp: now.UnixMilli()}
	a := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("hello")}, Timestamp: now.UnixMilli()}
	e1, err := NewV4Entry("00000001", "", now, u)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := NewV4Entry("00000002", "00000001", now, a)
	if err != nil {
		t.Fatal(err)
	}
	compaction := V4Entry{
		Type: EntryTypeCompaction, ID: "00000003", ParentID: "00000002", Timestamp: now,
		Summary: "summary", TokensBefore: 100,
		RetainedTail: []json.RawMessage{[]byte(`{"role":"user","content":[{"type":"text","text":"kept"}]}`)},
	}
	facts := []V4Fact{{Type: "fact", Kind: "label", Key: "00000001", Value: "start"}}
	var buf bytes.Buffer
	if err := WriteV4JSONL(&buf, header, []V4Entry{e1, e2, compaction}, facts); err != nil {
		t.Fatal(err)
	}
	gotHeader, entries, gotFacts, err := ReadV4JSONL(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if gotHeader.ID != header.ID || gotHeader.LeafID == nil || *gotHeader.LeafID != leaf {
		t.Fatalf("header = %+v", gotHeader)
	}
	if len(entries) != 3 || len(gotFacts) != 1 {
		t.Fatalf("entries=%d facts=%d", len(entries), len(gotFacts))
	}
	if entries[2].Type != EntryTypeCompaction || entries[2].Summary != "summary" {
		t.Fatalf("compaction = %+v", entries[2])
	}
}

func TestReadV4RejectsLegacyVersion(t *testing.T) {
	header := `{"type":"session","version":3,"id":"old","createdAt":"2026-08-12T10:00:00Z","cwd":"E:/project"}`
	_, _, _, err := ReadV4JSONL(strings.NewReader(header + "\n"))
	if err == nil || !strings.Contains(err.Error(), "requires version 4") {
		t.Fatalf("err = %v, want v4 rejection", err)
	}
}

func TestBuildProjectionUsesRetainedTail(t *testing.T) {
	now := time.Now().UTC()
	old := V4Entry{Type: EntryTypeMessage, ID: "1", Timestamp: now, Message: []byte(`{"role":"user","content":[{"type":"text","text":"old"}]}`)}
	comp := V4Entry{
		Type: EntryTypeCompaction, ID: "2", ParentID: "1", Timestamp: now, Summary: "sum",
		RetainedTail: []json.RawMessage{[]byte(`{"role":"user","content":[{"type":"text","text":"kept"}]}`)},
	}
	recent := V4Entry{Type: EntryTypeMessage, ID: "3", ParentID: "2", Timestamp: now, Message: []byte(`{"role":"assistant","content":[{"type":"text","text":"new"}]}`)}
	cfg := &LaneConfig{Model: "m", Provider: "p", ThinkingLevel: "medium"}
	proj, err := BuildProjection([]V4Entry{old, comp, recent}, []LaneState{{Lane: "main", LeafID: strPtr("3"), Config: cfg}}, "3", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(proj.Messages) != 3 {
		t.Fatalf("messages = %d, want compaction + retained tail + recent", len(proj.Messages))
	}
	if _, ok := proj.Messages[0].(agentcore.CompactionMessage); !ok {
		t.Fatalf("first message = %T, want compaction", proj.Messages[0])
	}
}

func strPtr(s string) *string { return &s }
