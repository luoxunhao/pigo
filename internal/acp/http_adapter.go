package acp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/smallnest/pigo/internal/httpclient"
)

// HTTPAdapter translates standard ACP methods into HTTP serve calls.
type HTTPAdapter struct {
	client    *httpclient.ClientWithResponses
	transport Transport
	version   string

	mu   sync.Mutex
	dirs map[string]string
}

// NewHTTPAdapter builds an adapter over a serve HTTP client.
func NewHTTPAdapter(client *httpclient.ClientWithResponses, transport Transport, version string) *HTTPAdapter {
	return &HTTPAdapter{client: client, transport: transport, version: version, dirs: make(map[string]string)}
}

func (a *HTTPAdapter) directory(sessionID string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	dir, ok := a.dirs[sessionID]
	return dir, ok
}

func (a *HTTPAdapter) remember(sessionID, directory string) {
	a.mu.Lock()
	a.dirs[sessionID] = directory
	a.mu.Unlock()
}

// HandleRequest dispatches a synchronous ACP request.
func (a *HTTPAdapter) HandleRequest(ctx context.Context, id RequestID, method string, params json.RawMessage) (any, *Error) {
	switch method {
	case MethodInitialize:
		return a.initialize(), nil
	case "authenticate":
		return nil, NewError(CodeInvalidParams, "unknown auth method")
	case MethodSessionNew:
		return a.sessionNew(ctx, params)
	case MethodSessionLoad:
		return a.sessionLoad(ctx, params)
	case MethodSessionList:
		return a.sessionList(ctx, params)
	case MethodSessionDelete:
		return a.sessionDelete(ctx, params)
	case MethodSessionClose:
		return a.sessionClose(ctx, params)
	case MethodSessionMode:
		return a.sessionMode(ctx, params)
	case MethodSessionConfigOpt:
		return a.sessionConfigOption(ctx, params)
	default:
		return nil, NewError(CodeMethodNotFound, "method not found: "+method)
	}
}

// HandleDeferredRequest handles session/prompt asynchronously.
func (a *HTTPAdapter) HandleDeferredRequest(ctx context.Context, id RequestID, method string, params json.RawMessage) bool {
	if method != MethodSessionPrompt {
		return false
	}
	go a.runPrompt(ctx, id, params)
	return true
}

// HandleNotification processes client notifications.
func (a *HTTPAdapter) HandleNotification(ctx context.Context, method string, params json.RawMessage) {
	if method != MethodSessionCancel {
		return
	}
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
		return
	}
	_, _ = a.client.CancelSessionPromptWithResponse(ctx, req.SessionID)
}

func (a *HTTPAdapter) initialize() map[string]any {
	return map[string]any{
		"protocolVersion": 1,
		"agentCapabilities": map[string]any{
			"loadSession": true,
			"promptCapabilities": map[string]any{
				"image":           true,
				"audio":           false,
				"embeddedContext": os.Getenv("PIGO_ACP_ENABLE_EMBEDDED_CONTEXT") == "true",
			},
			"sessionCapabilities": map[string]any{
				"list":   map[string]any{},
				"delete": map[string]any{},
				"close":  map[string]any{},
			},
			"mcpCapabilities": map[string]any{"http": false, "sse": false},
		},
		"authMethods": []any{},
		"agentInfo": map[string]any{
			"name":    "pigo",
			"title":   "pigo ACP",
			"version": a.version,
		},
	}
}

func (a *HTTPAdapter) sessionNew(ctx context.Context, params json.RawMessage) (any, *Error) {
	var req struct {
		Cwd                   string          `json:"cwd"`
		AdditionalDirectories []string        `json:"additionalDirectories,omitempty"`
		MCPServers            json.RawMessage `json:"mcpServers,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.Cwd == "" {
		return nil, NewError(CodeInvalidParams, "missing cwd")
	}
	resp, err := a.client.CreateSessionWithResponse(ctx, httpclient.CreateSessionJSONRequestBody{
		Directory:             req.Cwd,
		AdditionalDirectories: &req.AdditionalDirectories,
	})
	if err != nil || resp.JSON200 == nil {
		return nil, NewError(CodeInternalError, "session/new failed")
	}
	a.remember(resp.JSON200.SessionId, req.Cwd)
	go a.sendAvailableCommands(req.Cwd, resp.JSON200.SessionId)
	modes := a.modesState()
	return map[string]any{
		"sessionId":     resp.JSON200.SessionId,
		"configOptions": resp.JSON200.ConfigOptions,
		"modes":         modes,
	}, nil
}

func (a *HTTPAdapter) sessionLoad(ctx context.Context, params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID             string          `json:"sessionId"`
		Cwd                   string          `json:"cwd"`
		AdditionalDirectories []string        `json:"additionalDirectories,omitempty"`
		MCPServers            json.RawMessage `json:"mcpServers,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.Cwd == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or cwd")
	}
	limit := 50
	resp, err := a.client.LoadSessionWithResponse(ctx, req.SessionID, httpclient.LoadSessionJSONRequestBody{
		Directory: req.Cwd,
		Limit:     &limit,
	})
	if err != nil || resp.JSON200 == nil {
		return nil, NewError(CodeInternalError, "session/load failed")
	}
	a.remember(req.SessionID, req.Cwd)
	a.replayAll(ctx, req.SessionID, req.Cwd, resp.JSON200.Messages, resp.JSON200.NextCursor, resp.JSON200.HasMore)
	go a.sendAvailableCommands(req.Cwd, req.SessionID)
	return map[string]any{
		"configOptions": resp.JSON200.ConfigOptions,
		"modes":         a.modesState(),
	}, nil
}

