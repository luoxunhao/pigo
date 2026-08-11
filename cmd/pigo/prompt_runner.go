package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/cli/prompts"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

func httpDefaultEnabled() bool {
	return os.Getenv("PIGO_HTTP_DEFAULT") != "0"
}

func httpServeConfig(opts cliOptions) (httpapi.Config, error) {
	return httpServeConfigWithAutoReject(opts, true)
}

func httpServeConfigWithAutoReject(opts cliOptions, autoReject bool) (httpapi.Config, error) {
	runner, slash, err := makePromptRunner(opts)
	if err != nil {
		return httpapi.Config{}, err
	}
	pigoHome, err := sessionstore.PigoHome()
	if err != nil {
		return httpapi.Config{}, err
	}
	var approveDirs []string
	if opts.approve {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			approveDirs = append(approveDirs, cwd)
		}
	}
	return httpapi.Config{
		Version:             version,
		PigoHome:            pigoHome,
		ConfigPath:          config.FileConfigPath(),
		TrustPath:           trust.DefaultPath(),
		PromptRunner:        runner,
		AutoRejectUntrusted: autoReject,
		ApproveDirectories:  approveDirs,
		SlashRegistry:       slash,
	}, nil
}

func makePromptRunner(opts cliOptions) (httpapi.PromptRunner, *runtime.SlashRegistry, error) {
	env, err := run.SetupEnv(
		opts.model,
		opts.baseURL,
		opts.protocol,
		opts.provider,
		opts.apiKey,
		opts.noTools,
		opts.noSkills,
		opts.systemPrompt,
		opts.appendSystemPrompt,
		opts.memory.Memory.Enabled,
		run.NewToolPolicy(opts.allowedTools, opts.disallowedTools),
	)
	if err != nil {
		return nil, nil, err
	}
	models := acp.NewConfiguredModels(config.FileConfigPath())
	_ = models.Load()
	thinking := agentcore.ThinkingMedium
	if opts.thinkingLevel != "" {
		thinking = agentcore.ThinkingLevel(opts.thinkingLevel)
	}
	live := &cli.LiveConfig{
		Model:         env.Model,
		ProviderName:  env.ProviderName,
		Provider:      env.Provider,
		BaseURL:       opts.baseURL,
		Protocol:      opts.protocol,
		ThinkingLevel: thinking,
		ContextWindow: cli.DefaultContextWindow,
	}
	projectDir := ""
	if env.Cwd != "" {
		projectDir = filepath.Join(env.Cwd, ".pigo", "prompts")
	}
	projectTrusted := opts.approve
	if mgr, mgrErr := trust.NewManager(trust.DefaultPath()); mgrErr == nil && mgr.IsTrusted(env.Cwd) {
		projectTrusted = true
	}
	slash, err := prompts.BuildSlashRegistry(live, env.Skills, env.Plugins, prompts.PromptTemplateSources{
		Settings:       opts.configPrompts,
		CLI:            opts.promptTemplates,
		Disable:        opts.noPromptTemplates,
		ProjectDir:     projectDir,
		ProjectTrusted: projectTrusted,
	})
	if err != nil {
		return nil, nil, err
	}
	runner := &acp.RuntimeRunner{
		Provider:         env.Provider,
		ProviderName:     env.ProviderName,
		Model:            env.Model,
		APIKey:           env.APIKey,
		ThinkingLevel:    thinking,
		Tools:            env.Tools,
		ConfiguredModels: models,
	}
	mapper := &serveEventMapper{sessions: make(map[string]*serveEventState)}
	pigoHome, _ := sessionstore.PigoHome()
	return func(ctx context.Context, run httpapi.PromptRun) (gen.PromptResponse, error) {
		model := run.Model
		if model == "" {
			model = env.Model
		}
		thinking := run.ThinkingLevel
		if thinking == "" {
			thinking = opts.thinkingLevel
		}
		if thinking == "" {
			thinking = string(agentcore.ThinkingMedium)
		}

		var history agentcore.MessageList
		store, storeErr := sessionstore.OpenForWorkspace(pigoHome, run.Directory)
		var header session.SessionHeader
		if storeErr == nil {
			if _, h, msgs, loadErr := store.Load(run.SessionID); loadErr == nil {
				history = msgs
				header = h
			}
		}

		onEvent := func(ev agentcore.AgentEvent) {
			mapper.publish(run.SessionID, run.MessageID, run.Publish, ev)
		}
		msgs, last, err := runner.RunWithTools(ctx, run.Text, nil, history, env.SysPrompt, env.Tools, model, thinking, run.BeforeToolCall, onEvent, acp.TurnHooks{})
		if err != nil {
			return gen.PromptResponse{}, err
		}
		if store != nil && len(msgs) > 0 {
			tail := msgs
			if len(history) > 0 && len(msgs) >= len(history) {
				tail = msgs[len(history):]
			}
			header.ID = run.SessionID
			header.Model = model
			header.Provider = runner.ProviderName
			header.SystemPrompt = env.SysPrompt
			header.Cwd = run.Directory
			header.UpdatedAt = time.Now().UTC()
			if len(msgs) < len(history) {
				_ = store.TranscriptStore().Save(header, msgs)
			} else {
				_ = store.Append(run.SessionID, header.UpdatedAt, tail)
			}
			if meta, metaErr := store.LoadMetadata(run.SessionID); metaErr == nil {
				meta.ModelName = model
				meta.LastActiveAt = header.UpdatedAt
				if len(msgs) < len(history) {
					meta.MessageCount = len(msgs)
				}
				_ = store.SaveMetadata(meta)
			}
		}
		reply := ""
		if last != nil {
			reply = agentcore.ContentToText(last.Content)
		}
		return gen.PromptResponse{
			MessageId:  run.MessageID,
			StopReason: "end_turn",
			Text:       &reply,
		}, nil
	}, slash, nil
}

