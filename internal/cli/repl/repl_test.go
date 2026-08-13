package repl

// Tests for the line-based REPL (#106): slash-command dispatch (action prints
// message and does NOT run; prompt runs; unknown command errors and does NOT
// run; /exit and EOF exit cleanly), multi-turn history accumulation, and
// streaming assistant text. The REPL is driven with a fake provider so no
// network is involved — the whole read → run → stream-print loop runs over an
// in-memory input reader and output buffer.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/cli/prompts"
	"github.com/smallnest/pigo/internal/cli/ui"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

// replProvider is a minimal Provider that streams one scripted text turn per
// StreamCompletion call and records how many times it was called, so a test can
// assert whether a run was launched.
type replProvider struct {
	reply      string
	calls      int
	titleCalls int
}

func (p *replProvider) Name() string { return "faux" }
func (p *replProvider) Models() []provider.Model {
	return []provider.Model{{Provider: "faux", ID: "faux"}}
}

func (p *replProvider) StreamCompletion(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	if titleStream, ok := titleReplyStream(ctx, req); ok {
		p.titleCalls++
		return titleStream, nil
	}
	p.calls++
	partial := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}
	withText := partial
	withText.Content = agentcore.ContentList{agentcore.NewTextContent(p.reply)}
	final := withText
	final.StopReason = agentcore.StopReasonEndTurn
	s := provider.NewAssistantMessageEventStream(0)
	go func() {
		_ = s.Emit(ctx, provider.StreamStartEvent{Partial: partial})
		_ = s.Emit(ctx, provider.StreamTextEvent{Partial: withText})
		_ = s.Emit(ctx, provider.StreamDoneEvent{Message: final})
		s.Close()
	}()
	return s, nil
}

// newTestDeps builds replDeps wired to the fake provider and a temp session
// store, with a registry carrying one action command and one prompt command so
// slash dispatch can be exercised. actionRuns/promptResolved report whether each
// command fired.
func newTestDeps(t *testing.T, p provider.Provider) (replDeps, *sessionstore.Store) {
	t.Helper()
	store, err := sessionstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	live := &cli.LiveConfig{Model: "faux", ProviderName: "faux", Provider: p}
	reg := runtime.NewSlashRegistry()
	reg.AddBuiltin(runtime.SlashCommand{
		Name:   "ping",
		Action: func(string) string { return "pong" },
	})
	reg.AddUser(runtime.SlashCommand{
		Name:   "echo",
		Expand: func(args string) string { return "expanded: " + args },
	})
	deps := replDeps{
		store:    store,
		header:   session.SessionHeader{ID: session.NewID(time.Now().UTC()), Model: "faux", Provider: "faux"},
		agentCtx: &agentcore.AgentContext{},
		live:     live,
		reg:      agenttool.NewToolRegistry(),
		slash:    reg,
		creds:    provider.NewCredentialStore(nil),
	}
	return deps, store
}

// TestREPLExitCommand verifies /exit ends the loop cleanly with no error and no
// agent run.
func TestREPLExitCommand(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL returned error: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("/exit must not launch a run, got %d calls", p.calls)
	}
}

// TestREPLQuitCommand verifies /quit is an alias for /exit.
func TestREPLQuitCommand(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("/quit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL returned error: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("/quit must not launch a run, got %d calls", p.calls)
	}
}

