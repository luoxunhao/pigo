package acp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/provider"
)

// discoveredModel is one candidate from a /models endpoint. Missing metadata
// is left nil so the config frontend can decide whether to fill it.
type discoveredModel struct {
	ModelID        string   `json:"modelId"`
	Name           string   `json:"name"`
	ContextWindow  *int     `json:"contextWindow"`
	MaxTokens      *int     `json:"maxTokens"`
	ThinkingLevels []string `json:"thinkingLevels"`
	SupportsImages *bool    `json:"supportsImages"`
}

// discoverModelList calls the provider's model-list endpoint and returns
// candidate model entries. Errors never include the API key.
func discoverModelList(baseURL, apiKey, protocol string) ([]discoveredModel, error) {
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
			ID                  string `json:"id"`
			Name                string `json:"name"`
			ContextLength       int    `json:"context_length"`
			ContextWindow       int    `json:"contextWindow"`
			MaxCompletionTokens int    `json:"max_completion_tokens"`
			MaxTokens           int    `json:"max_tokens"`
			Reasoning           bool   `json:"reasoning"`
			Modalities          struct {
				Input []string `json:"input"`
			} `json:"modalities"`
		} `json:"data"`
		Models []struct {
			ID                  string `json:"id"`
			Name                string `json:"name"`
			ContextLength       int    `json:"context_length"`
			ContextWindow       int    `json:"contextWindow"`
			MaxCompletionTokens int    `json:"max_completion_tokens"`
			MaxTokens           int    `json:"max_tokens"`
			Reasoning           bool   `json:"reasoning"`
			Modalities          struct {
				Input []string `json:"input"`
			} `json:"modalities"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("discover: invalid response")
	}
	raw := payload.Data
	if len(raw) == 0 {
		raw = payload.Models
	}
	out := make([]discoveredModel, 0, len(raw))
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
		var contextWindow *int
		if m.ContextLength > 0 {
			contextWindow = &m.ContextLength
		} else if m.ContextWindow > 0 {
			contextWindow = &m.ContextWindow
		}
		var maxTokens *int
		if m.MaxCompletionTokens > 0 {
			maxTokens = &m.MaxCompletionTokens
		} else if m.MaxTokens > 0 {
			maxTokens = &m.MaxTokens
		}
		var supportsImages *bool
		for _, mod := range m.Modalities.Input {
			if mod == "image" {
				v := true
				supportsImages = &v
				break
			}
		}
		var thinkingLevels []string
		if m.Reasoning {
			thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh"}
		}
		out = append(out, discoveredModel{
			ModelID:        id,
			Name:           name,
			ContextWindow:  contextWindow,
			MaxTokens:      maxTokens,
			ThinkingLevels: thinkingLevels,
			SupportsImages: supportsImages,
		})
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

func fallbackProviderID(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return "custom-gateway"
	}
	host := strings.ToLower(u.Hostname())
	var b strings.Builder
	lastDash := false
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "custom-gateway"
	}
	return "custom-" + slug
}

// pigoModelsDiscover is the pigo/models/discover RPC: fetch a remote model
// list without persisting anything.
func (d *Dispatcher) pigoModelsDiscover(params json.RawMessage) (any, *Error) {
	var req struct {
		Provider string `json:"provider"`
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
	providerName := strings.TrimSpace(req.Provider)
	if providerName == "" {
		providerName = fallbackProviderID(req.BaseURL)
	}
	return map[string]any{
		"provider": providerName,
		"baseUrl":  strings.TrimSpace(req.BaseURL),
		"protocol": protocol,
		"models":   models,
	}, nil
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
