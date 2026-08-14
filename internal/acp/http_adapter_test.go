package acp_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/httpclient"
)

func TestHTTPAdapterStandardFlow(t *testing.T) {
	pigoHome := t.TempDir()
	cleanupStores(t)
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{
		Model: "test/provider",
		Models: []config.ModelConfig{{
			Provider: "test", ModelID: "provider", Name: "Provider",
			BaseURL: "http://localhost", Protocol: "openai",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewRouter(httpapi.Config{
		Version:    "test",
		PigoHome:   pigoHome,
		ConfigPath: cfgPath,
		TrustPath:  filepath.Join(pigoHome, "trust.json"),
		PromptRunner: func(_ context.Context, _ httpapi.PromptRun) (gen.PromptResponse, error) {
			return gen.PromptResponse{MessageId: "msg-1", StopReason: "end_turn"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()
	httpClient, err := httpclient.NewClientWithResponses(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	clientTransport, serverTransport := acp.NewChannelPair()
	adapter := acp.NewHTTPAdapter(httpClient, serverTransport, "test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = acp.NewServer(serverTransport, adapter).Serve(ctx) }()

	client := acp.NewClient(clientTransport)
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if sessionID == "" {
		t.Fatal("empty session id")
	}
	stopReason, err := client.Prompt(ctx, sessionID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if stopReason != "end_turn" {
		t.Fatalf("stopReason = %q", stopReason)
	}
}

func TestHTTPAdapterPromptDoesNotCarryConfig(t *testing.T) {
	pigoHome := t.TempDir()
	cleanupStores(t)
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{
		Model: "test/old",
		Models: []config.ModelConfig{
			{
				Provider: "test", ModelID: "old", Name: "Old",
				BaseURL: "http://localhost", Protocol: "openai",
			},
			{
				Provider: "test", ModelID: "new", Name: "New",
				BaseURL: "http://localhost", Protocol: "openai",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var promptModel, promptThinking string
	handler, err := httpapi.NewRouter(httpapi.Config{
		Version:    "test",
		PigoHome:   pigoHome,
		ConfigPath: cfgPath,
		TrustPath:  filepath.Join(pigoHome, "trust.json"),
		PromptRunner: func(_ context.Context, run httpapi.PromptRun) (gen.PromptResponse, error) {
			promptModel = run.Model
			promptThinking = run.ThinkingLevel
			return gen.PromptResponse{MessageId: "msg-1", StopReason: "end_turn"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()
	httpClient, err := httpclient.NewClientWithResponses(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	clientTransport, serverTransport := acp.NewChannelPair()
	adapter := acp.NewHTTPAdapter(httpClient, serverTransport, "test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = acp.NewServer(serverTransport, adapter).Serve(ctx) }()

	client := acp.NewClient(clientTransport)
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetConfigOption(ctx, sessionID, "model", "test/new"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Prompt(ctx, sessionID, "hello"); err != nil {
		t.Fatal(err)
	}
	if promptModel != "" {
		t.Fatalf("prompt model = %q, want empty (config belongs to session/update)", promptModel)
	}
	if promptThinking != "" {
		t.Fatalf("prompt thinking = %q, want empty (config belongs to session/update)", promptThinking)
	}
}
