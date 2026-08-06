package acp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/provider"
)

// CustomProviders is the concurrency-safe in-memory view of the custom provider
// registry persisted in config.toml. The ACP dispatcher and RuntimeRunner share
// one instance so upserts take effect for the next model request without a
// process restart.
type CustomProviders struct {
	mu   sync.RWMutex
	path string
	cfg  config.FileConfig
}

// customProviderList returns the current custom provider list, or nil when no
// registry is wired.
func (d *Dispatcher) customProviderList() []config.ProviderConfig {
	if d.providers == nil {
		return nil
	}
	return d.providers.List()
}

// NewCustomProviders builds a registry backed by the given config path.
func NewCustomProviders(path string) *CustomProviders {
	return &CustomProviders{path: path}
}

// Load reloads the registry from disk.
func (p *CustomProviders) Load() error {
	cfg, err := config.LoadFileConfig(p.path)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
	return nil
}

// List returns a copy of all custom providers.
func (p *CustomProviders) List() []config.ProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]config.ProviderConfig, len(p.cfg.Providers))
	copy(out, p.cfg.Providers)
	return out
}

// Get returns one custom provider by id.
func (p *CustomProviders) Get(id string) (config.ProviderConfig, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cfg.CustomProvider(id)
}

// Upsert inserts or updates a custom provider and persists the registry. An
// empty apiKey keeps the existing key on update. The returned entry carries the
// final providerId.
func (p *CustomProviders) Upsert(entry config.ProviderConfig) (config.ProviderConfig, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.TrimSpace(entry.ID) == "" {
		entry.ID = config.CustomProviderID(entry.Name, entry.BaseURL)
	}
	if existing, ok := p.cfg.CustomProvider(entry.ID); ok && strings.TrimSpace(entry.APIKey) == "" {
		entry.APIKey = existing.APIKey
	}
	p.cfg.UpsertProvider(entry)
	if err := config.SaveFileConfig(p.path, p.cfg); err != nil {
		return entry, err
	}
	return entry, nil
}

// Delete removes a custom provider idempotently and persists the registry.
func (p *CustomProviders) Delete(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg.DeleteProvider(id)
	return config.SaveFileConfig(p.path, p.cfg)
}

// Resolve builds a Provider for a custom provider id and model id.
func (p *CustomProviders) Resolve(providerID, modelID string) (provider.Provider, config.ProviderConfig, error) {
	entry, ok := p.Get(providerID)
	if !ok {
		return nil, entry, fmt.Errorf("custom provider %q not found", providerID)
	}
	models := make([]provider.Model, 0, len(entry.Models))
	for _, m := range entry.Models {
		models = append(models, provider.Model{
			Provider:    providerID,
			ID:          m.ModelID,
			DisplayName: m.Name,
		})
	}
	prov, err := provider.ResolveCustomProvider(entry.ID, entry.BaseURL, entry.Protocol, models)
	return prov, entry, err
}

// splitCustomModelID splits "custom-<slug>/<modelId>" into its parts.
func splitCustomModelID(model string) (providerID, modelID string, ok bool) {
	return config.SplitCustomModelID(model)
}

// discoverModelList calls the provider's model-list endpoint and returns cached
// model entries. Errors never include the API key.
func discoverModelList(baseURL, apiKey, protocol string) ([]config.ProviderModelConfig, error) {
	endpoint := normalizeDiscoverBaseURL(baseURL, protocol)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("discover: invalid endpoint")
	}
	req.Header.Set("Accept", "application/json")
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "anthropic":
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case provider.ProtocolGemini:
		req.Header.Set("x-goog-api-key", apiKey)
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discover: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discover: endpoint returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("discover: invalid response")
	}
	raw := payload.Data
	if len(raw) == 0 {
		raw = payload.Models
	}
	out := make([]config.ProviderModelConfig, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, m := range raw {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = id
		}
		out = append(out, config.ProviderModelConfig{ModelID: id, Name: name})
	}
	return out, nil
}

// normalizeDiscoverBaseURL derives the model-list endpoint for a protocol.
func normalizeDiscoverBaseURL(baseURL, protocol string) string {
	b := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "anthropic":
		b = strings.TrimSuffix(b, "/v1/messages")
		if strings.HasSuffix(b, "/models") {
			return b
		}
		if strings.HasSuffix(b, "/v1") {
			return b + "/models"
		}
		return b + "/v1/models"
	case provider.ProtocolGemini:
		b = stripGeminiModelSuffix(b)
		if strings.HasSuffix(b, "/models") {
			return b
		}
		if strings.HasSuffix(b, "/v1beta") {
			return b + "/models"
		}
		return b + "/v1beta/models"
	default:
		b = strings.TrimSuffix(b, "/chat/completions")
		b = strings.TrimSuffix(b, "/responses")
		if strings.HasSuffix(b, "/models") {
			return b
		}
		return b + "/models"
	}
}

