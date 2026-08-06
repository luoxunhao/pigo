package acp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/cli/run"
)

// TestACPRealProviderE2E drives the exact TUI/REPL ACP path (StartInProcess +
// client.Prompt) against the real provider configured in config.toml. It is
// gated behind PIGO_E2E=1 so normal test runs skip it.
func TestACPRealProviderE2E(t *testing.T) {
	if os.Getenv("PIGO_E2E") != "1" {
		t.Skip("set PIGO_E2E=1 to run the real-provider ACP e2e test")
	}
	cfgPath := config.FileConfigPath()
	cfg, err := config.LoadFileConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config %s: %v", cfgPath, err)
	}
	model := cfg.Model
	if model == "" {
		t.Skipf("no model in %s", cfgPath)
	}
	entry, ok := cfg.FindModel(model)
	if !ok {
		t.Skipf("model %q not in %s", model, cfgPath)
	}
	t.Logf("config: %s model=%s provider=%s base_url=%s", cfgPath, model, entry.Provider, entry.BaseURL)

	env, err := run.SetupEnv(model, entry.BaseURL, entry.Protocol, entry.Provider, entry.APIKey, false, true, "", nil, false)
	if err != nil {
		t.Fatalf("SetupEnv: %v", err)
	}
	runner := &acp.RuntimeRunner{
		Provider:      env.Provider,
		ProviderName:  env.ProviderName,
		Model:         model,
		APIKey:        env.APIKey,
		ThinkingLevel: agentcore.ThinkingMedium,
		Tools:         env.Tools,
	}
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	client, stop := acp.StartInProcess(runner, home, model, env.SysPrompt, ws, nil, nil)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sessionID, err := client.NewSession(ctx, ws)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	var streamed strings.Builder
	done := make(chan error, 1)
	go func() {
		_, err := client.Prompt(ctx, sessionID, "你好，请用一句话回复")
		done <- err
	}()

	timeout := time.After(120 * time.Second)
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("prompt: %v", err)
			}
			text := streamed.String()
			if strings.TrimSpace(text) == "" {
				t.Fatal("no assistant text streamed")
			}
			t.Logf("streamed reply: %s", text)
			return
		case msg := <-client.Notifications():
			if msg.Notification != nil && msg.Notification.Method == acp.NotificationSessionUpdate {
				var payload struct {
					Update map[string]any `json:"update"`
				}
				if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
					continue
				}
				if payload.Update["sessionUpdate"] == "agent_message_chunk" {
					content, _ := payload.Update["content"].(map[string]any)
					if text, _ := content["text"].(string); text != "" {
						streamed.WriteString(text)
						t.Logf("chunk: %s", text)
					}
				}
			}
		case <-timeout:
			t.Fatalf("timed out; streamed so far: %q", streamed.String())
		}
	}
}
