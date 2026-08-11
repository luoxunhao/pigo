package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/provider"
)

// ConfigService owns global config and provider management.
type ConfigService struct {
	path string
}

// NewConfigService builds a config service.
func NewConfigService(path string) *ConfigService {
	return &ConfigService{path: path}
}

func (s *ConfigService) load() (config.FileConfig, *APIError) {
	cfg, err := config.LoadFileConfig(s.path)
	if err != nil {
		return config.FileConfig{}, Internal(err.Error())
	}
	return cfg, nil
}

func (s *ConfigService) save(cfg config.FileConfig) *APIError {
	if err := config.SaveFileConfig(s.path, cfg); err != nil {
		return Internal(err.Error())
	}
	return nil
}

func (s *ConfigService) Get() (gen.ConfigResult, *APIError) {
	cfg, apiErr := s.load()
	if apiErr != nil {
		return gen.ConfigResult{}, apiErr
	}
	return configResponse(cfg), nil
}

func (s *ConfigService) Update(req gen.UpdateConfigRequest) (gen.ConfigResult, *APIError) {
	if req.Model == nil || *req.Model == "" {
		return gen.ConfigResult{}, InvalidParams("model is required")
	}
	cfg, apiErr := s.load()
	if apiErr != nil {
		return gen.ConfigResult{}, apiErr
	}
	if _, ok := cfg.FindModel(*req.Model); !ok {
		return gen.ConfigResult{}, &APIError{Status: http.StatusBadRequest, Code: CodeModelNotFound, Message: "unknown modelId: " + *req.Model}
	}
	cfg.Model = *req.Model
	if apiErr := s.save(cfg); apiErr != nil {
		return gen.ConfigResult{}, apiErr
	}
	return configResponse(cfg), nil
}

func (s *ConfigService) Providers() (gen.ProvidersResult, *APIError) {
	cfg, apiErr := s.load()
	if apiErr != nil {
		return gen.ProvidersResult{}, apiErr
	}
	byProvider := make(map[string][]gen.ModelEntry)
	for _, m := range cfg.Models {
		if !m.IsEnabled() {
			continue
		}
		byProvider[m.Provider] = append(byProvider[m.Provider], modelEntryFromConfig(m))
	}
	providers := make([]gen.Provider, 0, len(byProvider))
	for id, models := range byProvider {
		name := id
		if len(models) > 0 {
			name = models[0].Name
		}
		providers = append(providers, gen.Provider{Id: id, Name: name, Models: models})
	}
	return gen.ProvidersResult{DefaultModel: cfg.Model, Providers: providers}, nil
}

func (s *ConfigService) UpsertProvider(providerID string, input gen.ProviderInput) *APIError {
	cfg, apiErr := s.load()
	if apiErr != nil {
		return apiErr
	}
	models := []gen.ModelEntry{}
	if input.Models != nil {
		models = *input.Models
	}
	if len(models) == 0 && input.BaseUrl != nil && input.Protocol != nil {
		models = []gen.ModelEntry{{Provider: providerID, ModelId: "", Name: input.Name, BaseUrl: *input.BaseUrl, Protocol: *input.Protocol}}
	}
	for _, entry := range models {
		m := config.ModelConfig{
			Provider:       providerID,
			ModelID:        entry.ModelId,
			Name:           entry.Name,
			BaseURL:        entry.BaseUrl,
			Protocol:       entry.Protocol,
			ContextWindow:  intOrZero(entry.ContextWindow),
			MaxTokens:      intOrZero(entry.MaxTokens),
			SupportsImages: boolOrFalse(entry.SupportsImages),
		}
		if entry.ThinkingLevels != nil {
			m.ThinkingLevels = *entry.ThinkingLevels
		}
		if input.ApiKey != nil {
			m.APIKey = *input.ApiKey
		}
		cfg.UpsertModel(m)
	}
	return s.save(cfg)
}

func (s *ConfigService) DeleteProvider(providerID string) *APIError {
	cfg, apiErr := s.load()
	if apiErr != nil {
		return apiErr
	}
	kept := make([]config.ModelConfig, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		if m.Provider != providerID {
			kept = append(kept, m)
		}
	}
	cfg.Models = kept
	return s.save(cfg)
}

