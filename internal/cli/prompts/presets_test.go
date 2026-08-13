package prompts

// Tests for the /models configured-model listing. configuredModelListing
// renders only enabled [[models]] entries from config.toml.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
)

func writeConfiguredModels(t *testing.T, model string, models ...config.ModelConfig) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	path := filepath.Join(root, "pigo", "config.toml")
	if err := config.SaveFileConfig(path, config.FileConfig{Model: model, Models: models}); err != nil {
		t.Fatal(err)
	}
}

// TestConfiguredModelListingGroupsAndFilters verifies /models lists enabled
// configured models by default, marks the default, and filters by provider.
func TestConfiguredModelListingGroupsAndFilters(t *testing.T) {
	disabled := false
	writeConfiguredModels(t, "openai/agnes-2.5-flash",
		config.ModelConfig{Provider: "openai", ModelID: "agnes-2.5-flash", Name: "Agnes 2.5"},
		config.ModelConfig{Provider: "openai", ModelID: "hidden", Name: "Hidden", Enabled: &disabled},
		config.ModelConfig{Provider: "deepseek", ModelID: "deepseek-chat", Name: "DeepSeek Chat"},
	)
	all := configuredModelListing("")
	for _, want := range []string{"openai", "agnes-2.5-flash", "deepseek", "deepseek-chat", "(default)"} {
		if !strings.Contains(all, want) {
			t.Errorf("full listing missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "hidden") {
		t.Errorf("disabled model must not appear:\n%s", all)
	}
	ds := configuredModelListing("deepseek")
	if !strings.Contains(ds, "deepseek-chat") {
		t.Errorf("filtered listing missing deepseek-chat:\n%s", ds)
	}
	if strings.Contains(ds, "openai") {
		t.Errorf("deepseek filter must not include openai:\n%s", ds)
	}
	if got := configuredModelListing("bogus"); !strings.Contains(got, "no configured models for provider") {
		t.Errorf("unknown filter = %q, want a not-found message", got)
	}
}

// TestConfiguredModelListingEmpty verifies the no-config message.
func TestConfiguredModelListingEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := configuredModelListing(""); !strings.Contains(got, "no configured models") {
		t.Errorf("empty listing = %q, want no configured models", got)
	}
}