// TestRunResumeListsSessionID verifies the /resume list shows both the session
// title and the stable session id, not just an opaque timestamp line.
func TestRunResumeListsSessionID(t *testing.T) {
	cleanupStores(t)
	home := t.TempDir()
	t.Setenv("PIGO_HOME", home)
	ws := filepath.Join(home, "ws")
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	header := session.SessionHeader{ID: session.NewID(now), CreatedAt: now, UpdatedAt: now, Model: "faux", Provider: "faux", Cwd: ws}
	meta := sessionstore.NewMetadata(header.ID, "My Task", "pigo", "faux", ws)
	if err := store.Create(meta, header, agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hi")}},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	deps := replDeps{cwd: ws}
	var out bytes.Buffer
	runResume(&out, &deps, "/resume")
	got := out.String()
	if !strings.Contains(got, "My Task") || !strings.Contains(got, "["+header.ID+"]") {
		t.Fatalf("resume list = %q, want title and session id", got)
	}
}

// TestRunResumeDerivesDefaultTitle verifies a session created with the default
// "Session" name is listed using its first user message as the display title.
func TestRunResumeDerivesDefaultTitle(t *testing.T) {
	cleanupStores(t)
	home := t.TempDir()
	t.Setenv("PIGO_HOME", home)
	ws := filepath.Join(home, "ws")
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	header := session.SessionHeader{ID: session.NewID(now), CreatedAt: now, UpdatedAt: now, Model: "faux", Provider: "faux", Cwd: ws}
	meta := sessionstore.NewMetadata(header.ID, "Session", "pigo", "faux", ws)
	if err := store.Create(meta, header, agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("Inefficient string concatenation in call to WriteString" +
			"\nsecond line")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("done")}},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	deps := replDeps{cwd: ws}
	var out bytes.Buffer
	runResume(&out, &deps, "/resume")
	got := out.String()
	if !strings.Contains(got, "Inefficient string concatenation") || !strings.Contains(got, "["+header.ID+"]") {
		t.Fatalf("resume list = %q, want derived first-user title and session id", got)
	}
}

// TestREPLEOFExits verifies EOF (no /exit) ends the loop cleanly.
func TestREPLEOFExits(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	// Empty input → immediate EOF.
	if err := runREPL(strings.NewReader(""), &out, deps); err != nil {
		t.Fatalf("EOF should exit cleanly, got: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("EOF with no input must not run, got %d calls", p.calls)
	}
}

// TestREPLFinalLineNoNewline verifies the ReadString-based loop handles a final
// input line without a trailing newline: the line is still run as a prompt and
// the loop then exits cleanly on EOF. (The previous bufio.Scanner had the same
// behavior; this pins it for the reader-based loop.)
func TestREPLFinalLineNoNewline(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("hello"), &out, deps); err != nil {
		t.Fatalf("runREPL on no-trailing-newline input: %v", err)
	}
	if p.calls != 1 {
		t.Errorf("runs fired = %d, want 1 (the final line should run once)", p.calls)
	}
}

// TestREPLEmptyLineIgnored verifies blank lines are skipped without running.
func TestREPLEmptyLineIgnored(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("\n   \n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("blank lines must not run, got %d calls", p.calls)
	}
}

// TestREPLActionCommandNoRun verifies an action slash command prints its message
// and does NOT launch an agent run.
func TestREPLActionCommandNoRun(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("/ping\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("action command must not run, got %d calls", p.calls)
	}
	if !strings.Contains(out.String(), "pong") {
		t.Errorf("action command message not printed, out=%q", out.String())
	}
}

