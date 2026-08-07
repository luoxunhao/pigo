package acpcmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupEnvAppliesToolPolicy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PIGO_HOME", t.TempDir())

	cfgPath := filepath.Join(root, "pigo", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `
model = "openrouter/free"

[[models]]
provider = "openrouter"
model_id = "free"
base_url = "https://openrouter.ai/api/v1"
api_key = "test-key"
protocol = "openai"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := setupEnv(Options{
		Model:            "openrouter/free",
		AllowedTools:     []string{"read", "grep"},
		DisallowedTools:  []string{"bash"},
	})
	if err != nil {
		t.Fatalf("setupEnv: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range env.Tools {
		names[tool.Name()] = true
	}
	if !names["read"] || !names["grep"] {
		t.Fatalf("allowlist missing read/grep: %v", names)
	}
	if names["bash"] {
		t.Fatalf("denylist failed to remove bash: %v", names)
	}
	if len(env.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(env.Tools))
	}
}
