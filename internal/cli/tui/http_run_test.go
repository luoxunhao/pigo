package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/httpclient"
)

func TestHTTPSessionStreamsDomainEvents(t *testing.T) {
	pigoHome := t.TempDir()
	t.Setenv("PIGO_HOME", pigoHome)
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
	cfg := httpapi.Config{
		Version:             "test",
		PigoHome:            pigoHome,
		ConfigPath:          cfgPath,
		TrustPath:           filepath.Join(pigoHome, "trust.json"),
		AutoRejectUntrusted: true,
		PromptRunner: func(_ context.Context, run httpapi.PromptRun) (gen.PromptResponse, error) {
			run.Publish("message.part.delta", map[string]any{"partId": "text", "delta": "hello from serve"})
			text := "hello from serve"
			return gen.PromptResponse{MessageId: run.MessageID, StopReason: "end_turn", Text: &text}, nil
		},
	}
	handler, err := httpapi.NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client, err := httpclient.InProcessClient(handler)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := newHTTPSession(context.Background(), Options{Model: "test/provider", ProviderName: "test"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if s.header.ID == "" {
		t.Fatal("empty server session id")
	}
	if s.httpDir != cwd {
		t.Fatalf("httpDir = %q, want %q", s.httpDir, cwd)
	}

	ch, _ := s.httpStartRun("hi")
	select {
	case msg := <-ch:
		if _, ok := msg.(textDeltaMsg); !ok {
			t.Fatalf("first msg = %T, want textDeltaMsg", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for text delta")
	}
	select {
	case msg := <-ch:
		if _, ok := msg.(runEndMsg); !ok {
			t.Fatalf("second msg = %T, want runEndMsg", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for run end")
	}
}
