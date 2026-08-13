package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/session"
	sstore "github.com/smallnest/pigo/internal/sessionstore"
)

// ErrInputQueueFull is returned when a child session's input queue is full.
var ErrInputQueueFull = errors.New("subagent: input queue full")

// ChildSession is a first-class child session created by the task tool. It owns
// an independent message list, can receive user input while running, and stays
// alive after the initial delegated task so the client can continue it.
type ChildSession struct {
	ID           string
	ParentID     string
	ToolCallID   string
	Type         string
	SystemPrompt string
	Tools        []agentcore.AgentTool
	NewRunConfig func() RunConfig
	ConfigHook   func(*RunConfig)
	Cwd          string
	Home         string
	Store        *sstore.Store
	Header       session.SessionHeader
	reg          *Registry

	mu             sync.Mutex
	Messages       agentcore.MessageList
	Persisted      int
	CurLeaf        string
	running        bool
	cancel         context.CancelFunc
	inputCh        chan agentcore.AgentMessage
	lastActivityAt time.Time
}

// Registry tracks live child sessions keyed by deterministic id.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*ChildSession
	sink     EventSink
	home     string
	stores   map[string]*sstore.Store
}

// EventSink receives every raw agent event emitted by a child session. It is
// wired by the ACP dispatcher so child events can be mapped and sent as
// session/update notifications under the child session id.
type EventSink func(parentSessionID, childSessionID string, ev agentcore.AgentEvent)

// NewRegistry builds an empty child-session registry.
func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*ChildSession),
		stores:   make(map[string]*sstore.Store),
	}
}

// SetHome enables disk persistence and loads the subagent index for recovery.
func (r *Registry) SetHome(home string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.home = home
}

// SetEventSink installs the sink invoked for every child event.
func (r *Registry) SetEventSink(sink EventSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sink = sink
}

func (r *Registry) eventSink() EventSink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sink
}

// Get returns the live child session with id, or nil.
func (r *Registry) Get(id string) *ChildSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

// CreateOrGet returns the deterministic child session for a parent session and
// tool call, creating it on first use.
func (r *Registry) CreateOrGet(
	parentID, toolCallID, subagentType, systemPrompt string,
	tools []agentcore.AgentTool,
	factory func() RunConfig,
	hook func(*RunConfig),
	cwd string,
) *ChildSession {
	id := SessionID(parentID, toolCallID)
	r.mu.Lock()
	if s, ok := r.sessions[id]; ok {
		r.mu.Unlock()
		return s
	}
	s := &ChildSession{
		ID:           id,
		ParentID:     parentID,
		ToolCallID:   toolCallID,
		Type:         subagentType,
		SystemPrompt: systemPrompt,
		Tools:        tools,
		NewRunConfig: factory,
		ConfigHook:   hook,
		Cwd:          cwd,
		Home:         r.home,
		reg:          r,
		inputCh:      make(chan agentcore.AgentMessage, 64),
	}
	r.sessions[id] = s
	r.mu.Unlock()
	_ = s.ensurePersisted()
	return s
}

// Load returns the live child session with id, falling back to the persisted
// index so a restarted process can reattach to a child session.
func (r *Registry) Load(id string) *ChildSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		return s
	}
	return nil
}

// storeFor returns the shared project store for a workspace. All child
// sessions in one registry reuse the same Store so concurrent persistence is
// serialized by the store's own mutex instead of racing on shared index and
// metadata files.
func (r *Registry) storeFor(cwd string) (*sstore.Store, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.stores[cwd]; ok {
		return st, nil
	}
	st, err := sstore.OpenForWorkspace(r.home, cwd)
	if err != nil {
		return nil, err
	}
	r.stores[cwd] = st
	return st, nil
}

