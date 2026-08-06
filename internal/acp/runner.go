package acp

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/compaction"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
)

// RuntimeRunner drives pigo's real agent loop through runtime.RunHeadless. It
// is the production SessionRunner used by the ACP server.
type RuntimeRunner struct {
	Provider      provider.Provider
	ProviderName  string
	Model         string
	APIKey        string
	ThinkingLevel agentcore.ThinkingLevel
	Tools         []agentcore.AgentTool
	// Compaction is the auto-compaction policy used for every ACP-driven run.
	// The zero value selects compaction.DefaultCompactionSettings.
	Compaction compaction.CompactionSettings
	// ContextWindow overrides the model's advertised context window. Zero
	// resolves the window from the provider's model catalog.
	ContextWindow int
	// Snap is the rewind journal shared with the write/edit tools. When nil the
	// runner discovers it from the tool set.
	Snap *agenttool.FileSnapshotRecorder
	// ConfiguredModels is the shared configured-model store used to resolve
	// provider/model_id entries at run time.
	ConfiguredModels *ConfiguredModels
}

// Run executes one prompt turn. It appends the user message to a copy of the
// history, runs the loop, and returns the resulting message list plus the
// final assistant message. Agent events are streamed through onEvent as they
// are emitted by the loop.
func (r *RuntimeRunner) Run(ctx context.Context, prompt string, images []agentcore.Content, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error) {
	if model == "" {
		model = r.Model
	}
	prov, providerName, apiKey, wireModel, err := r.ResolveForModel(model)
	if err != nil {
		return nil, nil, err
	}
	level := r.ThinkingLevel
	if thinking != "" {
		level = agentcore.ThinkingLevel(thinking)
	}
	msgs := make(agentcore.MessageList, 0, len(history)+1)
	msgs = append(msgs, history...)
	content := agentcore.ContentList{agentcore.NewTextContent(prompt)}
	content = append(content, images...)
	msgs = append(msgs, agentcore.UserMessage{
		RoleField: agentcore.RoleUser,
		Content:   content,
	})

	agentCtx := &agentcore.AgentContext{
		SystemPrompt: sysPrompt,
		Messages:     msgs,
		Tools:        r.Tools,
	}

	reg := agenttool.NewToolRegistry()
	for _, tool := range r.Tools {
		_ = reg.Register(tool)
	}

	cfg := runtime.RunConfig{
		LoopConfig: runtime.LoopConfig{
			Model:         wireModel,
			Provider:      providerName,
			APIKey:        apiKey,
			ThinkingLevel: level,
			Stream:        provider.StreamFnFromProvider(prov),
			ContextWindow: r.effectiveContextWindow(wireModel, prov),
			Compaction:    r.effectiveCompaction(),
		},
		Batch: agenttool.BatchConfig{
			ToolExecutorConfig: agenttool.ToolExecutorConfig{Registry: reg, BeforeToolCall: beforeToolCall},
		},
	}
	if hooks.Steering != nil {
		cfg.GetSteeringMessages = hooks.Steering
	}
	if hooks.FollowUp != nil {
		cfg.GetFollowUpMessages = hooks.FollowUp
	}

	headless := runtime.HeadlessConfig{
		Mode:    runtime.PrintMode,
		Out:     io.Discard,
		Run:     cfg,
		OnEvent: onEvent,
	}
	err = runtime.RunHeadless(ctx, agentCtx, headless)
	if snap := r.snapshotRecorder(); err == nil && snap != nil {
		snap.Commit("", "acp turn")
	}
	last := agentcore.LastAssistantOf(agentCtx.Messages)
	return agentCtx.Messages, last, err
}

// ResolveForModel returns the provider, provider name, API key, and wire model
// id for a session model id. Custom ids are resolved from the registry; every
// other id uses the startup provider.
func (r *RuntimeRunner) ResolveForModel(model string) (provider.Provider, string, string, string, error) {
	if model == "" {
		model = r.Model
	}
	if _, _, ok := config.SplitModelID(model); ok && r.ConfiguredModels != nil {
		entry, found := r.ConfiguredModels.Find(model)
		if !found {
			return nil, "", "", "", fmt.Errorf("model %q is not configured", model)
		}
		wireModel := entry.ModelID
		if wireModel == "" {
			_, wireModel, _ = config.SplitModelID(model)
		}
		if entry.Protocol == provider.ProtocolGemini {
			return nil, "", "", "", fmt.Errorf("gemini runtime is not implemented")
		}
		prov, err := provider.ResolveConfiguredProvider(entry.Provider, entry.BaseURL, entry.Protocol, []provider.Model{
			{Provider: entry.Provider, ID: wireModel, DisplayName: entry.Name},
		})
		if err != nil {
			return nil, "", "", "", err
		}
		return prov, entry.Provider, entry.APIKey, wireModel, nil
	}
	if r.Provider == nil {
		return nil, "", "", "", fmt.Errorf("no provider configured")
	}
	return r.Provider, r.ProviderName, r.APIKey, model, nil
}

func (r *RuntimeRunner) effectiveCompaction() compaction.CompactionSettings {
	if r.Compaction.Enabled || r.Compaction.ReserveTokens > 0 || r.Compaction.KeepRecentTokens > 0 {
		return r.Compaction
	}
	return compaction.DefaultCompactionSettings
}

func (r *RuntimeRunner) effectiveContextWindow(model string, prov provider.Provider) int {
	if r.ContextWindow > 0 {
		return r.ContextWindow
	}
	if prov == nil {
		return 0
	}
	for _, m := range prov.Models() {
		if m.ID == model && m.ContextWindow > 0 {
			return m.ContextWindow
		}
	}
	return 0
}

// providerForModel exposes the runtime resolution to slash commands and title
// generation through the dispatcher.
func (d *Dispatcher) providerForModel(sess *AcpSession) (provider.Provider, string, string, string, error) {
	rr, ok := d.runner.(*RuntimeRunner)
	if !ok || rr == nil {
		return nil, "", "", "", fmt.Errorf("runtime runner is not available")
	}
	model := sess.Model
	if strings.TrimSpace(model) == "" {
		model = rr.Model
	}
	return rr.ResolveForModel(model)
}

// snapshotRecorder returns the rewind journal shared by the tool set, using
// the explicit Snap when set.
func (r *RuntimeRunner) snapshotRecorder() *agenttool.FileSnapshotRecorder {
	if r.Snap != nil {
		return r.Snap
	}
	for _, tool := range r.Tools {
		switch tt := tool.(type) {
		case *agenttool.WriteTool:
			if tt.Snap != nil {
				return tt.Snap
			}
		case *agenttool.EditTool:
			if tt.Snap != nil {
				return tt.Snap
			}
		}
	}
	return nil
}
