package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/cli"
	goalpkg "github.com/smallnest/pigo/internal/cli/goal"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/hooks"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/plugin"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

// serveGoalHost adapts a serve session to cli.Host so the real goal loop can
// run over HTTP without a terminal. It intentionally keeps the core goal loop
// intact and only replaces the terminal/store seams via goal.Options.
type serveGoalHost struct {
	cli.Host
	store      *sessionstore.Store
	header     session.SessionHeader
	agentCtx   *agentcore.AgentContext
	live       *cli.LiveConfig
	reg        *agenttool.ToolRegistry
	reminders  *runtime.ReminderRegistry
	creds      *provider.CredentialStore
	trust      *trust.Manager
	cwd        string
	goal       *agenttool.GoalState
	telemetry  *cli.TelemetryHolder
	dispatcher *hooks.Dispatcher
	hookDeps   run.HookDeps
}

func (h *serveGoalHost) Store() *sessionstore.Store           { return h.store }
func (h *serveGoalHost) Header() session.SessionHeader        { return h.header }
func (h *serveGoalHost) AgentCtx() *agentcore.AgentContext    { return h.agentCtx }
func (h *serveGoalHost) Live() *cli.LiveConfig                { return h.live }
func (h *serveGoalHost) Registry() *agenttool.ToolRegistry    { return h.reg }
func (h *serveGoalHost) Reminders() *runtime.ReminderRegistry { return h.reminders }
func (h *serveGoalHost) Slash() *runtime.SlashRegistry        { return nil }
func (h *serveGoalHost) Creds() *provider.CredentialStore     { return h.creds }
func (h *serveGoalHost) Notifier() *plugin.EventNotifier      { return nil }
func (h *serveGoalHost) NotifierHandle() func(agentcore.AgentEvent) {
	return nil
}
func (h *serveGoalHost) Trust() *trust.Manager              { return h.trust }
func (h *serveGoalHost) Goal() *agenttool.GoalState         { return h.goal }
func (h *serveGoalHost) Telemetry() *cli.TelemetryHolder    { return h.telemetry }
func (h *serveGoalHost) Dispatcher() *hooks.Dispatcher      { return h.dispatcher }
func (h *serveGoalHost) HookDeps() run.HookDeps             { return h.hookDeps }
func (h *serveGoalHost) Cwd() string                        { return h.cwd }
func (h *serveGoalHost) Input() *bufio.Reader               { return nil }
func (h *serveGoalHost) ConfirmMu() *sync.Mutex             { return nil }
func (h *serveGoalHost) CurLeaf() string                    { return "" }
func (h *serveGoalHost) SetCurLeaf(string)                  {}
func (h *serveGoalHost) Persisted() int                     { return 0 }
func (h *serveGoalHost) SetPersisted(int)                   {}
func (h *serveGoalHost) LastBtw() *agentcore.AgentContext   { return nil }
func (h *serveGoalHost) SetLastBtw(*agentcore.AgentContext) {}
func (h *serveGoalHost) LastBtwBase() int                   { return 0 }
func (h *serveGoalHost) SetLastBtwBase(int)                 {}

