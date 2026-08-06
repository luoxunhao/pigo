package run

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
)

func writeCustomConfig(t *testing.T, provider config.ProviderConfig) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	path := filepath.Join(root, "pigo", "config.toml")
	cfg := config.FileConfig{
		Model:     provider.ID + "/m1",
		Providers: []config.ProviderConfig{provider},
	}
	if err := config.SaveFileConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveStartupProviderCustom(t *testing.T) {
	writeCustomConfig(t, config.ProviderConfig{
		ID:       "custom-gw",
		Name:     "GW",
		BaseURL:  "https://gw.example/v1",
		APIKey:   "sk-config",
		Protocol: "openai",
		Models:   []config.ProviderModelConfig{{ModelID: "m1", Name: "M1"}},
	})
	prov, name, key, wireModel, err := resolveStartupProvider("custom-gw/m1", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "custom-gw" || key != "sk-config" || wireModel != "m1" {
		t.Fatalf("resolved = %q %q %q", name, key, wireModel)
	}
	if prov.Name() != "custom-gw" {
		t.Fatalf("provider name = %q", prov.Name())
	}
}

func TestResolveStartupProviderCustomFlagKeyWins(t *testing.T) {
	writeCustomConfig(t, config.ProviderConfig{
		ID:       "custom-gw",
		Name:     "GW",
		BaseURL:  "https://gw.example/v1",
		APIKey:   "sk-config",
		Protocol: "openai",
	})
	_, _, key, _, err := resolveStartupProvider("custom-gw/m1", "", "", "", "sk-flag")
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-flag" {
		t.Fatalf("key = %q, want sk-flag", key)
	}
}

func TestResolveStartupProviderCustomMissingProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _, _, _, err := resolveStartupProvider("custom-missing/m1", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveStartupProviderCustomMissingKey(t *testing.T) {
	writeCustomConfig(t, config.ProviderConfig{
		ID:       "custom-gw",
		Name:     "GW",
		BaseURL:  "https://gw.example/v1",
		Protocol: "openai",
	})
	_, _, _, _, err := resolveStartupProvider("custom-gw/m1", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveStartupProviderCustomLocalNoKey(t *testing.T) {
	writeCustomConfig(t, config.ProviderConfig{
		ID:       "custom-ollama",
		Name:     "Local",
		BaseURL:  "http://localhost:11434/v1",
		Protocol: "openai",
	})
	prov, name, key, wireModel, err := resolveStartupProvider("custom-ollama/m1", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "custom-ollama" || key != "" || wireModel != "m1" || prov.Name() != "custom-ollama" {
		t.Fatalf("resolved = %q %q %q", name, key, wireModel)
	}
}

func TestResolveStartupProviderNonCustom(t *testing.T) {
	prov, name, key, wireModel, err := resolveStartupProvider("openrouter/free", "", "", "", "sk-flag")
	if err != nil {
		t.Fatal(err)
	}
	if name != "openrouter" || key != "sk-flag" || wireModel != "openrouter/free" {
		t.Fatalf("resolved = %q %q %q", name, key, wireModel)
	}
	if prov == nil {
		t.Fatal("provider is nil")
	}
}
