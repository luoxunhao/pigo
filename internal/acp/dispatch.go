package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/sessionstore"
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
	registry        *runtime.SlashRegistry
	credStore       *provider.CredentialStore
	providers       *CustomProviders
	lastSessionCwd  string
	cwd             string
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
		mapper:    newEventMapper(""),
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

// SetCredentialStore wires the credential store used to decide which preset
// providers appear in ACP model options.
func (d *Dispatcher) SetCredentialStore(store *provider.CredentialStore) { d.credStore = store }

// SetCustomProviders wires the shared custom provider registry used by model
// listing, provider management, and runtime resolution.
func (d *Dispatcher) SetCustomProviders(p *CustomProviders) { d.providers = p }

// SetSlashRegistry wires the full slash registry so external clients see and
// can invoke user templates, skills, and plugin commands.
func (d *Dispatcher) SetSlashRegistry(reg *runtime.SlashRegistry) { d.registry = reg }

// SetCwd wires the workspace cwd used for tool-location resolution and startup
// info. In-process callers set it from the session workspace.
func (d *Dispatcher) SetCwd(cwd string) {
	d.cwd = cwd
	d.mapper.SetCwd(cwd)
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
	p := &queuedPrompt{text: applyGoal(sess, text), done: make(chan struct{})}
	if !sess.tryRun(p) {
		sess.waitForTurn(p)
		if p.delivered {
			<-p.done
			return
		}
	}
	hook := d.beforeToolCall(sessionID)
	onEvent := d.eventSink(sessionID)
	_, _ = d.manager.Run(context.Background(), sess, p.text, nil, hook, onEvent, TurnHooks{})
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
	case MethodSessionMode:
		return d.sessionMode(params)
	case MethodSessionConfigOpt:
		return d.sessionConfigOption(params)
	case MethodModelSet:
		return d.modelSet(params)
	case MethodPigoCommand:
		return d.pigoCommand(params)
	case MethodPigoStatus:
		return d.pigoStatus(params)
	case MethodPigoModels:
		return d.pigoModels(params)
	case MethodPigoModelsDiscover:
		return d.pigoModelsDiscover(params)
	case MethodPigoConfig:
		return d.pigoConfig(params)
	case MethodPigoMessages:
		return d.pigoMessages(params)
	case MethodPigoProvidersUpsert:
		return d.pigoProvidersUpsert(params)
	case MethodPigoProvidersList:
		return d.pigoProvidersList(params)
	case MethodPigoProvidersDelete:
		return d.pigoProvidersDelete(params)
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
		Cwd                   string          `json:"cwd"`
		AdditionalDirectories []string        `json:"additionalDirectories,omitempty"`
		MCPServers            json.RawMessage `json:"mcpServers,omitempty"`
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
	if len(req.MCPServers) > 0 && string(req.MCPServers) != "null" {
		if err := saveMCPServers(sess, req.MCPServers); err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
	}
	d.lastSessionCwd = req.Cwd
	d.SetCwd(req.Cwd)
	go d.announceSession(sess, true)
	return d.sessionPayload(sess), nil
}

