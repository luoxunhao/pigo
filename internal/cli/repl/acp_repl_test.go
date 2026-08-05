package repl

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
)

type acpFakeRunner struct{}

func (acpFakeRunner) Run(ctx context.Context, prompt string, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent)) (agentcore.MessageList, *agentcore.AssistantMessage, error) {
	partial := agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent("hello from acp")},
		StopReason: agentcore.StopReasonEndTurn,
	}
	onEvent(agentcore.MessageUpdateEvent{
		Message:               partial,
		AssistantMessageEvent: provider.StreamTextEvent{Partial: partial},
	})
	msgs := append(append(agentcore.MessageList{}, history...),
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent(prompt)}},
		partial,
	)
	return msgs, &partial, nil
}

func TestRunACPInteractive(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	client, stop := acp.StartInProcess(&acpFakeRunner{}, home, "openrouter/free", "sys", ws, nil, nil)
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
	var out bytes.Buffer
	if err := runACPInteractive(client, strings.NewReader("hi\n/exit\n"), &out, sessionID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hello from acp") {
		t.Fatalf("output = %q", out.String())
	}
}
