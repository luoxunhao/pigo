package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
	manager      *SessionManager
	transport    Transport
	version      string
	pigoHome     string
	model        string
	providerName string
	// sysPrompt and registry are the single-project defaults used by the
	// in-process factory. A shared stdio server installs a SessionContextFactory
	// that rebuilds both per session cwd instead.
	sysPrompt       string
	sessionCtx      SessionContextFactory
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
	subagents       *runtime.Registry
	subagentMappers map[string]*eventMapper
	subagentMu      sync.Mutex
	credStore       *provider.CredentialStore
	models          *ConfiguredModels
	hookSeam        HookSeamFunc
	lastSessionCwd  string
	runMu           map[string]*sync.Mutex
	runMuOnce       sync.Once
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
		broker:    broker,
		commands:  buildCommands(),
		snap:      snap,
	}
	if broker != nil {
		d.trustMgr = broker.TrustManager()
	}
	d.runMu = make(map[string]*sync.Mutex)
	d.sessionCtx = d.defaultSessionContext()
	if broker != nil {
		broker.SetTrustChangedHook(d.invalidateSessionRegistries)
	}
	return d
}

// SetCredentialStore wires the credential store used to decide which preset
// providers appear in ACP model options.
func (d *Dispatcher) SetCredentialStore(store *provider.CredentialStore) { d.credStore = store }

// SetHookSeam wires the per-session hook installer supplied by the CLI entry
// point. It is invoked for every turn with the live session id and workspace.
func (d *Dispatcher) SetHookSeam(fn HookSeamFunc) { d.hookSeam = fn }

// SetProviderName wires the startup resolved provider name used to keep the
// current model visible even when it is not part of the curated preset catalog.
func (d *Dispatcher) SetProviderName(name string) { d.providerName = name }

// SetConfiguredModels wires the shared configured-model store used by menus,
// config writes, and runtime resolution.
func (d *Dispatcher) SetConfiguredModels(m *ConfiguredModels) { d.models = m }

// SetSlashRegistry wires the full slash registry so external clients see and
// can invoke user templates, skills, and plugin commands. It is the default
// registry used by in-process servers; shared servers override the session
// context factory with a per-cwd registry builder.
func (d *Dispatcher) SetSlashRegistry(reg *runtime.SlashRegistry) { d.registry = reg }

// SetSubagentRegistry wires the live child-session registry so ACP can route
// session/prompt to child sessions and forward their events as session/update
// notifications under the child session id.
func (d *Dispatcher) SetSubagentRegistry(reg *runtime.Registry) {
	d.subagents = reg
	d.subagentMappers = make(map[string]*eventMapper)
	if reg != nil {
		reg.SetEventSink(d.subagentEventSink)
	}
}

// SetSessionContextFactory installs a per-session context builder. When unset
// the dispatcher uses the startup sysPrompt/registry/tools as a single-project
// default.
func (d *Dispatcher) SetSessionContextFactory(fn SessionContextFactory) {
	if fn != nil {
		d.sessionCtx = fn
	}
}

// invalidateSessionRegistries rebuilds the slash registry of every live
// session after a trust change. Registries are per-session snapshots, so a
// trust decision must replace them rather than waiting for the next load.
func (d *Dispatcher) invalidateSessionRegistries() {
	for _, sess := range d.manager.All() {
		ctx, err := d.sessionCtx(sess.Cwd, sess.AdditionalDirectories)
		if err != nil {
			continue
		}
		sess.Registry = ctx.Registry
	}
}

func (d *Dispatcher) defaultSessionContext() SessionContextFactory {
	return func(cwd string, additionalDirectories []string) (SessionContext, error) {
		tools := CloneToolsForSession(runnerTemplateTools(d.runner), cwd, additionalDirectories, nil)
		return SessionContext{
			SysPrompt: d.sysPrompt,
			Tools:     tools,
			Registry:  d.registry,
		}, nil
	}
}

