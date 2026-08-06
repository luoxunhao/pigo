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
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

func TestPigoEventChannelForwardsRawEvents(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &fakeRunner{
		events: []agentcore.AgentEvent{
			agentcore.CompactionEvent{Reason: "manual", TokensBefore: 100, TokensAfter: 50},
		},
	}
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
	if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "hi"}},
	}); err != nil {
		t.Fatal(err)
	}

	for {
		select {
		case msg := <-msgs:
			if msg.Notification == nil || msg.Notification.Method != MethodPigoEvent {
				continue
			}
			var payload struct {
				Event map[string]any `json:"event"`
			}
			if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Event["type"] != "compaction" {
				t.Fatalf("event = %+v", payload.Event)
			}
			return
		case <-ctx.Done():
			t.Fatal("timed out waiting for pigo/event")
		}
	}
}

func TestPigoCommandModelAndThink(t *testing.T) {
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

	raw, err = client.SendRequest(ctx, MethodPigoCommand, map[string]any{
		"sessionId": newResp.SessionID,
		"command":   "/model deepseek/deepseek-v4",
	})
	if err != nil {
		t.Fatal(err)
	}
	var cmdResp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &cmdResp); err != nil {
		t.Fatal(err)
	}
	if cmdResp.Text != "model set to deepseek/deepseek-v4" {
		t.Fatalf("cmd text = %q", cmdResp.Text)
	}

	raw, err = client.SendRequest(ctx, MethodPigoCommand, map[string]any{
		"sessionId": newResp.SessionID,
		"command":   "/think high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cmdResp); err != nil {
		t.Fatal(err)
	}
	if cmdResp.Text != "thinking set to high" {
		t.Fatalf("cmd text = %q", cmdResp.Text)
	}

	if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "after"}},
	}); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	models := append([]string{}, runner.models...)
	runner.mu.Unlock()
	if len(models) != 1 || models[0] != "deepseek/deepseek-v4" {
		t.Fatalf("models = %v", models)
	}
}

func TestPigoCommandTrust(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	mgr, err := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	broker := NewACPPermissionBroker(server, mgr, "", 0)
	runner := &fakeRunner{}
	home := t.TempDir()
	disp := NewDispatcher(NewSessionManager(runner), server, home, "m", "sys", broker, nil)
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
	if _, err := client.SendRequest(ctx, MethodPigoCommand, map[string]any{
		"sessionId": newResp.SessionID,
		"command":   "/trust on",
	}); err != nil {
		t.Fatal(err)
	}
	if !mgr.IsTrusted(ws) {
		t.Fatal("/trust on did not persist trust")
	}
}

func TestPigoStatusAndStubs(t *testing.T) {
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

	raw, err = client.SendRequest(ctx, MethodPigoStatus, map[string]any{"sessionId": newResp.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	var statusResp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &statusResp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusResp.Text, newResp.SessionID) {
		t.Fatalf("status text = %q", statusResp.Text)
	}

	for _, method := range []string{MethodPigoRewind, MethodPigoFork, MethodPigoTree, MethodPigoGoal, MethodPigoBtw, MethodPigoDream, MethodPigoRemoteControl} {
		_, err := client.SendRequest(ctx, method, map[string]any{"sessionId": newResp.SessionID})
		if err == nil {
			t.Fatalf("%s should not be implemented yet", method)
		}
		rpcErr, ok := err.(*Error)
		if !ok || rpcErr.Code != CodeNotImplemented {
			t.Fatalf("%s error = %v", method, err)
		}
	}

	_, err = client.SendRequest(ctx, MethodPigoCommand, map[string]any{
		"sessionId": newResp.SessionID,
		"command":   "/nope",
	})
	if err == nil {
		t.Fatal("unknown command should fail")
	}
}

func TestSessionTreeForkRewind(t *testing.T) {
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
	sess, err := mgr.New(ws, "openrouter/free", "sys", store)
	if err != nil {
		t.Fatal(err)
	}
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("a")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("1")}},
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("b")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("2")}},
	}
	if err := store.Append(sess.ID, time.Now().UTC(), msgs); err != nil {
		t.Fatal(err)
	}
	sess.Messages = msgs
	sess.Persisted = len(msgs)

	disp := NewDispatcher(mgr, nil, home, "openrouter/free", "sys", nil, nil)
	ctx := context.Background()

	treeText, rpcErr := buildCommands()["tree"](ctx, disp, sess, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if !strings.Contains(treeText, "user: a") || !strings.Contains(treeText, "user: b") {
		t.Fatalf("tree = %q", treeText)
	}

	forkText, rpcErr := buildCommands()["fork"](ctx, disp, sess, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if !strings.HasPrefix(forkText, "forked to ") {
		t.Fatalf("fork = %q", forkText)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("store list after fork = %+v", list)
	}

	rewindText, rpcErr := buildCommands()["rewind"](ctx, disp, sess, "1")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if !strings.Contains(rewindText, "rewound 1 turn") {
		t.Fatalf("rewind = %q", rewindText)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("messages after rewind = %d, want 2", len(sess.Messages))
	}
	_, _, stored, err := store.Load(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored messages after rewind = %d, want 2", len(stored))
	}
}

func TestDreamCommandRequiresConfig(t *testing.T) {
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
	sess, err := mgr.New(ws, "m", "sys", store)
	if err != nil {
		t.Fatal(err)
	}
	disp := NewDispatcher(mgr, nil, home, "m", "sys", nil, nil)
	_, rpcErr := buildCommands()["dream"](context.Background(), disp, sess, "")
	if rpcErr == nil || rpcErr.Code != CodeNotImplemented {
		t.Fatalf("dream without config error = %v", rpcErr)
	}
}

func TestCompactAndSessionCommands(t *testing.T) {
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
	sess, err := mgr.New(ws, "m", "sys", store)
	if err != nil {
		t.Fatal(err)
	}
	disp := NewDispatcher(mgr, nil, home, "m", "sys", nil, nil)
	ctx := context.Background()
	_, rpcErr := buildCommands()["compact"](ctx, disp, sess, "")
	if rpcErr == nil || rpcErr.Code != CodeNotImplemented {
		t.Fatalf("compact without config error = %v", rpcErr)
	}
	text, rpcErr := buildCommands()["session"](ctx, disp, sess, "")
	if rpcErr != nil || !strings.Contains(text, "Session:") {
		t.Fatalf("session command = %q, err %v", text, rpcErr)
	}
}

func TestHelpCopyExportImport(t *testing.T) {
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
	sess, err := mgr.New(ws, "m", "sys", store)
	if err != nil {
		t.Fatal(err)
	}
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hi")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("hello reply")}},
	}
	if err := store.Append(sess.ID, time.Now().UTC(), msgs); err != nil {
		t.Fatal(err)
	}
	sess.Messages = msgs
	sess.Persisted = len(msgs)

	disp := NewDispatcher(mgr, nil, home, "m", "sys", nil, nil)
	ctx := context.Background()

	helpText, rpcErr := buildCommands()["help"](ctx, disp, sess, "")
	if rpcErr != nil || !strings.Contains(helpText, "/model") {
		t.Fatalf("help = %q, err %v", helpText, rpcErr)
	}
	copyText, rpcErr := buildCommands()["copy"](ctx, disp, sess, "")
	if rpcErr != nil || (!strings.Contains(copyText, "copied last reply to clipboard") && !strings.Contains(copyText, "hello reply")) {
		t.Fatalf("copy = %q, err %v", copyText, rpcErr)
	}
	exportPath := filepath.Join(t.TempDir(), "sess.jsonl")
	exportText, rpcErr := buildCommands()["export"](ctx, disp, sess, exportPath)
	if rpcErr != nil || !strings.Contains(exportText, "exported 2 entries") {
		t.Fatalf("export = %q, err %v", exportText, rpcErr)
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatalf("export file missing: %v", err)
	}
	importText, rpcErr := buildCommands()["import"](ctx, disp, sess, exportPath)
	if rpcErr != nil || !strings.Contains(importText, "imported 2 entries") {
		t.Fatalf("import = %q, err %v", importText, rpcErr)
	}
}

