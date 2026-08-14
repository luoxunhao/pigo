// This file binds the full-screen TUI to the real agent run seam and the local
// session store (US-009, FR-16/17). It is the TUI counterpart to the REPL's
// replDeps + streamRun + cli.PersistTurn plumbing (internal/cli/repl): it
// assembles an AgentContext + RunConfig from the model's Options, feeds them to
// the event bridge (bridge.go's startRun → runtime.StartRun/DrainStream), and
// persists the growing conversation to $PIGO_HOME/sessions.db after each turn.
//
// It deliberately imports the SHARED lower-level packages the REPL also uses
// (session, runtime, provider, cli, cli/run, cli/headless, cli/ui) rather than
// the repl package itself, so the two entry paths share one store and one
// run-config shape without an import cycle (repl and tui are siblings; prompts
// imports tui, so tui must not reach back into repl/prompts).
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/cli/headless"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/cli/ui"
	"github.com/smallnest/pigo/internal/compaction"
	"github.com/smallnest/pigo/internal/contextbuild"
	"github.com/smallnest/pigo/internal/hooks"
	"github.com/smallnest/pigo/internal/httpclient"
	"github.com/smallnest/pigo/internal/memory"
	"github.com/smallnest/pigo/internal/plugin"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/sessiontitle"
	"github.com/smallnest/pigo/internal/trust"
)

// runSession holds the assembled per-session state for a TUI run: the persisted
// store + header, the growing conversation context, the live (mutable) run
// config, and the tool/credential collaborators. It mirrors the subset of
// repl.replDeps the TUI needs, and owns the same session-tree cursor bookkeeping
// (curLeaf / persisted) so each turn is persisted as a branch rather than a
// flattening rewrite.
type runSession struct {
	store     *sessionstore.Store
	header    session.SessionHeader
	agentCtx  *agentcore.AgentContext
	build     *run.ContextBuild
	live      *cli.LiveConfig
	reg       *agenttool.ToolRegistry
	reminders *runtime.ReminderRegistry
	creds     *provider.CredentialStore

	// cwd is the directory pigo was launched in, captured once at session
	// assembly. It is the trust key and the /status environment display.
	cwd string
	// trust persists project-trust decisions (US-018, #134). It is nil when
	// trust is disabled (store could not be loaded / no cwd); when nil /status
	// reports "disabled" and the trust-gated hook layer is skipped.
	trust *trust.Manager
	// slash is the shared slash-command registry the TUI consults exactly as the
	// REPL does. It is assembled per-session against the live config (withSession
	// rebinds the model's registry to this one) so /model switches reach it.
	slash *runtime.SlashRegistry
	// telemetry holds the retained per-run telemetry events (US-001, #291) and
	// the cumulative accumulator that sums metrics across all runs in the
	// session. The run loop folds each run's TelemetryEvent into it; /status
	// reads it back through the Host contract.
	telemetry *cli.TelemetryHolder

	// memoryRoot is the persistent-memory Store root (empty when memory is
	// disabled). It routes auto-compaction checkpoints and /rebuild recovery to
	// <memoryRoot>/sessions/<id>/, the canonical checkpoint location.
	memoryRoot string
	// memstore is the live persistent-memory Store (nil when memory is disabled).
	// It lets /memory inspect entry counts without re-opening the database.
	memstore *memory.Store

	// dispatcher is the session's hook dispatcher, nil when no hooks are
	// configured (FR-18). hookDeps carries the session id / project dir stamped
	// onto every HookInput and hook process environment.
	dispatcher *hooks.Dispatcher
	hookDeps   run.HookDeps
	// onEvent is the observer chain delivered to every run: the plugin notifier
	// (US-017) with the SessionEnd/PreCompact hook notifier chained after it.
	onEvent func(agentcore.AgentEvent)

	// curLeaf is the id of the on-disk entry the next turn descends from; persisted
	// is the number of agentCtx.Messages already written. persist() appends only
	// Messages[persisted:] as a branch from curLeaf (see cli.PersistTurn).
	curLeaf   string
	persisted int

	// compacted is set when the run loop compacted the context (CompactionEvent):
	// compaction rewrites Messages into a summary + recent tail, which both shrinks
	// the slice below persisted (so an incremental Messages[persisted:] would panic)
	// and invalidates the branch prefix. persist() honors this by re-saving the
	// flattened context linearly and resetting the branch cursor, then clears it.
	compacted bool

	// cancelRun cancels the in-flight run's context; startRun sets it and the
	// two-stage interrupt (Model.interruptFn → interrupt) calls it. It is nil
	// before the first run and after a run is cancelled.
	cancelRun context.CancelFunc

	// lastBtw is the /btw side thread's context from this process and
	// lastBtwBase the background-message index it diverged from. Both are
	// carried on the Host contract for parity with the REPL; the TUI does not
	// run /btw today, so they stay nil/0.
	lastBtw     *agentcore.AgentContext
	lastBtwBase int

	// remote owns the running remote-control server+bridge (remotecontrol.go),
	// nil when /remote-control is off. buildConfig reads it to install the remote
	// confirm seam so risky tool calls route to the paired browser while connected.
	remote *remoteSession

	// httpClient, when non-nil, routes agent runs through the serve HTTP API
	// instead of the direct runtime bridge. httpDir is the workspace directory
	// passed with every request, httpCursor is the next SSE event cursor, and
	// httpCancel cancels the in-flight event stream.
	httpClient *httpclient.ClientWithResponses
	httpDir    string
	httpCursor int64
	httpCancel context.CancelFunc
}

