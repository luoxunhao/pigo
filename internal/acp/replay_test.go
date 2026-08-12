package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/httpclient"
)

func TestReplayMessagesCarriesThinkingAndToolCalls(t *testing.T) {
	clientTransport, serverTransport := NewChannelPair()
	adapter := NewHTTPAdapter(nil, serverTransport, "test")
	entryID := "e1"
	entryType := "message"
	parentID := "e0"
	seq := 3
	lane := "main"
	messages := []httpclient.Message{
		{
			Id:        "u1",
			Role:      "user",
			EntryId:   &entryID,
			EntryType: &entryType,
			ParentId:  &parentID,
			Seq:       &seq,
			Lane:      &lane,
			Content:   []map[string]any{{"type": "text", "text": "hello"}},
		},
		{
			Id:        "a1",
			Role:      "assistant",
			EntryId:   &entryID,
			EntryType: &entryType,
			ParentId:  &parentID,
			Seq:       &seq,
			Lane:      &lane,
			Content: []map[string]any{
				{"type": "thinking", "thinking": "let me read"},
				{"type": "text", "text": ""},
				{"type": "toolCall", "id": "call-1", "name": "read", "arguments": map[string]any{"path": "README.md"}},
			},
		},
		{
			Id:        "r1",
			Role:      "toolResult",
			EntryId:   &entryID,
			EntryType: &entryType,
			ParentId:  &parentID,
			Seq:       &seq,
			Lane:      &lane,
			Content:   []map[string]any{{"type": "text", "text": "# README"}},
		},
	}
	adapter.replayMessages("s1", `E:\ws`, messages)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var updates []map[string]any
	for i := 0; i < 4; i++ {
		msg, err := clientTransport.Recv(ctx)
		if err != nil {
			t.Fatalf("recv %d: %v", i, err)
		}
		if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
			t.Fatalf("notification %d = %+v", i, msg)
		}
		var payload struct {
			Update map[string]any `json:"update"`
		}
		if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
			t.Fatal(err)
		}
		updates = append(updates, payload.Update)
	}
	if updates[0]["sessionUpdate"] != "user_message_chunk" {
		t.Fatalf("update[0] = %+v, want user_message_chunk", updates[0])
	}
	if updates[1]["sessionUpdate"] != "agent_thought_chunk" {
		t.Fatalf("update[1] = %+v, want agent_thought_chunk", updates[1])
	}
	if updates[2]["sessionUpdate"] != "tool_call" || updates[2]["toolCallId"] != "call-1" {
		t.Fatalf("update[2] = %+v, want tool_call", updates[2])
	}
	if updates[3]["sessionUpdate"] != "tool_call_update" || updates[3]["status"] != "completed" {
		t.Fatalf("update[3] = %+v, want completed tool_call_update", updates[3])
	}
	if updates[3]["rawOutput"] != "# README" {
		t.Fatalf("update[3] rawOutput = %v, want README", updates[3]["rawOutput"])
	}
	if updates[3]["messageId"] != "a1" {
		t.Fatalf("update[3] messageId = %v, want a1", updates[3]["messageId"])
	}
}

func TestReplayBashToolIncludesCommand(t *testing.T) {
	clientTransport, serverTransport := NewChannelPair()
	adapter := NewHTTPAdapter(nil, serverTransport, "test")
	messages := []httpclient.Message{
		{
			Id:      "a1",
			Role:    "assistant",
			Content: []map[string]any{{"type": "toolCall", "id": "call-bash", "name": "bash", "arguments": map[string]any{"command": "ls E:/ws"}}},
		},
		{
			Id:      "r1",
			Role:    "toolResult",
			Content: []map[string]any{{"type": "text", "text": "src\ntests\n"}},
		},
	}
	adapter.replayMessages("s1", `E:\ws`, messages)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var updates []map[string]any
	for i := 0; i < 2; i++ {
		msg, err := clientTransport.Recv(ctx)
		if err != nil {
			t.Fatalf("recv %d: %v", i, err)
		}
		var payload struct {
			Update map[string]any `json:"update"`
		}
		if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
			t.Fatal(err)
		}
		updates = append(updates, payload.Update)
	}
	if updates[0]["sessionUpdate"] != "tool_call" || updates[0]["title"] != "ls E:/ws" {
		t.Fatalf("update[0] = %+v, want bash tool_call with command title", updates[0])
	}
	meta, _ := updates[0]["_meta"].(map[string]any)
	info, _ := meta["terminal_info"].(map[string]any)
	if info["command"] != "ls E:/ws" || info["cwd"] != `E:\ws` {
		t.Fatalf("terminal_info = %+v", info)
	}
	if updates[1]["sessionUpdate"] != "tool_call_update" || updates[1]["status"] != "completed" {
		t.Fatalf("update[1] = %+v, want completed tool_call_update", updates[1])
	}
	endMeta, _ := updates[1]["_meta"].(map[string]any)
	out, _ := endMeta["terminal_output"].(map[string]any)
	if out["data"] != "src\ntests\n" {
		t.Fatalf("terminal_output = %+v", out)
	}
}
