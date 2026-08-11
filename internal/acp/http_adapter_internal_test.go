package acp

import (
	"context"
	"encoding/json"
	"testing"
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
		SessionID string `json:"sessionId"`
		Update    map[string]any `json:"update"`
	}
	if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "s1" || payload.Update["sessionUpdate"] != "agent_message_chunk" {
		t.Fatalf("payload = %+v", payload)
	}
}
