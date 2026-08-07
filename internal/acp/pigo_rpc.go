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

// pigoCommand runs a slash command through the command registry. Commands are
// shared across ACP clients; the response carries rendered text plus any
// follow-up prompt produced by an expand/run style command.
func (d *Dispatcher) pigoCommand(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
		Command   string `json:"command"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.Command == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or command")
	}
	sess := d.manager.Get(req.SessionID)
	if sess == nil {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	name, args := parseCommandLine(req.Command)
	if cmd, ok := d.commands[name]; ok {
		text, rpcErr := cmd(context.Background(), d, sess, args)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return map[string]any{"text": text, "notifications": []any{}}, nil
	}
	if d.registry != nil {
		if c, ok := d.registry.Lookup(name); ok {
			switch {
			case c.Action != nil:
				return map[string]any{"text": c.Action(args), "notifications": []any{}}, nil
			case c.Run != nil:
				message, prompt := c.Run(args)
				return map[string]any{"text": message, "prompt": prompt, "notifications": []any{}}, nil
			case c.Expand != nil:
				expanded := c.Expand(args)
				return map[string]any{"text": expanded, "prompt": expanded, "notifications": []any{}}, nil
			}
		}
	}
	return nil, NewError(CodeMethodNotFound, "unknown command: /"+name)
}

// pigoStatus returns a rendered session status line for any ACP client.
func (d *Dispatcher) pigoStatus(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId")
	}
	sess := d.manager.Get(req.SessionID)
	if sess == nil {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	return map[string]any{"text": sessionStatusText(sess)}, nil
}

// pigoModels returns the current model plus the configured model list with
// runtime metadata. It never consults PresetCatalog or a provider registry.
func (d *Dispatcher) pigoModels(_ json.RawMessage) (any, *Error) {
	configured := d.configuredModelList()
	models := make([]map[string]any, 0, len(configured))
	for _, m := range configured {
		models = append(models, map[string]any{
			"provider":       m.Provider,
			"modelId":        m.ModelID,
			"name":           configuredModelName(m),
			"contextWindow":  m.ContextWindow,
			"maxTokens":      m.MaxTokens,
			"thinkingLevels": m.ThinkingLevels,
			"supportsImages": m.SupportsImages,
			"enabled":        m.IsEnabled(),
		})
	}
	return map[string]any{
		"currentModelId": d.model,
		"models":         models,
	}, nil
}

// pigoConfig reads or writes the configured model list. API keys are never
// echoed back; each model reports only whether one is configured.
func (d *Dispatcher) pigoConfig(params json.RawMessage) (any, *Error) {
	var req struct {
		Model       *string               `json:"model,omitempty"`
		Models      *[]config.ModelConfig `json:"models,omitempty"`
		UpsertModel *config.ModelConfig   `json:"upsertModel,omitempty"`
		DeleteModel *string               `json:"deleteModel,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewError(CodeInvalidParams, "invalid params")
	}
	if d.models == nil {
		return nil, NewError(CodeInternalError, "configured model store is not available")
	}

	if req.Models != nil {
		for _, m := range *req.Models {
			if rpcErr := validateModelConfig(m); rpcErr != nil {
				return nil, rpcErr
			}
		}
		if err := d.models.Replace(*req.Models); err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
	}
	if req.UpsertModel != nil {
		if rpcErr := validateModelConfig(*req.UpsertModel); rpcErr != nil {
			return nil, rpcErr
		}
		if _, err := d.models.Upsert(*req.UpsertModel); err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
	}
	if req.DeleteModel != nil {
		key := strings.TrimSpace(*req.DeleteModel)
		if err := d.models.Delete(key); err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		if d.models.CurrentModel() == key {
			if err := d.models.SetModel(""); err != nil {
				return nil, NewError(CodeInternalError, err.Error())
			}
		}
	}
	if req.Model != nil {
		model := strings.TrimSpace(*req.Model)
		if model != "" {
			if _, ok := d.models.Find(model); !ok {
				return nil, NewError(CodeInvalidParams, "unknown modelId: "+model)
			}
		}
		if err := d.models.SetModel(model); err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
	}

	models := make([]map[string]any, 0)
	for _, m := range d.models.List() {
		models = append(models, configuredModelResponse(m))
	}
	return map[string]any{
		"model":  d.models.CurrentModel(),
		"models": models,
	}, nil
}

func validateModelConfig(m config.ModelConfig) *Error {
	if strings.TrimSpace(m.Provider) == "" || strings.TrimSpace(m.ModelID) == "" ||
		strings.TrimSpace(m.BaseURL) == "" || strings.TrimSpace(m.Protocol) == "" {
		return NewError(CodeInvalidParams, "model requires provider, modelId, baseUrl, protocol")
	}
	return nil
}

