package provider

// Provider resolution moved here from cmd/pigo (US-004, #361): mapping a model
// id / --provider / --protocol selection to a concrete wire driver, plus the
// base-url override precedence. Config-first resolution lives in
// configresolve.go; this file keeps the explicit named-provider path.

import (
	"fmt"
	"strings"

	"github.com/smallnest/pigo/internal/cli/config"
)

// ResolveNamedProvider builds the driver for an explicit --provider selection.
// It looks the name up in the built-in registry and constructs the wire driver
// matching the spec's Protocol: "openai" → an OpenAI-compatible (Bearer) driver,
// "anthropic" → an Anthropic-Messages driver. The base URL follows the override
// precedence in ResolveBaseURL (--base-url > provider-specific env > generic
// <PROVIDER>_BASE_URL > spec default). The returned provider-name string is the
// spec name, so downstream API-key resolution reads the provider's own env var
// (spec.EnvVars).
//
// Special providers with bespoke auth (azure/bedrock/vertex/cloudflare —
// AuthScheme aws/azure/special, or the cloudflare-* names) are routed to
// ResolveSpecialProvider, which validates their required env vars and composes
// the concrete endpoint (node #188).
func ResolveNamedProvider(name, model, baseURL, protocol string, env func(string) string) (Provider, string, error) {
	spec, ok := LookupProviderSpec(name)
	if !ok {
		return nil, "", fmt.Errorf("unknown --provider %q (available: %s)", name, strings.Join(ProviderNames(), ", "))
	}
	// A concurrently-set --protocol must agree with the provider's own protocol;
	// an incompatible pair is a user error naming both flags. Normalize the raw
	// value first so aliases (e.g. "openai/chat" for an "openai" spec) don't
	// falsely conflict, and a genuine typo surfaces as a clear "unknown --protocol"
	// error rather than a misleading conflict message.
	if strings.TrimSpace(protocol) != "" {
		canonical, err := NormalizeProtocol(protocol)
		if err != nil {
			return nil, "", err
		}
		if canonical != spec.Protocol {
			return nil, "", fmt.Errorf("--provider %q speaks the %q protocol, which conflicts with --protocol %q; drop --protocol or set it to %q", name, spec.Protocol, protocol, spec.Protocol)
		}
	}
	// Special-auth providers (Azure / Bedrock / Vertex / Cloudflare) compose
	// their endpoint from several env vars and/or need non-standard credential
	// validation, so route them to the dedicated resolver (US-007 / node #188).
	// It performs its own base-URL composition (honoring the --base-url override)
	// and returns a clear error naming any absent required env var.
	if IsSpecialAuthProvider(spec) {
		p, err := ResolveSpecialProvider(spec, model, baseURL, env)
		if err != nil {
			return nil, "", err
		}
		return p, spec.Name, nil
	}
	// Base-URL precedence (US-004 / FR-8, FR-9): --base-url flag > provider-
	// specific base-url env var(s) > generic <PROVIDER>_BASE_URL > spec default.
	url := ResolveBaseURL(spec, baseURL, env)
	models := []Model{{Provider: spec.Name, ID: model, SupportsImages: true}}
	// Note: spec.ExtraHeaders would be attached here, but the exported generic
	// constructors do not yet accept custom headers; all built-in specs currently
	// carry no ExtraHeaders, so this is a no-op today (refined alongside #188).
	switch spec.Protocol {
	case ProtocolAnthropic:
		// Auth header follows the spec's AuthScheme (x-api-key + anthropic-version
		// for anthropic/minimax/minimax-cn; Bearer for any anthropic-protocol
		// gateway that authenticates with a plain bearer token). The driver name is
		// the spec name so errors reference the selected provider.
		return NewAnthropicProtocolProvider(spec.Name, url, spec.AuthScheme, models), spec.Name, nil
	case ProtocolOpenAI:
		return NewOpenAICompatibleProvider(url, models), spec.Name, nil
	case ProtocolOpenAIResponses:
		return NewOpenAIResponsesProvider(spec.Name, url, models), spec.Name, nil
	default:
		// The registry only ever stores openai/openai-resp/anthropic; guard anyway
		// so an unexpected value is a clear error rather than a nil provider.
		return nil, "", fmt.Errorf("--provider %q has unsupported protocol %q", name, spec.Protocol)
	}
}

// ResolveBaseURL determines the effective base URL for a selected provider,
// applying the base_url override precedence (US-004 / FR-8, FR-9). The first
// non-empty source wins, in this order:
//
//  1. flagBaseURL — the explicit --base-url/-u flag (highest).
//  2. provider-specific base-url env var(s) from spec.BaseURLEnvVars, in the
//     order the registry declares them (e.g. AZURE_OPENAI_BASE_URL).
//  3. the generic <PROVIDER>_BASE_URL env var, where <PROVIDER> is the provider
//     name uppercased with '-' rewritten to '_' (e.g. zai-coding-cn →
//     ZAI_CODING_CN_BASE_URL).
//  4. spec.DefaultBaseURL — the registry default (lowest).
//
// Values are trimmed of surrounding whitespace before the non-empty check, so a
// whitespace-only env var does not shadow a lower-precedence source. Environment
// lookups go through the injected env func so callers control the environment.
func ResolveBaseURL(spec ProviderSpec, flagBaseURL string, env func(string) string) string {
	// 1. Explicit flag wins over every env-var convention.
	if v := strings.TrimSpace(flagBaseURL); v != "" {
		return v
	}
	// 2. Provider-specific override env vars, in registry precedence order.
	for _, name := range spec.BaseURLEnvVars {
		if v := strings.TrimSpace(env(name)); v != "" {
			return v
		}
	}
	// 3. Generic <PROVIDER>_BASE_URL convention.
	if envName := config.GenericBaseURLEnvVar(spec.Name); envName != "" {
		if v := strings.TrimSpace(env(envName)); v != "" {
			return v
		}
	}
	// 4. Registry default.
	return spec.DefaultBaseURL
}
