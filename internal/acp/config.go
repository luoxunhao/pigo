package acp

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
)

// ACP config option ids, matching pi-acp's advertised selectors.
const (
	configIDModel        = "model"
	configIDThoughtLevel = "thought_level"
)

var thinkingModes = []map[string]any{
	{"id": string(agentcore.ThinkingOff), "name": "Off"},
	{"id": string(agentcore.ThinkingMinimal), "name": "Minimal"},
	{"id": string(agentcore.ThinkingLow), "name": "Low"},
	{"id": string(agentcore.ThinkingMedium), "name": "Medium"},
	{"id": string(agentcore.ThinkingHigh), "name": "High"},
	{"id": string(agentcore.ThinkingXHigh), "name": "X-High"},
	{"id": string(agentcore.ThinkingMax), "name": "Max"},
}

func sessionModels(ctx context.Context, sess *AcpSession, creds *provider.CredentialStore) map[string]any {
	current := sess.Model
	if current == "" {
		current = "openrouter/free"
	}
	available := make([]map[string]any, 0, len(provider.PresetCatalog)+1)
	seen := make(map[string]bool, len(provider.PresetCatalog)+1)
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
	if !seen[current] {
		available = append(available, map[string]any{
			"modelId":     current,
			"name":        current,
			"description": nil,
		})
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

func sessionConfigOptions(ctx context.Context, sess *AcpSession, creds *provider.CredentialStore) []map[string]any {
	models := sessionModels(ctx, sess, creds)
	modes := sessionModes(sess)
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
	return []map[string]any{
		{
			"type":         "select",
			"id":           configIDModel,
			"category":     "model",
			"name":         "Model",
			"description":  "Select the model for this session",
			"currentValue": models["currentModelId"],
			"options":      modelOptions,
		},
		{
			"type":         "select",
			"id":           configIDThoughtLevel,
			"category":     "thought_level",
			"name":         "Thinking",
			"description":  "Set the reasoning effort for this session",
			"currentValue": modes["currentModeId"],
			"options":      modeOptions,
		},
	}
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