// newRunSession assembles the run session from the resolved Options, opening the
// shared $PIGO_HOME/sessions.db store. When Options carries a ResumeID it loads that
// session's entries and rebuilds the context (the returned history seeds the
// replayed transcript); otherwise it starts a fresh session with a new header.
// It is the production entry; newRunSessionWithStore holds the store-agnostic
// core so tests can drive it against a temp-dir store.
func newRunSession(opts Options) (*runSession, []agentcore.Message, error) {
	store, err := headless.ProjectStore()
	if err != nil {
		return nil, nil, err
	}
	return newRunSessionWithStore(store, opts)
}

// newRunSessionWithStore is the store-agnostic core of newRunSession: given an
// already-opened store it resolves resume-vs-fresh, builds the live config and
// collaborators, and returns the session plus the resumed history (nil for a
// fresh session).
func newRunSessionWithStore(store *sessionstore.Store, opts Options) (*runSession, []agentcore.Message, error) {
	creds := provider.NewCredentialStore(nil)
	creds.SetOverride(opts.ProviderName, opts.APIKey)

	// cwd is the launch directory, captured once: it stamps fresh sessions, is
	// the trust key, and feeds /status's environment section and the hook layer.
	cwd, _ := os.Getwd()

	now := time.Now().UTC()
	var (
		agentCtx *agentcore.AgentContext
		header   session.SessionHeader
		history  []agentcore.Message
		curLeaf  string
		proj     *session.ProjectLeaf
	)
	if opts.ResumeID != "" {
		_, h, msgs, err := store.Load(opts.ResumeID)
		if err != nil {
			return nil, nil, err
		}
		if p, projErr := store.Projection(opts.ResumeID, ""); projErr == nil {
			proj = p
			curLeaf = proj.LeafID
		}
		header = h
		agentCtx = &agentcore.AgentContext{Messages: msgs, Tools: opts.Tools}
		history = msgs
	} else {
		agentCtx = &agentcore.AgentContext{Tools: opts.Tools}
		// Stamp the launch directory onto a fresh session (#526/#524) so the
		// session is attributed to a project and a later /dream pass can distill it
		// under the right scope, mirroring headless/REPL. An unresolvable cwd
		// yields "" (session stays unattributed) rather than aborting.
		laneCfg := &session.LaneConfig{
			Model:         opts.Model,
			Provider:      opts.ProviderName,
			ThinkingLevel: string(opts.ThinkingLevel),
		}
		header = session.SessionHeader{
			ID:           session.NewID(now),
			CreatedAt:    now,
			UpdatedAt:    now,
			Model:        opts.Model,
			Provider:     opts.ProviderName,
			SystemPrompt: opts.SysPrompt,
			Cwd:          cwd,
		}
		proj = &session.ProjectLeaf{
			Model:         opts.Model,
			Provider:      opts.ProviderName,
			ThinkingLevel: string(opts.ThinkingLevel),
			Config:        laneCfg,
		}
		if err := store.CreateWithLaneConfig(
			sessionstore.NewMetadata(header.ID, "Session", "pigo", opts.Model, cwd),
			header,
			nil,
			laneCfg,
		); err != nil {
			return nil, nil, err
		}
	}
	build, buildErr := run.NewContextBuild(run.ContextBuildInput{
		Project:            proj,
		BaseInstruction:    opts.BaseInstruction,
		Cwd:                cwd,
		ContextFiles:       opts.ContextFiles,
		AppendInstructions: opts.AppendInstructions,
		Skills:             opts.Skills,
		AllTools:           opts.Tools,
		Plugins:            opts.Plugins,
		Reminders:          run.TodoReminders(opts.Tools),
		Warn:               os.Stderr,
	})
	if buildErr != nil {
		return nil, nil, buildErr
	}

	live := &cli.LiveConfig{
		Model:         opts.Model,
		ProviderName:  opts.ProviderName,
		Provider:      opts.Provider,
		BaseURL:       opts.BaseURL,
		Protocol:      opts.Protocol,
		Creds:         creds,
		ThinkingLevel: opts.ThinkingLevel,
		ContextWindow: cli.DefaultContextWindow,
	}
	live.PersistConfig = func() {
		if err := cli.PersistLaneConfig(store, header.ID, live); err != nil {
			fmt.Fprintf(os.Stderr, "pigo: persist lane config: %v\n", err)
		}
	}
	if opts.ResumeID != "" {
		if err := cli.ApplyProjectionToLive(live, proj, opts.BaseURL, opts.Protocol, opts.APIKey); err != nil {
			return nil, nil, err
		}
	}

	// Project trust (US-018, #134): load the persisted trust store for the
	// launch directory, mirroring the REPL. A load failure (or an unresolvable
	// cwd) is non-fatal: trust is disabled (mgr stays nil) and the TUI still
	// runs — the store is surfaced rather than silently overwritten.
	mgr, mgrErr := trust.NewManager(trust.DefaultPath())
	if mgrErr != nil {
		fmt.Fprintf(os.Stderr, "pigo: trust store unavailable, trust disabled: %v\n", mgrErr)
		mgr = nil
	}
	if cwd == "" && mgr != nil {
		fmt.Fprintf(os.Stderr, "pigo: cannot resolve working directory, trust disabled\n")
		mgr = nil
	}

	s := &runSession{
		store:      store,
		header:     header,
		agentCtx:   agentCtx,
		build:      build,
		live:       live,
		reg:        run.ToolRegistry(opts.Tools),
		reminders:  run.TodoReminders(opts.Tools),
		creds:      creds,
		cwd:        cwd,
		trust:      mgr,
		slash:      newSlashRegistry(opts, live),
		telemetry:  cli.NewTelemetryHolder(),
		curLeaf:    curLeaf,
		persisted:  len(history),
		memoryRoot: run.MemoryRootFromTools(opts.Tools),
		memstore:   run.MemoryStoreFromTools(opts.Tools),
	}
	// /trust is a per-session command (its closure captures mgr + cwd), so it is
	// registered here rather than in newSlashRegistry. A nil mgr is a no-op.
	trust.RegisterCommand(s.slash, mgr, cwd)

	// Wire hooks uniformly with every other driver (#425): resolve the trust-gated
	// hook set, build the dispatcher, dispatch SessionStart once, and compose the
	// SessionEnd/PreCompact observer with the plugin notifier. Trust is granted by
	// --approve (Options.Approve) or the shared trust store; project-layer hooks
	// only apply when trusted (FR-14). A malformed hook layer disables hooks with a
	// warning rather than failing the TUI launch.
	s.hookDeps = run.HookDeps{SessionID: header.ID, ProjectDir: cwd, WarnLog: os.Stderr}
	trusted := opts.Approve || (mgr != nil && mgr.IsTrusted(cwd))
	var baseOnEvent func(agentcore.AgentEvent)
	if n := plugin.NewEventNotifier(opts.Plugins, os.Stderr); n != nil {
		baseOnEvent = n.Handle
	}
	if set, err := run.ResolveHookSet(cwd, trusted); err != nil {
		fmt.Fprintf(os.Stderr, "pigo: hooks disabled: %v\n", err)
		s.onEvent = baseOnEvent
	} else if d := run.BuildDispatcher(set, s.hookDeps); d != nil {
		s.dispatcher = d
		if s.reminders == nil {
			s.reminders = runtime.NewReminderRegistry()
		}
		ssCfg := runtime.RunConfig{Reminders: s.reminders}
		run.DispatchSessionStart(context.Background(), d, &ssCfg, s.hookDeps, sessionStartSource(opts))
		s.reminders = ssCfg.Reminders
		n := hooks.NewHookNotifier(d, s.hookDeps.SessionID, s.hookDeps.ProjectDir)
		s.onEvent = chainTUIEvent(baseOnEvent, n.Handle)
	} else {
		s.onEvent = baseOnEvent
	}
	return s, history, nil
}

