package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
)

type bridgeFakeRunner struct{}

func (bridgeFakeRunner) Run(ctx context.Context, prompt string, images []agentcore.Content, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks acp.TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error) {
	partial := agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent("hello")},
		StopReason: agentcore.StopReasonEndTurn,
	}
	onEvent(agentcore.MessageUpdateEvent{Message: partial, AssistantMessageEvent: provider.StreamTextEvent{Partial: partial}})
	onEvent(agentcore.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: "bash", Args: map[string]any{"command": "echo hi"}})
	onEvent(agentcore.ToolExecutionEndEvent{ToolCallID: "call-1", ToolName: "bash", Result: agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("hi")}}})
	content := agentcore.ContentList{agentcore.NewTextContent(prompt)}
	content = append(content, images...)
	msgs := append(append(agentcore.MessageList{}, history...),
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: content},
		partial,
	)
	return msgs, &partial, nil
}

func TestStartACPRunBridgesToTeaMsgs(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	client, stop := acp.StartInProcess(&bridgeFakeRunner{}, home, "m", "sys", ws, nil, nil)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan tea.Msg, 64)
	startACPRun(client, sessionID, "hi", ch)

	var sawText, sawToolStart, sawToolEnd, sawEnd bool
	timeout := time.After(5 * time.Second)
	for !sawEnd {
		select {
		case m := <-ch:
			switch m := m.(type) {
			case textDeltaMsg:
				if m.delta == "hello" {
					sawText = true
				}
			case toolStartMsg:
				if m.name == "bash" || m.name == "echo hi" {
					sawToolStart = true
				}
			case toolEndMsg:
				if m.ok {
					sawToolEnd = true
				}
			case runEndMsg:
				sawEnd = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for bridged messages")
		}
	}
	if !sawText || !sawToolStart || !sawToolEnd {
		t.Fatalf("missing msgs: text=%v toolStart=%v toolEnd=%v", sawText, sawToolStart, sawToolEnd)
	}
}
