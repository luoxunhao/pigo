package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/smallnest/pigo/internal/httpapi/gen"
)

const promptQueueLimit = 100

// PromptRunner executes one prompt turn. It returns the ACP-compatible result.
type PromptRunner func(ctx context.Context, directory, text string) (gen.PromptResponse, error)

type pendingPrompt struct {
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
	runner PromptRunner
	broker *EventBroker
	mu     sync.Mutex
	states map[string]*promptState
}

// NewPromptManager builds a manager.
func NewPromptManager(runner PromptRunner, broker *EventBroker) *PromptManager {
	return &PromptManager{runner: runner, broker: broker, states: make(map[string]*promptState)}
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
	p := &pendingPrompt{messageID: newMessageID(), req: req, done: make(chan gen.PromptResponse, 1)}
	if !st.running {
		st.running = true
		go m.run(st, p)
	} else {
		st.queue = append(st.queue, p)
	}
	queued := len(st.queue)
	st.mu.Unlock()
	m.publishQueue(sessionID, queued)
	return p, nil
}

func (m *PromptManager) run(st *promptState, first *pendingPrompt) {
	current := first
	for {
		st.mu.Lock()
		ctx, cancel := context.WithCancel(context.Background())
		st.cancel = cancel
		st.mu.Unlock()

		m.publishStatus(current.req.Directory, current.messageID, "running")
		resp, err := m.runner(ctx, current.req.Directory, promptText(current.req.Prompt))
		if err != nil && ctx.Err() != nil {
			resp = gen.PromptResponse{MessageId: current.messageID, StopReason: "cancelled"}
			err = nil
		}
		if err != nil {
			current.err = Internal(err.Error())
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
		m.publishStatus(current.req.Directory, current.messageID, "idle")
		m.publishQueue(current.req.Directory, 0)
		return
	}
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
	m.publishQueue(sessionID, 0)
	m.broker.Publish("session.status", map[string]any{"sessionId": sessionID, "status": "cancelled"})
	return nil
}

func (m *PromptManager) publishQueue(sessionID string, queued int) {
	m.broker.Publish("queue.updated", map[string]any{
		"sessionId": sessionID, "queuedCount": queued,
	})
}

func (m *PromptManager) publishStatus(directory, messageID, status string) {
	data := map[string]any{"status": status}
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