// sessionStartSource maps the resolved run options to the SessionStart source
// tag: "resume" when continuing an existing session, "startup" otherwise.
func sessionStartSource(opts Options) string {
	if opts.ResumeID != "" {
		return "resume"
	}
	return "startup"
}

// chainTUIEvent composes the plugin notifier with the hook notifier into one
// observer; a nil operand is identity.
func chainTUIEvent(prev, next func(agentcore.AgentEvent)) func(agentcore.AgentEvent) {
	if prev == nil {
		return next
	}
	if next == nil {
		return prev
	}
	return func(ev agentcore.AgentEvent) {
		prev(ev)
		next(ev)
	}
}

// buildConfig assembles the RunConfig for one turn from the live config and
// collaborators. It replicates repl.streamRun's assembly (same LoopConfig fields,
// tool registry and reminders) minus the interactive trust confirmation hook: the
// TUI has no stdin prompt to confirm side-effect tool calls on, so tools run
// under the trust granted up front by --approve (Options.Approve) rather than a
// per-call BeforeToolCall prompt. The stream fn is derived from the live provider
// and the API key resolved through the credential store, exactly as the REPL does.
func (s *runSession) buildConfig() runtime.RunConfig {
	cfg := runtime.RunConfig{
		LoopConfig: runtime.LoopConfig{
			Model:         run.WireModel(s.live.Model),
			Provider:      s.live.ProviderName,
			ThinkingLevel: s.live.ThinkingLevel,
			Stream:        provider.StreamFnFromProvider(s.live.Provider),
			GetAPIKey:     s.creds.GetAPIKey,
			ContextWindow: s.live.ContextWindow,
			Compaction:    compaction.DefaultCompactionSettings,
		},
		Batch: agenttool.BatchConfig{
			ToolExecutorConfig: agenttool.ToolExecutorConfig{
				Registry: s.reg,
			},
		},
		Reminders: s.reminders,
		SessionID: s.header.ID,
		OnCompaction: func(ctx context.Context, res *compaction.CompactionResult) error {
			if res == nil || s.store == nil {
				return nil
			}
			s.header.UpdatedAt = time.Now().UTC()
			s.header.Model = s.live.Model
			s.header.Provider = s.live.ProviderName
			_, err := s.store.AppendCompaction(s.header.ID, s.header, res)
			if err == nil {
				s.compacted = true
			}
			return err
		},
	}
	// contextbuild owns per-request assembly; sync live lane state and reminders
	// before installing the request seam.
	if s.build != nil {
		s.build.Ctx.Model = s.live.Model
		s.build.Ctx.Provider = s.live.ProviderName
		s.build.Ctx.ThinkingLevel = s.live.ThinkingLevel
		s.build.Deps.Reminders = s.reminders
		rb := contextbuild.RequestBuilder(s.build.Ctx, s.build.Deps, s.build.Req)
		cfg.LoopConfig.RequestBuilder = func(ctx context.Context, msgs agentcore.MessageList) (provider.LlmContext, error) {
			llm, err := rb(ctx, msgs)
			if err == nil && llm.SystemPrompt != "" {
				s.header.SystemPrompt = llm.SystemPrompt
			}
			return llm, err
		}
	}
	// Per-turn wiring of the tool-execution + Stop seams; nil dispatcher is a
	// no-op so the hot path pays nothing when no hooks are configured (FR-18).
	if s.dispatcher != nil {
		run.InstallSeams(&cfg, s.dispatcher, s.hookDeps)
	}
	// When remote control is active, route side-effect tool-call confirmations to
	// the paired browser (no-op when no client is connected or the cwd is trusted,
	// so the non-remote path is unchanged). The trust manager is read from the
	// shared store; a nil manager disables the seam.
	if s.remote != nil {
		if mgr, err := trust.NewManager(trust.DefaultPath()); err == nil {
			cfg.Batch.ToolExecutorConfig.BeforeToolCall = remoteConfirmSeam(s.remote, mgr, s.hookDeps.ProjectDir)
		}
	}
	return cfg
}

