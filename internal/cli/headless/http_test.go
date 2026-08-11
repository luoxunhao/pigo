package headless

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpapi/gen"
)

func TestRunHTTPOnce(t *testing.T) {
	pigoHome := t.TempDir()
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
		Version:    "test",
		PigoHome:   pigoHome,
		ConfigPath: cfgPath,
		TrustPath:  filepath.Join(pigoHome, "trust.json"),
		PromptRunner: func(_ context.Context, _ httpapi.PromptRun) (gen.PromptResponse, error) {
			text := "headless reply"
			return gen.PromptResponse{MessageId: "msg-1", StopReason: "end_turn", Text: &text}, nil
		},
	}
	var out bytes.Buffer
	if err := RunHTTPOnce(context.Background(), cfg, "hello", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "headless reply") {
		t.Fatalf("output = %q", out.String())
	}
}
