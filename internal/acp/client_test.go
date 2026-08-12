package acp

import (
	"context"
	"testing"
	"time"
)

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
