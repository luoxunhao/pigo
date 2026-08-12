package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestHTTPAdapterMapEvent(t *testing.T) {
	clientTransport, serverTransport := NewChannelPair()
	adapter := NewHTTPAdapter(nil, serverTransport, "test")
	adapter.mapEvent("s1", "message.part.delta", map[string]any{"delta": "hello"})
	msg, err := clientTransport.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
		t.Fatalf("notification = %+v", msg)
	}
	var payload struct {
		SessionID string         `json:"sessionId"`
		Update    map[string]any `json:"update"`
	}
	if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "s1" || payload.Update["sessionUpdate"] != "agent_message_chunk" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestHTTPAdapterPermissionAskedUsesStandardToolCall(t *testing.T) {
	clientTransport, serverTransport := NewChannelPair()
	adapter := NewHTTPAdapter(nil, serverTransport, "test")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := make(chan map[string]any, 1)
	go func() {
		msg, err := clientTransport.Recv(ctx)
		if err != nil {
			return
		}
		if msg.Request == nil || msg.Request.Method != MethodRequestPermission {
			return
		}
		var params struct {
			ToolCall map[string]any `json:"toolCall"`
		}
		_ = json.Unmarshal(msg.Request.Params, &params)
		got <- params.ToolCall
		_ = clientTransport.SendResponse(ctx, msg.Request.ID, map[string]any{
			"outcome": map[string]any{"outcome": "selected", "optionId": "allow_once"},
		}, nil)
	}()
	adapter.mapEvent("s1", "permission.asked", map[string]any{
		"permissionId": "perm-1",
		"toolCall": map[string]any{
			"id":        "call-1",
			"name":      "bash",
			"arguments": `{"command":"ls"}`,
			"summary":   "run ls",
		},
		"options": []map[string]any{{"optionId": "allow_once", "kind": "allow_once", "name": "Allow once"}},
	})
	select {
	case tc := <-got:
		if tc["toolCallId"] != "call-1" || tc["title"] != "bash" || tc["status"] != "pending" {
			t.Fatalf("toolCall = %+v", tc)
		}
		if _, ok := tc["rawInput"]; !ok {
			t.Fatalf("toolCall missing rawInput: %+v", tc)
		}
	case <-ctx.Done():
		t.Fatal("permission request was never sent to the ACP client")
	}
}

func TestHTTPAdapterToolUpdatedCarriesResultShape(t *testing.T) {
	clientTransport, serverTransport := NewChannelPair()
	adapter := NewHTTPAdapter(nil, serverTransport, "test")
	adapter.mapEvent("s1", "tool.updated", map[string]any{
		"toolCallId": "call-1",
		"title":      "bash",
		"status":     "completed",
		"rawInput":   map[string]any{"command": "ls"},
		"output":     "AGENTS.md\nREADME.md\n",
	})
	msg, err := clientTransport.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
		t.Fatalf("notification = %+v", msg)
	}
	var payload struct {
		Update map[string]any `json:"update"`
	}
	if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
		t.Fatal(err)
	}
	u := payload.Update
	if u["sessionUpdate"] != "tool_call_update" || u["kind"] != "execute" {
		t.Fatalf("update = %+v", u)
	}
	if u["rawInput"] == nil {
		t.Fatalf("rawInput missing: %+v", u)
	}
	if u["content"] == nil || u["_meta"] == nil {
		t.Fatalf("content/_meta missing: %+v", u)
	}
	meta, _ := u["_meta"].(map[string]any)
	terminal, _ := meta["terminal_output"].(map[string]any)
	if terminal["data"] != "AGENTS.md\nREADME.md\n" {
		t.Fatalf("terminal output = %v", terminal["data"])
	}
}
