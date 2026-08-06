package acp

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
)

// ACP config option ids, matching pi-acp's advertised selectors.
const (
	configIDModel        = "model"
	configIDThoughtLevel = "thought_level"
)

var thinkingModes = []map[string]any{
	{"id": string(agentcore.ThinkingOff), "name": "Thinking: off"},
	{"id": string(agentcore.ThinkingMinimal), "name": "Thinking: minimal"},
	{"id": string(agentcore.ThinkingLow), "name": "Thinking: low"},
	{"id": string(agentcore.ThinkingMedium), "name": "Thinking: medium"},
	{"id": string(agentcore.ThinkingHigh), "name": "Thinking: high"},
	{"id": string(agentcore.ThinkingXHigh), "name": "Thinking: xhigh"},
}

func sessionModels(ctx context.Context, sess *AcpSession, creds *provider.CredentialStore, custom []config.ProviderConfig) map[string]any {
	current := sess.Model
	if current == "" {
		current = "openrouter/free"
	}
	available := make([]map[string]any, 0, len(provider.PresetCatalog)+8)
	seen := make(map[string]bool, len(provider.PresetCatalog)+8)
	for _, m := range provider.PresetCatalog {
		if !providerConfigured(ctx, m.Provider, creds) {
			continue
		}
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		available = append(available, map[string]any{
			"modelId":     m.ID,
			"name":        m.Label(),
			"description": nil,
		})
	}
	for _, p := range custom {
		for _, m := range p.Models {
			id := p.ID + "/" + m.ModelID
			if seen[id] {
				continue
			}
			seen[id] = true
			available = append(available, map[string]any{
				"modelId":     id,
				"name":        m.Name,
				"description": nil,
			})
		}
	}
	return map[string]any{
		"currentModelId":  current,
		"availableModels": available,
	}
}

func sessionModes(sess *AcpSession) map[string]any {
	current := sess.Thinking
	if current == "" {
		current = string(agentcore.ThinkingMedium)
	}
	return map[string]any{
		"currentModeId":  current,
		"availableModes": thinkingModes,
	}
}

func sessionConfigOptions(ctx context.Context, sess *AcpSession, creds *provider.CredentialStore, custom []config.ProviderConfig) []map[string]any {
	models := sessionModels(ctx, sess, creds, custom)
	modes := sessionModes(sess)
	return configOptionsFromModels(models, modes)
}

// configOptionsFromModels builds the ACP config option list. The model option
// is omitted entirely when there are no available models, matching pi-acp.
func configOptionsFromModels(models, modes map[string]any) []map[string]any {
	modelOptions := make([]map[string]any, 0)
	if raw, ok := models["availableModels"].([]map[string]any); ok {
		for _, m := range raw {
			modelOptions = append(modelOptions, map[string]any{
				"value":       m["modelId"],
				"name":        m["name"],
				"description": m["description"],
			})
		}
	}
	modeOptions := make([]map[string]any, 0, len(thinkingModes))
	for _, m := range thinkingModes {
		modeOptions = append(modeOptions, map[string]any{
			"value":       m["id"],
			"name":        m["name"],
			"description": nil,
		})
	}
	opts := make([]map[string]any, 0, 2)
	if len(modelOptions) > 0 {
		opts = append(opts, map[string]any{
			"type":         "select",
			"id":           configIDModel,
			"category":     "model",
			"name":         "Model",
			"description":  "Select the model for this session",
			"currentValue": models["currentModelId"],
			"options":      modelOptions,
		})
	}
	opts = append(opts, map[string]any{
		"type":         "select",
		"id":           configIDThoughtLevel,
		"category":     "thought_level",
		"name":         "Thinking",
		"description":  "Set the reasoning effort for this session",
		"currentValue": modes["currentModeId"],
		"options":      modeOptions,
	})
	return opts
}

// modelAvailable reports whether a model id is in the session's available
// model list (configured built-ins plus custom provider cache).
func (d *Dispatcher) modelAvailable(ctx context.Context, sess *AcpSession, modelID string) bool {
	models := sessionModels(ctx, sess, d.credStore, d.customProviderList())
	raw, ok := models["availableModels"].([]map[string]any)
	if !ok {
		return false
	}
	for _, m := range raw {
		if id, _ := m["modelId"].(string); id == modelID {
			return true
		}
	}
	// Accept provider/modelId for built-in providers: pigo allows any model id
	// under a configured provider, matching its "any valid id for a known
	// provider still works" contract. Custom providers must match the cache
	// exactly, so they are handled by the exact match above.
	if strings.HasPrefix(modelID, "custom-") {
		return false
	}
	providerName, _, found := strings.Cut(modelID, "/")
	if !found || strings.TrimSpace(providerName) == "" {
		return false
	}
	if _, known := provider.LookupProviderSpec(providerName); known && providerConfigured(ctx, providerName, d.credStore) {
		return true
	}
	return false
}

func providerConfigured(ctx context.Context, name string, creds *provider.CredentialStore) bool {
	for _, p := range provider.PresetProviders {
		if p.Name == name {
			if p.EnvVar == "" {
				return true
			}
			break
		}
	}
	if creds == nil {
		creds = provider.NewCredentialStore(nil)
	}
	return creds.HasCredential(ctx, name)
}

// availableCommandsPayload renders the full slash surface for
// available_commands_update. It merges the extension command map with the
// runtime slash registry when one is wired.
func availableCommandsPayload(commands map[string]commandFunc, registry *runtime.SlashRegistry) []map[string]any {
	seen := make(map[string]bool, len(commands)+32)
	out := make([]map[string]any, 0, len(commands)+32)
	if registry != nil {
		for _, c := range registry.List() {
			if seen[c.Name] {
				continue
			}
			seen[c.Name] = true
			out = append(out, map[string]any{
				"name":        c.Name,
				"description": c.Description,
			})
		}
	}
	names := make([]string, 0, len(commands))
	for name := range commands {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, map[string]any{
			"name":        name,
			"description": fmt.Sprintf("pigo command: /%s", name),
		})
	}
	return out
}

func startupInfoText(version string, sess *AcpSession, commandCount int) string {
	quiet := os.Getenv("PIGO_QUIET_STARTUP") == "true"
	if quiet {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "pigo %s\n", version)
	fmt.Fprintf(&b, "model: %s\n", sess.Model)
	fmt.Fprintf(&b, "cwd: %s\n", sess.Cwd)
	fmt.Fprintf(&b, "slash commands: %d", commandCount)
	return b.String()
}
