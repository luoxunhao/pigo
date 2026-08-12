package acp

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestServerInitialize(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv := NewServer(server, nil)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodInitialize, map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
			Prompt      any  `json:"promptCapabilities"`
			Session     any  `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
		AgentInfo map[string]string `json:"agentInfo"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ProtocolVersion != 1 {
		t.Fatalf("protocolVersion = %d", resp.ProtocolVersion)
	}
	if !resp.AgentCapabilities.LoadSession {
		t.Fatal("loadSession capability missing")
	}
	if resp.AgentInfo["name"] != "pigo" {
		t.Fatalf("agentInfo = %+v", resp.AgentInfo)
	}
}

func TestServerUnknownMethod(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv := NewServer(server, nil)
	go func() { _ = srv.Serve(ctx) }()

	_, err := client.SendRequest(ctx, "no/such_method", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	rpcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("code = %d, want %d", rpcErr.Code, CodeMethodNotFound)
	}
}

func TestStdioServerInitialize(t *testing.T) {
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

	srv := NewServer(server, nil)
	go func() { _ = srv.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodInitialize, nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ProtocolVersion != 1 {
		t.Fatalf("protocolVersion = %d", resp.ProtocolVersion)
	}
}