// makeGoalFunc builds the serve /goal backend. It loads the session context,
// runs the real goal loop with injected permission/persist seams, and returns
// a short terminal summary while the loop streams progress through out.
func makeGoalFunc(opts cliOptions, env run.Env, pigoHome string, thinking agentcore.ThinkingLevel) httpapi.GoalFunc {
	var mu sync.Mutex
	goals := make(map[string]*agenttool.GoalState)
	running := make(map[string]bool)
	return func(ctx context.Context, sessionID, directory, args string, out io.Writer, beforeToolCall agentcore.BeforeToolCallFunc, steering func() []string) (string, error) {
		store, err := sessionstore.OpenForWorkspace(pigoHome, directory)
		if err != nil {
			return "", err
		}
		meta, header, msgs, err := store.Load(sessionID)
		if err != nil {
			return "", err
		}
		model := meta.ModelName
		if model == "" {
			model = env.Model
		}
		header.Model = model
		header.Provider = env.ProviderName
		header.SystemPrompt = env.SysPrompt
		header.Cwd = directory

		live := &cli.LiveConfig{
			Model:         model,
			ProviderName:  env.ProviderName,
			Provider:      env.Provider,
			BaseURL:       opts.baseURL,
			Protocol:      opts.protocol,
			ThinkingLevel: thinking,
			ContextWindow: cli.DefaultContextWindow,
		}
		reg := agenttool.NewToolRegistry()
		for _, tool := range env.Tools {
			_ = reg.Register(tool)
		}
		creds := provider.NewCredentialStore(nil)
		creds.SetOverride(env.ProviderName, env.APIKey)
		live.Creds = creds
		mgr, _ := trust.NewManager(trust.DefaultPath())

		mu.Lock()
		state := goals[sessionID]
		if state == nil {
			state = agenttool.NewGoalState()
			goals[sessionID] = state
		}
		mu.Unlock()

		mu.Lock()
		isRunning := running[sessionID]
		mu.Unlock()
		arg := strings.TrimSpace(args)
		if isRunning {
			switch arg {
			case "", "status":
				return goalSummary(state), nil
			case "clear":
				state.Clear()
				return "goal cleared", nil
			case "pause", "resume":
				return "goal is already running; use /goal <new objective> to redirect it", nil
			default:
				state.UpdateObjective(arg)
				return "goal updated: " + arg, nil
			}
		}

		host := &serveGoalHost{
			store:     store,
			header:    header,
			agentCtx:  &agentcore.AgentContext{SystemPrompt: env.SysPrompt, Messages: msgs, Tools: env.Tools},
			live:      live,
			reg:       reg,
			reminders: runtime.NewReminderRegistry(),
			creds:     creds,
			trust:     mgr,
			cwd:       directory,
			goal:      state,
			telemetry: cli.NewTelemetryHolder(),
		}
		prevCount := len(msgs)
		persist := func() error {
			return persistGoalStore(store, sessionID, header, host.agentCtx.Messages, prevCount)
		}
		mu.Lock()
		running[sessionID] = true
		mu.Unlock()
		defer func() {
			mu.Lock()
			delete(running, sessionID)
			mu.Unlock()
		}()
		line := "/goal"
		if arg != "" {
			line += " " + arg
		}
		goalpkg.RunGoalWithOptions(
			func(context.CancelFunc) {},
			out,
			host,
			line,
			goalpkg.Options{
				Context:        ctx,
				BeforeToolCall: beforeToolCall,
				Persist:        persist,
				Steering:       steering,
			},
		)
		return goalSummary(state), nil
	}
}

func goalSummary(state *agenttool.GoalState) string {
	snap := state.Snapshot()
	switch snap.Status {
	case agenttool.GoalComplete:
		return "goal complete: " + snap.Summary
	case agenttool.GoalBlocked:
		return "goal blocked: " + snap.BlockReason
	case agenttool.GoalBudgetLimited:
		return fmt.Sprintf("goal paused - token budget reached (%d / %d). Run /goal resume to continue.", snap.TokensUsed, snap.TokenBudget)
	case agenttool.GoalPaused:
		return fmt.Sprintf("goal paused after %d turns. Run /goal resume to continue.", snap.Iterations)
	case agenttool.GoalActive:
		return fmt.Sprintf("goal: %s\nstatus: active\niterations: %d\nelapsed: %s", snap.Objective, snap.Iterations, time.Since(snap.StartedAt).Round(time.Second))
	default:
		return "goal finished"
	}
}

func persistGoalStore(store *sessionstore.Store, sessionID string, header session.SessionHeader, msgs agentcore.MessageList, prevCount int) error {
	var curLeaf string
	if proj, err := store.Projection(sessionID, ""); err == nil {
		curLeaf = proj.LeafID
	}
	header.UpdatedAt = time.Now().UTC()
	if len(msgs) < prevCount {
		// Auto-compaction was persisted by OnCompaction as a typed compaction
		// entry; flattening the tree with Save would destroy the retained tail.
		curLeaf = ""
	} else {
		tail := msgs
		if len(msgs) >= prevCount {
			tail = msgs[prevCount:]
		}
		_, err := store.AppendBranch(sessionID, header, curLeaf, tail)
		if err != nil {
			return err
		}
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	meta.ModelName = header.Model
	meta.LastActiveAt = header.UpdatedAt
	meta.MessageCount = len(msgs)
	return store.SaveMetadata(meta)
}
