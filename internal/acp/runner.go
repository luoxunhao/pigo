package acp

import (
	"context"
	"io"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
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
	// Snap is the rewind journal shared with the write/edit tools. When nil the
	// runner discovers it from the tool set.
	Snap *agenttool.FileSnapshotRecorder
}

// Run executes one prompt turn. It appends the user message to a copy of the
// history, runs the loop, and returns the resulting message list plus the
// final assistant message. Agent events are streamed through onEvent as they
// are emitted by the loop.
func (r *RuntimeRunner) Run(ctx context.Context, prompt string, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent)) (agentcore.MessageList, *agentcore.AssistantMessage, error) {
	if model == "" {
		model = r.Model
	}
	level := r.ThinkingLevel
	if thinking != "" {
		level = agentcore.ThinkingLevel(thinking)
	}
	msgs := make(agentcore.MessageList, 0, len(history)+1)
	msgs = append(msgs, history...)
	msgs = append(msgs, agentcore.UserMessage{
		RoleField: agentcore.RoleUser,
		Content:   agentcore.ContentList{agentcore.NewTextContent(prompt)},
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
			Model:         model,
			Provider:      r.ProviderName,
			APIKey:        r.APIKey,
			ThinkingLevel: level,
			Stream:        provider.StreamFnFromProvider(r.Provider),
		},
		Batch: agenttool.BatchConfig{
			ToolExecutorConfig: agenttool.ToolExecutorConfig{Registry: reg, BeforeToolCall: beforeToolCall},
		},
	}

	headless := runtime.HeadlessConfig{
		Mode:    runtime.PrintMode,
		Out:     io.Discard,
		Run:     cfg,
		OnEvent: onEvent,
	}
	err := runtime.RunHeadless(ctx, agentCtx, headless)
	if snap := r.snapshotRecorder(); err == nil && snap != nil {
		snap.Commit("", "acp turn")
	}
	last := agentcore.LastAssistantOf(agentCtx.Messages)
	return agentCtx.Messages, last, err
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