// TestREPLPromptCommandRuns verifies a prompt slash command expands and launches
// a run.
func TestREPLPromptCommandRuns(t *testing.T) {
	p := &replProvider{reply: "ack"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("/echo hello\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("prompt command should launch exactly 1 run, got %d", p.calls)
	}
	// The expanded prompt must have been appended as the user message.
	if len(deps.agentCtx.Messages) == 0 {
		t.Fatal("expected messages in context after run")
	}
	first, ok := deps.agentCtx.Messages[0].(agentcore.UserMessage)
	if !ok || agentcore.ContentToText(first.Content) != "expanded: hello" {
		t.Errorf("first message = %T %q, want expanded prompt", deps.agentCtx.Messages[0], agentcore.ContentToText(first.Content))
	}
}

// TestREPLUnknownCommandNoRun verifies an unknown slash command prints an error
// and does NOT run or crash.
func TestREPLUnknownCommandNoRun(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("/nope\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("unknown command must not run, got %d calls", p.calls)
	}
	if !strings.Contains(strings.ToLower(out.String()), "unknown") {
		t.Errorf("expected an unknown-command error line, out=%q", out.String())
	}
}

// TestREPLModelSwitchTakesEffect verifies the /model action command switches the
// live model mid-session (via registerLiveCommands + resolveProvider) without
// launching a run, and that the switch is reflected in live for the next turn.
func TestREPLModelSwitchTakesEffect(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	cfgPath := filepath.Join(root, "pigo", "config.toml")
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{
		Model: "openai/agnes-2.5-flash",
		Models: []config.ModelConfig{{
			Provider:       "openai",
			ModelID:        "agnes-2.5-flash",
			Name:           "Agnes 2.5 Flash",
			BaseURL:        "https://api.example.com/v1",
			APIKey:         "sk-config",
			Protocol:       "openai",
			ThinkingLevels: []string{"low", "high"},
		}, {
			Provider:       "openai",
			ModelID:        "no-key",
			Name:           "No Key",
			BaseURL:        "https://api.example.com/v1",
			Protocol:       "openai",
			ThinkingLevels: []string{"off"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "")
	deps.live.Creds = deps.creds
	// Register the real live action commands (/model, /models, /help) against the
	// same live config the REPL runs on, so /model mutates it.
	prompts.RegisterLiveCommands(deps.slash, deps.live)

	var out bytes.Buffer
	// /model with no arg reports the current model; /model <id> switches to a
	// configured model, resets thinking, and clears a stale credential when the
	// next entry has no key; /exit ends the loop.
	in := strings.NewReader("/model\n/model openai/agnes-2.5-flash\n/model openai/no-key\n/exit\n")
	if err := runREPL(in, &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("/model actions must not launch a run, got %d calls", p.calls)
	}
	if deps.live.Model != "openai/no-key" || deps.live.ProviderName != "openai" {
		t.Errorf("live not switched: model=%q provider=%q", deps.live.Model, deps.live.ProviderName)
	}
	if deps.live.ThinkingLevel != "off" {
		t.Errorf("thinking not reset after second switch: %q", deps.live.ThinkingLevel)
	}
	if got := deps.creds.GetAPIKey(context.Background(), "openai"); got != "" {
		t.Errorf("stale credential override = %q, want empty", got)
	}
	s := out.String()
	if !strings.Contains(s, "faux") {
		t.Errorf("/model (no arg) should report the current model, out=%q", s)
	}
	if !strings.Contains(s, "openai/no-key") {
		t.Errorf("/model switch should confirm the new model, out=%q", s)
	}
}

// TestREPLPersistsModelIntoHeader verifies that after a run the session header
// records the live model/provider (US-006: a /model switch is persisted with the
// session), by reloading the saved session and inspecting its header.
func TestREPLPersistsModelIntoHeader(t *testing.T) {
	p := &replProvider{reply: "ok"}
	deps, store := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("hello\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	headers, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(headers) != 1 {
		t.Fatalf("expected 1 saved session, got %d", len(headers))
	}
	if headers[0].Header == nil || headers[0].Header.Model != "faux" || headers[0].Header.Provider != "faux" {
		t.Errorf("header model/provider = %v, want live faux/faux", headers[0].Header)
	}
}

// TestREPLModelSwitchRejectsUnconfigured verifies /model refuses ids that are
// not in [[models]] and leaves the live config untouched.
func TestREPLModelSwitchRejectsUnconfigured(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prompts.RegisterLiveCommands(deps.slash, deps.live)

	var out bytes.Buffer
	if err := runREPL(strings.NewReader("/model nope/nope\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("/model actions must not launch a run, got %d calls", p.calls)
	}
	if deps.live.Model != "faux" || deps.live.ProviderName != "faux" {
		t.Errorf("live changed after rejected switch: model=%q provider=%q", deps.live.Model, deps.live.ProviderName)
	}
	if !strings.Contains(out.String(), "not configured") {
		t.Errorf("expected not-configured error, out=%q", out.String())
	}
}

// TestRenderToolResultTodoFull verifies the todo tool's multi-line progress
// block is rendered in full under a "← todo:" header (US-011: REPL shows
// progress on update), while a non-todo result stays a one-line summary.
func TestRenderToolResultTodoFull(t *testing.T) {
	var out bytes.Buffer
	ui.RenderToolResult(&out, agentcore.ToolResultMessage{
		RoleField: agentcore.RoleToolResult, ToolName: "todo",
		Content: agentcore.ContentList{agentcore.NewTextContent("Todos:\n  [x] a\n  [ ] b\n(1/2 completed)")},
	})
	s := out.String()
	for _, want := range []string{"← todo:", "[x] a", "[ ] b", "(1/2 completed)"} {
		if !strings.Contains(s, want) {
			t.Errorf("todo render missing %q, out=%q", want, s)
		}
	}

	out.Reset()
	ui.RenderToolResult(&out, agentcore.ToolResultMessage{
		RoleField: agentcore.RoleToolResult, ToolName: "bash",
		Content: agentcore.ContentList{agentcore.NewTextContent("line1\nline2")},
	})
	if got := out.String(); !strings.Contains(got, "← result: line1") {
		t.Errorf("non-todo render = %q, want a one-line summary", got)
	}
}

// TestReplayTranscriptRendersRoles verifies a resumed session's prior messages
// are echoed by role (user / assistant / tool result) before the first new
// prompt (US-006 acceptance: resumed conversation is replayed).
func TestReplayTranscriptRendersRoles(t *testing.T) {
	msgs := []agentcore.AgentMessage{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("what is 2+2")}},
		agentcore.AssistantMessage{
			RoleField: agentcore.RoleAssistant,
			Content:   agentcore.ContentList{agentcore.NewTextContent("Let me compute."), agentcore.NewToolCallContent("c1", "calc", []byte(`{"expr":"2+2"}`))},
		},
		agentcore.ToolResultMessage{RoleField: agentcore.RoleToolResult, ToolCallID: "c1", ToolName: "calc", Content: agentcore.ContentList{agentcore.NewTextContent("4")}},
	}
	var out bytes.Buffer
	replayTranscript(&out, msgs)
	s := out.String()
	for _, want := range []string{"> what is 2+2", "Let me compute.", `→ tool: calc {"expr":"2+2"}`, "← result: 4"} {
		if !strings.Contains(s, want) {
			t.Errorf("replay missing %q, out=%q", want, s)
		}
	}
}

// TestREPLTreePrintsAndSwitchesBranch drives the /tree command end to end over
// the REPL: after two turns, "/tree" prints a numbered tree with the current-leaf
// marker; "/tree 1" switches the active leaf to the first node, so the next
// prompt branches from there — leaving the original branch intact on disk (US-007,
// #123).
func TestREPLTreePrintsAndSwitchesBranch(t *testing.T) {
	p := &replProvider{reply: "reply"}
	deps, store := newTestDeps(t, p)
	var out bytes.Buffer
	// Two turns build a linear history (4 messages), then /tree lists it, /tree 1
	// switches to the first node, a new prompt branches, then /exit.
	in := strings.NewReader("first\nsecond\n/tree\n/tree 1\nn\nbranched\n/exit\n")
	if err := runREPL(in, &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "-> current") {
		t.Errorf("/tree should mark the current leaf, out=%q", s)
	}
	if !strings.Contains(s, "1. user:") {
		t.Errorf("/tree should number entries starting at the root user message, out=%q", s)
	}
	if !strings.Contains(s, "switched to branch at node 1") {
		t.Errorf("/tree 1 should confirm the switch, out=%q", s)
	}
	// The on-disk tree must retain both branches: the original 4-message line plus
	// the new branch off node 1. Reload and confirm the root has 2 children.
	entries, err := store.Entries(deps.header.ID)
	if err != nil {
		t.Fatalf("LoadEntries: %v", err)
	}
	rootID := ""
	for _, e := range entries {
		if e.ParentID == "" {
			rootID = e.ID
			break
		}
	}
	if rootID == "" {
		t.Fatal("no root entry found")
	}
	kids := 0
	for _, e := range entries {
		if e.ParentID == rootID {
			kids++
		}
	}
	if kids != 2 {
		t.Errorf("root should have 2 children after branching, got %d (entries=%d)", kids, len(entries))
	}
}

// TestREPLTreeEmptyNoop verifies /tree on a fresh session (no messages) prints a
// friendly notice and does not crash or run.
func TestREPLTreeEmptyNoop(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("/tree\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("/tree must not launch a run, got %d calls", p.calls)
	}
	if !strings.Contains(out.String(), "empty") {
		t.Errorf("/tree on empty session should say so, out=%q", out.String())
	}
}

// is printed, and history accumulates across two turns in the shared context.
func TestREPLStreamsAndAccumulatesHistory(t *testing.T) {
	p := &replProvider{reply: "the answer"}
	deps, store := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("first question\nsecond question\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 2 {
		t.Fatalf("two prompts should launch 2 runs, got %d", p.calls)
	}
	// The streamed reply text must appear in the output.
	if !strings.Contains(out.String(), "the answer") {
		t.Errorf("assistant reply not streamed to output, out=%q", out.String())
	}
	// History: user(first) + assistant + user(second) + assistant = 4 messages.
	if len(deps.agentCtx.Messages) != 4 {
		t.Fatalf("expected 4 accumulated messages, got %d", len(deps.agentCtx.Messages))
	}
	u0, _ := deps.agentCtx.Messages[0].(agentcore.UserMessage)
	u2, _ := deps.agentCtx.Messages[2].(agentcore.UserMessage)
	if agentcore.ContentToText(u0.Content) != "first question" || agentcore.ContentToText(u2.Content) != "second question" {
		t.Errorf("history not accumulated in order: %q, %q", agentcore.ContentToText(u0.Content), agentcore.ContentToText(u2.Content))
	}
	// The session must have been persisted after the runs.
	headers, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(headers) != 1 {
		t.Errorf("expected 1 saved session, got %d", len(headers))
	}
}

// errProvider streams a single turn that ends with stopReason error carrying an
// ErrorMessage, mimicking how the loop surfaces a request failure (e.g. a 4xx
// from the endpoint) as a terminal assistant message rather than a Go error.
type errProvider struct {
	reason string
	calls  int
}

func (p *errProvider) Name() string { return "faux" }
func (p *errProvider) Models() []provider.Model {
	return []provider.Model{{Provider: "faux", ID: "faux"}}
}

func (p *errProvider) StreamCompletion(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	if titleStream, ok := titleReplyStream(ctx, req); ok {
		return titleStream, nil
	}
	p.calls++
	partial := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}
	final := partial
	final.StopReason = agentcore.StopReasonError
	final.ErrorMessage = p.reason
	s := provider.NewAssistantMessageEventStream(0)
	go func() {
		_ = s.Emit(ctx, provider.StreamStartEvent{Partial: partial})
		_ = s.Emit(ctx, provider.StreamErrorEvent{Message: final})
		s.Close()
	}()
	return s, nil
}

// TestREPLSurfacesTurnError verifies a turn that ends with stopReason error is
// printed to the user instead of returning silently to the prompt. Without this
// an API failure (delivered as a terminal error message, not a run error) would
// produce no output at all.
func TestREPLSurfacesTurnError(t *testing.T) {
	p := &errProvider{reason: "401 unauthorized: bad api key"}
	deps, _ := newTestDeps(t, p)
	deps.live.Provider = p
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("hello\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("expected 1 run, got %d", p.calls)
	}
	got := out.String()
	if !strings.Contains(got, "error:") || !strings.Contains(got, "401 unauthorized: bad api key") {
		t.Errorf("turn error not surfaced to output, out=%q", got)
	}
}

// emptyProvider streams a clean end_turn with no content, thinking, or tool
// calls — the shape produced when an endpoint accepts the request with a 200 but
// returns nothing this protocol can decode.
type emptyProvider struct{ calls int }

func (p *emptyProvider) Name() string { return "faux" }
func (p *emptyProvider) Models() []provider.Model {
	return []provider.Model{{Provider: "faux", ID: "faux"}}
}

func (p *emptyProvider) StreamCompletion(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	if titleStream, ok := titleReplyStream(ctx, req); ok {
		return titleStream, nil
	}
	p.calls++
	partial := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}
	final := partial
	final.StopReason = agentcore.StopReasonEndTurn
	s := provider.NewAssistantMessageEventStream(0)
	go func() {
		_ = s.Emit(ctx, provider.StreamStartEvent{Partial: partial})
		_ = s.Emit(ctx, provider.StreamDoneEvent{Message: final})
		s.Close()
	}()
	return s, nil
}

func titleReplyStream(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, bool) {
	if len(req.Context.Messages) == 0 {
		return nil, false
	}
	u, ok := req.Context.Messages[0].(agentcore.UserMessage)
	if !ok || !strings.Contains(agentcore.ContentToText(u.Content), "Summarize this task in one short title:") {
		return nil, false
	}
	msg := agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent("Generated Title")},
		StopReason: agentcore.StopReasonEndTurn,
	}
	s := provider.NewAssistantMessageEventStream(0)
	go func() {
		defer s.Close()
		_ = s.Emit(ctx, provider.StreamStartEvent{Partial: msg})
		_ = s.Emit(ctx, provider.StreamDoneEvent{Message: msg})
	}()
	return s, true
}

