package acp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/trust"
)

// Dispatcher implements the ACP method surface for sessions. It is wired as
// the Server handler and owns the session manager plus the transport used for
// streaming notifications and deferred prompt responses.
type Dispatcher struct {
	manager         *SessionManager
	transport       Transport
	version         string
	pigoHome        string
	model           string
	sysPrompt       string
	mapper          *eventMapper
	broker          *ACPPermissionBroker
	trustMgr        *trust.Manager
	commands        map[string]commandFunc
	snap            *agenttool.FileSnapshotRecorder
	remote          *RemoteBridge
	remoteSessionID string
	dreamCfg        *DreamConfig
	compactCfg      *CompactConfig
	memoryCfg       *MemoryConfig
	runner          SessionRunner
	runMu           map[string]*sync.Mutex
	runMuOnce       sync.Once
	extraRootsMu    sync.Mutex
}

// NewDispatcher builds a dispatcher. model and sysPrompt are the session
// defaults resolved by the CLI entry point; model can be changed later via
// model/set (ticket 04).
func NewDispatcher(manager *SessionManager, transport Transport, pigoHome, model, sysPrompt string, broker *ACPPermissionBroker, snap *agenttool.FileSnapshotRecorder) *Dispatcher {
	d := &Dispatcher{
		manager:   manager,
		transport: transport,
		version:   VersionValue,
		pigoHome:  pigoHome,
		model:     model,
		sysPrompt: sysPrompt,
		mapper:    newEventMapper(),
		broker:    broker,
		commands:  buildCommands(),
		snap:      snap,
	}
	if broker != nil {
		d.trustMgr = broker.TrustManager()
	}
	d.runMu = make(map[string]*sync.Mutex)
	return d
}

// SetDreamConfig wires the /dream command settings.
func (d *Dispatcher) SetDreamConfig(cfg *DreamConfig) { d.dreamCfg = cfg }

// SetCompactConfig wires the /compact command settings.
func (d *Dispatcher) SetCompactConfig(cfg *CompactConfig) { d.compactCfg = cfg }

// SetMemoryConfig wires the /memory command settings.
func (d *Dispatcher) SetMemoryConfig(cfg *MemoryConfig) { d.memoryCfg = cfg }

// SetRunner exposes the session runner for /btw side threads.
func (d *Dispatcher) SetRunner(r SessionRunner) { d.runner = r }