func runnerTemplateTools(runner SessionRunner) []agentcore.AgentTool {
	rr, ok := runner.(*RuntimeRunner)
	if !ok || rr == nil {
		return nil
	}
	return rr.Tools
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
	hook := d.beforeToolCall(sess)
	onEvent := d.eventSink(sess)
	_, _ = d.manager.Run(context.Background(), sess, p.text, nil, hook, onEvent, d.turnHooks(sess))
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

func (d *Dispatcher) beforeToolCall(sess *AcpSession) agentcore.BeforeToolCallFunc {
	if d.broker != nil && sess != nil {
		return d.broker.BeforeToolCall(sess.ID, sess.Cwd)
	}
	return nil
}

// turnHooks builds the per-turn hooks for a session. The hook seam is bound to
// the session id and workspace so RuntimeRunner can install it into each
// freshly built RunConfig without knowing session details.
func (d *Dispatcher) turnHooks(sess *AcpSession) TurnHooks {
	if d.hookSeam == nil || sess == nil {
		return TurnHooks{}
	}
	return TurnHooks{
		InstallSeams: func(cfg *runtime.RunConfig) error {
			return d.hookSeam(cfg, sess.ID, sess.Cwd)
		},
	}
}

func (d *Dispatcher) eventSink(sess *AcpSession) func(agentcore.AgentEvent) {
	return func(ev agentcore.AgentEvent) {
		mapper := sess.Mapper
		if mapper == nil {
			mapper = newEventMapper(sess.Cwd)
		}
		for _, update := range mapper.Map(sess.ID, ev) {
			_ = d.transport.SendNotification(NotificationSessionUpdate, sessionUpdatePayload(sess.ID, update))
			if d.remote != nil {
				d.remote.SendOutput(updateText(update))
			}
		}
		_ = d.transport.SendNotification(MethodPigoEvent, pigoEventPayload(sess.ID, ev))
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
	case MethodPigoConfigTest:
		return d.pigoConfigTest(params)
	case MethodPigoMessages:
		return d.pigoMessages(params)
	case MethodPigoTrustList:
		return d.pigoTrustList(params)
	case MethodPigoTrustSet:
		return d.pigoTrustSet(params)
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
	if child := d.childSession(req.SessionID); child != nil {
		child.Cancel()
		return
	}
	if sess := d.manager.Get(req.SessionID); sess != nil {
		if sess.queueLen() > 0 {
			d.sendTextChunk(req.SessionID, "Cleared queued prompts.")
		}
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
	if !filepath.IsAbs(req.Cwd) {
		return nil, NewError(CodeInvalidParams, "cwd must be an absolute path")
	}
	ctx, ctxErr := d.sessionCtx(req.Cwd, req.AdditionalDirectories)
	if ctxErr != nil {
		return nil, NewError(CodeInternalError, ctxErr.Error())
	}
	ctx.AdditionalDirectories = req.AdditionalDirectories
	store, err := d.manager.StoreForWorkspace(d.pigoHome, req.Cwd)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	sess, err := d.manager.New(req.Cwd, d.model, ctx, store)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	if len(req.MCPServers) > 0 && string(req.MCPServers) != "null" {
		if err := saveMCPServers(sess, req.MCPServers); err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
	}
	d.lastSessionCwd = req.Cwd
	go d.announceSession(sess, true)
	return d.sessionPayload(sess), nil
}

func (d *Dispatcher) sessionLoad(params json.RawMessage) (any, *Error) {
	var req struct {
		SessionID             string          `json:"sessionId"`
		Cwd                   string          `json:"cwd"`
		ModelID               string          `json:"modelId,omitempty"`
		AdditionalDirectories []string        `json:"additionalDirectories,omitempty"`
		MCPServers            json.RawMessage `json:"mcpServers,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" || req.Cwd == "" {
		return nil, NewError(CodeInvalidParams, "missing sessionId or cwd")
	}
	if !filepath.IsAbs(req.Cwd) {
		return nil, NewError(CodeInvalidParams, "cwd must be an absolute path")
	}
	if strings.HasPrefix(req.SessionID, "subagent-") && d.subagents != nil {
		child := d.subagents.Load(req.SessionID)
		if child == nil {
			return nil, NewError(CodeInvalidParams, "subagent session not found: "+req.SessionID)
		}
		return map[string]any{
			"sessionId": child.ID,
			"_meta": map[string]any{
				"subagentParentInfo": map[string]any{
					"parentSessionId":  child.ParentID,
					"parentToolCallId": child.ToolCallID,
					"subagentType":     child.Type,
				},
			},
		}, nil
	}
	ctx, ctxErr := d.sessionCtx(req.Cwd, req.AdditionalDirectories)
	if ctxErr != nil {
		return nil, NewError(CodeInternalError, ctxErr.Error())
	}
	ctx.AdditionalDirectories = req.AdditionalDirectories
	store, err := d.manager.StoreForWorkspace(d.pigoHome, req.Cwd)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	sess, err := d.manager.Load(req.Cwd, req.SessionID, req.ModelID, ctx, store)
	if err != nil {
		return nil, NewError(CodeInvalidParams, "session not found: "+req.SessionID)
	}
	if meta, metaErr := store.LoadMetadata(req.SessionID); metaErr == nil {
		if lvl := readSessionThinking(meta); lvl != "" && validThinkingLevel(lvl) {
			sess.Thinking = lvl
		}
	}
	if !d.modeAllowed(sess, sess.Thinking) {
		if m, ok := d.models.Find(sess.Model); ok && len(m.ThinkingLevels) > 0 {
			sess.Thinking = m.ThinkingLevels[0]
			_ = persistSessionThinking(sess)
		}
	}
	if len(req.MCPServers) > 0 && string(req.MCPServers) != "null" {
		_ = saveMCPServers(sess, req.MCPServers)
	}
	d.lastSessionCwd = req.Cwd
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
		All    bool   `json:"all,omitempty"`
	}
	_ = json.Unmarshal(params, &req)
	var sessions []map[string]any
	effectiveCwd := req.Cwd
	if !req.All && effectiveCwd == "" {
		effectiveCwd = d.lastSessionCwd
	}
	switch {
	case req.All:
		metas, err := sessionstore.ListAll(d.pigoHome)
		if err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		sessions = sessionInfos(metas)
	case effectiveCwd != "":
		store, err := d.manager.StoreForWorkspace(d.pigoHome, effectiveCwd)
		if err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		metas, err := store.List()
		if err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		sessions = sessionInfos(metas)
	default:
		metas, err := sessionstore.ListAll(d.pigoHome)
		if err != nil {
			return nil, NewError(CodeInternalError, err.Error())
		}
		sessions = sessionInfos(metas)
	}
	offset, _ := strconv.Atoi(req.Cursor)
	offset = max(0, offset)
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
	if !d.modeAllowed(sess, req.ModeID) {
		return nil, NewError(CodeInvalidParams, "modeId not supported by current model: "+req.ModeID)
	}
	sess.Thinking = req.ModeID
	_ = persistSessionThinking(sess)
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
		if !d.modelAvailable(context.Background(), sess, req.Value) {
			return nil, NewError(CodeInvalidParams, "unknown modelId: "+req.Value)
		}
		d.applyModelSwitch(sess, req.Value)
	case configIDThoughtLevel:
		if !validThinkingLevel(req.Value) {
			return nil, NewError(CodeInvalidParams, "unknown thinking level: "+req.Value)
		}
		if !d.modeAllowed(sess, req.Value) {
			return nil, NewError(CodeInvalidParams, "thinking level not supported by current model: "+req.Value)
		}
		sess.Thinking = req.Value
		_ = persistSessionThinking(sess)
		d.sendSessionUpdate(req.SessionID, map[string]any{
			"sessionUpdate": "current_mode_update",
			"currentModeId": req.Value,
		})
	default:
		return nil, NewError(CodeInvalidParams, "unknown config option: "+req.ConfigID)
	}
	d.sendConfigOptionsUpdate(sess)
	return map[string]any{
		"configOptions": sessionConfigOptions(context.Background(), sess, d.configuredModelList()),
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
	if !d.modelAvailable(context.Background(), sess, req.ModelID) {
		return nil, NewError(CodeInvalidParams, "unknown modelId: "+req.ModelID)
	}
	d.applyModelSwitch(sess, req.ModelID)
	d.sendConfigOptionsUpdate(sess)
	return map[string]any{}, nil
}

// applyModelSwitch sets the session model and resets thinking to a supported
// level when the new model does not support the current one.
func (d *Dispatcher) applyModelSwitch(sess *AcpSession, modelID string) {
	sess.Model = modelID
	if d.models == nil || sess.Thinking == "" {
		return
	}
	m, ok := d.models.Find(modelID)
	if !ok || len(m.ThinkingLevels) == 0 {
		return
	}
	if !slices.Contains(m.ThinkingLevels, sess.Thinking) {
		sess.Thinking = m.ThinkingLevels[0]
		_ = persistSessionThinking(sess)
		d.sendSessionUpdate(sess.ID, map[string]any{
			"sessionUpdate": "current_mode_update",
			"currentModeId": sess.Thinking,
		})
	}
}

// modeAllowed reports whether a thinking level is supported by the current
// model. Models without an explicit list allow the global levels.
func (d *Dispatcher) modeAllowed(sess *AcpSession, mode string) bool {
	if d.models == nil {
		return true
	}
	m, ok := d.models.Find(sess.Model)
	if !ok || len(m.ThinkingLevels) == 0 {
		return true
	}
	return slices.Contains(m.ThinkingLevels, mode)
}

func (d *Dispatcher) childSession(id string) *runtime.ChildSession {
	if d.subagents == nil {
		return nil
	}
	return d.subagents.Get(id)
}

func (d *Dispatcher) subagentEventSink(parentSessionID, childSessionID string, ev agentcore.AgentEvent) {
	d.subagentMu.Lock()
	m := d.subagentMappers[childSessionID]
	if m == nil {
		cwd := ""
		if child := d.subagents.Get(childSessionID); child != nil {
			cwd = child.Cwd
		}
		m = newEventMapper(cwd)
		d.subagentMappers[childSessionID] = m
	}
	d.subagentMu.Unlock()
	for _, update := range m.Map(childSessionID, ev) {
		_ = d.transport.SendNotification(NotificationSessionUpdate, sessionUpdatePayload(childSessionID, update))
	}
}

func (d *Dispatcher) runChildPrompt(ctx context.Context, id RequestID, child *runtime.ChildSession, text string, images []agentcore.Content) {
	onEvent := func(ev agentcore.AgentEvent) {
		d.subagentEventSink(child.ParentID, child.ID, ev)
	}
	if child.Running() {
		if err := child.Prompt(text); err != nil {
			_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInternalError, err.Error()))
			return
		}
		_ = d.transport.SendResponse(ctx, id, map[string]any{"stopReason": "end_turn"}, nil)
		return
	}
	if len(images) > 0 {
		_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInvalidParams, "images are not supported on subagent sessions yet"))
		return
	}
	_, last, err := child.Continue(ctx, text, onEvent, nil)
	if err != nil {
		if ctx.Err() != nil {
			_ = d.transport.SendResponse(ctx, id, map[string]any{"stopReason": "cancelled"}, nil)
			return
		}
		_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInternalError, err.Error()))
		return
	}
	stop := "end_turn"
	if last != nil {
		switch last.StopReason {
		case agentcore.StopReasonLength:
			stop = "max_tokens"
		case agentcore.StopReasonAborted:
			stop = "cancelled"
		}
	}
	_ = d.transport.SendResponse(ctx, id, map[string]any{"stopReason": stop}, nil)
}

func (d *Dispatcher) runPrompt(ctx context.Context, id RequestID, params json.RawMessage) {
	sessionID, text, images, ok := parsePromptParams(params)
	if !ok {
		_ = d.transport.SendResponse(ctx, id, nil, NewError(CodeInvalidParams, "missing sessionId or prompt text"))
		return
	}
	if child := d.childSession(sessionID); child != nil {
		d.runChildPrompt(ctx, id, child, text, images)
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

	last, err := d.manager.Run(ctx, sess, p.text, p.images, d.beforeToolCall(sess), d.eventSink(sess), d.turnHooks(sess))
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
