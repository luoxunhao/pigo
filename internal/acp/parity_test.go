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
)

func TestSessionListDeleteAndConfigOptions(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	models := newConfiguredModels(t, config.ModelConfig{
		Provider: "deepseek", ModelID: "deepseek-v4-pro", BaseURL: "https://api.deepseek.com", Protocol: "openai",
	})
	client, stop := StartInProcess(&RuntimeRunner{
		Provider:         fakeProvider{},
		ProviderName:     "deepseek",
		Model:            "deepseek/deepseek-v4-pro",
		APIKey:           "sk-test",
		ConfiguredModels: models,
	}, home, "deepseek/deepseek-v4-pro", "sys", ws, nil, nil)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := client.ListSessions(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0]["sessionId"] != sessionID {
		t.Fatalf("sessions = %+v", sessions)
	}
	if err := client.SetMode(ctx, sessionID, "high"); err != nil {
		t.Fatal(err)
	}
	if err := client.SetConfigOption(ctx, sessionID, configIDThoughtLevel, "low"); err != nil {
		t.Fatal(err)
	}
	if err := client.SetConfigOption(ctx, sessionID, configIDModel, "deepseek/deepseek-v4-pro"); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteSession(ctx, sessionID); err != nil {
		t.Fatal(err)
	}
	sessions, err = client.ListSessions(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions after delete = %+v", sessions)
	}
}

func TestSlashActionDoesNotRunAgent(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &fakeRunner{}
	disp, home := newTestDispatcher(t, runner, server)
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

	sawCommands := false
	waitCommands := time.After(5 * time.Second)
	for !sawCommands {
		select {
		case msg := <-msgs:
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if json.Unmarshal(msg.Notification.Params, &payload) == nil && payload.Update["sessionUpdate"] == "available_commands_update" {
				sawCommands = true
			}
		case <-waitCommands:
			t.Fatal("no available_commands_update")
		}
	}

	if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "/status"}},
	}); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	ran := len(runner.models)
	runner.mu.Unlock()
	if ran != 0 {
		t.Fatalf("action slash command ran the agent: models=%d", ran)
	}
	_ = home
}

func TestPendingQueueSteeringAndFollowUp(t *testing.T) {
	sess := &AcpSession{ID: "s1", SteeringMode: "one-at-a-time", FollowUpMode: "one-at-a-time"}
	p1 := &queuedPrompt{text: "first", done: make(chan struct{})}
	if !sess.tryRun(p1) {
		t.Fatal("first prompt should own the turn")
	}
	p2 := &queuedPrompt{text: "steer", done: make(chan struct{})}
	if sess.tryRun(p2) {
		t.Fatal("second prompt should queue")
	}
	steered := sess.popSteering(false)
	if len(steered) != 1 || steered[0] != p2 {
		t.Fatalf("steering popped %+v", steered)
	}
	if !p2.delivered {
		t.Fatal("steering prompt not marked delivered")
	}
	sess.finishTurn("end_turn", nil)
	select {
	case <-p2.done:
	case <-time.After(2 * time.Second):
		t.Fatal("delivered prompt not resolved")
	}
	if p2.stopReason != "end_turn" {
		t.Fatalf("stopReason = %q", p2.stopReason)
	}

	p3 := &queuedPrompt{text: "third", done: make(chan struct{})}
	if !sess.tryRun(p3) {
		t.Fatal("turn slot should be free after finish")
	}
	p4 := &queuedPrompt{text: "fourth", done: make(chan struct{})}
	p5 := &queuedPrompt{text: "fifth", done: make(chan struct{})}
	sess.tryRun(p4)
	sess.tryRun(p5)
	follow := sess.popFollowUp(true)
	if len(follow) != 2 {
		t.Fatalf("follow-up all popped %d, want 2", len(follow))
	}
}

