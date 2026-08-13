package contextbuild

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	pigoruntime "github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
)

func testProject(entries []session.V4Entry, leaf string, cfg *session.LaneConfig) *session.ProjectLeaf {
	lanes := []session.LaneState{{Lane: "main", Config: cfg}}
	if leaf != "" {
		lanes[0].LeafID = &leaf
	}
	proj, err := session.BuildProjection(entries, lanes, leaf, nil)
	if err != nil {
		panic(err)
	}
	return proj
}

func userMsg(text string) agentcore.Message {
	return agentcore.UserMessage{
		RoleField: agentcore.RoleUser,
		Content:   agentcore.ContentList{agentcore.NewTextContent(text)},
	}
}

func assistantMsg(text, stop string) agentcore.Message {
	return agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent(text)},
		StopReason: stop,
	}
}

func TestBuildSessionContextFiltersFailedAssistant(t *testing.T) {
	now := time.Now().UTC()
	entries := []session.V4Entry{
		{Type: session.EntryTypeMessage, ID: "1", Timestamp: now, Message: mustMessageJSON(userMsg("hi"))},
		{Type: session.EntryTypeMessage, ID: "2", ParentID: "1", Timestamp: now, Message: mustMessageJSON(assistantMsg("ok", agentcore.StopReasonEndTurn))},
		{Type: session.EntryTypeMessage, ID: "3", ParentID: "2", Timestamp: now, Message: mustMessageJSON(assistantMsg("boom", agentcore.StopReasonError))},
	}
	cfg := &session.LaneConfig{Model: "m", Provider: "p", ThinkingLevel: "medium"}
	sess, err := BuildSessionContext(testProject(entries, "3", cfg), SessionBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("messages = %d, want user + successful assistant", len(sess.Messages))
	}
	for _, m := range sess.Messages {
		if a, ok := m.(agentcore.AssistantMessage); ok && a.StopReason == agentcore.StopReasonError {
			t.Fatal("failed assistant leaked into projected messages")
		}
	}
}

func TestBuildSessionContextCustomProjectorAndSkip(t *testing.T) {
	now := time.Now().UTC()
	entries := []session.V4Entry{
		{Type: session.EntryTypeCustom, ID: "1", Timestamp: now, CustomType: "note", Content: "hello"},
		{Type: session.EntryTypeCustom, ID: "2", ParentID: "1", Timestamp: now, CustomType: "unknown"},
	}
	opts := SessionBuildOptions{EntryProjectors: map[string]EntryProjector{
		"note": func(entry session.V4Entry, _ int, _ []session.V4Entry) []agentcore.Message {
			return []agentcore.Message{agentcore.CustomMessage{
				RoleField:  agentcore.RoleCustom,
				CustomType: "note",
				Content:    agentcore.ContentList{agentcore.NewTextContent(entry.Content)},
			}}
		},
	}}
	cfg := &session.LaneConfig{Model: "m", Provider: "p"}
	sess, err := BuildSessionContext(testProject(entries, "2", cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Messages) != 1 {
		t.Fatalf("messages = %d, want only the registered custom type", len(sess.Messages))
	}
	cm, ok := sess.Messages[0].(agentcore.CustomMessage)
	if !ok || cm.CustomType != "note" || agentcore.ContentToText(cm.Content) != "hello" {
		t.Fatalf("custom message = %#v", sess.Messages[0])
	}
}

func TestConvertToLlmRolesAndBlockImages(t *testing.T) {
	msgs := agentcore.MessageList{
		agentcore.CompactionMessage{RoleField: agentcore.RoleCompaction, Summary: "s"},
		agentcore.BranchSummaryMessage{RoleField: agentcore.RoleBranchSummary, Summary: "b"},
		agentcore.CustomMessage{RoleField: agentcore.RoleCustom, CustomType: "note", Content: agentcore.ContentList{agentcore.NewTextContent("c")}},
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("u")}},
	}
	out := ConvertToLlmWithOptions(msgs, ConvertOptions{BlockImages: true})
	if len(out) != 4 {
		t.Fatalf("converted = %d, want 4", len(out))
	}
	for _, m := range out {
		if m.Role() != agentcore.RoleUser {
			t.Errorf("role = %q, want user", m.Role())
		}
	}
}

