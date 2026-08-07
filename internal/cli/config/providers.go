package config

import (
	"encoding/json"
	"strings"
)

// ModelConfig is one configured model. Provider and ModelID form the identity
// "provider/model_id"; BaseURL, APIKey, and Protocol drive runtime wiring.
// APIKey is a secret and must never be echoed by callers that expose config.
type ModelConfig struct {
	Provider       string   `toml:"provider" json:"provider"`
	ModelID        string   `toml:"model_id" json:"modelId"`
	Name           string   `toml:"name" json:"name"`
	BaseURL        string   `toml:"base_url" json:"baseUrl"`
	APIKey         string   `toml:"api_key" json:"apiKey"`
	Protocol       string   `toml:"protocol" json:"protocol"`
	ContextWindow  int      `toml:"context_window" json:"contextWindow,omitempty"`
	MaxTokens      int      `toml:"max_tokens" json:"maxTokens,omitempty"`
	ThinkingLevels []string `toml:"thinking_levels" json:"thinkingLevels,omitempty"`
	SupportsImages bool     `toml:"supports_images" json:"supportsImages,omitempty"`
	Enabled        *bool    `toml:"enabled" json:"enabled,omitempty"`
}

// Key returns the configured model identity "provider/model_id".
func (m ModelConfig) Key() string { return m.Provider + "/" + m.ModelID }

// IsEnabled reports whether the model may appear in session model menus. A
// missing value means enabled for backward compatibility.
func (m ModelConfig) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

// MarshalJSON omits the API key so no response path can accidentally echo it.
func (m ModelConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Provider       string   `json:"provider"`
		ModelID        string   `json:"modelId"`
		Name           string   `json:"name"`
		BaseURL        string   `json:"baseUrl"`
		Protocol       string   `json:"protocol"`
		ContextWindow  int      `json:"contextWindow,omitempty"`
		MaxTokens      int      `json:"maxTokens,omitempty"`
		ThinkingLevels []string `json:"thinkingLevels,omitempty"`
		SupportsImages bool     `json:"supportsImages,omitempty"`
		Enabled        *bool    `json:"enabled,omitempty"`
	}{
		Provider:       m.Provider,
		ModelID:        m.ModelID,
		Name:           m.Name,
		BaseURL:        m.BaseURL,
		Protocol:       m.Protocol,
		ContextWindow:  m.ContextWindow,
		MaxTokens:      m.MaxTokens,
		ThinkingLevels: m.ThinkingLevels,
		SupportsImages: m.SupportsImages,
		Enabled:        m.Enabled,
	})
}

// FindModel returns the configured model with the given "provider/model_id".
func (f FileConfig) FindModel(key string) (ModelConfig, bool) {
	for _, m := range f.Models {
		if m.Key() == key {
			return m, true
		}
	}
	return ModelConfig{}, false
}

// UpsertModel inserts or replaces a configured model by "provider/model_id".
// It reports whether the entry already existed.
func (f *FileConfig) UpsertModel(m ModelConfig) bool {
	for i := range f.Models {
		if f.Models[i].Key() == m.Key() {
			f.Models[i] = m
			return true
		}
	}
	f.Models = append(f.Models, m)
	return false
}

// DeleteModel removes a configured model by "provider/model_id". It reports
// whether the entry existed.
func (f *FileConfig) DeleteModel(key string) bool {
	for i := range f.Models {
		if f.Models[i].Key() == key {
			f.Models = append(f.Models[:i], f.Models[i+1:]...)
			return true
		}
	}
	return false
}

// SplitModelID splits any "provider/model_id" into its parts.
func SplitModelID(model string) (providerID, modelID string, ok bool) {
	i := strings.Index(model, "/")
	if i <= 0 || i == len(model)-1 {
		return "", "", false
	}
	return model[:i], model[i+1:], true
}
