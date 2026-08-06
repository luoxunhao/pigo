package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileConfigModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
model = "opencode-go/deepseek-v4-flash"

[[models]]
provider = "opencode-go"
model_id = "deepseek-v4-flash"
name = "DeepSeek V4 Flash"
base_url = "https://opencode.ai/zen/go/v1"
api_key = "sk-secret"
protocol = "openai"
context_window = 128000
max_tokens = 8192
thinking_levels = ["off", "low", "medium", "high"]
supports_images = false
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFileConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "opencode-go/deepseek-v4-flash" {
		t.Fatalf("model = %q", cfg.Model)
	}
	if len(cfg.Models) != 1 {
		t.Fatalf("models = %+v", cfg.Models)
	}
	m := cfg.Models[0]
	if m.Key() != "opencode-go/deepseek-v4-flash" || m.APIKey != "sk-secret" ||
		m.ContextWindow != 128000 || m.MaxTokens != 8192 || len(m.ThinkingLevels) != 4 {
		t.Fatalf("model = %+v", m)
	}
}

func TestSaveFileConfigPreservesModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := FileConfig{
		Model: "opencode-go/deepseek-v4-flash",
		Models: []ModelConfig{
			{
				Provider: "opencode-go", ModelID: "deepseek-v4-flash",
				BaseURL: "https://opencode.ai/zen/go/v1", APIKey: "k", Protocol: "openai",
			},
		},
	}
	if err := SaveFileConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFileConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Models) != 1 || loaded.Models[0].Key() != "opencode-go/deepseek-v4-flash" || loaded.Models[0].APIKey != "k" {
		t.Fatalf("loaded models = %+v", loaded.Models)
	}
}

func TestUpsertDeleteFindModel(t *testing.T) {
	var cfg FileConfig
	if cfg.UpsertModel(ModelConfig{Provider: "a", ModelID: "m1", Name: "A"}) {
		t.Fatal("first upsert should insert")
	}
	if !cfg.UpsertModel(ModelConfig{Provider: "a", ModelID: "m1", Name: "A2"}) {
		t.Fatal("second upsert should replace")
	}
	if len(cfg.Models) != 1 || cfg.Models[0].Name != "A2" {
		t.Fatalf("models = %+v", cfg.Models)
	}
	got, ok := cfg.FindModel("a/m1")
	if !ok || got.Name != "A2" {
		t.Fatalf("find = %+v, %v", got, ok)
	}
	if !cfg.DeleteModel("a/m1") {
		t.Fatal("delete should report existing")
	}
	if cfg.DeleteModel("a/m1") {
		t.Fatal("second delete should report missing")
	}
}

func TestSplitModelID(t *testing.T) {
	providerID, modelID, ok := SplitModelID("opencode-go/deepseek-v4-flash")
	if !ok || providerID != "opencode-go" || modelID != "deepseek-v4-flash" {
		t.Fatalf("split = %q %q %v", providerID, modelID, ok)
	}
	if _, _, ok := SplitModelID("no-slash"); ok {
		t.Fatal("missing slash should not split")
	}
}

func TestModelConfigJSONNeverEchoesAPIKey(t *testing.T) {
	raw, err := json.Marshal(ModelConfig{Provider: "a", ModelID: "m", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("api key leaked in JSON: %s", raw)
	}
}
