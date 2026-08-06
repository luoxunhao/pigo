package acp

import (
	"path/filepath"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
)

func newConfiguredModels(t *testing.T, models ...config.ModelConfig) *ConfiguredModels {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.FileConfig{Models: models}
	if err := config.SaveFileConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	store := NewConfiguredModels(path)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	return store
}
