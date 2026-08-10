package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/sessionstore"
)

func TestSessionListScopingAndAbsolutePath(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	srv := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	ws1 := filepath.Join(t.TempDir(), "ws1")
	ws2 := filepath.Join(t.TempDir(), "ws2")
	for _, ws := range []string{ws1, ws2} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for _, ws := range []string{ws1, ws2} {
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
		ids = append(ids, resp.SessionID)
	}

	raw, err := client.SendRequest(ctx, MethodSessionList, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var scoped struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &scoped); err != nil {
		t.Fatal(err)
	}
	if len(scoped.Sessions) != 1 || scoped.Sessions[0]["cwd"] != ws2 {
		t.Fatalf("scoped sessions = %+v, want only %s", scoped.Sessions, ws2)
	}

	raw, err = client.SendRequest(ctx, MethodSessionList, map[string]any{"all": true})
	if err != nil {
		t.Fatal(err)
	}
	var all struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	if len(all.Sessions) != 2 {
		t.Fatalf("all sessions = %+v", all.Sessions)
	}

	if _, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": "relative"}); err == nil {
		t.Fatal("relative cwd in session/new should error")
	}
	if _, err := client.SendRequest(ctx, MethodSessionLoad, map[string]any{"sessionId": ids[0], "cwd": "relative"}); err == nil {
		t.Fatal("relative cwd in session/load should error")
	}
}