func TestHistoryReplayEmitsUpdates(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp := &Dispatcher{transport: server}
	msgs, stopReader := startClientReader(t, client)
	defer stopReader()

	sess := &AcpSession{ID: "s1", Messages: agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hi")}},
		agentcore.AssistantMessage{
			RoleField:  agentcore.RoleAssistant,
			Content:    agentcore.ContentList{agentcore.NewTextContent("ok"), agentcore.NewToolCallContent("c1", "read", json.RawMessage(`{"path":"a.txt"}`))},
			StopReason: agentcore.StopReasonEndTurn,
		},
		agentcore.ToolResultMessage{RoleField: agentcore.RoleToolResult, ToolCallID: "c1", ToolName: "read", Content: agentcore.ContentList{agentcore.NewTextContent("data")}},
	}}
	disp.replaySession(sess)

	seen := map[string]bool{}
	timeout := time.After(5 * time.Second)
	for !seen["user"] || !seen["agent"] || !seen["tool"] || !seen["tool_update"] {
		select {
		case msg := <-msgs:
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
				continue
			}
			switch payload.Update["sessionUpdate"] {
			case "user_message_chunk":
				seen["user"] = true
			case "agent_message_chunk":
				seen["agent"] = true
			case "tool_call":
				seen["tool"] = true
			case "tool_call_update":
				seen["tool_update"] = true
			}
		case <-timeout:
			t.Fatalf("missing history updates: %v", seen)
		}
	}
}

func TestEventMapperToolLocationAndDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("line1\nneedle\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newEventMapper(dir)
	start := m.Map("s1", agentcore.ToolExecutionStartEvent{
		ToolCallID: "c1",
		ToolName:   "edit",
		Args:       map[string]any{"path": "f.txt", "old_string": "needle"},
	})
	if len(start) != 1 {
		t.Fatalf("start = %+v", start)
	}
	locs := start[0]["locations"].([]map[string]any)
	if locs[0]["path"] != path || locs[0]["line"] != 2 {
		t.Fatalf("locations = %+v", locs)
	}
	if err := os.WriteFile(path, []byte("line1\nreplaced\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	end := m.Map("s1", agentcore.ToolExecutionEndEvent{
		ToolCallID: "c1",
		ToolName:   "edit",
		Result:     agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("changed")}},
	})
	content := end[0]["content"].([]map[string]any)
	if content[0]["type"] != "diff" || content[0]["path"] != path {
		t.Fatalf("diff content = %+v", content)
	}
}

func TestStartupInfoNameAndExportResourceLink(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	client, stop := StartInProcess(&fakeRunner{}, home, "openrouter/free", "sys", ws, nil, nil)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}

	sawStartup := false
	timeout := time.After(5 * time.Second)
	for !sawStartup {
		select {
		case msg := <-client.Notifications():
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if json.Unmarshal(msg.Notification.Params, &payload) == nil && payload.Update["sessionUpdate"] == "agent_message_chunk" {
				if text := nestedText(payload.Update); strings.Contains(text, "pigo") {
					sawStartup = true
				}
			}
		case <-timeout:
			t.Fatal("no startup info chunk")
		}
	}

	if _, err := client.Command(ctx, sessionID, "/name My Session"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Command(ctx, sessionID, "/export"); err != nil {
		t.Fatal(err)
	}

	sawInfo, sawLink := false, false
	timeout = time.After(5 * time.Second)
	for !sawInfo || !sawLink {
		select {
		case msg := <-client.Notifications():
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if json.Unmarshal(msg.Notification.Params, &payload) != nil {
				continue
			}
			switch payload.Update["sessionUpdate"] {
			case "session_info_update":
				if payload.Update["title"] == "My Session" {
					sawInfo = true
				}
			case "agent_message_chunk":
				if content, ok := payload.Update["content"].(map[string]any); ok && content["type"] == "resource_link" {
					sawLink = true
				}
			}
		case <-timeout:
			t.Fatalf("missing info/link: info=%v link=%v", sawInfo, sawLink)
		}
	}
}
