package acp

import (
	"strings"
	"sync"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
)

// eventMapper translates pigo agentcore events into standard ACP
// session/update payloads. Text and thinking deltas are computed against the
// last observed full text so the client receives increments, not cumulative
// snapshots (pigo providers emit cumulative partials).
type eventMapper struct {
	mu       sync.Mutex
	sessions map[string]*deltaTracker
}

func newEventMapper() *eventMapper {
	return &eventMapper{sessions: make(map[string]*deltaTracker)}
}

type deltaTracker struct {
	text    string
	thought string
}

// Map converts one agent event into zero or more session/update payloads.
func (m *eventMapper) Map(sessionID string, ev agentcore.AgentEvent) []map[string]any {
	switch e := ev.(type) {
	case agentcore.MessageUpdateEvent:
		switch se := e.AssistantMessageEvent.(type) {
		case provider.StreamTextEvent:
			full := agentcore.ContentToText(se.Partial.Content)
			delta := m.tracker(sessionID).textDelta(full)
			if delta == "" {
				return nil
			}
			return []map[string]any{textChunkUpdate(delta)}
		case provider.StreamThinkingEvent:
			full := thinkingText(se.Partial.Content)
			delta := m.tracker(sessionID).thoughtDelta(full)
			if delta == "" {
				return nil
			}
			return []map[string]any{thoughtChunkUpdate(delta)}
		}
	case agentcore.ToolExecutionStartEvent:
		return []map[string]any{toolCallStart(e.ToolCallID, e.ToolName, e.Args)}
	case agentcore.ToolExecutionEndEvent:
		return []map[string]any{toolCallEnd(e.ToolCallID, e.ToolName, e.IsError, toolResultText(e.Result))}
	case agentcore.TelemetryEvent:
		if e.ContextWindow > 0 {
			return []map[string]any{usageUpdate(e.ContextTokens, e.ContextWindow)}
		}
	}
	return nil
}

func (m *eventMapper) tracker(sessionID string) *deltaTracker {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.sessions[sessionID]
	if !ok {
		t = &deltaTracker{}
		m.sessions[sessionID] = t
	}
	return t
}

func (t *deltaTracker) textDelta(full string) string {
	if strings.HasPrefix(full, t.text) {
		delta := full[len(t.text):]
		t.text = full
		return delta
	}
	t.text = full
	return full
}

func (t *deltaTracker) thoughtDelta(full string) string {
	if strings.HasPrefix(full, t.thought) {
		delta := full[len(t.thought):]
		t.thought = full
		return delta
	}
	t.thought = full
	return full
}

func thinkingText(content agentcore.ContentList) string {
	var b strings.Builder
	for _, c := range content {
		if tc, ok := c.(agentcore.ThinkingContent); ok {
			b.WriteString(tc.Thinking)
		}
	}
	return b.String()
}

func toolResultText(result agentcore.AgentToolResult) string {
	return agentcore.ContentToText(result.Content)
}

// nestedText extracts the text from an ACP content chunk update payload.
func nestedText(u map[string]any) string {
	content, _ := u["content"].(map[string]any)
	text, _ := content["text"].(string)
	return text
}

// sessionUpdatePayload wraps an update object in the session/update
// notification shape expected by ACP clients.
func sessionUpdatePayload(sessionID string, update map[string]any) map[string]any {
	return map[string]any{"sessionId": sessionID, "update": update}
}

func textChunkUpdate(text string) map[string]any {
	return map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": text},
	}
}

func thoughtChunkUpdate(text string) map[string]any {
	return map[string]any{
		"sessionUpdate": "agent_thought_chunk",
		"content":       map[string]any{"type": "text", "text": text},
	}
}

func toolCallStart(id, name string, rawInput any) map[string]any {
	return map[string]any{
		"sessionUpdate": "tool_call",
		"toolCallId":    id,
		"title":         name,
		"kind":          inferToolKind(name),
		"status":        "in_progress",
		"rawInput":      rawInput,
	}
}

func toolCallEnd(id, name string, failed bool, rawOutput string) map[string]any {
	status := "completed"
	if failed {
		status = "failed"
	}
	return map[string]any{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    id,
		"title":         name,
		"kind":          inferToolKind(name),
		"status":        status,
		"rawOutput":     rawOutput,
	}
}

func usageUpdate(used, size int) map[string]any {
	return map[string]any{
		"sessionUpdate": "usage_update",
		"used":          used,
		"size":          size,
	}
}

func inferToolKind(name string) string {
	switch name {
	case "read":
		return "read"
	case "write", "edit":
		return "edit"
	case "bash", "bash_output", "bash_kill":
		return "execute"
	case "grep", "find":
		return "search"
	case "webfetch", "websearch":
		return "fetch"
	case "todo":
		return "think"
	default:
		return "other"
	}
}
