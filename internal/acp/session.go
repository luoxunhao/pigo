package acp

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

// ErrTurnCancelled marks a turn that ended because session/cancel was issued.
var ErrTurnCancelled = errors.New("acp: turn cancelled")

// AcpSession is one ACP session: identity, project store, transcript header,
// in-memory history, and the cancel handle for the running turn.
type AcpSession struct {
	ID     string
	Cwd    string
	Store  *sessionstore.Store
	Header session.SessionHeader
	// Mapper translates agent events against this session's cwd.
	Mapper *eventMapper
	// Tools is the session-scoped tool set rooted at Cwd.
	Tools []agentcore.AgentTool
	// Registry is the session-scoped slash command registry.
	Registry *runtime.SlashRegistry
	// AdditionalDirectories are the extra workspace roots this session was
	// created/loaded with, kept so trust changes can rebuild the registry.
	AdditionalDirectories []string
	// Messages is the in-memory history; Persisted is how many of those
	// messages are already on disk, so a run only appends its tail.
	Messages agentcore.MessageList
	// Persisted is how many of Messages are already on disk; CurLeaf is the
	// on-disk entry id the next turn descends from. Together they let a run
	// append only its tail as a new branch, preserving the session tree on the
	// single project-scoped store.
	Persisted int
	CurLeaf   string
	Model     string
	Thinking  string
	Goal      string
	// SteeringMode and FollowUpMode are the session-level delivery policies for
	// the pending queue: "one-at-a-time" (default) or "all".
	SteeringMode string
	FollowUpMode string

	mu     sync.Mutex
	cancel context.CancelFunc

	// turnMu guards the pending queue and the single-run turn slot. A prompt
	// arriving while a turn is active is queued; steering/follow-up hooks pop
	// it inside the running turn, and any leftovers become the next run.
	turnMu     sync.Mutex
	turnCond   *sync.Cond
	turnActive bool
	queue      []*queuedPrompt
	delivered  []*queuedPrompt
}

// queuedPrompt is one session/prompt waiting to be delivered by the pending
// queue. done is closed when the consuming run finishes (or the queue is
// cancelled), at which point stopReason/runErr hold the run's outcome.
type queuedPrompt struct {
	text       string
	images     []agentcore.Content
	done       chan struct{}
	delivered  bool
	stopReason string
	runErr     error
}

// TurnHooks are the runtime-loop seams a session manager installs for pending
// queue delivery. They mirror runtime.RunConfig's GetSteeringMessages and
// GetFollowUpMessages.
type TurnHooks struct {
	Steering func(ctx context.Context) []agentcore.AgentMessage
	FollowUp func(ctx context.Context, agentCtx *agentcore.AgentContext) []agentcore.AgentMessage
	// InstallSeams, when non-nil, installs the run's hook seams into a freshly
	// built RunConfig. Dispatcher binds it per session so RuntimeRunner stays
	// agnostic of session identity. An error fails the turn closed.
	InstallSeams func(cfg *runtime.RunConfig) error
}

// SetCancel installs the cancel handle of the running turn.
func (s *AcpSession) SetCancel(cancel context.CancelFunc) {
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
}

// Cancel cancels the running turn, if any.
func (s *AcpSession) Cancel() {
	s.mu.Lock()
	c := s.cancel
	s.mu.Unlock()
	s.turnMu.Lock()
	for _, p := range s.queue {
		p.delivered = true
		p.stopReason = "cancelled"
		close(p.done)
	}
	s.queue = nil
	if s.turnCond != nil {
		s.turnCond.Broadcast()
	}
	s.turnMu.Unlock()
	if c != nil {
		c()
	}
}

// tryRun claims the single turn slot. It returns true when the caller becomes
// the running turn; otherwise the prompt is queued and the caller must wait.
func (s *AcpSession) tryRun(p *queuedPrompt) bool {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.initTurnQueueLocked()
	if !s.turnActive {
		s.turnActive = true
		return true
	}
	s.queue = append(s.queue, p)
	return false
}

// waitForTurn blocks until p is delivered by steering/follow-up or becomes the
// head of the queue when the running turn finishes.
func (s *AcpSession) waitForTurn(p *queuedPrompt) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.initTurnQueueLocked()
	for {
		if p.delivered {
			return
		}
		if !s.turnActive && len(s.queue) > 0 && s.queue[0] == p {
			s.queue = s.queue[1:]
			s.turnActive = true
			return
		}
		s.turnCond.Wait()
	}
}

// queueLen returns the number of prompts waiting in the pending queue.
func (s *AcpSession) queueLen() int {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return len(s.queue)
}

// popSteering pops pending prompts for steering delivery at a tool boundary.
func (s *AcpSession) popSteering(all bool) []*queuedPrompt {
	return s.popQueue(all)
}

// popFollowUp pops pending prompts for follow-up delivery at a settle point.
func (s *AcpSession) popFollowUp(all bool) []*queuedPrompt {
	return s.popQueue(all)
}

