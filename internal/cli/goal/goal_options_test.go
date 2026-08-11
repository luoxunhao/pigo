package goal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/hooks"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/trust"
)

func TestRunGoalWithOptionsInjectsSeams(t *testing.T) {
	prov := &goalFakeProvider{
		turns: [][]provider.AssistantMessageEvent{
			goalToolCallTurn("call-1", "echo", `{}`),
			goalTextTurn("done"),
		},
	}
	reg := agenttool.NewToolRegistry()
	_ = reg.Register(goalFakeTool{})
	state := agenttool.NewGoalState()
	state.Start("goal-1", "do x", 0)
	host := &goalFakeHost{
		agentCtx: &agentcore.AgentContext{
			SystemPrompt: "sys",
			Messages: agentcore.MessageList{
				agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("start")}},
			},
			Tools: []agentcore.AgentTool{goalFakeTool{}},
		},
		live: &cli.LiveConfig{
			Model:         "m",
			ProviderName:  "goal-fake",
			Provider:      prov,
			ThinkingLevel: agentcore.ThinkingMedium,
			ContextWindow: 10000,
		},
		reg:   reg,
		goal:  state,
		creds: provider.NewCredentialStore(nil),
		trust: newTestTrust(t),
		cwd:   ".",
	}
	var out bytes.Buffer
	seamCalls := 0
	persistCalled := false
	RunGoalWithOptions(
		func(context.CancelFunc) {},
		&out,
		host,
		"/goal do x",
		Options{
			BeforeToolCall: func(ctx context.Context, call agentcore.AgentToolCall) *agentcore.BeforeToolCallDecision {
				seamCalls++
				return nil
			},
			Persist: func() error {
				persistCalled = true
				return nil
			},
		},
	)
	if seamCalls == 0 {
		t.Fatal("injected BeforeToolCall was not used")
	}
	if !persistCalled {
		t.Fatal("injected Persist was not called")
	}
}

func newTestTrust(t *testing.T) *trust.Manager {
	t.Helper()
	mgr, err := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

type goalFakeProvider struct {
	mu    sync.Mutex
	turns [][]provider.AssistantMessageEvent
	calls int
}

func (p *goalFakeProvider) Name() string { return "goal-fake" }
func (p *goalFakeProvider) Models() []provider.Model {
	return []provider.Model{{Provider: "goal-fake", ID: "m", SupportsTools: true}}
}
func (p *goalFakeProvider) StreamCompletion(ctx context.Context, _ provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	var turn []provider.AssistantMessageEvent
	if idx >= len(p.turns) {
		turn = goalTextTurn("")
	} else {
		turn = p.turns[idx]
	}
	p.mu.Unlock()
	s := provider.NewAssistantMessageEventStream(0)
	go func() {
		for _, ev := range turn {
			if err := s.Emit(ctx, ev); err != nil {
				s.SetError(err)
				break
			}
		}
		s.Close()
	}()
	return s, nil
}

func goalTextTurn(text string) []provider.AssistantMessageEvent {
	partial := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}
	withText := partial
	withText.Content = agentcore.ContentList{agentcore.NewTextContent(text)}
	final := withText
	final.StopReason = agentcore.StopReasonEndTurn
	return []provider.AssistantMessageEvent{
		provider.StreamStartEvent{Partial: partial},
		provider.StreamTextEvent{Partial: withText},
		provider.StreamDoneEvent{Message: final},
	}
}

func goalToolCallTurn(id, name, args string) []provider.AssistantMessageEvent {
	partial := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}
	withCall := partial
	withCall.Content = agentcore.ContentList{agentcore.NewToolCallContent(id, name, json.RawMessage(args))}
	final := withCall
	final.StopReason = agentcore.StopReasonToolUse
	return []provider.AssistantMessageEvent{
		provider.StreamStartEvent{Partial: partial},
		provider.StreamToolCallEvent{Partial: withCall},
		provider.StreamDoneEvent{Message: final},
	}
}

type goalFakeTool struct{}

func (goalFakeTool) Name() string        { return "echo" }
func (goalFakeTool) Description() string { return "echo" }
func (goalFakeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (goalFakeTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionSequential
}
func (goalFakeTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	return agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("ok")}}, nil
}

type goalFakeHost struct {
	cli.Host
	agentCtx *agentcore.AgentContext
	live     *cli.LiveConfig
	reg      *agenttool.ToolRegistry
	goal     *agenttool.GoalState
	creds    *provider.CredentialStore
	trust    *trust.Manager
	cwd      string
}

func (h *goalFakeHost) AgentCtx() *agentcore.AgentContext          { return h.agentCtx }
func (h *goalFakeHost) Live() *cli.LiveConfig                      { return h.live }
func (h *goalFakeHost) Registry() *agenttool.ToolRegistry          { return h.reg }
func (h *goalFakeHost) Goal() *agenttool.GoalState                 { return h.goal }
func (h *goalFakeHost) Creds() *provider.CredentialStore           { return h.creds }
func (h *goalFakeHost) Trust() *trust.Manager                      { return h.trust }
func (h *goalFakeHost) Cwd() string                                { return h.cwd }
func (h *goalFakeHost) Input() *bufio.Reader                       { return nil }
func (h *goalFakeHost) ConfirmMu() *sync.Mutex                     { return nil }
func (h *goalFakeHost) Dispatcher() *hooks.Dispatcher              { return nil }
func (h *goalFakeHost) HookDeps() run.HookDeps                     { return run.HookDeps{} }
func (h *goalFakeHost) NotifierHandle() func(agentcore.AgentEvent) { return nil }
