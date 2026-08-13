package provider

// Tests for config-first model resolution (ADR 0005): ResolveConfiguredModel
// honors explicit provider/base-url/protocol overrides and otherwise requires
// an enabled [[models]] entry, with no built-in preset or inference fallback.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
)

func writeProviderConfig(t *testing.T, model string, models ...config.ModelConfig) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	path := filepath.Join(root, "pigo", "config.toml")
	if err := config.SaveFileConfig(path, config.FileConfig{Model: model, Models: models}); err != nil {
		t.Fatal(err)
	}
}

// TestResolveConfiguredModelFromConfig verifies a configured entry supplies its
// own provider, wire model, and API key.
func TestResolveConfiguredModelFromConfig(t *testing.T) {
	writeProviderConfig(t, "custom-gw/m1", config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: "https://gw.example/v1", APIKey: "sk-config", Protocol: "openai",
	})
	prov, name, key, wire, err := ResolveConfiguredModel("custom-gw/m1", "", "", "", "", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if name != "custom-gw" || key != "sk-config" || wire != "m1" || prov.Name() != "custom-gw" {
		t.Fatalf("resolved = %q %q %q %T", name, key, wire, prov)
	}
}

// TestResolveConfiguredModelOverride verifies explicit API-key and base-url
// overrides win over the configured entry.
func TestResolveConfiguredModelOverride(t *testing.T) {
	writeProviderConfig(t, "custom-gw/m1", config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", BaseURL: "https://gw.example/v1",
		APIKey: "sk-config", Protocol: "openai",
	})
	_, _, key, _, err := ResolveConfiguredModel("custom-gw/m1", "https://flag.example/v1", "", "", "sk-flag", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-flag" {
		t.Fatalf("key = %q, want sk-flag", key)
	}
}

// TestResolveConfiguredModelUnknown verifies unknown ids fail with a clear
// not-configured error instead of falling back to a provider.
func TestResolveConfiguredModelUnknown(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _, _, _, err := ResolveConfiguredModel("nope/nope", "", "", "", "", os.Getenv)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want not configured", err)
	}
}

// TestResolveConfiguredModelDisabled verifies disabled entries are rejected.
func TestResolveConfiguredModelDisabled(t *testing.T) {
	disabled := false
	writeProviderConfig(t, "custom-gw/m1", config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", BaseURL: "https://gw.example/v1",
		Protocol: "openai", Enabled: &disabled,
	})
	_, _, _, _, err := ResolveConfiguredModel("custom-gw/m1", "", "", "", "", os.Getenv)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("error = %v, want disabled", err)
	}
}

// TestResolveConfiguredModelExplicitProtocol verifies explicit protocol
// selections retain their wire-driver semantics without a config entry.
func TestResolveConfiguredModelExplicitProtocol(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prov, name, _, _, err := ResolveConfiguredModel("any-model", "https://example.com/v1", "openai", "", "", os.Getenv)
	if err != nil || name != "openai" {
		t.Fatalf("protocol=openai = (%q, %v), want (openai, nil)", name, err)
	}
	if _, ok := prov.(*openAICompatDriver); !ok {
		t.Fatalf("protocol=openai built %T, want *openAICompatDriver", prov)
	}
	if _, _, _, _, err := ResolveConfiguredModel("any-model", "", "openai", "", "", os.Getenv); err == nil {
		t.Fatal("protocol=openai with no base-url should error")
	}
	if _, name, _, _, err := ResolveConfiguredModel("claude-x", "", "anthropic", "", "", os.Getenv); err != nil || name != "anthropic" {
		t.Fatalf("protocol=anthropic = (%q, %v), want (anthropic, nil)", name, err)
	}
	if _, _, _, _, err := ResolveConfiguredModel("any-model", "", "grpc", "", "", os.Getenv); err == nil {
		t.Fatal("unknown protocol should error")
	}
}

// TestResolveConfiguredModelExplicitProvider verifies --provider keeps its
// registry semantics and still wins over config lookup.
func TestResolveConfiguredModelExplicitProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, name, _, _, err := ResolveConfiguredModel("deepseek-chat", "", "", "deepseek", "", os.Getenv); err != nil || name != "deepseek" {
		t.Fatalf("provider=deepseek = (%q, %v), want (deepseek, nil)", name, err)
	}
	if _, _, _, _, err := ResolveConfiguredModel("m", "", "", "no-such-provider", "", os.Getenv); err == nil {
		t.Fatal("unknown provider should error")
	}
}

// TestResolveBaseURLPrecedence exercises all four precedence levels for a
// hyphenated provider (zai-coding-cn → ZAI_CODING_CN_BASE_URL).
func TestResolveBaseURLPrecedence(t *testing.T) {
	spec, ok := LookupProviderSpec("zai-coding-cn")
	if !ok {
		t.Fatal("expected zai-coding-cn in registry")
	}
	if got := ResolveBaseURL(spec, "", os.Getenv); got != spec.DefaultBaseURL {
		t.Errorf("default: got %q, want %q", got, spec.DefaultBaseURL)
	}
	t.Setenv("ZAI_CODING_CN_BASE_URL", "https://generic.example/v4")
	if got := ResolveBaseURL(spec, "", os.Getenv); got != "https://generic.example/v4" {
		t.Errorf("generic env: got %q, want %q", got, "https://generic.example/v4")
	}
	if got := ResolveBaseURL(spec, "https://flag.example/v4", os.Getenv); got != "https://flag.example/v4" {
		t.Errorf("flag over generic: got %q, want %q", got, "https://flag.example/v4")
	}
}

// TestResolveBaseURLProviderSpecificEnv covers a provider that declares a
// provider-specific base-url env var (azure), asserting it sits between the flag
// and the generic convention in precedence.
func TestResolveBaseURLProviderSpecificEnv(t *testing.T) {
	spec, ok := LookupProviderSpec("azure-openai-responses")
	if !ok {
		t.Fatal("expected azure-openai-responses in registry")
	}
	if len(spec.BaseURLEnvVars) == 0 {
		t.Fatal("expected azure-openai-responses to declare BaseURLEnvVars")
	}
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://specific.example")
	if got := ResolveBaseURL(spec, "", os.Getenv); got != "https://specific.example" {
		t.Errorf("provider-specific env: got %q, want %q", got, "https://specific.example")
	}
	t.Setenv("AZURE_OPENAI_RESPONSES_BASE_URL", "https://generic.example")
	if got := ResolveBaseURL(spec, "", os.Getenv); got != "https://specific.example" {
		t.Errorf("provider-specific beats generic: got %q, want %q", got, "https://specific.example")
	}
	if got := ResolveBaseURL(spec, "https://flag.example", os.Getenv); got != "https://flag.example" {
		t.Errorf("flag beats provider-specific: got %q, want %q", got, "https://flag.example")
	}
}
