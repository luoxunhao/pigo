package acp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/sessionstore"
)

func saveMCPServers(sess *AcpSession, raw json.RawMessage) error {
	meta, err := sess.Store.LoadMetadata(sess.ID)
	if err != nil {
		return err
	}
	meta.CustomMetadata = rawJSONValue(map[string]any{"mcpServers": raw})
	return sess.Store.SaveMetadata(meta)
}

func persistSessionThinking(sess *AcpSession) error {
	meta, err := sess.Store.LoadMetadata(sess.ID)
	if err != nil {
		return err
	}
	raw := map[string]any{}
	if len(meta.CustomMetadata) > 0 {
		_ = json.Unmarshal(meta.CustomMetadata, &raw)
	}
	if sess.Thinking == "" {
		delete(raw, "thinkingLevel")
	} else {
		raw["thinkingLevel"] = sess.Thinking
	}
	meta.CustomMetadata = rawJSONValue(raw)
	return sess.Store.SaveMetadata(meta)
}

func readSessionThinking(meta sessionstore.Metadata) string {
	raw := map[string]any{}
	if len(meta.CustomMetadata) > 0 {
		_ = json.Unmarshal(meta.CustomMetadata, &raw)
	}
	if s, ok := raw["thinkingLevel"].(string); ok {
		return s
	}
	return ""
}

func (d *Dispatcher) sessionPayload(sess *AcpSession) map[string]any {
	ctx := context.Background()
	configured := d.configuredModelList()
	return map[string]any{
		"sessionId":     sess.ID,
		"configOptions": sessionConfigOptions(ctx, sess, configured),
		"models":        sessionModels(ctx, sess, configured),
		"modes":         sessionModes(sess, d.currentConfiguredModel(sess)),
		"_meta": map[string]any{
			"pigo.startupInfo": startupInfoText(d.version, sess, d.commandCount()),
		},
	}
}

func sessionInfos(metas []sessionstore.Metadata) []map[string]any {
	out := make([]map[string]any, 0, len(metas))
	for _, m := range metas {
		title := m.SessionName
		if title == "" {
			title = "Session"
		}
		out = append(out, map[string]any{
			"sessionId": m.SessionID,
			"cwd":       m.WorkspacePath,
			"title":     title,
			"updatedAt": m.LastActiveAt.Format(time.RFC3339),
		})
	}
	return out
}

func validThinkingLevel(s string) bool {
	switch agentcore.ThinkingLevel(s) {
	case agentcore.ThinkingOff, agentcore.ThinkingMinimal, agentcore.ThinkingLow,
		agentcore.ThinkingMedium, agentcore.ThinkingHigh, agentcore.ThinkingXHigh:
		return true
	default:
		return false
	}
}

func (d *Dispatcher) resolveSlash(sess *AcpSession, line string) (prompt string, handled bool, message string, rpcErr *Error) {
	name, args := parseCommandLine(line)
	if cmd, ok := d.commands[name]; ok {
		text, err := cmd(context.Background(), d, sess, args)
		if err != nil {
			return "", true, "", err
		}
		return "", true, text, nil
	}
	if d.registry != nil {
		if c, ok := d.registry.Lookup(name); ok {
			switch {
			case c.Action != nil:
				return "", true, c.Action(args), nil
			case c.Run != nil:
				message, prompt := c.Run(args)
				return prompt, prompt == "", message, nil
			case c.Expand != nil:
				return c.Expand(args), false, "", nil
			}
		}
	}
	return line, false, "", nil
}

func (d *Dispatcher) commandCount() int {
	n := len(d.commands)
	if d.registry != nil {
		n += len(d.registry.List())
	}
	return n
}

func (d *Dispatcher) sendSessionUpdate(sessionID string, update map[string]any) {
	_ = d.transport.SendNotification(NotificationSessionUpdate, sessionUpdatePayload(sessionID, update))
}

func (d *Dispatcher) sendTextChunk(sessionID, text string) {
	if text == "" {
		return
	}
	d.sendSessionUpdate(sessionID, textChunkUpdate(text))
}

func (d *Dispatcher) sendConfigOptionsUpdate(sess *AcpSession) {
	d.sendSessionUpdate(sess.ID, map[string]any{
		"sessionUpdate": "config_option_update",
		"configOptions": sessionConfigOptions(context.Background(), sess, d.configuredModelList()),
	})
}

func (d *Dispatcher) sendQueued(sessionID string, position int) {
	d.sendTextChunk(sessionID, "Queued message (position "+strconv.Itoa(position)+").")
}

func (d *Dispatcher) announceSession(sess *AcpSession, withStartup bool) {
	if withStartup {
		if text := startupInfoText(d.version, sess, d.commandCount()); text != "" {
			d.sendTextChunk(sess.ID, text)
		}
	}
	d.sendSessionUpdate(sess.ID, map[string]any{
		"sessionUpdate":     "available_commands_update",
		"availableCommands": availableCommandsPayload(d.commands, d.registry),
	})
}

func (d *Dispatcher) replaySession(sess *AcpSession) {
	bashCmds := map[string]string{}
	for _, msg := range sess.Messages {
		switch m := msg.(type) {
		case agentcore.UserMessage:
			if text := agentcore.ContentToText(m.Content); text != "" {
				d.sendSessionUpdate(sess.ID, map[string]any{
					"sessionUpdate": "user_message_chunk",
					"content":       map[string]any{"type": "text", "text": text},
				})
			}
		case agentcore.AssistantMessage:
			if text := agentcore.ContentToText(m.Content); text != "" {
				d.sendSessionUpdate(sess.ID, textChunkUpdate(text))
			}
			for _, tc := range m.ToolCalls() {
				if isBashTool(tc.Name) {
					cmd := bashCommandFromArgs(json.RawMessage(tc.Arguments))
					bashCmds[tc.ID] = cmd
					d.sendSessionUpdate(sess.ID, bashToolCallStart(tc.ID, tc.Name, json.RawMessage(tc.Arguments), sess.Cwd, cmd))
					continue
				}
				d.sendSessionUpdate(sess.ID, map[string]any{
					"sessionUpdate": "tool_call",
					"toolCallId":    tc.ID,
					"title":         tc.Name,
					"kind":          inferToolKind(tc.Name),
					"status":        "completed",
					"rawInput":      json.RawMessage(tc.Arguments),
				})
			}
		case agentcore.ToolResultMessage:
			if isBashTool(m.ToolName) {
				cmd := bashCmds[m.ToolCallID]
				delete(bashCmds, m.ToolCallID)
				d.sendSessionUpdate(sess.ID, bashToolCallEnd(m.ToolCallID, m.ToolName, m.IsError, agentcore.AgentToolResult{
					Content: m.Content,
					Details: m.Details,
				}, sess.Cwd, cmd))
				continue
			}
			d.sendSessionUpdate(sess.ID, map[string]any{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    m.ToolCallID,
				"title":         m.ToolName,
				"kind":          inferToolKind(m.ToolName),
				"status":        boolStatus(m.IsError),
				"rawOutput":     agentcore.ContentToText(m.Content),
			})
		case agentcore.CompactionMessage:
			d.sendTextChunk(sess.ID, "[compacted] "+strings.TrimSpace(m.Summary))
		}
	}
}

func boolStatus(failed bool) string {
	if failed {
		return "failed"
	}
	return "completed"
}