func (s *ConfigService) Discover(req gen.DiscoverModelsRequest) (gen.DiscoverModelsResult, *APIError) {
	if req.BaseUrl == "" {
		return gen.DiscoverModelsResult{}, InvalidParams("baseUrl is required")
	}
	protocol := "openai"
	if req.Protocol != nil {
		protocol = *req.Protocol
	}
	normalized, err := normalizeProtocol(protocol)
	if err != nil {
		return gen.DiscoverModelsResult{}, InvalidParams(err.Error())
	}
	apiKey := ""
	if req.ApiKey != nil {
		apiKey = *req.ApiKey
	}
	models, err := discoverModelList(req.BaseUrl, apiKey, normalized)
	if err != nil {
		return gen.DiscoverModelsResult{}, Internal(err.Error())
	}
	providerName := fallbackProviderID(req.BaseUrl)
	if req.Name != nil && *req.Name != "" {
		providerName = *req.Name
	}
	return gen.DiscoverModelsResult{
		Provider: providerName,
		BaseUrl:  req.BaseUrl,
		Protocol: normalized,
		Models:   models,
	}, nil
}

func (s *ConfigService) TestModel(req gen.TestModelRequest) (gen.TestModelResult, *APIError) {
	cfg, apiErr := s.load()
	if apiErr != nil {
		return gen.TestModelResult{}, apiErr
	}
	entry, ok := cfg.FindModel(req.ModelId)
	if !ok {
		return gen.TestModelResult{Success: false}, nil
	}
	protocol, err := normalizeProtocol(entry.Protocol)
	if err != nil {
		return gen.TestModelResult{Success: false}, InvalidParams(err.Error())
	}
	prov, err := provider.ResolveConfiguredProvider(entry.Provider, entry.BaseURL, protocol, []provider.Model{
		{Provider: entry.Provider, ID: entry.ModelID, DisplayName: entry.Name},
	})
	if err != nil {
		return gen.TestModelResult{Success: false}, Internal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	stream, err := provider.StreamFnFromProvider(prov)(ctx, entry.ModelID, provider.LlmContext{
		SystemPrompt: "Reply with exactly: pong",
		Messages: agentcore.MessageList{
			agentcore.UserMessage{
				RoleField: agentcore.RoleUser,
				Content:   agentcore.ContentList{agentcore.NewTextContent("ping")},
			},
		},
	}, provider.StreamConfig{APIKey: entry.APIKey, ThinkingLevel: agentcore.ThinkingOff})
	if err != nil {
		return gen.TestModelResult{Success: false}, Internal(err.Error())
	}
	responseText := ""
	for ev := range stream.Events() {
		switch e := ev.(type) {
		case provider.StreamTextEvent:
			responseText = agentcore.ContentToText(e.Partial.Content)
		case provider.StreamErrorEvent:
			details := e.Message.ErrorMessage
			if details == "" && e.Err != nil {
				details = e.Err.Error()
			}
			return gen.TestModelResult{Success: false, ResponseTimeMs: intPtr(int(time.Since(start).Milliseconds()))}, nil
		case provider.StreamDoneEvent:
			doneText := strings.TrimSpace(agentcore.ContentToText(e.Message.Content))
			if doneText == "" {
				doneText = strings.TrimSpace(responseText)
			}
			return gen.TestModelResult{Success: true, ResponseTimeMs: intPtr(int(time.Since(start).Milliseconds())), ModelResponse: &doneText}, nil
		}
	}
	return gen.TestModelResult{Success: false, ResponseTimeMs: intPtr(int(time.Since(start).Milliseconds()))}, nil
}

func configResponse(cfg config.FileConfig) gen.ConfigResult {
	models := make([]gen.ModelEntry, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		models = append(models, modelEntryFromConfig(m))
	}
	return gen.ConfigResult{Model: cfg.Model, Models: models}
}

func modelEntryFromConfig(m config.ModelConfig) gen.ModelEntry {
	apiKeyConfigured := m.APIKey != ""
	enabled := m.IsEnabled()
	return gen.ModelEntry{
		Provider:         m.Provider,
		ModelId:          m.ModelID,
		Name:             configuredModelName(m),
		BaseUrl:          m.BaseURL,
		Protocol:         m.Protocol,
		ContextWindow:    intPtr(m.ContextWindow),
		MaxTokens:        intPtr(m.MaxTokens),
		ThinkingLevels:   &m.ThinkingLevels,
		SupportsImages:   boolPtr(m.SupportsImages),
		Enabled:          &enabled,
		ApiKeyConfigured: &apiKeyConfigured,
	}
}

func intOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func boolOrFalse(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func intPtr(v int) *int {
	return &v
}

func boolPtr(v bool) *bool {
	return &v
}