// rebuildDoneMsg reports the outcome of a manual /rebuild to the model: summary
// is the status line to show in the transcript, err is set when the rebuild
// failed (the context is then left unchanged).
type rebuildDoneMsg struct {
	summary string
	err     error
}

// rebuildCmd runs a context rebuild off the tea loop (the no-checkpoint fallback
// makes a summarization LLM call, so it must not block the UI goroutine) and
// yields a rebuildDoneMsg the model folds into the transcript. It mirrors the
// REPL's runManualRebuild.
func (s *runSession) rebuildCmd() tea.Cmd {
	return func() tea.Msg {
		summary, err := s.rebuild()
		return rebuildDoneMsg{summary: summary, err: err}
	}
}

// rebuild reconstructs the shared context from the session's persisted checkpoint
// (collapsing the pre-watermark prefix to the checkpoint summary and preserving
// the recent tail verbatim), falling back to lossy compaction when no checkpoint
// exists. It replaces agentCtx.Messages in place on success and flags compacted
// so persist() re-saves the flattened context linearly (as after a /compact).
func (s *runSession) rebuild() (string, error) {
	if s.httpClient != nil {
		args := ""
		resp, err := s.httpClient.ExecuteCommandWithResponse(context.Background(), s.header.ID, httpclient.ExecuteCommandJSONRequestBody{
			Directory: s.httpDir,
			Command:   "compact",
			Arguments: &args,
		})
		if err != nil {
			return "", err
		}
		if resp.JSON200 == nil {
			return "", fmt.Errorf("compact command failed")
		}
		text := ""
		if resp.JSON200.Text != nil {
			text = *resp.JSON200.Text
		}
		if text == "" {
			text = resp.JSON200.StopReason
		}
		return text, nil
	}
	before := compaction.EstimateContextTokens(s.agentCtx.Messages).Tokens
	proj, err := s.store.Projection(s.header.ID, "")
	if err != nil {
		return "", err
	}
	if len(proj.Messages) == 0 {
		return fmt.Sprintf("nothing to rebuild (%d tokens, %d messages)", before, len(s.agentCtx.Messages)), nil
	}
	s.agentCtx.Messages = proj.Messages
	s.curLeaf = proj.LeafID
	s.persisted = len(proj.Messages)
	after := compaction.EstimateContextTokens(proj.Messages).Tokens
	return fmt.Sprintf("context rebuilt from sqlite projection: %d -> %d tokens, %d messages", before, after, len(proj.Messages)), nil
}

