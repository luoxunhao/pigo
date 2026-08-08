package acp

import (
	"testing"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
)

func textPartial(text string) agentcore.AssistantMessage {
	return agentcore.AssistantMessage{
		RoleField: agentcore.RoleAssistant,
		Content:   agentcore.ContentList{agentcore.NewTextContent(text)},
	}
}

func TestMapTextDeltas(t *testing.T) {
	m := newEventMapper("")
	ev1 := agentcore.MessageUpdateEvent{
		Message:               textPartial("hel"),
		AssistantMessageEvent: provider.StreamTextEvent{Partial: textPartial("hel")},
	}
	ev2 := agentcore.MessageUpdateEvent{
		Message:               textPartial("hello"),
		AssistantMessageEvent: provider.StreamTextEvent{Partial: textPartial("hello")},
	}
	updates := m.Map("s1", ev1)
	if len(updates) != 1 || updates[0]["sessionUpdate"] != "agent_message_chunk" {
		t.Fatalf("first update = %+v", updates)
	}
	content := updates[0]["content"].(map[string]any)
	if content["text"] != "hel" {
		t.Fatalf("first delta = %v", content["text"])
	}
	updates = m.Map("s1", ev2)
	content = updates[0]["content"].(map[string]any)
	if content["text"] != "lo" {
		t.Fatalf("second delta = %v, want lo", content["text"])
	}
}

func TestMapThoughtChunk(t *testing.T) {
	m := newEventMapper("")
	partial := agentcore.AssistantMessage{
		RoleField: agentcore.RoleAssistant,
		Content:   agentcore.ContentList{agentcore.NewThinkingContent("reasoning")},
	}
	ev := agentcore.MessageUpdateEvent{
		Message:               partial,
		AssistantMessageEvent: provider.StreamThinkingEvent{Partial: partial},
	}
	updates := m.Map("s1", ev)
	if len(updates) != 1 || updates[0]["sessionUpdate"] != "agent_thought_chunk" {
		t.Fatalf("updates = %+v", updates)
	}
}

func TestMapToolStartEnd(t *testing.T) {
	m := newEventMapper("")
	start := m.Map("s1", agentcore.ToolExecutionStartEvent{
		ToolCallID: "call-1",
		ToolName:   "bash",
		Args:       map[string]any{"command": "echo hi"},
	})
	if len(start) != 1 {
		t.Fatalf("start updates = %+v", start)
	}
	s := start[0]
	if s["sessionUpdate"] != "tool_call" || s["kind"] != "execute" || s["status"] != "in_progress" {
		t.Fatalf("tool start shape = %+v", s)
	}
	if s["title"] != "echo hi" {
		t.Fatalf("tool start title = %v, want echo hi", s["title"])
	}
	startMeta := s["_meta"].(map[string]any)
	info := startMeta["terminal_info"].(map[string]any)
	if info["command"] != "echo hi" {
		t.Fatalf("terminal info command = %v", info["command"])
	}
	end := m.Map("s1", agentcore.ToolExecutionEndEvent{
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result:     agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("hi")}},
	})
	e := end[0]
	if e["sessionUpdate"] != "tool_call_update" || e["status"] != "completed" {
		t.Fatalf("tool end shape = %+v", e)
	}
	if e["title"] != "echo hi" {
		t.Fatalf("tool end title = %v, want echo hi", e["title"])
	}
	if e["rawInput"] == nil {
		t.Fatalf("tool end rawInput missing: %+v", e)
	}
	meta := e["_meta"].(map[string]any)
	out := meta["terminal_output"].(map[string]any)
	if out["data"] != "> echo hi\nhi" {
		t.Fatalf("terminal output = %v, want hi", out["data"])
	}
}

func TestMapToolPending(t *testing.T) {
	m := newEventMapper("")
	updates := m.Map("s1", agentcore.ToolExecutionPendingEvent{
		ToolCallID: "call-1",
		ToolName:   "bash",
		Args:       map[string]any{"command": "echo hi"},
	})
	if len(updates) != 1 {
		t.Fatalf("pending updates = %+v", updates)
	}
	u := updates[0]
	if u["sessionUpdate"] != "tool_call" || u["status"] != "pending" || u["kind"] != "execute" {
		t.Fatalf("pending shape = %+v", u)
	}
	if u["rawInput"] == nil {
		t.Fatalf("pending rawInput missing: %+v", u)
	}
	if u["title"] != "echo hi" {
		t.Fatalf("pending bash title = %v, want echo hi", u["title"])
	}
}

func TestMapToolEndCarriesRawInputAfterPending(t *testing.T) {
	m := newEventMapper("")
	m.Map("s1", agentcore.ToolExecutionPendingEvent{
		ToolCallID: "call-1",
		ToolName:   "bash",
		Args:       map[string]any{"command": "echo hi"},
	})
	end := m.Map("s1", agentcore.ToolExecutionEndEvent{
		ToolCallID: "call-1",
		ToolName:   "bash",
		IsError:    true,
		Result:     agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("blocked")}},
	})
	if len(end) != 1 {
		t.Fatalf("end updates = %+v", end)
	}
	u := end[0]
	if u["status"] != "failed" || u["rawInput"] == nil {
		t.Fatalf("failed end shape = %+v", u)
	}
}

func TestMapUsage(t *testing.T) {
	m := newEventMapper("")
	updates := m.Map("s1", agentcore.TelemetryEvent{ContextTokens: 10, ContextWindow: 1000})
	if len(updates) != 1 {
		t.Fatalf("updates = %+v", updates)
	}
	u := updates[0]
	if u["sessionUpdate"] != "usage_update" || u["used"] != 10 || u["size"] != 1000 {
		t.Fatalf("usage shape = %+v", u)
	}
}

func TestMapIgnoresUnmappedEvents(t *testing.T) {
	m := newEventMapper("")
	if updates := m.Map("s1", agentcore.AgentStartEvent{SessionID: "s1"}); len(updates) != 0 {
		t.Fatalf("unmapped event produced updates: %+v", updates)
	}
}