func (s *AcpSession) popQueue(all bool) []*queuedPrompt {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.initTurnQueueLocked()
	if len(s.queue) == 0 {
		return nil
	}
	n := 1
	if all {
		n = len(s.queue)
	}
	items := append([]*queuedPrompt(nil), s.queue[:n]...)
	s.queue = s.queue[n:]
	for _, p := range items {
		p.delivered = true
	}
	s.delivered = append(s.delivered, items...)
	s.turnCond.Broadcast()
	return items
}

// finishTurn releases the turn slot and resolves every prompt delivered during
// the run. Leftover queue entries wake the next runner.
func (s *AcpSession) finishTurn(stopReason string, runErr error) {
	s.turnMu.Lock()
	s.turnActive = false
	for _, p := range s.delivered {
		p.stopReason = stopReason
		p.runErr = runErr
		close(p.done)
	}
	s.delivered = nil
	if s.turnCond != nil {
		s.turnCond.Broadcast()
	}
	s.turnMu.Unlock()
}

func (s *AcpSession) initTurnQueueLocked() {
	if s.turnCond == nil {
		s.turnCond = sync.NewCond(&s.turnMu)
	}
}

// SessionRunner executes one prompt turn against pigo's agent loop. The runner
// owns the run context; it receives the accumulated history and returns the
// full post-run message list plus the final assistant message.
type SessionRunner interface {
	Run(ctx context.Context, prompt string, images []agentcore.Content, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error)
}

// TooledRunner is implemented by session runners that accept a per-session tool
// set. SessionManager uses it when present so a shared process can run each
// session against its own roots instead of the process-wide template.
type TooledRunner interface {
	RunWithTools(ctx context.Context, prompt string, images []agentcore.Content, history agentcore.MessageList, sysPrompt string, tools []agentcore.AgentTool, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error)
}

// SessionManager owns the live ACP sessions and the project-scoped stores they
// persist to. It serializes prompt execution per session by construction: each
// session has one cancel handle and Run replaces it per turn.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*AcpSession
	stores   map[string]*sessionstore.Store
	runner   SessionRunner
}

// NewSessionManager builds a manager backed by runner.
func NewSessionManager(runner SessionRunner) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*AcpSession),
		stores:   make(map[string]*sessionstore.Store),
		runner:   runner,
	}
}

// StoreForWorkspace returns the project store for a workspace, opening it once
// per process.
func (m *SessionManager) StoreForWorkspace(pigoHome, workspacePath string) (*sessionstore.Store, error) {
	slug := sessionstore.WorkspaceSlug(workspacePath)
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.stores[slug]; ok {
		return s, nil
	}
	s, err := sessionstore.OpenForWorkspace(pigoHome, workspacePath)
	if err != nil {
		return nil, err
	}
	m.stores[slug] = s
	return s, nil
}

// New creates a fresh persisted session in the given workspace with an
// isolated session context.
func (m *SessionManager) New(cwd, model string, ctx SessionContext, store *sessionstore.Store) (*AcpSession, error) {
	now := time.Now().UTC()
	id := session.NewID(now)
	header := session.SessionHeader{
		ID:           id,
		CreatedAt:    now,
		UpdatedAt:    now,
		Model:        model,
		SystemPrompt: ctx.SysPrompt,
		Cwd:          cwd,
	}
	meta := sessionstore.NewMetadata(id, "Session", "pigo", model, cwd)
	if err := store.Create(meta, header, nil); err != nil {
		return nil, err
	}
	sess := &AcpSession{
		ID:                    id,
		Cwd:                   cwd,
		Store:                 store,
		Header:                header,
		Mapper:                newEventMapper(cwd),
		Tools:                 ctx.Tools,
		Registry:              ctx.Registry,
		AdditionalDirectories: ctx.AdditionalDirectories,
		Model:                 model,
	}
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()
	return sess, nil
}

// Load restores an existing persisted session into memory.
func (m *SessionManager) Load(cwd, sessionID, model string, ctx SessionContext, store *sessionstore.Store) (*AcpSession, error) {
	meta, header, msgs, err := store.Load(sessionID)
	if err != nil {
		return nil, err
	}
	if model == "" {
		model = meta.ModelName
	}
	// Rebuild the persisted system prompt from the session's own cwd so a
	// shared process never leaves a stale startup-directory prompt behind.
	header.SystemPrompt = ctx.SysPrompt
	if err := store.UpdateHeader(sessionID, header); err != nil {
		return nil, err
	}
	curLeaf := ""
	if _, entries, err := store.TranscriptStore().LoadEntries(sessionID); err == nil && len(entries) > 0 {
		curLeaf = entries[len(entries)-1].ID
	}
	sess := &AcpSession{
		ID:                    sessionID,
		Cwd:                   cwd,
		Store:                 store,
		Header:                header,
		Mapper:                newEventMapper(cwd),
		Tools:                 ctx.Tools,
		Registry:              ctx.Registry,
		AdditionalDirectories: ctx.AdditionalDirectories,
		Messages:              msgs,
		Persisted:             len(msgs),
		CurLeaf:               curLeaf,
		Model:                 model,
	}
	m.mu.Lock()
	if old, ok := m.sessions[sessionID]; ok {
		old.Cancel()
	}
	m.sessions[sessionID] = sess
	m.mu.Unlock()
	return sess, nil
}