func TestRegistryOrderAndProjectorDuplicate(t *testing.T) {
	reg := NewRegistry()
	var order []string
	_ = reg.RegisterTransform("a", func(_ context.Context, msgs agentcore.MessageList) agentcore.MessageList {
		order = append(order, "a")
		return msgs
	})
	_ = reg.RegisterTransform("b", func(_ context.Context, msgs agentcore.MessageList) agentcore.MessageList {
		order = append(order, "b")
		return msgs
	})
	reg.ApplyTransforms(context.Background(), nil)
	if strings.Join(order, ",") != "a,b" {
		t.Fatalf("transform order = %v, want a,b", order)
	}
	if err := reg.RegisterEntryProjector("x", func(session.V4Entry, int, []session.V4Entry) []agentcore.Message { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterEntryProjector("x", func(session.V4Entry, int, []session.V4Entry) []agentcore.Message { return nil }); err == nil {
		t.Fatal("duplicate projector must error")
	}
}

func TestBuildSystemPromptLayerOrder(t *testing.T) {
	cfg := PromptBuildOptions{
		BaseInstruction:     "BASE",
		WorkingDir:          "E:/p",
		ContextFilesEnabled: true,
		AppendInstructions:  []string{"APPEND"},
		Skills: []*pigoruntime.Skill{{
			Frontmatter: pigoruntime.SkillFrontmatter{Name: "s1", Description: "d1"},
			Path:        "E:/skills/s1.md",
		}},
		Tools: []agentcore.AgentTool{newParityTool(parityTool{Name: "read", Description: "reads"})},
		ReadFile: func(path string) ([]byte, error) {
			if strings.Contains(path, "AGENTS") {
				return []byte("AGENTS CONTENT"), nil
			}
			return nil, os.ErrNotExist
		},
		IsWorktreeRoot: func(string) bool { return false },
	}
	got, err := BuildSystemPrompt(cfg)
	if err != nil {
		t.Fatal(err)
	}
	idxBase := strings.Index(got, "BASE")
	idxTools := strings.Index(got, "# Available tools")
	idxGuidelines := strings.Index(got, "# Guidelines")
	idxAppend := strings.Index(got, "APPEND")
	idxCtx := strings.Index(got, "<project_instructions")
	idxSkills := strings.Index(got, "<available_skills>")
	idxEnv := strings.Index(got, "Environment:")
	seq := []int{idxBase, idxTools, idxGuidelines, idxAppend, idxCtx, idxSkills, idxEnv}
	for i := 1; i < len(seq); i++ {
		if seq[i-1] < 0 || seq[i] < 0 || seq[i-1] >= seq[i] {
			t.Fatalf("section order broken at %d: %v", i, seq)
		}
	}
	if strings.Contains(got, "- Date:") {
		t.Fatal("system prompt must not contain a date (fingerprint stability)")
	}
}

func TestBuildProviderContextRemindersEphemeral(t *testing.T) {
	cfg := &session.LaneConfig{Model: "m", Provider: "p"}
	sess, err := BuildSessionContext(testProject(nil, "", cfg), SessionBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sess.Messages = agentcore.MessageList{userMsg("hi")}
	reg := pigoruntime.NewReminderRegistry(pigoruntime.ReminderFunc{
		NameField: "parity",
		Fn: func(context.Context, agentcore.MessageList) (string, bool) {
			return "background context", true
		},
	})
	deps := BuildDeps{Registry: NewRegistry(), Convert: ConvertToLlm, Reminders: reg}
	req := RequestOptions{BaseInstruction: "BASE", Cwd: "E:/p", AllTools: nil}
	pr, err := BuildProviderContext(context.Background(), sess, deps, req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range pr.LlmContext.Messages {
		text := ""
		switch v := m.(type) {
		case agentcore.UserMessage:
			text = agentcore.ContentToText(v.Content)
		case agentcore.AssistantMessage:
			text = agentcore.ContentToText(v.Content)
		case agentcore.ToolResultMessage:
			text = agentcore.ContentToText(v.Content)
		}
		if strings.Contains(text, "background context") {
			found = true
		}
	}
	if !found {
		t.Fatal("reminder not injected into request")
	}
	if len(sess.Messages) != 1 {
		t.Fatalf("sess.Messages grew to %d; reminders must be ephemeral", len(sess.Messages))
	}
}

func mustMessageJSON(m agentcore.Message) json.RawMessage {
	raw, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return raw
}
