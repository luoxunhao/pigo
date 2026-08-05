package tui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/runtime"
)

func TestApplyUpdateMergesTextDeltas(t *testing.T) {
	m := acpChatModel{}
	m.applyUpdate(map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "hel"},
	})
	m.applyUpdate(map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "lo"},
	})
	if len(m.lines) != 1 || m.lines[0].kind != "assistant" || m.lines[0].text != "hello" {
		t.Fatalf("lines = %+v", m.lines)
	}
}

func TestApplyUpdateTracksToolStatus(t *testing.T) {
	m := acpChatModel{}
	m.applyUpdate(map[string]any{
		"sessionUpdate": "tool_call",
		"toolCallId":    "call-1",
		"title":         "bash",
		"status":        "in_progress",
	})
	m.applyUpdate(map[string]any{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "call-1",
		"status":        "completed",
	})
	if len(m.lines) != 1 || m.lines[0].kind != "tool" || m.lines[0].status != "completed" {
		t.Fatalf("lines = %+v", m.lines)
	}
}

func TestRespondPermissionSendsDecision(t *testing.T) {
	m := acpChatModel{}
	respond := make(chan any, 1)
	m.permResp = respond
	m.prompting = true
	m = mustRespond(t, m, respond)
	if m.prompting {
		t.Fatal("still prompting after response")
	}
	select {
	case v := <-respond:
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != `{"optionId":"allow_always","outcome":"selected"}` {
			t.Fatalf("decision = %s", raw)
		}
	default:
		t.Fatal("no decision sent")
	}
}

func mustRespond(t *testing.T, m acpChatModel, respond chan<- any) acpChatModel {
	t.Helper()
	m, _ = m.respondPermission("allow_always", "allowed always")
	return m
}

func TestWithACPSessionBindsBridge(t *testing.T) {
	client, server := acp.NewChannelPair()
	defer client.Close()
	defer server.Close()
	m := NewModel(Options{}).withACPSession(&runSession{}, nil, acp.NewClient(client), "s1")
	if m.startRunFn == nil {
		t.Fatal("startRunFn not bound")
	}
	if m.interruptFn == nil {
		t.Fatal("interruptFn not bound")
	}
}

func TestWithACPSessionRoutesSlashCommands(t *testing.T) {
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
	live := &cli.LiveConfig{Model: "m"}
	m := NewModel(Options{}).withSession(&runSession{live: live, slash: runtime.NewSlashRegistry()}, nil)
	m.startRunFn = nil
	m.interruptFn = nil
	m = m.withACPSession(&runSession{live: live, slash: runtime.NewSlashRegistry()}, nil, client, sessionID)
	cmd, ok := m.slash.Lookup("model")
	if !ok {
		t.Fatal("/model not registered")
	}
	if text := cmd.Action("deepseek/deepseek-v4"); text != "model set to deepseek/deepseek-v4" {
		t.Fatalf("action = %q", text)
	}
	if live.Model != "deepseek/deepseek-v4" {
		t.Fatalf("live model = %q", live.Model)
	}
}