func (s *ChildSession) ensurePersisted() error {
	if s.reg == nil || s.Home == "" || s.Cwd == "" {
		return nil
	}
	st, err := s.reg.storeFor(s.Cwd)
	if err != nil {
		return err
	}
	s.Store = st
	meta, header, msgs, err := st.Load(s.ID)
	if err == nil {
		s.Header = header
		s.Messages = msgs
		s.Persisted = len(msgs)
		if projection, pe := st.Projection(s.ID, ""); pe == nil {
			s.CurLeaf = projection.LeafID
		}
		ApplySubagentMetadata(&meta, s.ParentID, s.ToolCallID, s.Type)
		return st.SaveMetadata(meta)
	}
	now := time.Now().UTC()
	s.Header = session.SessionHeader{
		Version:       session.SchemaVersion,
		ID:            s.ID,
		CreatedAt:     now,
		UpdatedAt:     now,
		SystemPrompt:  s.SystemPrompt,
		Cwd:           s.Cwd,
		ParentSession: s.ParentID,
	}
	meta = sstore.NewMetadata(s.ID, "Session", "pigo", "", s.Cwd)
	ApplySubagentMetadata(&meta, s.ParentID, s.ToolCallID, s.Type)
	return st.Create(meta, s.Header, nil)
}

func (s *ChildSession) persist() error {
	if s.Store == nil {
		return nil
	}
	persisted := s.Persisted
	if persisted > len(s.Messages) {
		persisted = len(s.Messages)
	}
	tail := s.Messages[persisted:]
	if len(tail) == 0 {
		return nil
	}
	s.Header.UpdatedAt = time.Now().UTC()
	leaf, err := s.Store.AppendBranch(s.ID, s.Header, s.CurLeaf, tail)
	if err != nil {
		return err
	}
	s.Persisted = len(s.Messages)
	s.CurLeaf = leaf
	return nil
}

// Prompt queues a user message into the child session. While the initial run is
// active the message is injected at the next turn/tool boundary; after the run
// settles a later implementation starts a fresh turn from it.
func (s *ChildSession) Prompt(text string) error {
	msg := agentcore.UserMessage{
		RoleField: agentcore.RoleUser,
		Content:   agentcore.ContentList{agentcore.NewTextContent(text)},
	}
	select {
	case s.inputCh <- msg:
		return nil
	default:
		return ErrInputQueueFull
	}
}

// Running reports whether a child turn is currently active.
func (s *ChildSession) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Cancel aborts the currently running child turn, if any. It is safe to call
// when the child is idle; the next run starts with a fresh context.
func (s *ChildSession) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// LastActivity returns the last activity timestamp (zero when none yet).
func (s *ChildSession) LastActivity() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivityAt
}

func (s *ChildSession) touchActivity() {
	s.mu.Lock()
	s.lastActivityAt = time.Now().UTC()
	s.mu.Unlock()
}

// Continue starts a fresh turn from a user prompt on an already-created child
// session. It is used when the initial delegated task has settled and the user
// keeps talking to the child session.
func (s *ChildSession) Continue(
	ctx context.Context,
	prompt string,
	onEvent func(agentcore.AgentEvent),
	onText func(string),
) (string, *agentcore.AssistantMessage, error) {
	return s.RunInitial(ctx, prompt, onEvent, onText)
}

