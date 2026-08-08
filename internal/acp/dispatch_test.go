package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/cli/config"
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

func TestPromptAfterStoreFailureDoesNotQueue(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &fakeRunner{}
	disp, _ := newTestDispatcher(t, runner, server)
	srv := NewServer(server, disp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	msgs, stopReader := startClientReader(t, client)
	defer stopReader()
	_ = msgs

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
	sess := disp.manager.Get(newResp.SessionID)
	if sess == nil {
		t.Fatal("session not found after session/new")
	}
	if err := os.RemoveAll(sess.Store.Dir()); err != nil {
		t.Fatal(err)
	}

	prompt := func() {
		t.Helper()
		if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
			"sessionId": newResp.SessionID,
			"prompt":    []map[string]any{{"type": "text", "text": "first"}},
		}); err == nil {
			t.Fatal("prompt should fail when sessionstore cannot persist")
		}
	}
	prompt()
	prompt()

	runner.mu.Lock()
	ran := len(runner.models)
	runner.mu.Unlock()
	if ran != 2 {
		t.Fatalf("runner ran %d prompts, want 2 (turn slot must be released after store failure)", ran)
	}
}

func TestSessionListAndLoadMessages(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &fakeRunner{}
	disp, _ := newTestDispatcher(t, runner, server)
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "test", ModelID: "provider", Name: "Test", BaseURL: "https://x", Protocol: "openai",
	}))
	srv := NewServer(server, disp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	newSession := func() string {
		raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		return resp.SessionID
	}
	first := newSession()
	second := newSession()

	// Persist one turn so session/load has messages to return.
	if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": first,
		"prompt":    []map[string]any{{"type": "text", "text": "hello"}},
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := client.SendRequest(ctx, MethodSessionList, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var listResp struct {
		Sessions []struct {
			SessionID    string `json:"sessionId"`
			Title        string `json:"title"`
			MessageCount int    `json:"messageCount"`
			Cwd          string `json:"cwd"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Sessions) != 2 {
		t.Fatalf("session list length = %d, want 2", len(listResp.Sessions))
	}
	seen := map[string]bool{}
	for _, s := range listResp.Sessions {
		seen[s.SessionID] = true
		if s.Title == "" || s.Cwd != ws {
			t.Fatalf("session summary missing fields: %+v", s)
		}
	}
	if !seen[first] || !seen[second] {
		t.Fatalf("session list = %+v, want both ids", seen)
	}

	raw, err = client.SendRequest(ctx, MethodSessionLoad, map[string]any{
		"sessionId": first,
		"cwd":       ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	var loadResp struct {
		SessionID string           `json:"sessionId"`
		Messages  []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &loadResp); err != nil {
		t.Fatal(err)
	}
	if loadResp.SessionID != first {
		t.Fatalf("loaded session = %q, want %q", loadResp.SessionID, first)
	}
	if len(loadResp.Messages) < 2 {
		t.Fatalf("loaded messages = %d, want at least user+assistant", len(loadResp.Messages))
	}
	user := loadResp.Messages[0]
	if user["role"] != "user" || user["id"] == "" || user["content"] == nil {
		t.Fatalf("first message shape = %+v", user)
	}
	if _, ok := user["timestamp"]; !ok {
		t.Fatalf("first message missing timestamp: %+v", user)
	}
}

func TestSessionLoadAppliesAdditionalDirectories(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(t.TempDir(), "lib")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}

	read := &agenttool.ReadTool{Root: ws}
	write := &agenttool.WriteTool{Root: ws}
	edit := &agenttool.EditTool{Root: ws}
	runner := &RuntimeRunner{Tools: []agentcore.AgentTool{read, write, edit}}
	disp := NewDispatcher(NewSessionManager(runner), server, t.TempDir(), "openrouter/free", "you are pigo", nil, nil)
	disp.SetRunner(runner)
	srv := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	msgs, stopReader := startClientReader(t, client)
	defer stopReader()

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

	raw, err = client.SendRequest(ctx, MethodSessionLoad, map[string]any{
		"sessionId":             newResp.SessionID,
		"cwd":                   ws,
		"additionalDirectories": []string{extra},
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, roots := range map[string][]string{
		"read":  read.ExtraRoots,
		"write": write.ExtraRoots,
		"edit":  edit.ExtraRoots,
	} {
		if len(roots) != 1 || filepath.Clean(roots[0]) != filepath.Clean(extra) {
			t.Fatalf("%s ExtraRoots = %v, want [%s]", name, roots, extra)
		}
	}
	_ = msgs
}

func TestPigoWebExtensions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &fakeRunner{}
	disp, _ := newTestDispatcher(t, runner, server)
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "test", ModelID: "provider", Name: "Test", BaseURL: "https://x", Protocol: "openai",
	}))
	srv := NewServer(server, disp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodPigoModels, nil)
	if err != nil {
		t.Fatal(err)
	}
	var modelsResp struct {
		CurrentModelID string           `json:"currentModelId"`
		Models         []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &modelsResp); err != nil {
		t.Fatal(err)
	}
	if modelsResp.CurrentModelID == "" || len(modelsResp.Models) == 0 {
		t.Fatalf("models response = %+v", modelsResp)
	}

	raw, err = client.SendRequest(ctx, MethodPigoConfig, map[string]any{"model": "test/provider"})
	if err != nil {
		t.Fatal(err)
	}
	var cfgResp struct {
		Model  string           `json:"model"`
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &cfgResp); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "needsRestart") || cfgResp.Model != "test/provider" || len(cfgResp.Models) != 1 {
		t.Fatalf("config response = %+v", cfgResp)
	}

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err = client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var newResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &newResp); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "hello"}},
	}); err != nil {
		t.Fatal(err)
	}

	raw, err = client.SendRequest(ctx, MethodPigoMessages, map[string]any{"sessionId": newResp.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	var msgsResp struct {
		Messages []map[string]any `json:"messages"`
		HasMore  bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(raw, &msgsResp); err != nil {
		t.Fatal(err)
	}
	if len(msgsResp.Messages) < 2 || msgsResp.HasMore {
		t.Fatalf("messages response = %+v", msgsResp)
	}

	if _, err := client.SendRequest(ctx, MethodSessionDelete, map[string]any{"sessionId": newResp.SessionID}); err != nil {
		t.Fatal(err)
	}
	raw, err = client.SendRequest(ctx, MethodSessionList, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var listResp struct {
		Sessions []any `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Sessions) != 0 {
		t.Fatalf("sessions after delete = %+v", listResp.Sessions)
	}
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
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "deepseek", ModelID: "deepseek-v4-pro", BaseURL: "https://api.deepseek.com", Protocol: "openai",
	}))
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
				"modelId":   "deepseek/deepseek-v4-pro",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	runner.mu.Lock()
	models := append([]string{}, runner.models...)
	runner.mu.Unlock()
	want := []string{"openrouter/free", "deepseek/deepseek-v4-pro"}
	if len(models) != 2 || models[0] != want[0] || models[1] != want[1] {
		t.Fatalf("models seen by runner = %v, want %v", models, want)
	}
}
