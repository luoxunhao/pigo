package acp

import (
	"sync"

	"github.com/smallnest/pigo/internal/cli/config"
)

// ConfiguredModels is the concurrency-safe view of the configured model list
// persisted in config.toml. ACP and the runtime share one instance so config
// writes take effect without a restart.
type ConfiguredModels struct {
	mu   sync.RWMutex
	path string
	cfg  config.FileConfig
}

// NewConfiguredModels builds a configured-model store backed by a config path.
func NewConfiguredModels(path string) *ConfiguredModels {
	return &ConfiguredModels{path: path}
}

// Load reloads the configured model list from disk.
func (m *ConfiguredModels) Load() error {
	cfg, err := config.LoadFileConfig(m.path)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	return nil
}

// List returns a copy of all configured models.
func (m *ConfiguredModels) List() []config.ModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]config.ModelConfig, len(m.cfg.Models))
	copy(out, m.cfg.Models)
	return out
}

// CurrentModel returns the configured default model id.
func (m *ConfiguredModels) CurrentModel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Model
}

// SetModel updates the default model id and persists the config.
func (m *ConfiguredModels) SetModel(model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Model = model
	return config.SaveFileConfig(m.path, m.cfg)
}

// Replace replaces the configured model list, deduplicating by
// provider/model_id, and persists the config.
func (m *ConfiguredModels) Replace(models []config.ModelConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	deduped := make([]config.ModelConfig, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		if seen[model.Key()] {
			continue
		}
		seen[model.Key()] = true
		if existing, ok := m.cfg.FindModel(model.Key()); ok && model.APIKey == "" {
			model.APIKey = existing.APIKey
		}
		deduped = append(deduped, model)
	}
	m.cfg.Models = deduped
	return config.SaveFileConfig(m.path, m.cfg)
}

// Find returns one configured model by "provider/model_id".
func (m *ConfiguredModels) Find(key string) (config.ModelConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.FindModel(key)
}

// Upsert inserts or replaces a configured model and persists the config.
func (m *ConfiguredModels) Upsert(model config.ModelConfig) (config.ModelConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.cfg.FindModel(model.Key()); ok && model.APIKey == "" {
		model.APIKey = existing.APIKey
	}
	m.cfg.UpsertModel(model)
	if err := config.SaveFileConfig(m.path, m.cfg); err != nil {
		return model, err
	}
	return model, nil
}

// Delete removes a configured model idempotently and persists the config.
func (m *ConfiguredModels) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.DeleteModel(key)
	return config.SaveFileConfig(m.path, m.cfg)
}
