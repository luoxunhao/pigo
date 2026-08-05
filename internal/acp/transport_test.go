package acp

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestChannelPairRequestResponse(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type reply struct {
		Pong string `json:"pong"`
	}
	go func() {
		msg, err := server.Recv(ctx)
		if err != nil {
			t.Errorf("server recv: %v", err)
			return
		}
		if msg.Request == nil || msg.Request.Method != "ping" {
			t.Errorf("server got %+v", msg)
			return
		}
		if err := server.SendResponse(ctx, msg.Request.ID, reply{Pong: "ok"}, nil); err != nil {
			t.Errorf("server respond: %v", err)
		}
	}()

	raw, err := client.SendRequest(ctx, "ping", map[string]any{"q": 1})
	if err != nil {
		t.Fatal(err)
	}
	var got reply
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Pong != "ok" {
		t.Fatalf("reply = %+v", got)
	}
}

func TestChannelPairNotification(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		msg, err := server.Recv(ctx)
		if err != nil {
			t.Errorf("server recv: %v", err)
			return
		}
		if msg.Notification == nil || msg.Notification.Method != "notify" {
			t.Errorf("server got %+v", msg)
		}
	}()
	if err := client.SendNotification("notify", map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("notification not delivered")
	}
}

func TestStdioTransportRoundTrip(t *testing.T) {
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	defer serverIn.Close()
	defer clientIn.Close()
	defer serverOut.Close()
	defer clientOut.Close()

	client := NewStdioTransport(clientIn, clientOut)
	server := NewStdioTransport(serverIn, serverOut)
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		msg, err := server.Recv(ctx)
		if err != nil {
			t.Errorf("server recv: %v", err)
			return
		}
		if msg.Request == nil || msg.Request.Method != "initialize" {
			t.Errorf("server got %+v", msg)
			return
		}
		if err := server.SendResponse(ctx, msg.Request.ID, map[string]any{"ok": true}, nil); err != nil {
			t.Errorf("server respond: %v", err)
		}
	}()

	raw, err := client.SendRequest(ctx, "initialize", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true {
		t.Fatalf("reply = %v", got)
	}
}