// SetRemoteControl installs (or removes) the remote-control bridge and routes
// remote approvals through the permission broker.
func (d *Dispatcher) SetRemoteControl(rb *RemoteBridge) {
	d.remote = rb
	if d.broker != nil {
		if rb == nil {
			d.broker.SetRemoteConfirm(nil)
			return
		}
		d.broker.SetRemoteConfirm(func(tool, summary string) (allow, always bool, ok bool) {
			if !rb.Enabled() {
				return false, false, false
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			dec, ok := rb.Confirm(ctx, tool, summary)
			return dec.Approve, dec.Always, ok
		})
	}
}

// startRemoteControl starts the remote-control server for a session.
func (d *Dispatcher) startRemoteControl(sess *AcpSession) (string, *Error) {
	if d.remote != nil {
		return "", NewError(CodeInvalidParams, "remote control already running")
	}
	rb, err := NewRemoteBridge("", 0, nil, nil)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	url, err := rb.Start()
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	d.SetRemoteControl(rb)
	d.remoteSessionID = sess.ID
	go d.pumpRemoteInput(rb, sess.ID)
	return url, nil
}

// stopRemoteControl stops the remote-control server.
func (d *Dispatcher) stopRemoteControl() error {
	if d.remote == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := d.remote.Stop(ctx)
	d.SetRemoteControl(nil)
	d.remoteSessionID = ""
	return err
}

func (d *Dispatcher) pumpRemoteInput(rb *RemoteBridge, sessionID string) {
	for text := range rb.RemoteInput() {
		if strings.TrimSpace(text) == "" {
			continue
		}
		d.runRemotePrompt(sessionID, text)
	}
}

func (d *Dispatcher) runRemotePrompt(sessionID, text string) {
	sess := d.manager.Get(sessionID)
	if sess == nil {
		return
	}
	lock := d.lockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()
	hook := d.beforeToolCall(sessionID)
	onEvent := d.eventSink(sessionID)
	_, _ = d.manager.Run(context.Background(), sess, applyGoal(sess, text), hook, onEvent)
}

func (d *Dispatcher) lockFor(sessionID string) *sync.Mutex {
	d.runMuOnce.Do(func() { d.runMu = make(map[string]*sync.Mutex) })
	if mu, ok := d.runMu[sessionID]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	d.runMu[sessionID] = mu
	return mu
}

func (d *Dispatcher) beforeToolCall(sessionID string) agentcore.BeforeToolCallFunc {
	if d.broker != nil {
		return d.broker.BeforeToolCall(sessionID)
	}
	return nil
}

func (d *Dispatcher) eventSink(sessionID string) func(agentcore.AgentEvent) {
	return func(ev agentcore.AgentEvent) {
		for _, update := range d.mapper.Map(sessionID, ev) {
			_ = d.transport.SendNotification(NotificationSessionUpdate, sessionUpdatePayload(sessionID, update))
			if d.remote != nil {
				d.remote.SendOutput(updateText(update))
			}
		}
		_ = d.transport.SendNotification(MethodPigoEvent, pigoEventPayload(sessionID, ev))
		if d.remote != nil {
			d.remote.SendEvent(ev)
		}
	}
}

func updateText(update map[string]any) string {
	switch update["sessionUpdate"] {
	case "agent_message_chunk", "agent_thought_chunk":
		return nestedText(update) + "\n"
	}
	return ""
}

// HandleRequest dispatches a synchronous ACP request.
func (d *Dispatcher) HandleRequest(ctx context.Context, id RequestID, method string, params json.RawMessage) (any, *Error) {
	switch method {
	case MethodInitialize:
		return buildInitializeResponse(d.version), nil
	case MethodSessionNew:
		return d.sessionNew(params)
	case MethodSessionLoad:
		return d.sessionLoad(params)
	case MethodSessionList:
		return d.sessionList(params)
	case MethodSessionDelete:
		return d.sessionDelete(params)
	case MethodSessionClose:
		return d.sessionClose(params)
	case MethodModelSet:
		return d.modelSet(params)
	case MethodPigoCommand:
		return d.pigoCommand(params)
	case MethodPigoStatus:
		return d.pigoStatus(params)
	case MethodPigoModels:
		return d.pigoModels(params)
	case MethodPigoConfig:
		return d.pigoConfig(params)
	case MethodPigoMessages:
		return d.pigoMessages(params)
	case MethodPigoRewind, MethodPigoFork, MethodPigoTree, MethodPigoGoal, MethodPigoBtw, MethodPigoDream, MethodPigoRemoteControl:
		return nil, NewError(CodeNotImplemented, method+" is not implemented yet")
	default:
		return nil, NewError(CodeMethodNotFound, "method not found: "+method)
	}
}

// HandleDeferredRequest claims requests whose response is sent asynchronously
// from a background task. session/prompt returns true so the server loop stays
// responsive to session/cancel while the turn runs.
func (d *Dispatcher) HandleDeferredRequest(ctx context.Context, id RequestID, method string, params json.RawMessage) bool {
	if method != MethodSessionPrompt {
		return false
	}
	go d.runPrompt(ctx, id, params)
	return true
}

// HandleNotification processes client notifications.
func (d *Dispatcher) HandleNotification(ctx context.Context, method string, params json.RawMessage) {
	if method != MethodSessionCancel {
		return
	}
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
		return
	}
	if sess := d.manager.Get(req.SessionID); sess != nil {
		sess.Cancel()
	}
}

