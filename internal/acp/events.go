package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	mu            sync.Mutex
	sessions      map[string]*deltaTracker
	fileSnapshots map[string]fileToolSnapshot
	bashCalls     map[string]bool
	cwd           string
}

type fileToolSnapshot struct {
	Path    string
	OldText string
}

func newEventMapper(cwd string) *eventMapper {
	return &eventMapper{
		sessions:      make(map[string]*deltaTracker),
		fileSnapshots: make(map[string]fileToolSnapshot),
		bashCalls:     make(map[string]bool),
		cwd:           cwd,
	}
}

// SetCwd updates the workspace root used for resolving relative tool paths.
func (m *eventMapper) SetCwd(cwd string) {
	m.mu.Lock()
	m.cwd = cwd
	m.mu.Unlock()
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
		return []map[string]any{m.toolCallStart(e)}
	case agentcore.ToolExecutionEndEvent:
		return []map[string]any{m.toolCallEnd(e)}
	case agentcore.TelemetryEvent:
		if e.ContextWindow > 0 {
			return []map[string]any{usageUpdate(e.ContextTokens, e.ContextWindow)}
		}
	case agentcore.CompactionStartEvent:
		return []map[string]any{textChunkUpdate("Context nearing limit, running automatic compaction...")}
	case agentcore.CompactionEvent:
		if e.ErrorMessage != "" {
			return []map[string]any{textChunkUpdate("Automatic compaction failed: " + e.ErrorMessage)}
		}
		return []map[string]any{textChunkUpdate("Automatic compaction finished; context was summarized to continue the session.")}
	}
	return nil
}

func (m *eventMapper) toolCallStart(e agentcore.ToolExecutionStartEvent) map[string]any {
	cwd := m.cwdValue()
	if isBashTool(e.ToolName) {
		m.mu.Lock()
		m.bashCalls[e.ToolCallID] = true
		m.mu.Unlock()
		return bashToolCallStart(e.ToolCallID, e.ToolName, e.Args, cwd)
	}
	update := toolCallStart(e.ToolCallID, e.ToolName, e.Args)
	path, oldText, line := m.toolFileState(e.ToolCallID, e.ToolName, e.Args, cwd)
	if path != "" {
		loc := map[string]any{"path": path}
		if line > 0 {
			loc["line"] = line
		}
		update["locations"] = []map[string]any{loc}
		if oldText != "" || e.ToolName == "write" || e.ToolName == "edit" {
			m.mu.Lock()
			m.fileSnapshots[e.ToolCallID] = fileToolSnapshot{Path: path, OldText: oldText}
			m.mu.Unlock()
		}
	}
	return update
}

func (m *eventMapper) toolCallEnd(e agentcore.ToolExecutionEndEvent) map[string]any {
	m.mu.Lock()
	isBash := m.bashCalls[e.ToolCallID]
	snap, hasSnap := m.fileSnapshots[e.ToolCallID]
	delete(m.bashCalls, e.ToolCallID)
	delete(m.fileSnapshots, e.ToolCallID)
	m.mu.Unlock()

	if isBash {
		return bashToolCallEnd(e.ToolCallID, e.ToolName, e.IsError, e.Result, m.cwdValue())
	}
	if hasSnap && !e.IsError {
		newText, err := os.ReadFile(snap.Path)
		if err == nil && (snap.OldText == "" || string(newText) != snap.OldText) {
			return diffToolCallEnd(e.ToolCallID, e.ToolName, false, snap.Path, snap.OldText, string(newText))
		}
	}
	return toolCallEnd(e.ToolCallID, e.ToolName, e.IsError, toolResultText(e.Result))
}

// toolFileState resolves a file tool's path, captures the pre-mutation text for
// edit/write diffs, and infers a 1-based line for edit when the oldText match
// is unique.
func (m *eventMapper) toolFileState(toolCallID, toolName string, rawArgs any, cwd string) (path, oldText string, line int) {
	args, ok := rawArgs.(map[string]any)
	if !ok {
		if b, err := json.Marshal(rawArgs); err == nil {
			_ = json.Unmarshal(b, &args)
		}
	}
	rawPath := argString(args, "path")
	if rawPath == "" {
		return "", "", 0
	}
	path = rawPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	if toolName != "edit" && toolName != "write" {
		return path, "", 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return path, "", 0
	}
	oldText = string(data)
	if toolName == "edit" {
		needle := argString(args, "old_string")
		line = findUniqueLineNumber(oldText, needle)
	}
	return path, oldText, line
}

func (m *eventMapper) cwdValue() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cwd
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func findUniqueLineNumber(content, needle string) int {
	if needle == "" || content == "" {
		return 0
	}
	count := 0
	line := 0
	for i, l := range strings.Split(content, "\n") {
		if strings.Contains(l, needle) {
			count++
			line = i + 1
		}
	}
	if count == 1 {
		return line
	}
	return 0
}

func isBashTool(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "bash")
}

func bashToolCallStart(id, name string, rawInput any, cwd string) map[string]any {
	return map[string]any{
		"sessionUpdate": "tool_call",
		"toolCallId":    id,
		"title":         name,
		"kind":          "execute",
		"status":        "in_progress",
		"rawInput":      rawInput,
		"content":       []map[string]any{{"type": "terminal", "terminalId": id}},
		"_meta": map[string]any{
			"terminal_info": map[string]any{"terminal_id": id, "cwd": cwd},
		},
	}
}

func bashToolCallEnd(id, name string, failed bool, result agentcore.AgentToolResult, cwd string) map[string]any {
	status := "completed"
	if failed {
		status = "failed"
	}
	return map[string]any{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    id,
		"title":         name,
		"kind":          "execute",
		"status":        status,
		"content":       []map[string]any{{"type": "terminal", "terminalId": id}},
		"_meta": map[string]any{
			"terminal_output": map[string]any{"terminal_id": id, "data": toolResultText(result)},
			"terminal_exit":   map[string]any{"terminal_id": id, "exit_code": bashExitCode(result, failed), "signal": nil},
		},
	}
}

func bashExitCode(result agentcore.AgentToolResult, failed bool) int {
	if result.Details != nil {
		b, err := json.Marshal(result.Details)
		if err == nil {
			var d struct {
				ExitCode *int `json:"exitCode"`
				Code     *int `json:"code"`
			}
			if json.Unmarshal(b, &d) == nil {
				if d.ExitCode != nil {
					return *d.ExitCode
				}
				if d.Code != nil {
					return *d.Code
				}
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

func diffToolCallEnd(id, name string, failed bool, path, oldText, newText string) map[string]any {
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
		"content": []map[string]any{
			{"type": "diff", "path": path, "oldText": oldText, "newText": newText},
		},
	}
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
