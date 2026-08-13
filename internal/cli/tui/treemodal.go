package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/compaction"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

// treeModal is the full-screen /tree selector. It lists the session's typed
// entries with the same numbering as the REPL, highlights the selected line,
// and supports navigation, label editing, and branch-summary choice before a
// lane move is committed.
type treeModal struct {
	active         bool
	entries        []session.V4Entry
	lines          []session.V4TreeLine
	currentLeaf    string
	selected       int
	labelBuf       string
	editingLabel   bool
	summaryPrompt  bool
	pendingSummary bool
	summary        string
	message        string
}

func (tm *treeModal) open(store *sessionstore.Store, sessionID string) error {
	entries, err := store.Entries(sessionID)
	if err != nil {
		return err
	}
	leaf, err := store.MainLeaf(sessionID)
	if err != nil {
		return err
	}
	tm.entries = entries
	tm.lines = session.RenderTreeLinesV4(entries, leaf)
	tm.currentLeaf = leaf
	tm.selected = len(tm.lines) - 1
	for i, l := range tm.lines {
		if l.Entry.ID == leaf {
			tm.selected = i
			break
		}
	}
	if tm.selected < 0 {
		tm.selected = 0
	}
	tm.active = true
	tm.message = ""
	return nil
}

func (tm *treeModal) close() {
	*tm = treeModal{}
}

func (tm *treeModal) move(delta int) {
	if len(tm.lines) == 0 {
		return
	}
	tm.selected += delta
	if tm.selected < 0 {
		tm.selected = 0
	}
	if tm.selected >= len(tm.lines) {
		tm.selected = len(tm.lines) - 1
	}
}

func (tm treeModal) selectedEntry() (session.V4Entry, bool) {
	if !tm.active || tm.selected < 0 || tm.selected >= len(tm.lines) {
		return session.V4Entry{}, false
	}
	return tm.lines[tm.selected].Entry, true
}

