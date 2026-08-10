package tui

import (
	"context"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/cli/headless"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

// RunACP starts the ACP-driven full TUI: an in-process ACP server is started
// over a channel transport and the full-featured chat model drives it
// exclusively through the ACP client.
func RunACP(opts Options) error {
	home, err := sessionstore.PigoHome()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if opts.ResumeID != "" {
		// Migrate a legacy flat session into the project-scoped store once so
		// the ACP server can load it and every subsequent write has one home.
		if err := headless.EnsureProjectSession(home, cwd, opts.ResumeID); err != nil {
			return err
		}
	}
	mgr, err := trust.NewManager(trust.DefaultPath())
	if err != nil {
		return err
	}
	if opts.Approve {
		mgr.SetSessionTrust(cwd)
	}
	runner := &acp.RuntimeRunner{
		Provider:      opts.Provider,
		ProviderName:  opts.ProviderName,
		Model:         opts.Model,
		APIKey:        opts.APIKey,
		ThinkingLevel: opts.ThinkingLevel,
		Tools:         opts.Tools,
	}
	configured := acp.NewConfiguredModels(config.FileConfigPath())
	_ = configured.Load()
	runner.ConfiguredModels = configured
	dreamCfg := &acp.DreamConfig{
		Model:         opts.Model,
		BaseURL:       opts.BaseURL,
		Protocol:      opts.Protocol,
		ProviderName:  opts.ProviderName,
		APIKey:        opts.APIKey,
		ThinkingLevel: opts.ThinkingLevel,
	}
	if entry, found := configured.Find(opts.Model); found {
		if runner.APIKey == "" {
			runner.APIKey = entry.APIKey
		}
		if runner.ProviderName == "" {
			runner.ProviderName = entry.Provider
		}
		dreamCfg.BaseURL = entry.BaseURL
		dreamCfg.Protocol = entry.Protocol
		dreamCfg.ProviderName = entry.Provider
	}
	client, stop := acp.StartInProcessWithHooks(runner, home, opts.Model, opts.SysPrompt, cwd, mgr, dreamCfg, run.SessionHookSeam())
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		return err
	}
	sessionID := opts.ResumeID
	if sessionID == "" {
		sessionID, err = client.NewSession(ctx, cwd)
	} else {
		sessionID, err = client.LoadSession(ctx, sessionID, cwd)
	}
	if err != nil {
		return err
	}

	proj, err := sessionstore.OpenForWorkspace(home, cwd)
	if err != nil {
		return err
	}
	_, header, _, err := proj.Load(sessionID)
	if err != nil {
		return err
	}
	s, history, err := newRunSessionWithStore(proj.TranscriptStore(), opts)
	if err != nil {
		return err
	}
	// The ACP server owns persistence for this session; the TUI's runSession
	// only supplies display state and must never write a second transcript.
	s.acpBacked = true
	s.store = nil
	s.header = header
	if header.SystemPrompt != "" {
		s.agentCtx.SystemPrompt = header.SystemPrompt
	}
	s.hookDeps.SessionID = sessionID
	m := NewModel(opts).withACPSession(s, history, client, sessionID)
	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}

// withACPSession binds the full Model to the ACP bridge: runs start through
// the ACP client and interrupts cancel the server-side turn.
func (m Model) withACPSession(s *runSession, history []agentcore.Message, client *acp.Client, sessionID string) Model {
	m = m.withSession(s, history)
	permCh := make(chan tea.Msg, 8)
	m.permissionCh = permCh
	client.SetPermissionHandler(func(req acp.Request) (any, *acp.Error) {
		respond := make(chan any, 1)
		permCh <- permissionRequestedMsg{req: req, respond: respond}
		v := <-respond
		return v, nil
	})
	m.startRunFn = func(prompt string) (chan tea.Msg, tea.Cmd) {
		ch := newEventChan()
		startACPRun(client, sessionID, prompt, ch)
		return ch, waitForEvent(ch)
	}
	m.interruptFn = func() { _ = client.Cancel(sessionID) }
	installACPSlashCommands(&m, client, sessionID, s.live)
	return m
}

// installACPSlashCommands overrides the built-in slash commands that have an
// ACP extension counterpart so the full TUI routes them through pigo/command.
func installACPSlashCommands(m *Model, client *acp.Client, sessionID string, live *cli.LiveConfig) {
	if m.slash == nil {
		return
	}
	action := func(name string) func(args string) string {
		return func(args string) string {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if name == "model" && strings.TrimSpace(args) != "" && live != nil {
				live.Model = strings.TrimSpace(args)
			}
			text, err := client.Command(ctx, sessionID, "/"+name+" "+strings.TrimSpace(args))
			if err != nil {
				return "error: " + err.Error()
			}
			return text
		}
	}
	for _, name := range []string{
		"model", "think", "trust", "status", "rewind", "fork", "tree",
		"compact", "session", "help", "copy", "export", "import",
		"rebuild", "memory", "goal", "btw", "dream", "remote-control",
	} {
		m.slash.ReplaceBuiltin(runtime.SlashCommand{
			Name:         name,
			Description:  "route through ACP",
			ArgumentHint: "...",
			Action:       action(name),
		})
	}
}
