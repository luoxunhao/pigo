package provider

import (
	"context"
	"fmt"
	"strings"
)

// ProtocolGemini is the ash-workbench-facing protocol selector for Google
// Gemini-compatible endpoints. pigo has no native Gemini wire driver yet, so
// custom Gemini endpoints currently ride the OpenAI-compatible driver.
const ProtocolGemini = "gemini"

// DeferredErrorProvider is a Provider that always fails at stream build time
// with a fixed error. It lets pigo start with an unconfigured model and only
// surface the error when a request is actually attempted.
type DeferredErrorProvider struct {
	Err error
}

func (p DeferredErrorProvider) Name() string    { return "" }
func (p DeferredErrorProvider) Models() []Model { return nil }
func (p DeferredErrorProvider) StreamCompletion(ctx context.Context, req CompletionRequest) (*AssistantMessageEventStream, error) {
	return nil, p.Err
}

// ResolveConfiguredProvider builds a Provider from a configured model entry.
// The returned provider carries the entry's provider name so per-provider
// API-key overrides and error messages stay stable.
func ResolveConfiguredProvider(id, baseURL, protocol string, models []Model) (Provider, error) {
	id = strings.TrimSpace(id)
	baseURL = strings.TrimSpace(baseURL)
	if id == "" {
		return nil, fmt.Errorf("custom provider: empty id")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("custom provider %q: missing base_url", id)
	}
	if len(models) == 0 {
		models = []Model{{Provider: id, ID: "default"}}
	}
	for i := range models {
		if models[i].Provider == "" {
			models[i].Provider = id
		}
	}
	url := strings.TrimRight(baseURL, "/")
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "openai", "openai/chat":
		return NewCustomOpenAICompatibleProvider(id, url, models), nil
	case "responses", "openai/resp_api":
		return NewOpenAIResponsesProvider(id, url, models), nil
	case "anthropic":
		return NewAnthropicProtocolProvider(id, url, AuthXAPIKey, models), nil
	case ProtocolGemini:
		// Native Gemini streaming is not implemented in pigo; route through the
		// OpenAI-compatible driver so a Gemini-compatible gateway can still work.
		return NewCustomOpenAICompatibleProvider(id, url, models), nil
	default:
		return nil, fmt.Errorf("custom provider %q: unknown protocol %q", id, protocol)
	}
}

// NewCustomOpenAICompatibleProvider builds a generic OpenAI-compatible provider
// whose identity is the custom provider id instead of the neutral "openai".
func NewCustomOpenAICompatibleProvider(name, baseURL string, models []Model) Provider {
	return newOpenAICompat(openAICompatPreset{
		name:         name,
		defaultURL:   "",
		requiresAuth: true,
	}, baseURL, models)
}
