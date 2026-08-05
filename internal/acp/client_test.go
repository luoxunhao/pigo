package acp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
)

func TestClientChatRoundTrip(t *testing.T) {
	runner := &fakeRunner{
		events: []agentcore.AgentEvent{
			agentcore.MessageUpdateEvent{
				Message:               textPartial("hello from acp"),
				AssistantMessageEvent: provider.StreamTextEvent{Partial: textPartial("hello from acp")},
			},
		},
	}
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	cl, stop := StartInProcess(runner, home, "openrouter/free", "sys", ws, nil, nil)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := cl.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := cl.NewSession(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if sessionID == "" {
		t.Fatal("empty session id")
	}

	done := make(chan string, 1)
	go func() {
		stopReason, err := cl.Prompt(ctx, sessionID, "hi")
		if err != nil {
			t.Errorf("prompt: %v", err)
			done <- ""
			return
		}
		done <- stopReason
	}()

	var sawText bool
	for {
		select {
		case msg := <-cl.Notifications():
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Update["sessionUpdate"] == "agent_message_chunk" {
				sawText = true
			}
		case stopReason := <-done:
			if stopReason != "end_turn" {
				t.Fatalf("stopReason = %q", stopReason)
			}
			if !sawText {
				t.Fatal("no text notification received")
			}
			return
		case <-ctx.Done():
			t.Fatal("timed out")
		}
	}
}

func TestClientRoutesPermissionRequests(t *testing.T) {
	clientT, serverT := NewChannelPair()
	defer clientT.Close()
	defer serverT.Close()

	cl := NewClient(clientT)
	defer cl.Close()
	handlerCalled := make(chan struct{}, 1)
	cl.SetPermissionHandler(func(req Request) (any, *Error) {
		close(handlerCalled)
		return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow_always"}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		// Mimic an ACP server: handshake, session/new, then a prompt that asks
		// permission before replying.
		for i := 0; i < 2; i++ {
			msg, err := serverT.Recv(ctx)
			if err != nil {
				return
			}
			if msg.Request != nil {
				_ = serverT.SendResponse(ctx, msg.Request.ID, map[string]any{"sessionId": "s1"}, nil)
			}
		}
		msg, err := serverT.Recv(ctx)
		if err != nil {
			return
		}
		if msg.Request == nil {
			return
		}
		_, err = serverT.SendRequest(ctx, MethodRequestPermission, map[string]any{
			"sessionId": "s1",
			"toolCall": map[string]any{
				"toolCallId": "call-1",
				"title":      "bash",
				"status":     "pending",
			},
			"options": []map[string]any{{"optionId": "allow_always", "name": "Always allow", "kind": "allow_always"}},
		})
		if err != nil {
			return
		}
		_ = serverT.SendResponse(ctx, msg.Request.ID, map[string]any{"stopReason": "end_turn"}, nil)
	}()

	if err := cl.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.NewSession(ctx, "/tmp/ws"); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Prompt(ctx, "s1", "hi"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerCalled:
	case <-ctx.Done():
		t.Fatal("permission handler never called")
	}
}
