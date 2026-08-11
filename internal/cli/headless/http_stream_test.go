package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpapi/gen"
)

func TestRunHTTPStream(t *testing.T) {
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
			run.Publish("message.part.delta", map[string]any{"partId": "text", "delta": "streamed reply"})
			text := "streamed reply"
			return gen.PromptResponse{MessageId: run.MessageID, StopReason: "end_turn", Text: &text}, nil
		},
	}
	var out bytes.Buffer
	var errOut bytes.Buffer
	code := RunHTTPStream(context.Background(), cfg, "hello", "", &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("output = %q", out.String())
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["type"] != "agent_start" || first["sessionId"] == "" {
		t.Fatalf("first event = %+v", first)
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last["type"] != "agent_end" || last["stopReason"] != "end_turn" {
		t.Fatalf("last event = %+v", last)
	}
	if !strings.Contains(out.String(), "streamed reply") {
		t.Fatalf("output does not contain streamed text: %q", out.String())
	}
}