// prompt to the growing context as a user message, then hands the context and a
// freshly-built config to the event bridge (bridge.startRun → runtime.StartRun +
// DrainStream on a goroutine), returning the bridge channel and the first
// waitForEvent Cmd so Update can pump the run's events. The context grows in
// place (agentCtx is a pointer), so the next turn continues the conversation.
func (s *runSession) startRun(prompt string) (chan tea.Msg, tea.Cmd) {
	if s.httpClient != nil {
		return s.httpStartRun(prompt)
	}
	content, err := ui.BuildUserContent(prompt)
	if err != nil {
		// A malformed image reference must not swallow the turn: fall back to the
		// raw prompt as plain text so the run still starts.
		content = agentcore.ContentList{agentcore.NewTextContent(prompt)}
	}
	// UserPromptSubmit runs before the prompt is committed to the context: a block
	// aborts the turn (emitting a runEndMsg carrying the reason) without leaving a
	// dangling user message; additionalContext is injected into this turn only.
	if s.dispatcher != nil {
		pc := runtime.RunConfig{Reminders: s.reminders}
		if block, reason := run.DispatchUserPromptSubmit(context.Background(), s.dispatcher, &pc, s.hookDeps, prompt); block {
			ch := newEventChan()
			go func() { ch <- runEndMsg{err: fmt.Errorf("prompt blocked by hook: %s", reason)} }()
			return ch, waitForEvent(ch)
		}
		s.reminders = pc.Reminders
	}
	s.agentCtx.Messages = append(s.agentCtx.Messages, agentcore.UserMessage{
		RoleField: agentcore.RoleUser,
		Content:   content,
	})
	// Use a cancellable context so the two-stage interrupt (FR-14) can stop this
	// run: cancelling propagates through StartRun/DrainStream, which then emits a
	// runEndMsg and the model returns to idle.
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelRun = cancel
	return startRun(ctx, s.agentCtx, s.buildConfig(), s.onEvent)
}

