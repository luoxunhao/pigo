package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/trust"
)

const promptQueueLimit = 100

// PromptRun carries the per-turn context a prompt runner needs: the owning
// session, the resolved model/thinking options, the prompt text, the event
// publisher for domain events, and the permission seam installed by serve.
type PromptRun struct {
	SessionID      string
	Directory      string
	MessageID      string
	Model          string
	ThinkingLevel  string
	Text           string
	BeforeToolCall agentcore.BeforeToolCallFunc
	Publish        func(eventType string, data map[string]any)
}

// PromptRunner executes one prompt turn and returns the ACP-compatible result.
type PromptRunner func(ctx context.Context, run PromptRun) (gen.PromptResponse, error)

type pendingPrompt struct {
	sessionID string
	messageID string
	req       gen.PromptRequest
	done      chan gen.PromptResponse
	err       *APIError
}

type promptState struct {
	mu      sync.Mutex
	cond    *sync.Cond
	running bool
	queue   []*pendingPrompt
	cancel  context.CancelFunc
}

// PromptManager owns per-session prompt queues and cancellation.
type PromptManager struct {
	runner      PromptRunner
	broker      *EventBroker
	permissions *PermissionManager
	trust       *trust.Manager
	autoReject  bool
	mu          sync.Mutex
	states      map[string]*promptState
}

// NewPromptManager builds a manager.
func NewPromptManager(runner PromptRunner, broker *EventBroker) *PromptManager {
	return &PromptManager{runner: runner, broker: broker, states: make(map[string]*promptState)}
}

// SetPermissionSeam installs the trust/permission gate used before side-effect
// tools run. A nil permission manager disables gating.
func (m *PromptManager) SetPermissionSeam(permissions *PermissionManager, mgr *trust.Manager) {
	m.permissions = permissions
	m.trust = mgr
}

// SetAutoRejectUntrusted blocks side-effect tools immediately in untrusted
// directories instead of publishing a permission request. In-process
// front-ends without a permission UI use this so an unanswered request never
// stalls a turn.
func (m *PromptManager) SetAutoRejectUntrusted(enabled bool) {
	m.autoReject = enabled
}

func (m *PromptManager) stateFor(sessionID string) *promptState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.states[sessionID]; ok {
		return st
	}
	st := &promptState{}
	st.cond = sync.NewCond(&st.mu)
	m.states[sessionID] = st
	return st
}

// SubmitSync queues a prompt and waits for its turn.
func (m *PromptManager) SubmitSync(sessionID string, req gen.PromptRequest) (gen.PromptResponse, *APIError) {
	p, apiErr := m.enqueue(sessionID, req)
	if apiErr != nil {
		return gen.PromptResponse{}, apiErr
	}
	resp := <-p.done
	return resp, p.err
}

// SubmitAsync queues a prompt and returns immediately.
func (m *PromptManager) SubmitAsync(sessionID string, req gen.PromptRequest) (gen.PromptAsyncResponse, *APIError) {
	p, apiErr := m.enqueue(sessionID, req)
	if apiErr != nil {
		return gen.PromptAsyncResponse{}, apiErr
	}
	return gen.PromptAsyncResponse{MessageId: p.messageID, Accepted: true}, nil
}

func (m *PromptManager) enqueue(sessionID string, req gen.PromptRequest) (*pendingPrompt, *APIError) {
	if req.Directory == "" {
		return nil, InvalidParams("directory is required")
	}
	st := m.stateFor(sessionID)
	st.mu.Lock()
	if len(st.queue) >= promptQueueLimit {
		st.mu.Unlock()
		return nil, &APIError{Status: 429, Code: CodeQueueFull, Message: "prompt queue is full"}
	}
	p := &pendingPrompt{sessionID: sessionID, messageID: newMessageID(), req: req, done: make(chan gen.PromptResponse, 1)}
	if !st.running {
		st.running = true
		go m.run(st, p)
	} else {
		st.queue = append(st.queue, p)
	}
	queued := len(st.queue)
	st.mu.Unlock()
	m.publishQueue(sessionID, req.Directory, queued)
	return p, nil
}