func stripGeminiModelSuffix(s string) string {
	if i := strings.Index(s, "/v1beta/models"); i >= 0 {
		return s[:i] + "/v1beta"
	}
	if i := strings.Index(s, "/models"); i >= 0 {
		return s[:i]
	}
	return s
}

// pigoModelsDiscover is the pigo/models/discover RPC: fetch a remote model
// list without persisting anything.
func (d *Dispatcher) pigoModelsDiscover(params json.RawMessage) (any, *Error) {
	var req struct {
		Name     string `json:"name"`
		BaseURL  string `json:"baseUrl"`
		APIKey   string `json:"apiKey"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(params, &req); err != nil || strings.TrimSpace(req.BaseURL) == "" {
		return nil, NewError(CodeInvalidParams, "missing baseUrl")
	}
	protocol, err := normalizeCustomProtocol(req.Protocol)
	if err != nil {
		return nil, NewError(CodeInvalidParams, err.Error())
	}
	models, err := discoverModelList(req.BaseURL, req.APIKey, protocol)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	providerID := config.CustomProviderID(req.Name, req.BaseURL)
	providerName := strings.TrimSpace(req.Name)
	if providerName == "" {
		providerName = providerID
	}
	return map[string]any{
		"providerId":   providerID,
		"providerName": providerName,
		"models":       models,
	}, nil
}

// pigoProvidersUpsert is the pigo/providers/upsert RPC.
func (d *Dispatcher) pigoProvidersUpsert(params json.RawMessage) (any, *Error) {
	var req struct {
		ProviderID string                       `json:"providerId,omitempty"`
		Name       string                       `json:"name"`
		BaseURL    string                       `json:"baseUrl"`
		APIKey     string                       `json:"apiKey,omitempty"`
		Protocol   string                       `json:"protocol"`
		Models     []config.ProviderModelConfig `json:"models,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil || strings.TrimSpace(req.BaseURL) == "" {
		return nil, NewError(CodeInvalidParams, "missing baseUrl")
	}
	if d.providers == nil {
		return nil, NewError(CodeInternalError, "custom provider registry is not configured")
	}
	protocol, err := normalizeCustomProtocol(req.Protocol)
	if err != nil {
		return nil, NewError(CodeInvalidParams, err.Error())
	}
	entry, err := d.providers.Upsert(config.ProviderConfig{
		ID:       strings.TrimSpace(req.ProviderID),
		Name:     strings.TrimSpace(req.Name),
		BaseURL:  strings.TrimSpace(req.BaseURL),
		APIKey:   req.APIKey,
		Protocol: protocol,
		Models:   req.Models,
	})
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	return map[string]any{"providerId": entry.ID}, nil
}

// normalizeCustomProtocol validates and canonicalizes the protocol accepted by
// the custom provider methods.
func normalizeCustomProtocol(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "openai", "openai/chat":
		return "openai", nil
	case "responses", "openai/resp_api":
		return "openai/resp_api", nil
	case "anthropic":
		return "anthropic", nil
	case provider.ProtocolGemini:
		return provider.ProtocolGemini, nil
	default:
		return "", fmt.Errorf("unknown protocol %q", raw)
	}
}

// pigoProvidersList is the pigo/providers/list RPC. It never echoes apiKey.
func (d *Dispatcher) pigoProvidersList(_ json.RawMessage) (any, *Error) {
	if d.providers == nil {
		return nil, NewError(CodeInternalError, "custom provider registry is not configured")
	}
	providers := make([]map[string]any, 0)
	for _, p := range d.providers.List() {
		providers = append(providers, map[string]any{
			"providerId":       p.ID,
			"name":             p.Name,
			"baseUrl":          p.BaseURL,
			"protocol":         p.Protocol,
			"apiKeyConfigured": p.APIKey != "",
			"models":           p.Models,
		})
	}
	return map[string]any{"providers": providers}, nil
}

// pigoProvidersDelete is the pigo/providers/delete RPC.
func (d *Dispatcher) pigoProvidersDelete(params json.RawMessage) (any, *Error) {
	var req struct {
		ProviderID string `json:"providerId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || strings.TrimSpace(req.ProviderID) == "" {
		return nil, NewError(CodeInvalidParams, "missing providerId")
	}
	if d.providers == nil {
		return nil, NewError(CodeInternalError, "custom provider registry is not configured")
	}
	if err := d.providers.Delete(strings.TrimSpace(req.ProviderID)); err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	return map[string]any{}, nil
}
