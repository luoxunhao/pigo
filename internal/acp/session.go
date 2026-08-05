package acp

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
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
	// Messages is the in-memory history; Persisted is how many of those
	// messages are already on disk, so a run only appends its tail.
	Messages  agentcore.MessageList
	Persisted int
	Model     string
	Thinking  string
	Goal      string

	mu     sync.Mutex
	cancel context.CancelFunc
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
	if c != nil {
		c()
	}
}

// SessionRunner executes one prompt turn against pigo's agent loop. The runner
// owns the run context; it receives the accumulated history and returns the
// full post-run message list plus the final assistant message.
type SessionRunner interface {
	Run(ctx context.Context, prompt string, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent)) (agentcore.MessageList, *agentcore.AssistantMessage, error)
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

// New creates a fresh persisted session in the given workspace.
func (m *SessionManager) New(cwd, model, sysPrompt string, store *sessionstore.Store) (*AcpSession, error) {
	now := time.Now().UTC()
	id := session.NewID(now)
	header := session.SessionHeader{
		ID:           id,
		CreatedAt:    now,
		UpdatedAt:    now,
		Model:        model,
		SystemPrompt: sysPrompt,
		Cwd:          cwd,
	}
	meta := sessionstore.NewMetadata(id, "Session", "pigo", model, cwd)
	if err := store.Create(meta, header, nil); err != nil {
		return nil, err
	}
	sess := &AcpSession{ID: id, Cwd: cwd, Store: store, Header: header, Model: model}
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()
	return sess, nil
}

// Load restores an existing persisted session into memory.
func (m *SessionManager) Load(cwd, sessionID, model string, store *sessionstore.Store) (*AcpSession, error) {
	meta, header, msgs, err := store.Load(sessionID)
	if err != nil {
		return nil, err
	}
	if model == "" {
		model = meta.ModelName
	}
	sess := &AcpSession{
		ID:        sessionID,
		Cwd:       cwd,
		Store:     store,
		Header:    header,
		Messages:  msgs,
		Persisted: len(msgs),
		Model:     model,
	}
	m.mu.Lock()
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
// cancelled; the caller maps that to ACP's cancelled stop reason.
func (m *SessionManager) Run(ctx context.Context, sess *AcpSession, prompt string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent)) (*agentcore.AssistantMessage, error) {
	runCtx, cancel := context.WithCancel(ctx)
	sess.SetCancel(cancel)
	defer sess.SetCancel(nil)

	messages, last, err := m.runner.Run(runCtx, prompt, sess.Messages, sess.Header.SystemPrompt, sess.Model, sess.Thinking, beforeToolCall, onEvent)
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
		if perr := sess.Store.Append(sess.ID, now, tail); perr != nil {
			return last, perr
		}
		sess.Persisted = len(messages)
	}
	sess.Messages = messages
	return last, err
}