func (m *PromptManager) run(st *promptState, first *pendingPrompt) {
	current := first
	for {
		st.mu.Lock()
		ctx, cancel := context.WithCancel(context.Background())
		st.cancel = cancel
		st.mu.Unlock()

		m.publishStatus(current.sessionID, current.req.Directory, current.messageID, "running")
		run := PromptRun{
			SessionID:      current.sessionID,
			Directory:      current.req.Directory,
			MessageID:      current.messageID,
			Model:          stringPtr(current.req.Model),
			ThinkingLevel:  stringPtr(current.req.ThinkingLevel),
			Text:           promptText(current.req.Prompt),
			BeforeToolCall: m.beforeToolCall(current.sessionID, current.req.Directory),
			Publish: func(eventType string, data map[string]any) {
				if data == nil {
					data = map[string]any{}
				}
				data["sessionId"] = current.sessionID
				if _, ok := data["messageId"]; !ok {
					data["messageId"] = current.messageID
				}
				m.broker.Publish(eventType, data)
			},
		}
		resp, err := m.runner(ctx, run)
		if err != nil && ctx.Err() != nil {
			resp = gen.PromptResponse{MessageId: current.messageID, StopReason: "cancelled"}
			err = nil
		}
		if err != nil {
			current.err = Internal(err.Error())
			m.broker.Publish("session.status", map[string]any{
				"sessionId": current.sessionID,
				"messageId": current.messageID,
				"status":    "error",
				"error":     err.Error(),
			})
		} else {
			resp.MessageId = current.messageID
		}

		st.mu.Lock()
		st.cancel = nil
		cancel()
		current.done <- resp
		if len(st.queue) > 0 {
			current = st.queue[0]
			st.queue = st.queue[1:]
			st.mu.Unlock()
			continue
		}
		st.running = false
		st.mu.Unlock()
		m.publishStatus(current.sessionID, current.req.Directory, current.messageID, "idle")
		m.publishQueue(current.sessionID, current.req.Directory, 0)
		return
	}
}

// beforeToolCall builds the trust/permission seam for one prompt turn. When
// auto-reject is configured the gate blocks immediately instead of asking, so
// non-interactive in-process front-ends never stall on an unanswered request.
func (m *PromptManager) beforeToolCall(sessionID, directory string) agentcore.BeforeToolCallFunc {
	if m.permissions == nil || m.trust == nil {
		return nil
	}
	return func(ctx context.Context, call agentcore.AgentToolCall) *agentcore.BeforeToolCallDecision {
		if !trust.SideEffectTools[call.Name] || m.trust.IsTrusted(directory) {
			return nil
		}
		if m.autoReject {
			return blockedTool(call.Name, directory+" is not trusted (use /trust to allow)")
		}
		payload := map[string]any{
			"toolCallId": call.ID,
			"title":      call.Name,
			"kind":       toolKind(call.Name),
			"status":     "pending",
			"rawInput":   call.Arguments,
			"summary":    trust.ToolCallSummary(call),
		}
		options := []map[string]any{
			{"optionId": "allow_once", "kind": "allow_once", "name": "Allow once"},
			{"optionId": "allow_always", "kind": "allow_always", "name": "Always allow"},
			{"optionId": "reject_once", "kind": "reject_once", "name": "Reject"},
		}
		option, apiErr := m.permissions.Ask(sessionID, payload, options)
		if apiErr != nil {
			return blockedTool(call.Name, "permission request failed: "+apiErr.Message)
		}
		switch option {
		case "allow_always":
			_ = m.trust.SetDecision(directory, trust.Trusted)
			return nil
		case "allow_once":
			return nil
		default:
			return blockedTool(call.Name, "rejected")
		}
	}
}

func toolKind(name string) string {
	switch name {
	case "read":
		return "read"
	case "write", "edit":
		return "edit"
	case "bash", "bash_output", "bash_kill":
		return "execute"
	case "grep", "find":
		return "search"
	case "webfetch", "websearch":
		return "fetch"
	case "todo":
		return "think"
	default:
		return "other"
	}
}

func blockedTool(name, reason string) *agentcore.BeforeToolCallDecision {
	msg := "tool " + name + " blocked: " + reason
	content := agentcore.ContentList{agentcore.NewTextContent(msg)}
	return &agentcore.BeforeToolCallDecision{Block: true, Content: &content}
}

func stringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Cancel cancels the running turn and clears the queue.
func (m *PromptManager) Cancel(sessionID string) *APIError {
	st := m.stateFor(sessionID)
	st.mu.Lock()
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	for _, p := range st.queue {
		p.done <- gen.PromptResponse{MessageId: p.messageID, StopReason: "cancelled"}
	}
	st.queue = nil
	st.mu.Unlock()
	m.publishQueue(sessionID, "", 0)
	m.broker.Publish("session.status", map[string]any{"sessionId": sessionID, "status": "cancelled"})
	return nil
}

func (m *PromptManager) publishQueue(sessionID, directory string, queued int) {
	m.broker.Publish("queue.updated", map[string]any{
		"sessionId": sessionID, "directory": directory, "queuedCount": queued,
	})
}

func (m *PromptManager) publishStatus(sessionID, directory, messageID, status string) {
	data := map[string]any{"status": status}
	if sessionID != "" {
		data["sessionId"] = sessionID
	}
	if directory != "" {
		data["directory"] = directory
	}
	if messageID != "" {
		data["messageId"] = messageID
	}
	m.broker.Publish("session.status", data)
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

func newMessageID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "msg-unknown"
	}
	return "msg-" + hex.EncodeToString(b[:])
}