func (a *HTTPAdapter) sessionList(ctx context.Context, params json.RawMessage) (any, *Error) {
	var req struct {
		Cwd string `json:"cwd"`
	}
	_ = json.Unmarshal(params, &req)
	resp, err := a.client.ListSessionsWithResponse(ctx, &httpclient.ListSessionsParams{Directory: &req.Cwd})
	if err != nil || resp.JSON200 == nil {
		return nil, NewError(CodeInternalError, "session/list failed")
	}
	sessions := make([]map[string]any, 0, len(resp.JSON200.Sessions))
	for _, s := range resp.JSON200.Sessions {
		sessions = append(sessions, map[string]any{
			"sessionId": s.SessionId,
			"cwd":       s.Directory,
			"title":     s.Title,
			"updatedAt": s.UpdatedAt,
		})
	}
	return map[string]any{"sessions": sessions}, nil
}

func (a *HTTPAdapter) sessionDelete(ctx context.Context, params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId")
	}
	dir, ok := a.directory(req.SessionID)
	if !ok {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	if _, err := a.client.DeleteSessionWithResponse(ctx, req.SessionID, &httpclient.DeleteSessionParams{Directory: dir}); err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	return map[string]any{}, nil
}

func (a *HTTPAdapter) sessionClose(ctx context.Context, params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId")
	}
	dir, ok := a.directory(req.SessionID)
	if !ok {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	if _, err := a.client.CloseSessionWithResponse(ctx, req.SessionID, &httpclient.CloseSessionParams{Directory: dir}); err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	return map[string]any{}, nil
}

func (a *HTTPAdapter) sessionMode(ctx context.Context, params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
		ModeID    string `json:"modeId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.ModeID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or modeId")
	}
	dir, ok := a.directory(req.SessionID)
	if !ok {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	if _, err := a.client.SetSessionModeWithResponse(ctx, req.SessionID, httpclient.SetSessionModeJSONRequestBody{Directory: dir, ModeId: req.ModeID}); err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	return map[string]any{}, nil
}

func (a *HTTPAdapter) sessionConfigOption(ctx context.Context, params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
		ConfigID  string `json:"configId"`
		Value     string `json:"value"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.ConfigID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or configId")
	}
	dir, ok := a.directory(req.SessionID)
	if !ok {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	var configOptions []map[string]any
	switch req.ConfigID {
	case "model", "thought_level":
		resp, err := a.client.UpdateSessionConfigWithResponse(ctx, req.SessionID, httpclient.UpdateSessionConfigJSONRequestBody{
			Directory:     dir,
			Model:         optionPtr(req.ConfigID == "model", req.Value),
			ThinkingLevel: optionPtr(req.ConfigID == "thought_level", req.Value),
		})
		if err != nil || resp.JSON200 == nil {
			return nil, NewError(CodeInternalError, "set_config_option failed")
		}
		configOptions = configOptionsFromHTTP(resp.JSON200.ConfigOptions)
	case "mode":
		if _, err := a.client.SetSessionModeWithResponse(ctx, req.SessionID, httpclient.SetSessionModeJSONRequestBody{Directory: dir, ModeId: req.Value}); err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		configOptions = []map[string]any{}
	default:
		return nil, NewError(CodeInvalidParams, "unknown config option: "+req.ConfigID)
	}
	return map[string]any{"configOptions": configOptions}, nil
}

