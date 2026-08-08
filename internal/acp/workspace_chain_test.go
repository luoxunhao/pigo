package acp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/trust"
)

type chainTool struct {
	mu       sync.Mutex
	executed int
}

func (t *chainTool) Name() string        { return "bash" }
func (t *chainTool) Description() string { return "recorded bash" }
func (t *chainTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)
}
func (t *chainTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionParallel
}
func (t *chainTool) Execute(_ context.Context, _ string, _ json.RawMessage, _ agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	t.mu.Lock()
	t.executed++
	t.mu.Unlock()
	return agentcore.AgentToolResult{
		Content: agentcore.ContentList{agentcore.NewTextContent("recorded")},
	}, nil
}

type chainTurn []provider.AssistantMessageEvent

func chainTextTurn(text string) chainTurn {
	partial := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}
	withText := partial
	withText.Content = agentcore.ContentList{agentcore.NewTextContent(text)}
	final := withText
	final.StopReason = agentcore.StopReasonEndTurn
	return chainTurn{
		provider.StreamStartEvent{Partial: partial},
		provider.StreamTextEvent{Partial: withText},
		provider.StreamDoneEvent{Message: final},
	}
}

func chainToolTurn(id, name, args string) chainTurn {
	partial := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}
	withCall := partial
	withCall.Content = agentcore.ContentList{agentcore.NewToolCallContent(id, name, json.RawMessage(args))}
	final := withCall
	final.StopReason = agentcore.StopReasonToolUse
	return chainTurn{
		provider.StreamStartEvent{Partial: partial},
		provider.StreamToolCallEvent{Partial: withCall},
		provider.StreamDoneEvent{Message: final},
	}
}

type chainProvider struct {
	mu    sync.Mutex
	turns []chainTurn
	calls int
}

func (p *chainProvider) Name() string { return "test" }
func (p *chainProvider) Models() []provider.Model {
	return []provider.Model{{Provider: "test", ID: "test"}}
}

func (p *chainProvider) StreamCompletion(ctx context.Context, _ provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	turn := p.turns[idx%len(p.turns)]
	p.mu.Unlock()
	s := provider.NewAssistantMessageEventStream(0)
	go func() {
		defer s.Close()
		for _, ev := range turn {
			select {
			case <-ctx.Done():
				s.SetError(ctx.Err())
				return
			default:
			}
			if err := s.Emit(ctx, ev); err != nil {
				s.SetError(err)
				return
			}
		}
	}()
	return s, nil
}

func TestACPWorkspacePermissionChain(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "project")
	trustPath := filepath.Join(home, "trust.json")
	mgr, err := trust.NewManager(trustPath)
	if err != nil {
		t.Fatal(err)
	}

	tool := &chainTool{}
	provider := &chainProvider{
		turns: []chainTurn{
			chainToolTurn("call-1", "bash", `{"command":"echo hi"}`),
			chainTextTurn("done"),
		},
	}
	runner := &RuntimeRunner{
		Provider:     provider,
		ProviderName: "test",
		Model:        "test",
		Tools:        []agentcore.AgentTool{tool},
	}
	client, stop := StartInProcess(runner, home, "test", "sys", ws, mgr, nil)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client.SetPermissionHandler(func(req Request) (any, *Error) {
		return map[string]any{
			"outcome": map[string]any{"outcome": "selected", "optionId": "allow_always"},
		}, nil
	})

	var updatesMu sync.Mutex
	var updates []map[string]any
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range client.Notifications() {
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if json.Unmarshal(msg.Notification.Params, &payload) == nil {
				updatesMu.Lock()
				updates = append(updates, payload.Update)
				updatesMu.Unlock()
			}
		}
	}()

	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Prompt(ctx, sessionID, "run bash"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	tool.mu.Lock()
	executed := tool.executed
	tool.mu.Unlock()
	if executed != 1 {
		t.Fatalf("tool executed = %d, want 1", executed)
	}

	updatesMu.Lock()
	defer updatesMu.Unlock()
	var sawPending, sawCompleted bool
	for _, u := range updates {
		if u["sessionUpdate"] == "tool_call" && u["status"] == "pending" && u["toolCallId"] == "call-1" {
			sawPending = true
		}
		if u["sessionUpdate"] == "tool_call_update" && u["status"] == "completed" && u["toolCallId"] == "call-1" {
			sawCompleted = true
		}
	}
	if !sawPending || !sawCompleted {
		t.Fatalf("missing pending/completed updates: %+v", updates)
	}

	reloaded, err := trust.NewManager(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsTrusted(ws) {
		t.Fatalf("allow_always did not persist trust across reload")
	}
}
