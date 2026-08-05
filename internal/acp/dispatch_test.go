package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/sessionstore"
)

type fakeRunner struct {
	mu         sync.Mutex
	events     []agentcore.AgentEvent
	final      agentcore.AssistantMessage
	err        error
	waitCancel bool
	started    chan struct{}
	startOnce  sync.Once
	models     []string
}

func (f *fakeRunner) Run(ctx context.Context, prompt string, images []agentcore.Content, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error) {
	f.startOnce.Do(func() {
		if f.started != nil {
			close(f.started)
		}
	})
	f.mu.Lock()
	events := f.events
	final := f.final
	runErr := f.err
	f.models = append(f.models, model)
	f.mu.Unlock()
	for _, ev := range events {
		onEvent(ev)
	}
	if f.waitCancel {
		<-ctx.Done()
	}
	msg := final
	if msg.RoleField == "" {
		msg = agentcore.AssistantMessage{
			RoleField:  agentcore.RoleAssistant,
			Content:    agentcore.ContentList{agentcore.NewTextContent("done")},
			StopReason: agentcore.StopReasonEndTurn,
		}
	}
	if f.waitCancel {
		msg.StopReason = agentcore.StopReasonAborted
	}
	content := agentcore.ContentList{agentcore.NewTextContent(prompt)}
	content = append(content, images...)
	msgs := append(append(agentcore.MessageList{}, history...),
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: content},
		msg,
	)
	if f.waitCancel {
		return msgs, &msg, ctx.Err()
	}
	return msgs, &msg, runErr
}

func newTestDispatcher(t *testing.T, runner SessionRunner, transport Transport) (*Dispatcher, string) {
	t.Helper()
	home := t.TempDir()
	mgr := NewSessionManager(runner)
	return NewDispatcher(mgr, transport, home, "openrouter/free", "you are pigo", nil, nil), home
}

func startClientReader(t *testing.T, client Transport) (chan IncomingMessage, context.CancelFunc) {
	t.Helper()
	ch := make(chan IncomingMessage, 256)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			msg, err := client.Recv(ctx)
			if err != nil {
				return
			}
			select {
			case ch <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, cancel
}

func TestSessionLifecyclePromptStreamsEvents(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &fakeRunner{
		events: []agentcore.AgentEvent{
			agentcore.MessageUpdateEvent{
				Message:               textPartial("hello"),
				AssistantMessageEvent: provider.StreamTextEvent{Partial: textPartial("hello")},
			},
			agentcore.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: "bash", Args: map[string]any{"command": "echo hi"}},
			agentcore.ToolExecutionEndEvent{ToolCallID: "call-1", ToolName: "bash", Result: agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("hi")}}},
			agentcore.TelemetryEvent{ContextTokens: 10, ContextWindow: 1000},
		},
	}
	disp, home := newTestDispatcher(t, runner, server)
	srv := NewServer(server, disp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	msgs, stopReader := startClientReader(t, client)
	defer stopReader()

	if _, err := client.SendRequest(ctx, MethodInitialize, nil); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var newResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &newResp); err != nil {
		t.Fatal(err)
	}
	if newResp.SessionID == "" {
		t.Fatal("empty session id")
	}

	raw, err = client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var promptResp struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &promptResp); err != nil {
		t.Fatal(err)
	}
	if promptResp.StopReason != "end_turn" {
		t.Fatalf("stopReason = %q", promptResp.StopReason)
	}

	var sawText, sawToolStart, sawToolEnd, sawUsage bool
	for {
		select {
		case msg := <-msgs:
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
				t.Fatal(err)
			}
			switch payload.Update["sessionUpdate"] {
			case "agent_message_chunk":
				sawText = true
			case "tool_call":
				sawToolStart = true
			case "tool_call_update":
				sawToolEnd = true
			case "usage_update":
				sawUsage = true
			}
			if sawText && sawToolStart && sawToolEnd && sawUsage {
				goto done
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for session updates")
		}
	}