// TestREPLNotesEmptyResponse verifies a clean turn that produced no output at
// all is flagged with a note rather than returning silently to the prompt.
func TestREPLNotesEmptyResponse(t *testing.T) {
	p := &emptyProvider{}
	deps, _ := newTestDeps(t, p)
	deps.live.Provider = p
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("hello\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("expected 1 run, got %d", p.calls)
	}
	if got := out.String(); !strings.Contains(got, "empty response from the model") {
		t.Errorf("empty response not flagged, out=%q", got)
	}
}

// TestREPLExportImportRoundTrip drives /export and /import end to end over the
// REPL: after a turn, "/export <path>" writes a JSONL file, then "/import
// <path>" materializes it as a fresh session and switches to it (US-008, #124).
func TestREPLExportImportRoundTrip(t *testing.T) {
	p := &replProvider{reply: "the answer"}
	deps, store := newTestDeps(t, p)
	origID := deps.header.ID
	out := filepath.Join(t.TempDir(), "sess.jsonl")

	var buf bytes.Buffer
	in := strings.NewReader("hello\n/export " + out + "\n/import " + out + "\n/exit\n")
	if err := runREPL(in, &buf, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, "exported") {
		t.Errorf("/export should confirm, out=%q", s)
	}
	if !strings.Contains(s, "imported") {
		t.Errorf("/import should confirm, out=%q", s)
	}
	// The export file must exist and be non-empty.
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Fatalf("export file missing or empty: err=%v", err)
	}
	// The import creates a new session distinct from the original, so the store
	// should now hold at least 2 sessions.
	headers, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	var foundNew bool
	for _, h := range headers {
		if h.SessionID != origID && h.ParentSessionID == origID {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("expected an imported session with ParentSession=%q, headers=%+v", origID, headers)
	}
}

