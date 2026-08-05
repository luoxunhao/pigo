package tui

import (
	"context"
	"encoding/json"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
)

// startACPRun launches client.Prompt and pumps ACP notifications into the
// existing TUI msg types, ending with runEndMsg. It is the ACP bridge seam
// (D-02) that lets the full-featured Model consume an ACP-driven turn without
// calling the agent core directly.
func startACPRun(client *acp.Client, sessionID, prompt string, ch chan tea.Msg) {
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := client.Prompt(ctx, sessionID, prompt)
			done <- err
		}()
		for {
			select {
			case err := <-done:
				ch <- runEndMsg{err: err}
				return
			case msg, ok := <-client.Notifications():
				if !ok {
					return
				}
				if m := acpToTea(msg); m != nil {
					ch <- m
				}
			}
		}
	}()
}

// acpToTea maps one ACP incoming message to the TUI's existing msg types.
func acpToTea(msg acp.IncomingMessage) tea.Msg {
	if msg.Notification == nil {
		return nil
	}
	switch msg.Notification.Method {
	case acp.NotificationSessionUpdate:
		var payload struct {
			Update map[string]any `json:"update"`
		}
		if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
			return nil
		}
		return updateToTea(payload.Update)
	case acp.MethodPigoEvent:
		var payload struct {
			Event map[string]any `json:"event"`
		}
		if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
			return nil
		}
		switch payload.Event["type"] {
		case "compaction":
			return compactionMsg{}
		case "subagent_progress":
			return subagentProgressMsg{
				id:       strVal(payload.Event["toolCallId"]),
				desc:     strVal(payload.Event["description"]),
				activity: strVal(payload.Event["activity"]),
				tokens:   intVal(payload.Event["tokens"]),
			}
		}
	}
	return nil
}

func updateToTea(u map[string]any) tea.Msg {
	switch u["sessionUpdate"] {
	case "agent_message_chunk":
		return textDeltaMsg{delta: nestedText(u)}
	case "agent_thought_chunk":
		if t := nestedText(u); t != "" {
			return textDeltaMsg{delta: "· " + t}
		}
	case "tool_call":
		return toolStartMsg{
			id:    strVal(u["toolCallId"]),
			name:  strVal(u["title"]),
			input: argsToMap(u["rawInput"]),
		}
	case "tool_call_update":
		return toolEndMsg{
			id:     strVal(u["toolCallId"]),
			ok:     strVal(u["status"]) == "completed",
			result: strVal(u["rawOutput"]),
		}
	case "usage_update":
		return telemetryMsg{ev: agentcore.TelemetryEvent{
			ContextTokens: intVal(u["used"]),
			ContextWindow: intVal(u["size"]),
		}}
	}
	return nil
}

func strVal(v any) string {
	s, _ := v.(string)
	return s
}

func intVal(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