func TestRebuildCommandRequiresConfig(t *testing.T) {
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
	sess, err := mgr.New(ws, "m", "sys", store)
	if err != nil {
		t.Fatal(err)
	}
	disp := NewDispatcher(mgr, nil, home, "m", "sys", nil, nil)
	_, rpcErr := buildCommands()["rebuild"](context.Background(), disp, sess, "")
	if rpcErr == nil || rpcErr.Code != CodeNotImplemented {
		t.Fatalf("rebuild without config error = %v", rpcErr)
	}
}

func TestMemoryCommandDisabled(t *testing.T) {
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
	sess, err := mgr.New(ws, "m", "sys", store)
	if err != nil {
		t.Fatal(err)
	}
	disp := NewDispatcher(mgr, nil, home, "m", "sys", nil, nil)
	text, rpcErr := buildCommands()["memory"](context.Background(), disp, sess, "")
	if rpcErr != nil || !strings.Contains(text, "disabled") {
		t.Fatalf("memory = %q, err %v", text, rpcErr)
	}
}

func TestGoalAndBtw(t *testing.T) {
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
	sess, err := mgr.New(ws, "m", "sys", store)
	if err != nil {
		t.Fatal(err)
	}
	disp := NewDispatcher(mgr, nil, home, "m", "sys", nil, nil)
	disp.SetRunner(&fakeRunner{})
	ctx := context.Background()

	text, rpcErr := buildCommands()["goal"](ctx, disp, sess, "fix the bug")
	if rpcErr != nil || text != "goal set: fix the bug" {
		t.Fatalf("goal = %q, err %v", text, rpcErr)
	}
	if !strings.Contains(applyGoal(sess, "do it"), "Current goal: fix the bug") {
		t.Fatal("goal not injected into prompt")
	}
	if text, rpcErr := buildCommands()["goal"](ctx, disp, sess, ""); rpcErr != nil || !strings.Contains(text, "fix the bug") {
		t.Fatalf("goal show = %q, err %v", text, rpcErr)
	}

	btwText, rpcErr := buildCommands()["btw"](ctx, disp, sess, "what do you think?")
	if rpcErr != nil || !strings.Contains(btwText, "done") {
		t.Fatalf("btw = %q, err %v", btwText, rpcErr)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("btw persisted a side thread: %d sessions", len(list))
	}
}

func TestRemoteBridgeLifecycle(t *testing.T) {
	rb, err := NewRemoteBridge("127.0.0.1", 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	url, err := rb.Start()
	if err != nil {
		t.Fatal(err)
	}
	if url == "" {
		t.Fatal("empty pairing url")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rb.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if rb.Enabled() {
		t.Fatal("remote still enabled after stop")
	}
}