// TestREPLExportDefaultsToJSONL verifies "/export" with no path defaults to
// "<session-id>.jsonl" and does not launch an agent run.
func TestREPLExportDefaultsToJSONL(t *testing.T) {
	p := &replProvider{reply: "ok"}
	deps, _ := newTestDeps(t, p)
	dir := t.TempDir()
	// Run inside a temp dir so the default relative filename lands there.
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(cwd)

	var buf bytes.Buffer
	if err := runREPL(strings.NewReader("hi\n/export\n/exit\n"), &buf, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	want := deps.header.ID + ".jsonl"
	if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
		t.Errorf("default export file %q not created: %v", want, err)
	}
}

// TestREPLImportTokenBoundary verifies that a command sharing a prefix with
// /import (e.g. "/important") is NOT treated as /import — it falls through to
// slash resolution and reports an unknown command rather than importing.
func TestREPLImportTokenBoundary(t *testing.T) {
	p := &replProvider{reply: "ok"}
	deps, _ := newTestDeps(t, p)
	var buf bytes.Buffer
	if err := runREPL(strings.NewReader("/important\n/exit\n"), &buf, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if strings.Contains(buf.String(), "imported") {
		t.Errorf("/important must not trigger /import, out=%q", buf.String())
	}
	if p.calls != 0 {
		t.Errorf("/important should not launch a run, got %d calls", p.calls)
	}
}

// TestREPLSessionStats drives the /session command: after a turn it prints the
// session id, message count, token estimate, model, and compaction count without
// launching another run (US-009, #125).
func TestREPLSessionStats(t *testing.T) {
	p := &replProvider{reply: "the answer"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("hello\n/session\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 1 {
		t.Errorf("/session must not launch a run (only the prompt), got %d calls", p.calls)
	}
	s := out.String()
	for _, want := range []string{"session:", "messages:", "tokens (est):", "model:", "compactions:"} {
		if !strings.Contains(s, want) {
			t.Errorf("/session output missing %q, out=%q", want, s)
		}
	}
	// After one turn the context holds user + assistant = 2 messages.
	if !strings.Contains(s, "messages:     2") {
		t.Errorf("/session should report 2 messages, out=%q", s)
	}
}

// TestREPLCopyEmpty verifies /copy on a session with no assistant reply prints a
// friendly notice rather than copying or crashing.
func TestREPLCopyEmpty(t *testing.T) {
	p := &replProvider{reply: "hi"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("/copy\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("/copy must not launch a run, got %d calls", p.calls)
	}
	if !strings.Contains(out.String(), "nothing to copy") {
		t.Errorf("/copy on empty session should say so, out=%q", out.String())
	}
}

// TestREPLCopyDegradesToPrint verifies /copy degrades to printing the last reply
// when no clipboard utility is available (PATH pointed at an empty dir), so the
// content is never lost.
func TestREPLCopyDegradesToPrint(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p := &replProvider{reply: "the important answer"}
	deps, _ := newTestDeps(t, p)
	var out bytes.Buffer
	if err := runREPL(strings.NewReader("ask\n/copy\n/exit\n"), &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "no clipboard utility") {
		t.Errorf("/copy should report missing clipboard utility, out=%q", s)
	}
	if !strings.Contains(s, "the important answer") {
		t.Errorf("/copy should print the reply when degrading, out=%q", s)
	}
}

// TestREPLLabelSetsAndClearsFact verifies /label writes and clears a fact.
func TestREPLLabelSetsAndClearsFact(t *testing.T) {
	p := &replProvider{reply: "reply"}
	deps, store := newTestDeps(t, p)
	var out bytes.Buffer
	in := strings.NewReader("hello\n/label 1 task\n/tree\n/label 1\n/exit\n")
	if err := runREPL(in, &out, deps); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if !strings.Contains(out.String(), "set label on node 1: task") {
		t.Errorf("label set message missing, out=%q", out.String())
	}
	if !strings.Contains(out.String(), "cleared label on node 1") {
		t.Errorf("label clear message missing, out=%q", out.String())
	}
	facts, err := store.Facts(deps.header.ID)
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	for _, f := range facts {
		if f.Kind == "label" && f.Value != "" {
			t.Fatalf("label fact should be cleared, got %+v", f)
		}
	}
}