func (d *Dispatcher) sessionLoad(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID  string          `json:"sessionId"`
		Cwd        string          `json:"cwd"`
		MCPServers json.RawMessage `json:"mcpServers,omitempty"`
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
	if len(req.MCPServers) > 0 && string(req.MCPServers) != "null" {
		_ = saveMCPServers(sess, req.MCPServers)
	}
	d.lastSessionCwd = req.Cwd
	d.SetCwd(req.Cwd)
	go d.replaySession(sess)
	go d.announceSession(sess, false)
	resp := d.sessionPayload(sess)
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

func (d *Dispatcher) sessionList(params json.RawMessage) (any, *Error) {
	var req struct {
		Cwd    string `json:"cwd"`
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(params, &req)
	var sessions []map[string]any
	if req.Cwd != "" {
		store, err := d.manager.StoreForWorkspace(d.pigoHome, req.Cwd)
		if err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		metas, err := store.List()
		if err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		sessions = sessionInfos(metas)
	} else {
		metas, err := sessionstore.ListAll(d.pigoHome)
		if err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		sessions = sessionInfos(metas)
	}
	offset, _ := strconv.Atoi(req.Cursor)
	if offset < 0 {
		offset = 0
	}
	end := offset + 50
	if end > len(sessions) {
		end = len(sessions)
	}
	if offset > len(sessions) {
		offset = len(sessions)
	}
	var next any
	if end < len(sessions) {
		next = strconv.Itoa(end)
	}
	return map[string]any{
		"sessions":   sessions[offset:end],
		"nextCursor": next,
	}, nil
}

func (d *Dispatcher) sessionDelete(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId")
	}
	d.manager.Close(req.SessionID)
	metas, err := sessionstore.ListAll(d.pigoHome)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	for _, m := range metas {
		if m.SessionID != req.SessionID {
			continue
		}
		store, err := sessionstore.OpenForWorkspace(d.pigoHome, m.WorkspacePath)
		if err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		if err := store.Delete(req.SessionID); err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		return map[string]any{}, nil
	}
	_ = os.Remove(filepath.Join(d.pigoHome, "sessions", req.SessionID+".jsonl"))
	return map[string]any{}, nil
}

func (d *Dispatcher) sessionMode(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
		ModeID    string `json:"modeId"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.ModeID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or modeId")
	}
	if !validThinkingLevel(req.ModeID) {
		return nil, NewError(CodeInvalidParams, "unknown modeId: "+req.ModeID)
	}
	sess := d.manager.Get(req.SessionID)
	if sess == nil {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	sess.Thinking = req.ModeID
	d.sendSessionUpdate(req.SessionID, map[string]any{
		"sessionUpdate": "current_mode_update",
		"currentModeId": req.ModeID,
	})
	d.sendConfigOptionsUpdate(sess)
	return map[string]any{}, nil
}

func (d *Dispatcher) sessionConfigOption(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID string `json:"sessionId"`
		ConfigID  string `json:"configId"`
		Value     string `json:"value"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.ConfigID == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or configId")
	}
	sess := d.manager.Get(req.SessionID)
	if sess == nil {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	switch req.ConfigID {
	case configIDModel:
		if req.Value == "" {
			return nil, NewError(CodeInvalidParams, "missing model value")
		}
		sess.Model = req.Value
	case configIDThoughtLevel:
		if !validThinkingLevel(req.Value) {
			return nil, NewError(CodeInvalidParams, "unknown thinking level: "+req.Value)
		}
		sess.Thinking = req.Value
		d.sendSessionUpdate(req.SessionID, map[string]any{
			"sessionUpdate": "current_mode_update",
			"currentModeId": req.Value,
		})
	default:
		return nil, NewError(CodeInvalidParams, "unknown config option: "+req.ConfigID)
	}
	d.sendConfigOptionsUpdate(sess)
	return map[string]any{
		"configOptions": sessionConfigOptions(context.Background(), sess, d.credStore, d.customProviderList()),
	}, nil
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
	sessionID, text, images, ok := parsePromptParams(params)
	if !ok {
		_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInvalidParams, "missing sessionId or prompt text"))
		return
	}
	sess := d.manager.Get(sessionID)
	if sess == nil {
		_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInvalidParams, "session not found: "+sessionID))
		return
	}

	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		prompt, handled, message, rpcErr := d.resolveSlash(sess, text)
		if rpcErr != nil {
			_ = d.transport.SendResponse(ctx, id, nil, rpcErr)
			return
		}
		if handled {
			d.sendTextChunk(sessionID, message)
			_ = d.transport.SendResponse(ctx, id, map[string]any{"stopReason": "end_turn"}, nil)
			return
		}
		text = prompt
	}

	p := &queuedPrompt{
		text:   applyGoal(sess, text),
		images: images,
		done:   make(chan struct{}),
	}
	if !sess.tryRun(p) {
		d.sendQueued(sessionID, sess.queueLen())
		sess.waitForTurn(p)
		if p.delivered {
			<-p.done
			if p.runErr != nil {
				_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInternalError, p.runErr.Error()))
				return
			}
			_ = d.transport.SendResponse(ctx, id, map[string]any{"stopReason": p.stopReason}, nil)
			return
		}
	}

	last, err := d.manager.Run(ctx, sess, p.text, p.images, d.beforeToolCall(sessionID), d.eventSink(sessionID), TurnHooks{})
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

func parsePromptParams(params json.RawMessage) (sessionID, text string, images []agentcore.Content, ok bool) {
	var req struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
			URI      string `json:"uri"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return "", "", nil, false
	}
	if req.SessionID == "" {
		return "", "", nil, false
	}
	for _, block := range req.Prompt {
		switch block.Type {
		case "text", "":
			text += block.Text
		case "image":
			if block.Data != "" {
				mime := block.MimeType
				if mime == "" {
					mime = "image/png"
				}
				images = append(images, agentcore.NewImageContent(block.Data, mime))
			}
		case "resource":
			text += "[resource: " + block.URI + "]"
		case "resource_link":
			path := strings.TrimPrefix(block.URI, "file://")
			if path != "" {
				if data, err := os.ReadFile(path); err == nil {
					if len(data) > 64*1024 {
						data = data[:64*1024]
					}
					text += "\n\n<resource_link:" + path + ">\n" + string(data) + "\n</resource_link>"
				}
			}
		case "audio":
			text += "[audio not supported]"
		}
	}
	return req.SessionID, text, images, true
}