// interrupt cancels the in-flight run, if any. It is bound to Model.interruptFn
// by withSession so pressing Esc / Ctrl+C while running stops the current run
// instead of quitting the program (FR-14). Safe to call when no run is active.
func (s *runSession) interrupt() {
	if s.httpClient != nil {
		if s.httpCancel != nil {
			s.httpCancel()
		}
		_, _ = s.httpClient.CancelSessionPromptWithResponse(context.Background(), s.header.ID)
		return
	}
	if s.cancelRun != nil {
		s.cancelRun()
	}
}

// persist writes the messages produced since the last persist as a new branch
// descending from the active leaf, advancing the leaf and the persisted cursor.
// It mirrors cli.PersistTurn: growing the on-disk tree with AppendBranch (rather
// than a linear rewrite) keeps history intact. A no-op when nothing new was
// produced, so an idle turn-end never regenerates entry ids.
func (s *runSession) persist() error {
	if s.httpClient != nil {
		return nil
	}
	// Auto-compaction was already persisted as a typed entry by OnCompaction;
	// the leaf advanced and the retained tail is authoritative, so the flat
	// Save path must not run and clobber the tree.
	if s.compacted {
		s.persisted = len(s.agentCtx.Messages)
		if leaf, err := s.store.MainLeaf(s.header.ID); err == nil {
			s.curLeaf = leaf
		}
		s.compacted = false
		return nil
	}
	// A compaction during the run rewrote Messages into a summary + recent tail,
	// so the append-a-tail branch model no longer holds: the prefix changed and
	// the slice may be shorter than persisted. Re-save the flattened context
	// linearly and reset the branch cursor to the new leaf, mirroring the REPL's
	// /compact handling.
	if s.persisted > len(s.agentCtx.Messages) {
		s.header.UpdatedAt = time.Now().UTC()
		s.header.Model = s.live.Model
		s.header.Provider = s.live.ProviderName
		if err := s.store.Save(s.header, s.agentCtx.Messages); err != nil {
			return err
		}
		s.persisted = len(s.agentCtx.Messages)
		s.curLeaf = ""
		if proj, err := s.store.Projection(s.header.ID, ""); err == nil {
			s.curLeaf = proj.LeafID
		}
		return nil
	}
	tail := s.agentCtx.Messages[s.persisted:]
	if len(tail) == 0 {
		return nil
	}
	firstPersist := s.persisted == 0
	s.header.UpdatedAt = time.Now().UTC()
	s.header.Model = s.live.Model
	s.header.Provider = s.live.ProviderName
	leaf, err := s.store.AppendBranch(s.header.ID, s.header, s.curLeaf, tail)
	if err != nil {
		return err
	}
	s.curLeaf = leaf
	s.persisted = len(s.agentCtx.Messages)
	if firstPersist {
		for _, m := range tail {
			if u, ok := m.(agentcore.UserMessage); ok {
				if text := strings.TrimSpace(agentcore.ContentToText(u.Content)); text != "" {
					s.maybeAutoTitle(text)
				}
				break
			}
		}
	}
	return nil
}

func (s *runSession) maybeAutoTitle(firstUserText string) {
	if s.live == nil || s.live.Provider == nil || s.store == nil {
		return
	}
	apiKey := ""
	if s.creds != nil {
		apiKey = s.creds.GetAPIKey(context.Background(), s.live.ProviderName)
	}
	_ = sessiontitle.AutoTitle(context.Background(), s.store, s.header.ID, firstUserText,
		provider.StreamFnFromProvider(s.live.Provider),
		provider.Model{Provider: s.live.ProviderName, ID: run.WireModel(s.live.Model), ContextWindow: s.live.ContextWindow},
		provider.StreamConfig{APIKey: apiKey, ThinkingLevel: s.live.ThinkingLevel},
		nil)
}

// seedTranscript replays a resumed session's prior messages into the transcript
// so the user sees the conversation so far before re-prompting (the TUI analogue
// of repl.replayTranscript). User and assistant text become their respective
// blocks; assistant tool calls render as system lines (tool cards land in #389).
// Tool-result messages are omitted here — their content is echoed live during a
// run, and replaying raw results would clutter the resumed view.
func seedTranscript(t *transcript, history []agentcore.Message) {
	for _, m := range history {
		switch msg := m.(type) {
		case agentcore.UserMessage:
			if text := agentcore.ContentToText(msg.Content); text != "" {
				t.addUser(text)
			}
		case agentcore.AssistantMessage:
			if text := agentcore.ContentToText(msg.Content); text != "" {
				t.finalizeTurn(msg)
			}
			for _, c := range msg.ToolCalls() {
				t.addSystem("· " + c.Name)
			}
		}
	}
}
