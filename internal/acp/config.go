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

// sessionModels builds the ACP model list strictly from the configured model
// list. There is no fallback: a model not present in config is not offered.
func sessionModels(ctx context.Context, sess *AcpSession, models []config.ModelConfig) map[string]any {
	available := make([]map[string]any, 0, len(models))
	for _, m := range models {
		if !m.IsEnabled() {
			continue
		}
		available = append(available, map[string]any{
			"modelId":        m.Key(),
			"name":           configuredModelName(m),
			"description":    nil,
			"contextWindow":  m.ContextWindow,
			"maxTokens":      m.MaxTokens,
			"thinkingLevels": m.ThinkingLevels,
			"supportsImages": m.SupportsImages,
		})
	}
	return map[string]any{
		"currentModelId":  sess.Model,
		"availableModels": available,
	}
}

func configuredModelName(m config.ModelConfig) string {
	if m.Name != "" {
		return m.Name
	}
	return m.Key()
}

func sessionModes(sess *AcpSession, model *config.ModelConfig) map[string]any {
	current := sess.Thinking
	if current == "" {
		current = string(agentcore.ThinkingMedium)
	}
	modes := thinkingModes
	if model != nil && len(model.ThinkingLevels) > 0 {
		allowed := make(map[string]bool, len(model.ThinkingLevels))
		for _, l := range model.ThinkingLevels {
			allowed[strings.ToLower(strings.TrimSpace(l))] = true
		}
		modes = nil
		for _, m := range thinkingModes {
			if id, _ := m["id"].(string); allowed[id] {
				modes = append(modes, m)
			}
		}
		if len(modes) == 0 {
			modes = thinkingModes
		}
	}
	if !containsMode(modes, current) {
		current = defaultMode(modes)
	}
	return map[string]any{
		"currentModeId":  current,
		"availableModes": modes,
	}
}

func containsMode(modes []map[string]any, id string) bool {
	for _, m := range modes {
		if mid, _ := m["id"].(string); mid == id {
			return true
		}
	}
	return false
}

func defaultMode(modes []map[string]any) string {
	if len(modes) > 0 {
		if id, _ := modes[0]["id"].(string); id != "" {
			return id
		}
	}
	return string(agentcore.ThinkingMedium)
}

func sessionConfigOptions(ctx context.Context, sess *AcpSession, models []config.ModelConfig) []map[string]any {
	modelMap := sessionModels(ctx, sess, models)
	current := currentConfiguredModel(sess, models)
	modes := sessionModes(sess, current)
	return configOptionsFromModels(modelMap, modes)
}

func currentConfiguredModel(sess *AcpSession, models []config.ModelConfig) *config.ModelConfig {
	for i := range models {
		if models[i].Key() == sess.Model {
			return &models[i]
		}
	}
	return nil
}

func (d *Dispatcher) currentConfiguredModel(sess *AcpSession) *config.ModelConfig {
	return currentConfiguredModel(sess, d.configuredModelList())
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
	modeOptions := make([]map[string]any, 0)
	if raw, ok := modes["availableModes"].([]map[string]any); ok {
		for _, m := range raw {
			modeOptions = append(modeOptions, map[string]any{
				"value":       m["id"],
				"name":        m["name"],
				"description": nil,
			})
		}
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

// modelAvailable reports whether a model id is in the configured model list.
func (d *Dispatcher) modelAvailable(ctx context.Context, sess *AcpSession, modelID string) bool {
	if d.models == nil {
		return false
	}
	_, ok := d.models.Find(modelID)
	return ok
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