func (tm treeModal) view(width, height int) string {
	if !tm.active {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	var b strings.Builder
	b.WriteString("Session tree")
	if tm.currentLeaf != "" {
		b.WriteString("  (current: " + tm.currentLeaf + ")")
	}
	b.WriteString("\n")
	if tm.summaryPrompt {
		b.WriteString("Branch summary: [N]o / [S]ummarize / [C]ustom  ")
	}
	if tm.editingLabel {
		b.WriteString("Label: " + tm.labelBuf + "_")
	}
	if tm.message != "" {
		b.WriteString(tm.message)
	}
	b.WriteString("\n")
	for i, l := range tm.lines {
		if i >= height-4 {
			break
		}
		marker := "  "
		if i == tm.selected {
			marker = "> "
		}
		line := fmt.Sprintf("%s%2d. %s", marker, i+1, l.Text)
		line = TruncateToWidth(line, width)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("up/down: select  enter: go  shift+l: label  esc: close")
	return b.String()
}

func (m Model) handleTreeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.tree.editingLabel {
		switch msg.String() {
		case "enter":
			if m.tree.summaryPrompt {
				m.tree.summary = m.tree.labelBuf
				m.tree.editingLabel = false
				m.tree.summaryPrompt = false
				return m.commitTreeNavigation()
			}
			if err := m.saveTreeLabel(); err != nil {
				m.tree.message = "label failed: " + err.Error()
			} else {
				m.tree.editingLabel = false
				m.tree.message = ""
			}
			m.relayout()
			return m, nil
		case "esc":
			m.tree.editingLabel = false
			m.tree.message = ""
			m.relayout()
			return m, nil
		case "backspace":
			r := []rune(m.tree.labelBuf)
			if len(r) > 0 {
				m.tree.labelBuf = string(r[:len(r)-1])
			}
			return m, nil
		default:
			if msg.Text != "" {
				m.tree.labelBuf += msg.Text
			}
			return m, nil
		}
	}
	if m.tree.summaryPrompt {
		switch strings.ToLower(msg.String()) {
		case "n":
			m.tree.summaryPrompt = false
			return m.commitTreeNavigation()
		case "s":
			m.tree.pendingSummary = true
			m.tree.message = "Generating branch summary..."
			m.relayout()
			return m, m.summarizeBranchCmd()
		case "c":
			m.tree.summaryPrompt = false
			m.tree.editingLabel = true
			m.tree.labelBuf = ""
			m.tree.message = "Enter custom branch summary:"
			m.relayout()
			return m, nil
		case "esc":
			m.tree.summaryPrompt = false
			m.tree.message = ""
			m.relayout()
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "up":
		m.tree.move(-1)
	case "down":
		m.tree.move(1)
	case "pgup":
		m.tree.move(-10)
	case "pgdown":
		m.tree.move(10)
	case "shift+l":
		m.tree.editingLabel = true
		m.tree.labelBuf = ""
		m.tree.message = "Label (enter to save, esc to cancel):"
	case "enter":
		target, ok := m.tree.selectedEntry()
		if !ok {
			return m, nil
		}
		if target.ID == m.tree.currentLeaf {
			m.tree.close()
			m.transcript.addSystem("Already at this point")
			m.relayout()
			return m, nil
		}
		m.tree.summaryPrompt = true
		m.tree.message = ""
	case "esc":
		m.tree.close()
		m.relayout()
		return m, nil
	default:
		return m, nil
	}
	m.relayout()
	return m, nil
}

func (m Model) saveTreeLabel() error {
	if m.session == nil {
		return errors.New("no active session")
	}
	target, ok := m.tree.selectedEntry()
	if !ok {
		return errors.New("no selection")
	}
	return m.session.store.SetLabel(m.session.header.ID, target.ID, m.tree.labelBuf)
}

func (m Model) summarizeBranchCmd() tea.Cmd {
	return func() tea.Msg {
		target, ok := m.tree.selectedEntry()
		if !ok || m.live == nil {
			return branchSummaryDoneMsg{err: errors.New("no tree selection")}
		}
		path := session.PathToLeafV4(m.tree.entries, target.ID)
		msgs := make(agentcore.MessageList, 0, len(path))
		for _, e := range path {
			if e.IsMessageEntry() {
				if mm, msgErr := e.MessageValue(); msgErr == nil {
					msgs = append(msgs, mm)
				}
			}
		}
		stream := provider.StreamFnFromProvider(m.live.Provider)
		model := provider.Model{Provider: m.live.ProviderName, ID: run.WireModel(m.live.Model), ContextWindow: m.live.ContextWindow}
		scfg := provider.StreamConfig{}
		if m.session != nil && m.session.creds != nil {
			scfg.APIKey = m.session.creds.GetAPIKey(context.Background(), m.live.ProviderName)
		}
		summary, err := compaction.GenerateSummary(context.Background(), stream, model, msgs, compaction.DefaultCompactionSettings.ReserveTokens, "", scfg)
		return branchSummaryDoneMsg{summary: summary, err: err}
	}
}

func (m Model) commitTreeNavigation() (tea.Model, tea.Cmd) {
	if m.session == nil {
		m.tree.close()
		return m, nil
	}
	target, ok := m.tree.selectedEntry()
	if !ok {
		m.tree.close()
		return m, nil
	}
	if m.tree.summary != "" {
		entry := session.V4Entry{
			Type:      session.EntryTypeBranchSummary,
			ID:        session.NewEntryID(),
			ParentID:  target.ID,
			Timestamp: time.Now().UTC(),
			Summary:   m.tree.summary,
		}
		if _, err := m.session.store.AppendV4Entry(m.session.header.ID, m.session.header, entry); err != nil {
			m.tree.message = "persist branch summary: " + err.Error()
			m.relayout()
			return m, nil
		}
	}
	targetID := target.ID
	if err := m.session.store.MoveLane(m.session.header.ID, "main", &targetID); err != nil {
		m.tree.message = err.Error()
		m.relayout()
		return m, nil
	}
	proj, err := m.session.store.Projection(m.session.header.ID, target.ID)
	if err != nil {
		m.tree.message = err.Error()
		m.relayout()
		return m, nil
	}
	m.session.agentCtx.Messages = proj.Messages
	m.session.curLeaf = proj.LeafID
	m.session.persisted = len(proj.Messages)
	m.transcript.addSystem(fmt.Sprintf("switched to branch at %s (%d messages)", target.ID, len(proj.Messages)))
	m.tree.close()
	m.relayout()
	return m, nil
}
