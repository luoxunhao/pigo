package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCustomProviderIDStableAndNoSlash(t *testing.T) {
	first := CustomProviderID("DeepSeek Proxy", "https://api.deepseek.com/v1")
	second := CustomProviderID("DeepSeek Proxy", "https://changed.example/v1")
	if first != second {
		t.Fatalf("id changed after rename: %q vs %q", first, second)
	}
	if first != "custom-deepseek-proxy" {
		t.Fatalf("id = %q, want custom-deepseek-proxy", first)
	}
	if containsSlash(first) {
		t.Fatalf("id %q must not contain /", first)
	}
}

func TestCustomProviderIDFallsBackToBaseURL(t *testing.T) {
	if got := CustomProviderID("", "https://gateway.example.com/v1"); got != "custom-gateway-example-com-v1" {
		t.Fatalf("id = %q", got)
	}
}

func TestLoadFileConfigProvidersAndLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
model = "custom-deepseek-proxy/deepseek-v4"
base_url = "https://old.example"

[[providers]]
id = "custom-deepseek-proxy"
name = "DeepSeek Proxy"
base_url = "https://api.deepseek.com/v1"
api_key = "sk-secret"
protocol = "openai"

[[providers.models]]
model_id = "deepseek-v4"
name = "DeepSeek V4"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("LoadFileConfig: %v", err)
	}
	if cfg.Model != "custom-deepseek-proxy/deepseek-v4" || cfg.BaseURL != "https://old.example" {
		t.Fatalf("legacy fields changed: %+v", cfg)
	}
	entry, ok := cfg.CustomProvider("custom-deepseek-proxy")
	if !ok {
		t.Fatal("custom provider not found")
	}
	if entry.APIKey != "sk-secret" || entry.Protocol != "openai" {
		t.Fatalf("provider = %+v", entry)
	}
	if len(entry.Models) != 1 || entry.Models[0].ModelID != "deepseek-v4" {
		t.Fatalf("models = %+v", entry.Models)
	}
}

func TestSaveFileConfigPreservesProviders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := FileConfig{
		Model: "custom-x/model",
		Providers: []ProviderConfig{
			{ID: "custom-x", Name: "X", BaseURL: "https://x.example/v1", APIKey: "k", Protocol: "openai"},
		},
	}
	if err := SaveFileConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFileConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers) != 1 || loaded.Providers[0].ID != "custom-x" || loaded.Providers[0].APIKey != "k" {
		t.Fatalf("loaded providers = %+v", loaded.Providers)
	}
}

func TestFileConfigUpsertDeleteProvider(t *testing.T) {
	var cfg FileConfig
	cfg.UpsertProvider(ProviderConfig{ID: "custom-a", Name: "A"})
	cfg.UpsertProvider(ProviderConfig{ID: "custom-a", Name: "A2", BaseURL: "https://a.example"})
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "A2" {
		t.Fatalf("providers after upsert = %+v", cfg.Providers)
	}
	if !cfg.DeleteProvider("custom-a") {
		t.Fatal("delete should report existing entry")
	}
	if cfg.DeleteProvider("custom-a") {
		t.Fatal("second delete should report missing entry")
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("providers after delete = %+v", cfg.Providers)
	}
}

func TestSplitCustomModelID(t *testing.T) {
	providerID, modelID, ok := SplitCustomModelID("custom-deepseek/deepseek-v4")
	if !ok || providerID != "custom-deepseek" || modelID != "deepseek-v4" {
		t.Fatalf("split = %q %q %v", providerID, modelID, ok)
	}
	if _, _, ok := SplitCustomModelID("openrouter/free"); ok {
		t.Fatal("non-custom id should not split")
	}
	if _, _, ok := SplitCustomModelID("custom-no-model"); ok {
		t.Fatal("custom id without model should not split")
	}
}

func containsSlash(s string) bool {
	for _, r := range s {
		if r == '/' {
			return true
		}
	}
	return false
}