// serveEventState tracks the last observed cumulative text/thinking so domain
// events carry deltas, matching the streaming contract of the SSE stream.
type serveEventState struct {
	text    string
	thought string
}

func (s *serveEventState) textDelta(full string) string {
	if strings.HasPrefix(full, s.text) {
		delta := full[len(s.text):]
		s.text = full
		return delta
	}
	s.text = full
	return full
}

func (s *serveEventState) thoughtDelta(full string) string {
	if strings.HasPrefix(full, s.thought) {
		delta := full[len(s.thought):]
		s.thought = full
		return delta
	}
	s.thought = full
	return full
}

// serveEventMapper converts agentcore events into the domain events consumed
// by the SSE stream, TUI, and the ACP adapter.
type serveEventMapper struct {
	mu       sync.Mutex
	sessions map[string]*serveEventState
}

func (m *serveEventMapper) state(sessionID string) *serveEventState {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		s = &serveEventState{}
		m.sessions[sessionID] = s
	}
	return s
}

func (m *serveEventMapper) publish(sessionID, messageID string, publish func(string, map[string]any), ev agentcore.AgentEvent) {
	if publish == nil {
		return
	}
	switch e := ev.(type) {
	case agentcore.AgentEndEvent:
		m.mu.Lock()
		delete(m.sessions, sessionID)
		m.mu.Unlock()
	case agentcore.MessageUpdateEvent:
		switch se := e.AssistantMessageEvent.(type) {
		case provider.StreamTextEvent:
			full := agentcore.ContentToText(se.Partial.Content)
			if delta := m.state(sessionID).textDelta(full); delta != "" {
				publish("message.part.delta", map[string]any{"partId": "text", "delta": delta})
			}
		case provider.StreamThinkingEvent:
			full := thinkingText(se.Partial.Content)
			if delta := m.state(sessionID).thoughtDelta(full); delta != "" {
				publish("message.part.delta", map[string]any{"partId": "thinking", "thinking": delta})
			}
		}
	case agentcore.ToolExecutionPendingEvent:
		publish("tool.updated", toolUpdateData(e.ToolCallID, e.ToolName, "pending", e.Args, ""))
	case agentcore.ToolExecutionStartEvent:
		publish("tool.updated", toolUpdateData(e.ToolCallID, e.ToolName, "in_progress", e.Args, ""))
	case agentcore.ToolExecutionUpdateEvent:
		publish("tool.updated", toolUpdateData(e.ToolCallID, e.ToolName, "in_progress", nil, agentcore.ContentToText(e.PartialResult.Content)))
	case agentcore.SubAgentProgressEvent:
		publish("tool.updated", toolUpdateData(e.ToolCallID, "task", "in_progress", nil, e.Activity))
	case agentcore.ToolExecutionEndEvent:
		status := "completed"
		if e.IsError {
			status = "failed"
		}
		publish("tool.updated", toolUpdateData(e.ToolCallID, e.ToolName, status, nil, agentcore.ContentToText(e.Result.Content)))
	case agentcore.CompactionStartEvent:
		publish("session.status", map[string]any{"status": "compacting"})
	case agentcore.CompactionEvent:
		status := "compacted"
		if e.ErrorMessage != "" {
			status = "compaction_failed"
		}
		data := map[string]any{"status": status}
		if e.ErrorMessage != "" {
			data["error"] = e.ErrorMessage
		}
		publish("session.status", data)
	case agentcore.TelemetryEvent:
		publish("session.status", map[string]any{
			"status": "telemetry",
			"contextUsage": map[string]any{
				"used": e.ContextTokens,
				"size": e.ContextWindow,
			},
		})
	}
}

func toolUpdateData(id, name, status string, args any, output string) map[string]any {
	data := map[string]any{
		"toolCallId": id,
		"title":      name,
		"status":     status,
	}
	if args != nil {
		data["rawInput"] = args
	}
	if output != "" {
		data["output"] = output
	}
	return data
}

func thinkingText(content agentcore.ContentList) string {
	var b strings.Builder
	for _, c := range content {
		if tc, ok := c.(agentcore.ThinkingContent); ok {
			b.WriteString(tc.Thinking)
		}
	}
	return b.String()
}
