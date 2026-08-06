package acp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/provider"
)

// pigoModels returns the current model plus the built-in preset catalog so a
// client can render a model picker without knowing pigo's registry internals.
func (d *Dispatcher) pigoModels(params json.RawMessage) (any, *Error) {
	models := make([]any, 0, len(provider.PresetCatalog))
	seen := map[string]bool{}
	for _, p := range provider.PresetCatalog {
		key := p.Provider + "/" + p.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		models = append(models, map[string]any{
			"provider":    p.Provider,
			"modelId":     p.ID,
			"displayName": p.Label(),
		})
	}
	return map[string]any{
		"currentModelId": d.model,
		"models":         models,
	}, nil
}

// pigoConfig reads or writes pigo's config.toml. API keys are never echoed
// back; the response only reports whether one is configured. Writes report
// needsRestart because provider resolution happens at process startup.
func (d *Dispatcher) pigoConfig(params json.RawMessage) (any, *Error) {
	var req struct {
		Model         *string `json:"model,omitempty"`
		BaseURL       *string `json:"baseUrl,omitempty"`
		APIKey        *string `json:"apiKey,omitempty"`
		Protocol      *string `json:"protocol,omitempty"`
		Provider      *string `json:"provider,omitempty"`
		ThinkingLevel *string `json:"thinkingLevel,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewError(CodeInvalidParams, "invalid params")
	}
	path := config.FileConfigPath()
	cfg, err := config.LoadFileConfig(path)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}

	wrote := req.Model != nil || req.BaseURL != nil || req.APIKey != nil ||
		req.Protocol != nil || req.Provider != nil || req.ThinkingLevel != nil
	if wrote {
		if req.Model != nil {
			cfg.Model = *req.Model
		}
		if req.BaseURL != nil {
			cfg.BaseURL = *req.BaseURL
		}
		if req.APIKey != nil {
			cfg.APIKey = *req.APIKey
		}
		if req.Protocol != nil {
			cfg.Protocol = *req.Protocol
		}
		if req.Provider != nil {
			cfg.Provider = *req.Provider
		}
		if req.ThinkingLevel != nil {
			cfg.ThinkingLevel = *req.ThinkingLevel
		}
		if err := config.SaveFileConfig(path, cfg); err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
	}

	return map[string]any{
		"model":            cfg.Model,
		"baseUrl":          cfg.BaseURL,
		"protocol":         cfg.Protocol,
		"provider":         cfg.Provider,
		"thinkingLevel":    cfg.ThinkingLevel,
		"apiKeyConfigured": cfg.APIKey != "",
		"configPath":       path,
		"needsRestart":     wrote,
	}, nil
}

// pigoMessages pages through a session's persisted transcript. before is the
// id of the oldest message the client already has; limit defaults to 50.
func (d *Dispatcher) pigoMessages(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
		Before    string `json:"before,omitempty"`
		Limit     int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId")
	}
	sess := d.manager.Get(req.SessionID)
	if sess == nil {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	_, entries, err := sess.Store.TranscriptStore().LoadEntries(sess.ID)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	end := len(entries)
	if req.Before != "" {
		for i, e := range entries {
			if e.ID == req.Before {
				end = i
				break
			}
		}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	messages := make([]any, 0, end-start)
	for _, e := range entries[start:end] {
		messages = append(messages, entryToACPMessage(e))
	}
	nextCursor := any(nil)
	if start > 0 && len(messages) > 0 {
		if m, ok := messages[0].(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				nextCursor = id
			}
		}
	}
	return map[string]any{
		"messages":   messages,
		"hasMore":    start > 0,
		"nextCursor": nextCursor,
	}, nil
}

// generateTitle names a session after its first turn using a lightweight
// provider call. It is best-effort: failures keep the default session name.
func (d *Dispatcher) generateTitle(sess *AcpSession, firstPrompt string) {
	if d.runner == nil {
		return
	}
	meta, err := sess.Store.LoadMetadata(sess.ID)
	if err != nil || meta.SessionName != "Session" {
		return
	}
	rr, ok := d.runner.(*RuntimeRunner)
	if !ok || rr.Provider == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := provider.StreamFnFromProvider(rr.Provider)(ctx, rr.Model, provider.LlmContext{
		SystemPrompt: "You generate short coding-agent session titles. Reply with a single line, at most 40 characters, no quotes, no period.",
		Messages: agentcore.MessageList{
			agentcore.UserMessage{
				RoleField: agentcore.RoleUser,
				Content:   agentcore.ContentList{agentcore.NewTextContent("Summarize this task in one short title: " + firstPrompt)},
			},
		},
	}, provider.StreamConfig{APIKey: rr.APIKey, ThinkingLevel: rr.ThinkingLevel})
	if err != nil {
		return
	}
	title := ""
	for ev := range stream.Events() {
		switch e := ev.(type) {
		case provider.StreamTextEvent:
			title = agentcore.ContentToText(e.Partial.Content)
		case provider.StreamDoneEvent:
			title = agentcore.ContentToText(e.Message.Content)
		}
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	meta.SessionName = title
	_ = sess.Store.SaveMetadata(meta)
}

// applyAdditionalDirectories merges workspace-level extra roots into the file
// tools. The ACP process is scoped to one workspace, so a process-wide merge
// matches the pi-web workspace configuration model.
func (d *Dispatcher) applyAdditionalDirectories(dirs []string) {
	if len(dirs) == 0 {
		return
	}
	rr, ok := d.runner.(*RuntimeRunner)
	if !ok {
		return
	}
	d.extraRootsMu.Lock()
	defer d.extraRootsMu.Unlock()
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		for _, tool := range rr.Tools {
			switch t := tool.(type) {
			case *agenttool.ReadTool:
				t.ExtraRoots = appendUniquePath(t.ExtraRoots, abs)
			case *agenttool.WriteTool:
				t.ExtraRoots = appendUniquePath(t.ExtraRoots, abs)
			case *agenttool.EditTool:
				t.ExtraRoots = appendUniquePath(t.ExtraRoots, abs)
			}
		}
	}
}

func appendUniquePath(list []string, path string) []string {
	for _, existing := range list {
		if filepath.Clean(existing) == filepath.Clean(path) {
			return list
		}
	}
	return append(list, path)
}