func (a *HTTPAdapter) runPrompt(ctx context.Context, id RequestID, params json.RawMessage) {
	var req struct {
		SessionID string                   `json:"sessionId"`
		Prompt    []map[string]interface{} `json:"prompt"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
		_ = a.transport.SendResponse(ctx, id, nil, NewError(CodeInvalidParams, "missing sessionId or prompt"))
		return
	}
	dir, ok := a.directory(req.SessionID)
	if !ok {
		_ = a.transport.SendResponse(ctx, id, nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID))
		return
	}
	text := promptText(req.Prompt)
	stopReason := "end_turn"
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		resp, err := a.client.ExecuteCommandWithResponse(ctx, req.SessionID, httpclient.ExecuteCommandJSONRequestBody{Directory: dir, Command: commandName(text)})
		if err == nil && resp.JSON200 != nil {
			stopReason = resp.JSON200.StopReason
			if resp.JSON200.Text != nil {
				_ = a.transport.SendNotification(NotificationSessionUpdate, sessionUpdatePayload(req.SessionID, textChunkUpdate(*resp.JSON200.Text)))
			}
		}
	} else {
		resp, err := a.client.PromptSessionWithResponse(ctx, req.SessionID, httpclient.PromptSessionJSONRequestBody{Directory: dir, Prompt: req.Prompt})
		if err == nil && resp.JSON200 != nil {
			stopReason = resp.JSON200.StopReason
		}
	}
	_ = a.transport.SendResponse(ctx, id, map[string]any{"stopReason": stopReason}, nil)
}

func (a *HTTPAdapter) modesState() map[string]any {
	resp, err := a.client.ListModesWithResponse(context.Background(), &httpclient.ListModesParams{})
	if err != nil || resp.JSON200 == nil {
		return map[string]any{"currentModeId": "build", "availableModes": []any{}}
	}
	modes := make([]map[string]any, 0, len(resp.JSON200.Modes))
	for _, m := range resp.JSON200.Modes {
		modes = append(modes, map[string]any{"id": m.Id, "name": m.Name, "description": m.Description})
	}
	current := "build"
	if len(modes) > 0 {
		if id, ok := modes[0]["id"].(string); ok {
			current = id
		}
	}
	return map[string]any{"currentModeId": current, "availableModes": modes}
}

func (a *HTTPAdapter) sendAvailableCommands(directory, sessionID string) {
	resp, err := a.client.ListCommandsWithResponse(context.Background(), &httpclient.ListCommandsParams{Directory: &directory})
	if err != nil || resp.JSON200 == nil {
		return
	}
	_ = a.transport.SendNotification(NotificationSessionUpdate, sessionUpdatePayload(sessionID, map[string]any{
		"sessionUpdate":     "available_commands_update",
		"availableCommands": resp.JSON200.Commands,
	}))
}

func (a *HTTPAdapter) replayAll(ctx context.Context, sessionID, directory string, messages []httpclient.Message, nextCursor *string, hasMore bool) {
	for {
		for _, msg := range messages {
			text := messageText(msg)
			if text == "" {
				continue
			}
			update := "user_message_chunk"
			if msg.Role == "assistant" {
				update = "agent_message_chunk"
			}
			_ = a.transport.SendNotification(NotificationSessionUpdate, sessionUpdatePayload(sessionID, map[string]any{
				"sessionUpdate": update,
				"content":       map[string]any{"type": "text", "text": text},
			}))
		}
		if !hasMore || nextCursor == nil {
			return
		}
		limit := 50
		resp, err := a.client.GetSessionMessagesWithResponse(ctx, sessionID, &httpclient.GetSessionMessagesParams{
			Directory: directory,
			Before:    nextCursor,
			Limit:     &limit,
		})
		if err != nil || resp.JSON200 == nil {
			return
		}
		messages = resp.JSON200.Messages
		nextCursor = resp.JSON200.NextCursor
		hasMore = resp.JSON200.HasMore
	}
}

func promptText(blocks []map[string]interface{}) string {
	var text string
	for _, block := range blocks {
		if t, ok := block["text"].(string); ok {
			if text != "" {
				text += "\n"
			}
			text += t
		}
	}
	return text
}

func commandName(line string) string {
	line = strings.TrimSpace(strings.TrimPrefix(line, "/"))
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return line[:i]
	}
	return line
}

func optionPtr(ok bool, value string) *string {
	if !ok {
		return nil
	}
	return &value
}

func configOptionsFromHTTP(options []httpclient.ConfigOption) []map[string]any {
	out := make([]map[string]any, 0, len(options))
	for _, o := range options {
		item := map[string]any{"id": o.Id, "name": o.Name, "type": o.Type}
		if o.CurrentValue != nil {
			item["currentValue"] = *o.CurrentValue
		}
		if o.Options != nil {
			item["options"] = *o.Options
		}
		out = append(out, item)
	}
	return out
}

func messageText(msg httpclient.Message) string {
	for _, block := range msg.Content {
		if t, ok := block["text"].(string); ok {
			return t
		}
	}
	return ""
}