// Get returns the live session, or nil.
func (m *SessionManager) Get(sessionID string) *AcpSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionID]
}

// All returns a snapshot of every live session.
func (m *SessionManager) All() []*AcpSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*AcpSession, 0, len(m.sessions))
	for _, sess := range m.sessions {
		out = append(out, sess)
	}
	return out
}

// Close cancels a running turn and drops the live session.
func (m *SessionManager) Close(sessionID string) {
	m.mu.Lock()
	sess := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if sess != nil {
		sess.Cancel()
	}
}

// DeleteEverywhere removes a session from every open project store. The store
// delete is idempotent for missing sessions, so this is safe to call against
// stores that never contained the id.
func (m *SessionManager) DeleteEverywhere(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.stores {
		if err := s.Delete(sessionID); err != nil {
			return err
		}
	}
	delete(m.sessions, sessionID)
	return nil
}

// Run executes one prompt turn on a session, streams agent events through
// onEvent, persists the newly produced messages, and returns the final
// assistant message. A cancellation surfaces as an error whose context is
// cancelled; the caller maps that to ACP's cancelled stop reason. The turn
// slot is always released, even when persistence fails, so a later prompt
// cannot queue forever behind a failed sessionstore write.
func (m *SessionManager) Run(ctx context.Context, sess *AcpSession, prompt string, images []agentcore.Content, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks TurnHooks) (last *agentcore.AssistantMessage, err error) {
	defer func() {
		stop := "end_turn"
		switch {
		case err == ErrTurnCancelled:
			stop = "cancelled"
		case last != nil:
			switch last.StopReason {
			case agentcore.StopReasonLength:
				stop = "max_tokens"
			case agentcore.StopReasonAborted:
				stop = "cancelled"
			}
		}
		sess.finishTurn(stop, err)
	}()

	runCtx, cancel := context.WithCancel(ctx)
	sess.SetCancel(cancel)
	defer sess.SetCancel(nil)

	if sess.SteeringMode == "" {
		sess.SteeringMode = "one-at-a-time"
	}
	if sess.FollowUpMode == "" {
		sess.FollowUpMode = "one-at-a-time"
	}
	if hooks.Steering == nil {
		hooks.Steering = func(ctx context.Context) []agentcore.AgentMessage {
			return queuedToMessages(sess.popSteering(sess.SteeringMode == "all"))
		}
	}
	if hooks.FollowUp == nil {
		hooks.FollowUp = func(ctx context.Context, agentCtx *agentcore.AgentContext) []agentcore.AgentMessage {
			return queuedToMessages(sess.popFollowUp(sess.FollowUpMode == "all"))
		}
	}

	var messages agentcore.MessageList
	if tr, ok := m.runner.(TooledRunner); ok {
		messages, last, err = tr.RunWithTools(runCtx, prompt, images, sess.Messages, sess.Header.SystemPrompt, sess.Tools, sess.Model, sess.Thinking, beforeToolCall, onEvent, hooks)
	} else {
		messages, last, err = m.runner.Run(runCtx, prompt, images, sess.Messages, sess.Header.SystemPrompt, sess.Model, sess.Thinking, beforeToolCall, onEvent, hooks)
	}
	if err != nil && runCtx.Err() != nil {
		err = ErrTurnCancelled
	}

	// Persist the tail. Compaction can shrink the list below the persisted
	// cursor; clamp so we never slice out of bounds (mirrors headless persist).
	persisted := sess.Persisted
	if persisted > len(messages) {
		persisted = len(messages)
	}
	tail := messages[persisted:]
	if len(tail) > 0 {
		now := time.Now().UTC()
		sess.Header.UpdatedAt = now
		leaf, perr := sess.Store.AppendBranch(sess.ID, sess.Header, sess.CurLeaf, tail)
		if perr != nil {
			return last, perr
		}
		sess.CurLeaf = leaf
		sess.Persisted = len(messages)
	}
	sess.Messages = messages

	return last, err
}

func queuedToMessages(items []*queuedPrompt) []agentcore.AgentMessage {
	out := make([]agentcore.AgentMessage, 0, len(items))
	for _, p := range items {
		content := agentcore.ContentList{agentcore.NewTextContent(p.text)}
		content = append(content, p.images...)
		out = append(out, agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   content,
		})
	}
	return out
}
