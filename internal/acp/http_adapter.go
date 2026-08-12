package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/httpclient"
)

// HTTPAdapter translates standard ACP methods into HTTP serve calls.
type HTTPAdapter struct {
	client    *httpclient.ClientWithResponses
	transport Transport
	version   string

	mu      sync.Mutex
	dirs    map[string]string
	cursors map[string]int64
	tree    map[string]sessionTreeState
	treeEnabled bool
}

type sessionTreeState struct {
	CurrentLeafID string
	CurrentLane   string
	Lanes         []map[string]any
}

// NewHTTPAdapter builds an adapter over a serve HTTP client.
func NewHTTPAdapter(client *httpclient.ClientWithResponses, transport Transport, version string) *HTTPAdapter {
	return &HTTPAdapter{client: client, transport: transport, version: version, dirs: make(map[string]string), cursors: make(map[string]int64), tree: make(map[string]sessionTreeState)}
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

func (a *HTTPAdapter) cursor(sessionID string) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cursors[sessionID]
}

func (a *HTTPAdapter) setCursor(sessionID string, id int64) {
	a.mu.Lock()
	a.cursors[sessionID] = id
	a.mu.Unlock()
}

// HandleRequest dispatches a synchronous ACP request.
func (a *HTTPAdapter) HandleRequest(ctx context.Context, id RequestID, method string, params json.RawMessage) (any, *Error) {
	switch method {
	case MethodInitialize:
		return a.initialize(params), nil
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

func (a *HTTPAdapter) initialize(params json.RawMessage) map[string]any {
	var req struct {
		ClientCapabilities struct {
			Meta struct {
				Pigo struct {
					SessionTree struct {
						Version int `json:"version"`
					} `json:"sessionTree"`
				} `json:"pigo"`
			} `json:"_meta"`
		} `json:"clientCapabilities"`
	}
	treeVersion := 0
	if json.Unmarshal(params, &req) == nil {
		treeVersion = req.ClientCapabilities.Meta.Pigo.SessionTree.Version
	}
	a.mu.Lock()
	a.treeEnabled = treeVersion == 1
	a.mu.Unlock()
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
			"_meta": map[string]any{
				"pigo": map[string]any{
					"sessionTree": map[string]any{"version": 1},
				},
			},
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
		LeafID                *string         `json:"leafId,omitempty"`
		MCPServers            json.RawMessage `json:"mcpServers,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.Cwd == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or cwd")
	}
	limit := 50
	resp, err := a.client.LoadSessionWithResponse(ctx, req.SessionID, httpclient.LoadSessionJSONRequestBody{
		Directory: req.Cwd,
		Limit:     &limit,
		LeafId:    req.LeafID,
	})
	if err != nil || resp.JSON200 == nil {
		return nil, NewError(CodeInternalError, "session/load failed")
	}
	a.remember(req.SessionID, req.Cwd)
	a.replayAll(ctx, req.SessionID, req.Cwd, resp.JSON200.Messages, resp.JSON200.NextCursor, resp.JSON200.HasMore)
	go a.sendAvailableCommands(req.Cwd, req.SessionID)
	a.refreshTree(ctx, req.SessionID, req.Cwd)
	return map[string]any{
		"sessionId":     req.SessionID,
		"configOptions": resp.JSON200.ConfigOptions,
		"modes":         a.modesState(),
	}, nil
}

func (a *HTTPAdapter) sessionList(ctx context.Context, params json.RawMessage) (any, *Error) {
	var req struct {
		Cwd              string `json:"cwd"`
		IncludeSubagents bool   `json:"includeSubagents,omitempty"`
	}
	_ = json.Unmarshal(params, &req)
	resp, err := a.client.ListSessionsWithResponse(ctx, &httpclient.ListSessionsParams{Directory: &req.Cwd, IncludeSubagents: &req.IncludeSubagents})
	if err != nil || resp.JSON200 == nil {
		return nil, NewError(CodeInternalError, "session/list failed")
	}
	sessions := make([]map[string]any, 0, len(resp.JSON200.Sessions))
	for _, s := range resp.JSON200.Sessions {
		item := map[string]any{
			"sessionId": s.SessionId,
			"cwd":       s.Directory,
			"title":     s.Title,
			"updatedAt": s.UpdatedAt,
		}
		if s.ParentSessionId != nil {
			item["parentSessionId"] = *s.ParentSessionId
		}
		if s.ParentToolCallId != nil {
			item["parentToolCallId"] = *s.ParentToolCallId
		}
		if s.SubagentType != nil {
			item["subagentType"] = *s.SubagentType
		}
		if s.SessionKind != nil {
			item["sessionKind"] = *s.SessionKind
		}
		sessions = append(sessions, item)
	}
	result := map[string]any{"sessions": sessions}
	if a.treeEnabledForSession() {
		result["_meta"] = map[string]any{"pigo": map[string]any{"sessionList": map[string]any{"version": 1}}}
	}
	return result, nil
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go a.streamEvents(ctx, req.SessionID)
	text := promptText(req.Prompt)
	stopReason := "end_turn"
	var structured any
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		args := commandArgs(text)
		resp, err := a.client.ExecuteCommandWithResponse(ctx, req.SessionID, httpclient.ExecuteCommandJSONRequestBody{Directory: dir, Command: commandName(text), Arguments: &args})
		if err == nil && resp.JSON200 != nil {
			stopReason = resp.JSON200.StopReason
			structured = resp.JSON200.Structured
			if resp.JSON200.Text != nil {
				payload := sessionUpdatePayload(req.SessionID, textChunkUpdate(*resp.JSON200.Text))
				a.attachTreeMeta(req.SessionID, payload, nil)
				_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
			}
		} else {
			promptResp, promptErr := a.client.PromptSessionWithResponse(ctx, req.SessionID, httpclient.PromptSessionJSONRequestBody{Directory: dir, Prompt: req.Prompt})
			if promptErr != nil || promptResp.JSON200 == nil {
				_ = a.transport.SendResponse(ctx, id, nil, NewError(CodeInternalError, "session/prompt failed"))
				return
			}
			stopReason = promptResp.JSON200.StopReason
		}
	} else {
		resp, err := a.client.PromptSessionWithResponse(ctx, req.SessionID, httpclient.PromptSessionJSONRequestBody{Directory: dir, Prompt: req.Prompt})
		if err != nil || resp.JSON200 == nil {
			_ = a.transport.SendResponse(ctx, id, nil, NewError(CodeInternalError, "session/prompt failed"))
			return
		}
		stopReason = resp.JSON200.StopReason
	}
	a.refreshTree(ctx, req.SessionID, dir)
	result := map[string]any{"stopReason": stopReason}
	if structured != nil {
		result["_meta"] = map[string]any{"pigo": map[string]any{"structured": structured}}
	}
	_ = a.transport.SendResponse(ctx, id, result, nil)
}

func (a *HTTPAdapter) streamEvents(ctx context.Context, sessionID string) {
	after := int(a.cursor(sessionID))
	resp, err := a.client.GetEvents(ctx, &httpclient.GetEventsParams{SessionId: &sessionID, After: &after})
	if err != nil || resp == nil {
		return
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	eventType := ""
	var dataBuf strings.Builder
	flush := func() {
		if eventType == "" {
			return
		}
		var payload struct {
			ID   int64          `json:"id"`
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal([]byte(dataBuf.String()), &payload)
		if payload.ID > 0 {
			a.setCursor(sessionID, payload.ID)
		}
		a.mapEvent(sessionID, eventType, payload.Data)
		eventType = ""
		dataBuf.Reset()
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			flush()
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (a *HTTPAdapter) mapEvent(sessionID, eventType string, data map[string]any) {
	switch eventType {
	case "message.part.delta":
		if delta, ok := data["delta"].(string); ok && delta != "" {
			payload := sessionUpdatePayload(sessionID, map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": delta},
				"messageId":     data["messageId"],
			})
			a.attachTreeMeta(sessionID, payload, nil)
			_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
		}
		if thinking, ok := data["thinking"].(string); ok && thinking != "" {
			payload := sessionUpdatePayload(sessionID, map[string]any{
				"sessionUpdate": "agent_thought_chunk",
				"content":       map[string]any{"type": "text", "text": thinking},
				"messageId":     data["messageId"],
			})
			a.attachTreeMeta(sessionID, payload, nil)
			_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
		}
	case "tool.updated":
		id, _ := data["toolCallId"].(string)
		title, _ := data["title"].(string)
		status, _ := data["status"].(string)
		rawInput := data["rawInput"]
		output, _ := data["output"].(string)
		dir, _ := a.directory(sessionID)
		var update map[string]any
		failed := status == "failed"
		if isBashTool(title) {
			command := bashCommandFromArgs(rawInput)
			switch status {
			case "pending":
				update = bashToolCallPending(id, title, rawInput, dir, command)
			case "in_progress":
				if output != "" {
					update = toolCallUpdateText(id, title, output)
					update["_meta"] = map[string]any{"terminal_output": map[string]any{"terminal_id": id, "data": output}}
				} else {
					update = bashToolCallStart(id, title, rawInput, dir, command)
				}
			default:
				update = bashToolCallEnd(id, title, failed, agentcore.AgentToolResult{
					Content: agentcore.ContentList{agentcore.NewTextContent(output)},
				}, dir, command, rawInput)
			}
		} else {
			switch status {
			case "pending":
				update = toolCallPending(id, title, rawInput)
			case "in_progress":
				if output != "" {
					update = toolCallUpdateText(id, title, output)
				} else {
					update = toolCallStart(id, title, rawInput)
				}
			default:
				update = toolCallEnd(id, title, failed, output, rawInput)
			}
		}
		payload := sessionUpdatePayload(sessionID, update)
		if messageID, ok := data["messageId"]; ok {
			payload["update"].(map[string]any)["messageId"] = messageID
		}
		a.attachTreeMeta(sessionID, payload, nil)
		_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
	case "mode.updated":
		payload := sessionUpdatePayload(sessionID, map[string]any{
			"sessionUpdate": "current_mode_update",
			"currentModeId": data["currentModeId"],
		})
		a.attachTreeMeta(sessionID, payload, nil)
		_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
	case "config.updated":
		payload := sessionUpdatePayload(sessionID, map[string]any{
			"sessionUpdate": "config_option_update",
			"configOptions": data["configOptions"],
		})
		a.attachTreeMeta(sessionID, payload, nil)
		_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
	case "session.updated":
		payload := sessionUpdatePayload(sessionID, map[string]any{
			"sessionUpdate": "session_info_update",
			"title":         data["title"],
			"updatedAt":     data["updatedAt"],
		})
		a.attachTreeMeta(sessionID, payload, nil)
		_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
	case "commands.updated":
		payload := sessionUpdatePayload(sessionID, map[string]any{
			"sessionUpdate":     "available_commands_update",
			"availableCommands": data["availableCommands"],
		})
		a.attachTreeMeta(sessionID, payload, nil)
		_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
	case "permission.asked":
		a.handlePermissionAsked(sessionID, data)
	}
}

func (a *HTTPAdapter) handlePermissionAsked(sessionID string, data map[string]any) {
	permissionID, _ := data["permissionId"].(string)
	if permissionID == "" {
		return
	}
	toolCall, _ := data["toolCall"].(map[string]any)
	if toolCall == nil {
		toolCall = map[string]any{}
	}
	if _, ok := toolCall["toolCallId"]; !ok {
		if id, ok := toolCall["id"].(string); ok {
			toolCall["toolCallId"] = id
		}
	}
	if _, ok := toolCall["title"]; !ok {
		if name, ok := toolCall["name"].(string); ok {
			toolCall["title"] = name
		}
	}
	if _, ok := toolCall["status"]; !ok {
		toolCall["status"] = "pending"
	}
	if _, ok := toolCall["rawInput"]; !ok {
		if args, ok := toolCall["arguments"].(string); ok {
			toolCall["rawInput"] = json.RawMessage(args)
		}
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	raw, err := a.transport.SendRequest(reqCtx, MethodRequestPermission, map[string]any{
		"sessionId": sessionID,
		"toolCall":  toolCall,
		"options":   anyOptions(data["options"]),
	})
	if err != nil {
		return
	}
	var resp struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	if json.Unmarshal(raw, &resp) != nil || resp.Outcome.Outcome != "selected" || resp.Outcome.OptionID == "" {
		return
	}
	if a.client != nil {
		_, _ = a.client.ReplyPermissionWithResponse(context.Background(), sessionID, permissionID, httpclient.ReplyPermissionJSONRequestBody{
			OptionId: resp.Outcome.OptionID,
		})
	}
}

func anyOptions(raw any) []map[string]any {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
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
	payload := sessionUpdatePayload(sessionID, map[string]any{
		"sessionUpdate":     "available_commands_update",
		"availableCommands": resp.JSON200.Commands,
	})
	a.attachTreeMeta(sessionID, payload, nil)
	_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
}

func (a *HTTPAdapter) replayAll(ctx context.Context, sessionID, directory string, messages []httpclient.Message, nextCursor *string, hasMore bool) {
	for {
		a.replayMessages(sessionID, directory, messages)
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

type pendingToolCall struct {
	id           string
	name         string
	rawInput     any
	assistantMsg httpclient.Message
}

func (a *HTTPAdapter) replayMessages(sessionID, directory string, messages []httpclient.Message) {
	var pending []pendingToolCall
	for _, msg := range messages {
		if msg.Role == "compaction" || msg.Role == "branch_summary" {
			continue
		}
		if msg.Role == "toolResult" {
			if len(pending) > 0 {
				p := pending[0]
				pending = pending[1:]
				output := messageText(msg)
				a.sendReplayToolCall(sessionID, directory, p, output)
			}
			continue
		}
		for _, block := range msg.Content {
			switch block["type"] {
			case "text":
				text, _ := block["text"].(string)
				if text == "" {
					continue
				}
				update := "user_message_chunk"
				if msg.Role == "assistant" {
					update = "agent_message_chunk"
				}
				a.sendReplayUpdate(sessionID, msg, map[string]any{
					"sessionUpdate": update,
					"content":       map[string]any{"type": "text", "text": text},
				})
			case "thinking":
				thinking, _ := block["thinking"].(string)
				if thinking == "" {
					continue
				}
				a.sendReplayUpdate(sessionID, msg, map[string]any{
					"sessionUpdate": "agent_thought_chunk",
					"content":       map[string]any{"type": "text", "text": thinking},
				})
			case "toolCall":
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				if id == "" || name == "" {
					continue
				}
				pending = append(pending, pendingToolCall{
					id:           id,
					name:         name,
					rawInput:     block["arguments"],
					assistantMsg: msg,
				})
			}
		}
	}
	for _, p := range pending {
		a.sendReplayToolCall(sessionID, directory, p, "")
	}
}

func (a *HTTPAdapter) sendReplayToolCall(sessionID, directory string, p pendingToolCall, output string) {
	if isBashTool(p.name) {
		command := bashCommandFromArgs(p.rawInput)
		a.sendReplayUpdate(sessionID, p.assistantMsg, bashToolCallStart(p.id, p.name, p.rawInput, directory, command))
		result := agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent(output)}}
		a.sendReplayUpdate(sessionID, p.assistantMsg, bashToolCallEnd(p.id, p.name, false, result, directory, command, p.rawInput))
		return
	}
	a.sendReplayUpdate(sessionID, p.assistantMsg, toolCallStart(p.id, p.name, p.rawInput))
	a.sendReplayUpdate(sessionID, p.assistantMsg, toolCallEnd(p.id, p.name, false, output, p.rawInput))
}

func (a *HTTPAdapter) sendReplayUpdate(sessionID string, msg httpclient.Message, update map[string]any) {
	if msg.Id != "" {
		if _, ok := update["messageId"]; !ok {
			update["messageId"] = msg.Id
		}
	}
	payload := sessionUpdatePayload(sessionID, update)
	a.attachTreeMeta(sessionID, payload, replayTreeExtra(msg))
	_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
}

func replayTreeExtra(msg httpclient.Message) map[string]any {
	extra := map[string]any{}
	if msg.EntryId != nil {
		extra["entryId"] = *msg.EntryId
	}
	if msg.EntryType != nil {
		extra["entryType"] = *msg.EntryType
	}
	if msg.ParentId != nil {
		extra["parentId"] = *msg.ParentId
	}
	if msg.Seq != nil {
		extra["seq"] = *msg.Seq
	}
	if msg.Lane != nil {
		extra["lane"] = *msg.Lane
	}
	return extra
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

func commandArgs(line string) string {
	line = strings.TrimSpace(strings.TrimPrefix(line, "/"))
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return strings.TrimSpace(line[i+1:])
	}
	return ""
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

func (a *HTTPAdapter) treeEnabledForSession() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.treeEnabled
}

func (a *HTTPAdapter) treeState(sessionID string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.tree[sessionID]
	if !ok {
		return map[string]any{
			"version":       1,
			"currentLeafId": nil,
			"currentLane":   "main",
			"lanes":         []map[string]any{{"lane": "main", "leafId": nil}},
		}
	}
	lanes := st.Lanes
	if lanes == nil {
		lanes = []map[string]any{{"lane": st.CurrentLane, "leafId": st.CurrentLeafID}}
	}
	return map[string]any{
		"version":       1,
		"currentLeafId": st.CurrentLeafID,
		"currentLane":   st.CurrentLane,
		"lanes":         lanes,
	}
}

func (a *HTTPAdapter) attachTreeMeta(sessionID string, payload map[string]any, extra map[string]any) {
	if !a.treeEnabledForSession() {
		return
	}
	tree := a.treeState(sessionID)
	for k, v := range extra {
		tree[k] = v
	}
	meta, _ := payload["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	pigo, _ := meta["pigo"].(map[string]any)
	if pigo == nil {
		pigo = map[string]any{}
	}
	pigo["sessionTree"] = tree
	meta["pigo"] = pigo
	payload["_meta"] = meta
}

func (a *HTTPAdapter) refreshTree(ctx context.Context, sessionID, directory string) {
	if !a.treeEnabledForSession() {
		return
	}
	resp, err := a.client.GetSessionStatusWithResponse(ctx, sessionID, &httpclient.GetSessionStatusParams{Directory: directory})
	if err != nil || resp.JSON200 == nil {
		return
	}
	st := sessionTreeState{CurrentLane: "main"}
	if resp.JSON200.CurrentLeafId != nil {
		st.CurrentLeafID = *resp.JSON200.CurrentLeafId
	}
	if resp.JSON200.CurrentLane != nil {
		st.CurrentLane = *resp.JSON200.CurrentLane
	}
	if resp.JSON200.Lanes != nil {
		for _, l := range *resp.JSON200.Lanes {
			item := map[string]any{"lane": l.Lane}
			if l.LeafId != nil {
				item["leafId"] = *l.LeafId
			} else {
				item["leafId"] = nil
			}
			st.Lanes = append(st.Lanes, item)
		}
	}
	a.mu.Lock()
	a.tree[sessionID] = st
	a.mu.Unlock()
	update := map[string]any{
		"sessionUpdate":  "session_info_update",
		"currentLeafId":  st.CurrentLeafID,
		"currentLane":    st.CurrentLane,
		"lanes":          st.Lanes,
	}
	payload := sessionUpdatePayload(sessionID, update)
	a.attachTreeMeta(sessionID, payload, nil)
	_ = a.transport.SendNotification(NotificationSessionUpdate, payload)
}

func messageText(msg httpclient.Message) string {
	for _, block := range msg.Content {
		if t, ok := block["text"].(string); ok {
			return t
		}
	}
	return ""
}