func (d *Dispatcher) sessionNew(params json.RawMessage) (any, *Error) {
	var req struct {
		Cwd                   string   `json:"cwd"`
		AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.Cwd == "" {
		return nil, NewError(CodeInvalidParams, "missing cwd")
	}
	d.applyAdditionalDirectories(req.AdditionalDirectories)
	store, err := d.manager.StoreForWorkspace(d.pigoHome, req.Cwd)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	sess, err := d.manager.New(req.Cwd, d.model, d.sysPrompt, store)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	return map[string]any{
		"sessionId":     sess.ID,
		"configOptions": []any{},
		"models": map[string]any{
			"currentModelId":  sess.Model,
			"availableModels": []any{},
		},
	}, nil
}

func (d *Dispatcher) sessionLoad(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.Cwd == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or cwd")
	}
	store, err := d.manager.StoreForWorkspace(d.pigoHome, req.Cwd)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	sess, err := d.manager.Load(req.Cwd, req.SessionID, d.model, store)
	if err != nil {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	resp := map[string]any{
		"sessionId":     sess.ID,
		"configOptions": []any{},
		"models": map[string]any{
			"currentModelId":  sess.Model,
			"availableModels": []any{},
		},
	}
	if tr := sess.Store.TranscriptStore(); tr != nil {
		if _, entries, err := tr.LoadEntries(sess.ID); err == nil {
			messages := make([]any, 0, len(entries))
			for _, e := range entries {
				messages = append(messages, entryToACPMessage(e))
			}
			resp["messages"] = messages
		}
	}
	return resp, nil
}

func (d *Dispatcher) sessionList(params json.RawMessage) (any, *Error) {
	var req struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.Cwd == "" {
		return nil, NewError(CodeInvalidParams, "missing cwd")
	}
	store, err := d.manager.StoreForWorkspace(d.pigoHome, req.Cwd)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	metas, err := store.List()
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	sessions := make([]any, 0, len(metas))
	for _, m := range metas {
		sessions = append(sessions, map[string]any{
			"sessionId":       m.SessionID,
			"title":           m.SessionName,
			"cwd":             m.WorkspacePath,
			"model":           m.ModelName,
			"createdAt":       m.CreatedAt,
			"updatedAt":       m.LastActiveAt,
			"messageCount":    m.MessageCount,
			"toolCallCount":   m.ToolCallCount,
			"parentSessionId": m.ParentSessionID,
		})
	}
	return map[string]any{"sessions": sessions, "nextCursor": nil}, nil
}

func (d *Dispatcher) sessionClose(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId")
	}
	d.manager.Close(req.SessionID)
	return map[string]any{}, nil
}

func (d *Dispatcher) modelSet(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
		ModelID   string `json:"modelId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.ModelID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or modelId")
	}
	sess := d.manager.Get(req.SessionID)
	if sess == nil {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	sess.Model = req.ModelID
	return map[string]any{}, nil
}

func (d *Dispatcher) runPrompt(ctx context.Context, id RequestID, params json.RawMessage) {
	sessionID, text, ok := parsePromptParams(params)
	if !ok {
		_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInvalidParams, "missing sessionId or prompt text"))
		return
	}
	sess := d.manager.Get(sessionID)
	if sess == nil {
		_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInvalidParams, "session not found: "+sessionID))
		return
	}

	lock := d.lockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()
	last, err := d.manager.Run(ctx, sess, applyGoal(sess, text), d.beforeToolCall(sessionID), d.eventSink(sessionID))
	if err != nil {
		if err == ErrTurnCancelled {
			_ = d.transport.SendResponse(ctx, id, map[string]any{"stopReason": "cancelled"}, nil)
			return
		}
		_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInternalError, err.Error()))
		return
	}
	go d.generateTitle(sess, text)

	stop := "end_turn"
	if last != nil {
		switch last.StopReason {
		case agentcore.StopReasonEndTurn, agentcore.StopReasonToolUse:
			stop = "end_turn"
		case agentcore.StopReasonLength:
			stop = "max_tokens"
		case agentcore.StopReasonAborted:
			stop = "cancelled"
		case agentcore.StopReasonError:
			stop = "end_turn"
		}
	}
	_ = d.transport.SendResponse(ctx, id, map[string]any{"stopReason": stop}, nil)
}

func applyGoal(sess *AcpSession, text string) string {
	if sess.Goal == "" {
		return text
	}
	return "Current goal: " + sess.Goal + "\n\n" + text
}

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
	cmd, ok := d.commands[name]
	if !ok {
		return nil, NewError(CodeMethodNotFound, "unknown command: /"+name)
	}
	text, rpcErr := cmd(context.Background(), d, sess, args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return map[string]any{"text": text, "notifications": []any{}}, nil
}

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

func parsePromptParams(params json.RawMessage) (sessionID, text string, ok bool) {
	var req struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			URI  string `json:"uri"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return "", "", false
	}
	if req.SessionID == "" {
		return "", "", false
	}
	for _, block := range req.Prompt {
		if block.Type == "text" || block.Type == "" {
			text += block.Text
			continue
		}
		if block.Type == "resource_link" {
			path := strings.TrimPrefix(block.URI, "file://")
			if path != "" {
				if data, err := os.ReadFile(path); err == nil {
					if len(data) > 64*1024 {
						data = data[:64*1024]
					}
					text += "\n\n<resource_link:" + path + ">\n" + string(data) + "\n</resource_link>"
				}
			}
		}
	}
	return req.SessionID, text, true
}
