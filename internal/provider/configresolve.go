package provider

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/smallnest/pigo/internal/cli/config"
)

// ErrModelNotConfigured reports that a model id has no [[models]] entry.
var ErrModelNotConfigured = errors.New("model is not configured")

// ErrModelDisabled reports that a [[models]] entry exists but is disabled.
var ErrModelDisabled = errors.New("model is disabled")

// ResolveConfiguredModel resolves a model id against config.toml, honoring
// explicit provider/base-url/protocol/API-key overrides. It is the single
// resolution path for interactive switching, dream, /btw, and subagent RPC
// after the built-in preset catalog was removed.
func ResolveConfiguredModel(model, baseURL, protocol, providerName, apiKey string, env func(string) string) (Provider, string, string, string, error) {
	if env == nil {
		env = os.Getenv
	}
	model = strings.TrimSpace(model)

	// An explicit --provider selection wins and keeps its existing semantics.
	if strings.TrimSpace(providerName) != "" {
		prov, name, err := ResolveNamedProvider(providerName, model, baseURL, protocol, env)
		if err != nil {
			return nil, "", "", "", err
		}
		return prov, name, apiKey, model, nil
	}

	// An explicit protocol selection points at a specific wire format.
	canonical, err := NormalizeProtocol(protocol)
	if err != nil {
		return nil, "", "", "", err
	}
	switch canonical {
	case ProtocolOpenAI:
		if strings.TrimSpace(baseURL) == "" {
			return nil, "", "", "", fmt.Errorf("--protocol openai requires --base-url")
		}
		return NewOpenAICompatibleProvider(baseURL, []Model{{Provider: "openai", ID: model, SupportsImages: true}}), "openai", apiKey, model, nil
	case ProtocolOpenAIResponses:
		if strings.TrimSpace(baseURL) == "" {
			return nil, "", "", "", fmt.Errorf("--protocol openai/resp_api requires --base-url")
		}
		return NewOpenAIResponsesProvider("openai", baseURL, []Model{{Provider: "openai", ID: model, SupportsImages: true}}), "openai", apiKey, model, nil
	case ProtocolAnthropic:
		return NewAnthropicProvider(baseURL, []Model{{Provider: "anthropic", ID: model, SupportsImages: true}}), "anthropic", apiKey, model, nil
	}

	// Otherwise the model id must name an enabled config.toml entry.
	cfg, err := config.LoadFileConfig(config.FileConfigPath())
	if err != nil {
		return nil, "", "", "", err
	}
	entry, ok := cfg.FindModel(model)
	if !ok {
		return nil, "", "", "", fmt.Errorf("%w: %q", ErrModelNotConfigured, model)
	}
	if !entry.IsEnabled() {
		return nil, "", "", "", fmt.Errorf("%w: %q", ErrModelDisabled, model)
	}
	entryBaseURL := entry.BaseURL
	if strings.TrimSpace(baseURL) != "" {
		entryBaseURL = baseURL
	}
	entryProtocol := entry.Protocol
	if strings.TrimSpace(protocol) != "" {
		entryProtocol = protocol
	}
	entryAPIKey := entry.APIKey
	if strings.TrimSpace(apiKey) != "" {
		entryAPIKey = apiKey
	}
	prov, err := ResolveConfiguredProvider(entry.Provider, entryBaseURL, entryProtocol, []Model{{
		Provider:    entry.Provider,
		ID:          entry.ModelID,
		DisplayName: entry.Name,
	}})
	if err != nil {
		return nil, "", "", "", err
	}
	return prov, entry.Provider, entryAPIKey, entry.ModelID, nil
}
