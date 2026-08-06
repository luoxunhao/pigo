package run

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/provider"
)

func writeCustomConfig(t *testing.T, model string, models ...config.ModelConfig) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	path := filepath.Join(root, "pigo", "config.toml")
	cfg := config.FileConfig{Model: model, Models: models}
	if err := config.SaveFileConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveStartupProviderCustom(t *testing.T) {
	writeCustomConfig(t, "custom-gw/m1", config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: "https://gw.example/v1", APIKey: "sk-config", Protocol: "openai",
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
	writeCustomConfig(t, "custom-gw/m1", config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1",
		BaseURL: "https://gw.example/v1", APIKey: "sk-config", Protocol: "openai",
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
	prov, _, _, _, err := resolveStartupProvider("custom-missing/m1", "", "", "", "")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	deferred, ok := prov.(provider.DeferredErrorProvider)
	if !ok {
		t.Fatalf("provider type = %T, want DeferredErrorProvider", prov)
	}
	if !strings.Contains(deferred.Err.Error(), "not configured") {
		t.Fatalf("deferred error = %v", deferred.Err)
	}
}

func TestResolveStartupProviderCustomMissingKey(t *testing.T) {
	writeCustomConfig(t, "custom-gw/m1", config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1",
		BaseURL: "https://gw.example/v1", Protocol: "openai",
	})
	prov, name, key, _, err := resolveStartupProvider("custom-gw/m1", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "custom-gw" || key != "" || prov.Name() != "custom-gw" {
		t.Fatalf("resolved = %q %q", name, key)
	}
}

func TestResolveStartupProviderCustomLocalNoKey(t *testing.T) {
	writeCustomConfig(t, "custom-ollama/m1", config.ModelConfig{
		Provider: "custom-ollama", ModelID: "m1",
		BaseURL: "http://localhost:11434/v1", Protocol: "openai",
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prov, name, key, wireModel, err := resolveStartupProvider("openrouter/free", "", "", "", "sk-flag")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if name != "" || key != "" || wireModel != "openrouter/free" {
		t.Fatalf("resolved = %q %q %q", name, key, wireModel)
	}
	deferred, ok := prov.(provider.DeferredErrorProvider)
	if !ok {
		t.Fatalf("provider type = %T, want DeferredErrorProvider", prov)
	}
	if !strings.Contains(deferred.Err.Error(), "not configured") {
		t.Fatalf("deferred error = %v", deferred.Err)
	}
}
