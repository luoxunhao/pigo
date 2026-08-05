package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

// RunACP starts the ACP-driven chat TUI: an in-process ACP server is started
// over a channel transport and the chat model drives it exclusively through
// the ACP client. This is the ticket 06 pilot; the direct path stays intact
// until ticket 10 removes it.
func RunACP(opts Options) error {
	home, err := sessionstore.PigoHome()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
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
	dreamCfg := &acp.DreamConfig{
		Model:         opts.Model,
		BaseURL:       opts.BaseURL,
		Protocol:      opts.Protocol,
		ProviderName:  opts.ProviderName,
		APIKey:        opts.APIKey,
		ThinkingLevel: opts.ThinkingLevel,
	}
	client, stop := acp.StartInProcess(runner, home, opts.Model, opts.SysPrompt, cwd, mgr, dreamCfg)
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

	// The full-featured Model renders through the ACP bridge; permission
	// requests arrive via withACPSession's handler and are answered inline.
	s, history, err := newRunSession(opts)
	if err != nil {
		return err
	}
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
		select {
		case v := <-respond:
			return v, nil
		}
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

type acpChatLine struct {
	kind   string // user | assistant | tool | system
	text   string
	status string
}

// acpChatModel is the compact ACP-driven chat UI. It renders a transcript,
// streams session/update notifications, answers permission requests with
// single-key decisions, cancels running turns, and supports /model via the
// pigo/command extension.
type acpChatModel struct {
	client    *acp.Client
	cwd       string
	modelName string
	resumeID  string

	sessionID    string
	width        int
	height       int
	input        string
	lines        []acpChatLine
	running      bool
	prompting    bool
	permResp     chan<- any
	events       chan tea.Msg
	quitCh       chan struct{}
	quitOnce     sync.Once
	quitting     bool
	promptCancel context.CancelFunc
}

type acpInitMsg struct {
	sessionID string
	err       error
}

type acpNotifyMsg struct {
	msg acp.IncomingMessage
}

type acpPermMsg struct {
	req     acp.Request
	respond chan<- any
}

type acpPromptDoneMsg struct {
	stopReason string
	err        error
}

func newACPChatModel(client *acp.Client, cwd, modelName, resumeID string) acpChatModel {
	m := acpChatModel{
		client:    client,
		cwd:       cwd,
		modelName: modelName,
		resumeID:  resumeID,
		events:    make(chan tea.Msg, 256),
		quitCh:    make(chan struct{}),
	}
	client.SetPermissionHandler(func(req acp.Request) (any, *acp.Error) {
		respond := make(chan any, 1)
		select {
		case m.events <- acpPermMsg{req: req, respond: respond}:
		case <-m.quitCh:
			return nil, acp.NewError(acp.CodeInternalError, "closed")
		}
		select {
		case v := <-respond:
			return v, nil
		case <-m.quitCh:
			return nil, acp.NewError(acp.CodeInternalError, "closed")
		}
	})
	go m.pumpNotifications()
	return m
}

func (m *acpChatModel) pumpNotifications() {
	for msg := range m.client.Notifications() {
		select {
		case m.events <- acpNotifyMsg{msg: msg}:
		case <-m.quitCh:
			return
		}
	}
}

func (m acpChatModel) Init() tea.Cmd {
	return tea.Batch(m.initSession(), m.waitEvents())
}

func (m acpChatModel) initSession() tea.Cmd {
	return func() tea.Msg {
		if m.sessionID != "" {
			return acpInitMsg{sessionID: m.sessionID}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var id string
		var err error
		if m.resumeID != "" {
			id, err = m.client.LoadSession(ctx, m.resumeID, m.cwd)
		} else {
			id, err = m.client.NewSession(ctx, m.cwd)
		}
		return acpInitMsg{sessionID: id, err: err}
	}
}

func (m acpChatModel) waitEvents() tea.Cmd {
	return func() tea.Msg { return <-m.events }
}

func (m acpChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case acpInitMsg:
		m.sessionID = msg.sessionID
		if msg.err != nil {
			m.addLine("system", "session error: "+msg.err.Error())
			m.quitting = true
			return m, tea.Quit
		}
		m.addLine("system", "session ready: "+msg.sessionID)
		return m, nil
	case acpNotifyMsg:
		m.handleNotify(msg.msg)
		return m, nil
	case acpPermMsg:
		m.prompting = true
		m.permResp = msg.respond
		m.addLine("system", "permission: "+toolSummaryFromPerm(msg.req))
		return m, nil
	case acpPromptDoneMsg:
		m.running = false
		m.promptCancel = nil
		if msg.err != nil {
			m.addLine("system", "turn error: "+msg.err.Error())
		} else {
			m.addLine("system", "turn done: "+msg.stopReason)
		}
		return m, nil
	}
	return m, nil
}

func (m acpChatModel) handleKey(k tea.KeyPressMsg) (acpChatModel, tea.Cmd) {
	if m.prompting {
		switch k.String() {
		case "y":
			return m.respondPermission("allow_once", "allowed once")
		case "a":
			return m.respondPermission("allow_always", "allowed always")
		case "n":
			return m.respondPermission("reject_once", "rejected once")
		case "r":
			return m.respondPermission("reject_always", "rejected always")
		case "esc", "ctrl+c":
			return m.respondPermission("", "permission cancelled")
		}
		return m, nil
	}
	switch k.String() {
	case "enter":
		if m.running || m.sessionID == "" {
			return m, nil
		}
		return m.submit()
	case "backspace":
		r := []rune(m.input)
		if len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
		return m, nil
	case "space":
		m.input += " "
		return m, nil
	case "esc", "ctrl+c", "ctrl+d":
		if m.running {
			_ = m.client.Cancel(m.sessionID)
			if m.promptCancel != nil {
				m.promptCancel()
			}
			m.addLine("system", "cancelling turn")
			return m, nil
		}
		m.quitOnce.Do(func() { close(m.quitCh) })
		m.quitting = true
		return m, tea.Quit
	default:
		if text := k.Key().Text; text != "" {
			m.input += text
		}
	}
	return m, nil
}

func (m acpChatModel) submit() (acpChatModel, tea.Cmd) {
	text := strings.TrimSpace(m.input)
	if text == "" {
		return m, nil
	}
	m.addLine("user", text)
	m.input = ""
	m.running = true
	sessionID := m.sessionID
	if strings.HasPrefix(text, "/") {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out, err := m.client.Command(ctx, sessionID, text)
			select {
			case m.events <- acpPromptDoneMsg{stopReason: out, err: err}:
			case <-m.quitCh:
			}
		}()
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.promptCancel = cancel
	go func() {
		stopReason, err := m.client.Prompt(ctx, sessionID, text)
		select {
		case m.events <- acpPromptDoneMsg{stopReason: stopReason, err: err}:
		case <-m.quitCh:
		}
	}()
	return m, nil
}

func (m acpChatModel) respondPermission(optionID, label string) (acpChatModel, tea.Cmd) {
	if m.permResp == nil {
		return m, nil
	}
	if optionID == "" {
		m.permResp <- map[string]any{"outcome": "cancelled"}
	} else {
		m.permResp <- map[string]any{"outcome": "selected", "optionId": optionID}
	}
	m.permResp = nil
	m.prompting = false
	m.addLine("system", "permission: "+label)
	return m, nil
}

func (m *acpChatModel) handleNotify(msg acp.IncomingMessage) {
	if msg.Notification == nil {
		return
	}
	switch msg.Notification.Method {
	case acp.NotificationSessionUpdate:
		var payload struct {
			Update map[string]any `json:"update"`
		}
		if err := json.Unmarshal(msg.Notification.Params, &payload); err == nil {
			m.applyUpdate(payload.Update)
		}
	case acp.MethodPigoEvent:
		var payload struct {
			Event map[string]any `json:"event"`
		}
		if err := json.Unmarshal(msg.Notification.Params, &payload); err == nil {
			if t, _ := payload.Event["type"].(string); t == "compaction" {
				m.addLine("system", "context compacted")
			}
		}
	}
}

func (m *acpChatModel) applyUpdate(u map[string]any) {
	switch u["sessionUpdate"] {
	case "agent_message_chunk":
		if text := nestedText(u); text != "" {
			m.appendAssistant(text)
		}
	case "agent_thought_chunk":
		if text := nestedText(u); text != "" {
			m.appendAssistant("· " + text)
		}
	case "tool_call":
		id, _ := u["toolCallId"].(string)
		title, _ := u["title"].(string)
		m.lines = append(m.lines, acpChatLine{kind: "tool", text: title + " [" + id + "]", status: "running"})
	case "tool_call_update":
		status, _ := u["status"].(string)
		for i := len(m.lines) - 1; i >= 0; i-- {
			if m.lines[i].kind == "tool" {
				m.lines[i].status = status
				break
			}
		}
	}
}

func nestedText(u map[string]any) string {
	content, _ := u["content"].(map[string]any)
	text, _ := content["text"].(string)
	return text
}

func (m *acpChatModel) appendAssistant(delta string) {
	if len(m.lines) > 0 && m.lines[len(m.lines)-1].kind == "assistant" {
		m.lines[len(m.lines)-1].text += delta
		return
	}
	m.lines = append(m.lines, acpChatLine{kind: "assistant", text: delta})
}

func (m *acpChatModel) addLine(kind, text string) {
	m.lines = append(m.lines, acpChatLine{kind: kind, text: text})
}

func (m acpChatModel) View() tea.View {
	if m.quitting {
		return tea.View{AltScreen: true}
	}
	var b strings.Builder
	for _, line := range m.lines {
		prefix := map[string]string{
			"user":      "you: ",
			"assistant": "",
			"tool":      "▶ ",
			"system":    "· ",
		}[line.kind]
		text := prefix + line.text
		if line.kind == "tool" && line.status != "" {
			text += " [" + line.status + "]"
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	if m.prompting {
		b.WriteString("\n[permission] y=once a=always n=reject r=reject-always esc=cancel\n")
	}
	status := "idle"
	if m.running {
		status = "running (esc cancels)"
	}
	fmt.Fprintf(&b, "\n> %s\npigo acp | %s | %s\n", m.input, m.modelName, status)
	return tea.View{Content: b.String(), AltScreen: true}
}

func toolSummaryFromPerm(req acp.Request) string {
	var params struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
	}
	_ = json.Unmarshal(req.Params, &params)
	if params.ToolCall.Title != "" {
		return params.ToolCall.Title
	}
	return "tool call"
}