// RunInitial runs the delegated task on a fresh child context, drains the child
// loop, and returns the final text plus final assistant message. Input queued
// via Prompt is injected through the loop's follow-up/steering seams.
func (s *ChildSession) RunInitial(
	ctx context.Context,
	prompt string,
	onEvent func(agentcore.AgentEvent),
	onText func(string),
) (string, *agentcore.AssistantMessage, error) {
	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		cancel()
		return "", nil, errors.New("subagent: child session already running")
	}
	s.running = true
	s.cancel = cancel
	s.Messages = append(s.Messages, agentcore.UserMessage{
		RoleField: agentcore.RoleUser,
		Content:   agentcore.ContentList{agentcore.NewTextContent(prompt)},
	})
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.running = false
		s.mu.Unlock()
	}()

	cfg := s.NewRunConfig()
	if cfg.Stream == nil {
		return "", nil, errors.New("subagent: child session has no run configuration")
	}
	if s.ConfigHook != nil {
		s.ConfigHook(&cfg)
	}
	if cfg.GetFollowUpMessages == nil {
		cfg.GetFollowUpMessages = func(context.Context, *agentcore.AgentContext) []agentcore.AgentMessage {
			return s.popInputs(true)
		}
	}
	if cfg.GetSteeringMessages == nil {
		cfg.GetSteeringMessages = func(context.Context) []agentcore.AgentMessage {
			return s.popInputs(true)
		}
	}

	corrections := 0
	var final *agentcore.AssistantMessage
	var text string
	for {
		agentCtx := &agentcore.AgentContext{
			SystemPrompt: s.SystemPrompt,
			Messages:     s.Messages,
			Tools:        s.Tools,
		}
		var streamed strings.Builder
		stream := StartRun(runCtx, agentCtx, cfg)
		curFinal, err := DrainStream(runCtx, stream, StreamHandler{
			OnText: func(delta string) {
				streamed.WriteString(delta)
				s.touchActivity()
				if onText != nil {
					onText(delta)
				}
			},
			OnEvent: func(ev agentcore.AgentEvent) {
				s.touchActivity()
				if onEvent != nil {
					onEvent(ev)
				}
			},
		})
		s.mu.Lock()
		s.Messages = agentCtx.Messages
		s.mu.Unlock()
		if perr := s.persist(); perr != nil {
			return "", nil, perr
		}
		if err != nil {
			return "", nil, err
		}
		final = curFinal
		text = ""
		if final != nil {
			text = agentcore.ContentToText(final.Content)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			text = strings.TrimSpace(streamed.String())
		}
		if final != nil && (final.StopReason == agentcore.StopReasonError || final.StopReason == agentcore.StopReasonAborted) {
			if final.StopReason == agentcore.StopReasonError && final.ErrorMessage == "" && len(text) >= minSubAgentErrorContentLen {
				if hasSubAgentDoneMarker(text) {
					text = stripSubAgentDoneMarker(text)
				}
				break
			}
			return text, final, fmt.Errorf("sub-agent failed (%s): %s", final.StopReason, subAgentFailureText(final, text))
		}
		if final != nil && final.ErrorMessage != "" && !strings.Contains(text, final.ErrorMessage) {
			if text != "" {
				text = final.ErrorMessage + "\n\n" + text
			} else {
				text = final.ErrorMessage
			}
		}
		if text == "" {
			return "", final, fmt.Errorf("sub-agent produced no text output after tool use")
		}
		if hasSubAgentDoneMarker(text) {
			text = stripSubAgentDoneMarker(text)
			break
		}
		if corrections >= maxSubAgentCorrections {
			return "", final, fmt.Errorf("sub-agent did not produce a completed final report (missing %s after %d corrections)", subAgentDoneMarker, corrections)
		}
		corrections++
		s.Messages = append(s.Messages, agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   agentcore.ContentList{agentcore.NewTextContent(subAgentCorrectionMessage)},
		})
	}
	if perr := s.persist(); perr != nil {
		return "", nil, perr
	}
	return text, final, nil
}

func (s *ChildSession) popInputs(all bool) []agentcore.AgentMessage {
	var out []agentcore.AgentMessage
	for {
		select {
		case m := <-s.inputCh:
			out = append(out, m)
			if !all {
				return out
			}
		default:
			return out
		}
	}
}

// SessionID deterministically derives a child session id from the parent
// session and the task tool call id. Replaying or retrying the same task call
// reattaches to the same child session instead of spawning a twin.
func SessionID(parentSessionID, toolCallID string) string {
	sum := sha256.Sum256([]byte(parentSessionID + "\x00" + toolCallID))
	return "subagent-" + hex.EncodeToString(sum[:16])
}

// ApplySubagentMetadata stamps a persisted child session's relationship
// metadata. The same fields are mirrored into CustomMetadata so clients that
// read either shape see the relationship.
func ApplySubagentMetadata(meta *sstore.Metadata, parentSessionID, parentToolCallID, subagentType string) {
	if meta == nil {
		return
	}
	meta.SessionKind = sstore.SessionKindSubagent
	meta.ParentSessionID = parentSessionID
	meta.ParentToolCallID = parentToolCallID
	meta.SubagentType = subagentType
	raw := map[string]any{
		"kind":             sstore.SessionKindSubagent,
		"parentSessionId":  parentSessionID,
		"parentToolCallId": parentToolCallID,
		"subagentType":     subagentType,
	}
	if b, err := json.Marshal(raw); err == nil {
		meta.CustomMetadata = b
	}
}
