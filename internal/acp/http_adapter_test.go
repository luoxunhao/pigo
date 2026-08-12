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
	if err := client.Resume(ctx, sessionID, workspace); err != nil {
		t.Fatalf("resume: %v", err)
	}
}