done:
	if !sawText || !sawToolStart || !sawToolEnd || !sawUsage {
		t.Fatalf("missing updates: text=%v toolStart=%v toolEnd=%v usage=%v", sawText, sawToolStart, sawToolEnd, sawUsage)
	}

	// The session must be persisted in the project store.
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].SessionID != newResp.SessionID {
		t.Fatalf("store list = %+v", list)
	}
}

func TestSessionCancelStopsTurn(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &fakeRunner{waitCancel: true, started: make(chan struct{})}
	disp, _ := newTestDispatcher(t, runner, server)
	srv := NewServer(server, disp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	msgs, stopReader := startClientReader(t, client)
	defer stopReader()

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var newResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &newResp); err != nil {
		t.Fatal(err)
	}

	type promptReply struct {
		raw json.RawMessage
		err error
	}
	replyCh := make(chan promptReply, 1)
	go func() {
		raw, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
			"sessionId": newResp.SessionID,
			"prompt":    []map[string]any{{"type": "text", "text": "blocking"}},
		})
		replyCh <- promptReply{raw: raw, err: err}
	}()

	select {
	case <-runner.started:
	case <-ctx.Done():
		t.Fatal("runner never started")
	}
	if err := client.SendNotification(MethodSessionCancel, map[string]any{"sessionId": newResp.SessionID}); err != nil {
		t.Fatal(err)
	}

	select {
	case reply := <-replyCh:
		if reply.err != nil {
			t.Fatal(reply.err)
		}
		var resp struct {
			StopReason string `json:"stopReason"`
		}
		if err := json.Unmarshal(reply.raw, &resp); err != nil {
			t.Fatal(err)
		}
		if resp.StopReason != "cancelled" {
			t.Fatalf("stopReason = %q, want cancelled", resp.StopReason)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for cancelled response")
	}
	_ = msgs
}

func TestSessionCloseAndLoad(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &fakeRunner{}
	disp, _ := newTestDispatcher(t, runner, server)
	srv := NewServer(server, disp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var newResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &newResp); err != nil {
		t.Fatal(err)
	}

	if _, err := client.SendRequest(ctx, MethodSessionClose, map[string]any{"sessionId": newResp.SessionID}); err != nil {
		t.Fatal(err)
	}
	_, err = client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "after close"}},
	})
	if err == nil {
		t.Fatal("prompt after close should fail")
	}

	raw, err = client.SendRequest(ctx, MethodSessionLoad, map[string]any{
		"sessionId": newResp.SessionID,
		"cwd":       ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	var loadResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &loadResp); err != nil {
		t.Fatal(err)
	}
	if loadResp.SessionID != newResp.SessionID {
		t.Fatalf("load session = %q", loadResp.SessionID)
	}
	raw, err = client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "after load"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var promptResp struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &promptResp); err != nil {
		t.Fatal(err)
	}
	if promptResp.StopReason != "end_turn" {
		t.Fatalf("stopReason = %q", promptResp.StopReason)
	}
}

func TestModelSetChangesNextTurn(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &fakeRunner{}
	disp, _ := newTestDispatcher(t, runner, server)
	srv := NewServer(server, disp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var newResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &newResp); err != nil {
		t.Fatal(err)
	}

	for _, prompt := range []string{"first", "second"} {
		raw, err = client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
			"sessionId": newResp.SessionID,
			"prompt":    []map[string]any{{"type": "text", "text": prompt}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if prompt == "first" {
			if _, err := client.SendRequest(ctx, MethodModelSet, map[string]any{
				"sessionId": newResp.SessionID,
				"modelId":   "deepseek/deepseek-v4",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	runner.mu.Lock()
	models := append([]string{}, runner.models...)
	runner.mu.Unlock()
	want := []string{"openrouter/free", "deepseek/deepseek-v4"}
	if len(models) != 2 || models[0] != want[0] || models[1] != want[1] {
		t.Fatalf("models seen by runner = %v, want %v", models, want)
	}
}