func configuredModelResponse(m config.ModelConfig) map[string]any {
	return map[string]any{
		"provider":         m.Provider,
		"modelId":          m.ModelID,
		"name":             m.Name,
		"baseUrl":          m.BaseURL,
		"protocol":         m.Protocol,
		"apiKeyConfigured": m.APIKey != "",
		"contextWindow":    m.ContextWindow,
		"maxTokens":        m.MaxTokens,
		"thinkingLevels":   m.ThinkingLevels,
		"supportsImages":   m.SupportsImages,
		"enabled":          m.IsEnabled(),
	}
}

// pigoConfigTest sends a minimal completion request for a configured model.
// The API key stays inside pigo and is never accepted or returned.
func (d *Dispatcher) pigoConfigTest(params json.RawMessage) (any, *Error) {
	var req struct {
		ModelID string `json:"modelId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || strings.TrimSpace(req.ModelID) == "" {
		return nil, NewError(CodeInvalidParams, "missing modelId")
	}
	if d.models == nil {
		return nil, NewError(CodeInternalError, "configured model store is not available")
	}
	modelID := strings.TrimSpace(req.ModelID)
	entry, ok := d.models.Find(modelID)
	if !ok {
		return configTestFailure(0, "unknown modelId: "+modelID), nil
	}
	protocol, err := normalizeCustomProtocol(entry.Protocol)
	if err != nil {
		return configTestFailure(0, err.Error()), nil
	}
	prov, err := provider.ResolveConfiguredProvider(entry.Provider, entry.BaseURL, protocol, []provider.Model{
		{Provider: entry.Provider, ID: entry.ModelID, DisplayName: entry.Name},
	})
	if err != nil {
		return configTestFailure(0, err.Error()), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	stream, err := provider.StreamFnFromProvider(prov)(ctx, entry.ModelID, provider.LlmContext{
		SystemPrompt: "Reply with exactly: pong",
		Messages: agentcore.MessageList{
			agentcore.UserMessage{
				RoleField: agentcore.RoleUser,
				Content:   agentcore.ContentList{agentcore.NewTextContent("ping")},
			},
		},
	}, provider.StreamConfig{APIKey: entry.APIKey, ThinkingLevel: agentcore.ThinkingOff})
	if err != nil {
		return configTestFailure(time.Since(start).Milliseconds(), err.Error()), nil
	}

	responseText := ""
	for ev := range stream.Events() {
		switch e := ev.(type) {
		case provider.StreamTextEvent:
			responseText = agentcore.ContentToText(e.Partial.Content)
		case provider.StreamErrorEvent:
			details := e.Message.ErrorMessage
			if details == "" && e.Err != nil {
				details = e.Err.Error()
			}
			if details == "" {
				details = agentcore.ContentToText(e.Message.Content)
			}
			return configTestFailure(time.Since(start).Milliseconds(), details), nil
		case provider.StreamDoneEvent:
			doneText := strings.TrimSpace(agentcore.ContentToText(e.Message.Content))
			if doneText == "" {
				doneText = strings.TrimSpace(responseText)
			}
			return map[string]any{
				"success":          true,
				"response_time_ms": time.Since(start).Milliseconds(),
				"model_response":   doneText,
			}, nil
		}
	}
	return configTestFailure(time.Since(start).Milliseconds(), "stream ended without a response"), nil
}

func configTestFailure(responseTimeMs int64, details string) map[string]any {
	return map[string]any{
		"success":          false,
		"response_time_ms": responseTimeMs,
		"error_details":    details,
	}
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
	if err != nil || (meta.SessionName != "" && meta.SessionName != "Session") {
		return
	}
	rr, ok := d.runner.(*RuntimeRunner)
	if !ok || rr == nil {
		return
	}
	prov, _, apiKey, wireModel, err := d.providerForModel(sess)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := provider.StreamFnFromProvider(prov)(ctx, wireModel, provider.LlmContext{
		SystemPrompt: "You generate short coding-agent session titles. Reply with a single line, at most 40 characters, no quotes, no period.",
		Messages: agentcore.MessageList{
			agentcore.UserMessage{
				RoleField: agentcore.RoleUser,
				Content:   agentcore.ContentList{agentcore.NewTextContent("Summarize this task in one short title: " + firstPrompt)},
			},
		},
	}, provider.StreamConfig{APIKey: apiKey, ThinkingLevel: rr.ThinkingLevel})
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
	meta, err = sess.Store.LoadMetadata(sess.ID)
	if err != nil || (meta.SessionName != "" && meta.SessionName != "Session") {
		return
	}
	meta.SessionName = title
	if err := sess.Store.SaveMetadata(meta); err != nil {
		return
	}
	d.sendSessionUpdate(sess.ID, map[string]any{
		"sessionUpdate": "session_info_update",
		"title":         title,
		"updatedAt":     time.Now().UTC().Format(time.RFC3339),
	})
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