func TestConfigSurfaceModesAndOrder(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetCredentialStore(provider.NewCredentialStore(nil))
	disp.model = "deepseek/deepseek-v4-pro"
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "deepseek", ModelID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro",
		BaseURL: "https://api.deepseek.com", Protocol: "openai",
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
	var resp struct {
		SessionID     string           `json:"sessionId"`
		ConfigOptions []map[string]any `json:"configOptions"`
		Modes         struct {
			AvailableModes []map[string]any `json:"availableModes"`
		} `json:"modes"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.ConfigOptions) != 2 || resp.ConfigOptions[0]["id"] != configIDModel || resp.ConfigOptions[1]["id"] != configIDThoughtLevel {
		t.Fatalf("configOptions = %+v, want model then thought_level", resp.ConfigOptions)
	}
	seen := map[string]bool{}
	for _, m := range resp.Modes.AvailableModes {
		id, _ := m["id"].(string)
		name, _ := m["name"].(string)
		if id == string(agentcore.ThinkingMax) {
			t.Fatalf("max mode leaked: %+v", m)
		}
		if !strings.HasPrefix(name, "Thinking: ") {
			t.Fatalf("mode name = %q, want Thinking: prefix", name)
		}
		seen[id] = true
	}
	for _, id := range []string{"off", "minimal", "low", "medium", "high", "xhigh"} {
		if !seen[id] {
			t.Fatalf("missing mode %q in %v", id, seen)
		}
	}

	if _, err := client.SendRequest(ctx, MethodSessionMode, map[string]any{"sessionId": resp.SessionID, "modeId": "max"}); err == nil {
		t.Fatal("set_mode max should error")
	}
	if _, err := client.SendRequest(ctx, MethodSessionMode, map[string]any{"sessionId": resp.SessionID, "modeId": "high"}); err != nil {
		t.Fatal(err)
	}
}

func TestConfigOptionsOmitEmptyModel(t *testing.T) {
	opts := configOptionsFromModels(
		map[string]any{"currentModelId": "x", "availableModels": []map[string]any{}},
		sessionModes(&AcpSession{}, nil),
	)
	if len(opts) != 1 || opts[0]["id"] != configIDThoughtLevel {
		t.Fatalf("configOptions = %+v, want only thought_level", opts)
	}
}

func TestModelSetValidation(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
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

	valid := map[string]any{"sessionId": newResp.SessionID, "modelId": "deepseek/deepseek-v4-pro"}
	if _, err := client.SendRequest(ctx, MethodModelSet, valid); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"unknown-provider/x", "custom-missing/x"} {
		if _, err := client.SendRequest(ctx, MethodModelSet, map[string]any{"sessionId": newResp.SessionID, "modelId": bad}); err == nil {
			t.Fatalf("model/set %q should error", bad)
		}
	}
	if _, err := client.SendRequest(ctx, MethodSessionConfigOpt, map[string]any{
		"sessionId": newResp.SessionID, "configId": configIDModel, "value": "deepseek/deepseek-v4-pro",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendRequest(ctx, MethodSessionConfigOpt, map[string]any{
		"sessionId": newResp.SessionID, "configId": configIDModel, "value": "unknown-provider/x",
	}); err == nil {
		t.Fatal("config option unknown model should error")
	}
}

func TestSessionLoadRestoresPersistedModel(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := NewSessionManager(&fakeRunner{})
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.New(ws, "deepseek/deepseek-v4-pro", SessionContext{SysPrompt: "sys"}, store)
	if err != nil {
		t.Fatal(err)
	}
	disp := NewDispatcher(mgr, server, home, "openrouter/free", "sys", nil, nil)
	srv := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	if _, err := client.SendRequest(ctx, MethodSessionLoad, map[string]any{"sessionId": sess.ID, "cwd": ws}); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Get(sess.ID).Model; got != "deepseek/deepseek-v4-pro" {
		t.Fatalf("restored model = %q", got)
	}
	if _, err := client.SendRequest(ctx, MethodSessionLoad, map[string]any{
		"sessionId": sess.ID, "cwd": ws, "modelId": "openrouter/free",
	}); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Get(sess.ID).Model; got != "openrouter/free" {
		t.Fatalf("override model = %q", got)
	}
}

func TestCancelClearsQueueFeedback(t *testing.T) {
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
	replyCh := make(chan promptReply, 2)
	start := func() {
		go func() {
			raw, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
				"sessionId": newResp.SessionID,
				"prompt":    []map[string]any{{"type": "text", "text": "first"}},
			})
			replyCh <- promptReply{raw: raw, err: err}
		}()
	}
	start()
	select {
	case <-runner.started:
	case <-ctx.Done():
		t.Fatal("runner never started")
	}
	start()

	sawQueued := false
	timeout := time.After(5 * time.Second)
	for !sawQueued {
		select {
		case msg := <-msgs:
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if json.Unmarshal(msg.Notification.Params, &payload) == nil && nestedText(payload.Update) == "Queued message (position 1)." {
				sawQueued = true
			}
		case <-timeout:
			t.Fatal("no queued message notification")
		}
	}

	if err := client.SendNotification(MethodSessionCancel, map[string]any{"sessionId": newResp.SessionID}); err != nil {
		t.Fatal(err)
	}
	sawCleared := false
	for i := 0; i < 2; i++ {
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
			t.Fatal("timed out waiting for prompt replies")
		}
	}
	timeout = time.After(5 * time.Second)
	for !sawCleared {
		select {
		case msg := <-msgs:
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if json.Unmarshal(msg.Notification.Params, &payload) == nil && nestedText(payload.Update) == "Cleared queued prompts." {
				sawCleared = true
			}
		case <-timeout:
			t.Fatal("no cleared queue notification")
		}
	}
}

func TestTurnIdleWatchdogReleasesQueue(t *testing.T) {
	t.Setenv("PIGO_TURN_IDLE_TIMEOUT", "100ms")
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	runner := &fakeRunner{waitCancel: true, started: make(chan struct{})}
	disp, _ := newTestDispatcher(t, runner, server)
	srv := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	_, stopReader := startClientReader(t, client)
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
	replyCh := make(chan promptReply, 2)
	start := func(text string) {
		go func() {
			raw, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
				"sessionId": newResp.SessionID,
				"prompt":    []map[string]any{{"type": "text", "text": text}},
			})
			replyCh <- promptReply{raw: raw, err: err}
		}()
	}
	start("first")
	select {
	case <-runner.started:
	case <-ctx.Done():
		t.Fatal("runner never started")
	}
	start("second")

	// The watchdog must release the turn slot without any event heartbeat, so
	// the queued second prompt becomes the running turn.
	queuedStarted := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		n := len(runner.models)
		runner.mu.Unlock()
		if n >= 2 {
			queuedStarted = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !queuedStarted {
		t.Fatal("queued prompt did not start after the idle watchdog fired")
	}

	// Cancel the now-running second turn so both prompt goroutines finish.
	if err := client.SendNotification(MethodSessionCancel, map[string]any{"sessionId": newResp.SessionID}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case r := <-replyCh:
			if r.err != nil {
				t.Fatal(r.err)
			}
			var resp struct {
				StopReason string `json:"stopReason"`
			}
			if err := json.Unmarshal(r.raw, &resp); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for prompt replies")
		}
	}
}

func TestSessionStatsCommand(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := NewSessionManager(&fakeRunner{})
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.New(ws, "m", SessionContext{SysPrompt: "sys"}, store)
	if err != nil {
		t.Fatal(err)
	}
	sess.Messages = agentcore.MessageList{
		agentcore.AssistantMessage{
			RoleField: agentcore.RoleAssistant,
			Content:   agentcore.ContentList{agentcore.NewTextContent("hi")},
			Usage:     &agentcore.Usage{InputTokens: 10, OutputTokens: 5},
		},
	}
	disp := NewDispatcher(mgr, nil, home, "m", "sys", nil, nil)
	text, rpcErr := buildCommands()["session"](context.Background(), disp, sess, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	for _, want := range []string{"Session:", "Session file:", "Messages: 1", "Tokens: in 10, out 5, total 15"} {
		if !strings.Contains(text, want) {
			t.Fatalf("session output missing %q: %s", want, text)
		}
	}
}

func TestInitializeExtensionsUnchanged(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	srv := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	raw, err := client.SendRequest(ctx, MethodInitialize, map[string]any{"protocolVersion": 1})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		AgentCapabilities struct {
			SessionCapabilities map[string]any `json:"sessionCapabilities"`
			Meta                map[string]any `json:"_meta"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.AgentCapabilities.SessionCapabilities["close"]; !ok {
		t.Fatalf("session close capability missing: %+v", resp.AgentCapabilities.SessionCapabilities)
	}
	if resp.AgentCapabilities.Meta["pigo.models.discover"] != true {
		t.Fatalf("pigo extension flags missing: %+v", resp.AgentCapabilities.Meta)
	}
}

func TestCompactCustomInstructions(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := NewSessionManager(&fakeRunner{})
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.New(ws, "fake-model", SessionContext{SysPrompt: "sys"}, store)
	if err != nil {
		t.Fatal(err)
	}
	sess.Messages = fillMessages(1000)
	runner := &RuntimeRunner{Provider: fakeProvider{}, ProviderName: "fake", Model: "fake-model"}
	disp := NewDispatcher(mgr, nil, home, "fake-model", "sys", nil, nil)
	disp.SetRunner(runner)
	disp.SetCompactConfig(&CompactConfig{
		Provider:      fakeProvider{},
		ProviderName:  "fake",
		Model:         "fake-model",
		ContextWindow: 128000,
	})
	text, rpcErr := buildCommands()["compact"](context.Background(), disp, sess, "custom instructions")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if !strings.Contains(text, "compacted:") || !strings.Contains(text, "hi from fake") {
		t.Fatalf("compact output = %q", text)
	}
}

func TestCompactAndSessionSlashWire(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := NewSessionManager(&fakeRunner{})
	if _, err := sessionstore.OpenForWorkspace(home, ws); err != nil {
		t.Fatal(err)
	}
	runner := &RuntimeRunner{Provider: fakeProvider{}, ProviderName: "fake", Model: "fake-model"}
	disp := NewDispatcher(mgr, server, home, "fake-model", "sys", nil, nil)
	disp.SetRunner(runner)
	disp.SetCompactConfig(&CompactConfig{
		Provider:      fakeProvider{},
		ProviderName:  "fake",
		Model:         "fake-model",
		ContextWindow: 128000,
	})
	srv := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	sess := mgr.Get(newResp.SessionID)
	sess.Messages = fillMessages(1000)

	if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "/compact custom instructions"}},
	}); err != nil {
		t.Fatal(err)
	}
	if text := waitForTextChunk(t, msgs, ctx, "compacted:"); !strings.Contains(text, "hi from fake") {
		t.Fatalf("compact chunk = %q", text)
	}

	sess.Messages = append(sess.Messages, agentcore.AssistantMessage{
		RoleField: agentcore.RoleAssistant,
		Content:   agentcore.ContentList{agentcore.NewTextContent("hi")},
		Usage:     &agentcore.Usage{InputTokens: 10, OutputTokens: 5},
	})
	if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "/session"}},
	}); err != nil {
		t.Fatal(err)
	}
	text := waitForTextChunk(t, msgs, ctx, "Session:")
	if !strings.Contains(text, "Tokens: in 10, out 5, total 15") {
		t.Fatalf("session chunk = %q", text)
	}
}

func fillMessages(n int) agentcore.MessageList {
	msgs := make(agentcore.MessageList, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   agentcore.ContentList{agentcore.NewTextContent(strings.Repeat("x", 100))},
		})
	}
	return msgs
}

func waitForTextChunk(t *testing.T, msgs chan IncomingMessage, ctx context.Context, needle string) string {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case msg := <-msgs:
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if json.Unmarshal(msg.Notification.Params, &payload) != nil {
				continue
			}
			text := nestedText(payload.Update)
			if strings.Contains(text, needle) {
				return text
			}
		case <-timeout:
			t.Fatalf("timed out waiting for text chunk containing %q", needle)
		case <-ctx.Done():
			t.Fatalf("context done waiting for text chunk containing %q", needle)
		}
	}
}
