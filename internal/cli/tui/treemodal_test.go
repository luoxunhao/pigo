package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/agentcore"
)

func TestTreeModalOpensAndNavigates(t *testing.T) {
	store := newTestStore(t)
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("first")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("reply")}},
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("second")}},
	}
	id := saveSession(t, store, msgs)
	opts := Options{Model: "m", ProviderName: "p", ResumeID: id}
	s, history, err := newRunSessionWithStore(store, opts)
	if err != nil {
		t.Fatalf("newRunSessionWithStore: %v", err)
	}
	m := NewModel(opts).withSession(s, history)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = next.(Model)

	next, _ = m.runSlash("/tree")
	m = next.(Model)
	if !m.tree.active {
		t.Fatal("tree modal did not open")
	}
	if !strings.Contains(m.View().Content, "Session tree") {
		t.Fatalf("tree view missing header: %q", m.View().Content)
	}

	// Select the root and navigate with "No summary".
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = next.(Model)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = next.(Model)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if !m.tree.summaryPrompt {
		t.Fatal("branch summary prompt did not open")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)
	if m.tree.active {
		t.Fatal("tree modal should close after navigation")
	}
	if m.session.curLeaf == "" {
		t.Fatal("curLeaf should be set after navigation")
	}
}

func TestTreeModalEditsLabel(t *testing.T) {
	store := newTestStore(t)
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hello")}},
	}
	id := saveSession(t, store, msgs)
	opts := Options{Model: "m", ProviderName: "p", ResumeID: id}
	s, history, err := newRunSessionWithStore(store, opts)
	if err != nil {
		t.Fatalf("newRunSessionWithStore: %v", err)
	}
	m := NewModel(opts).withSession(s, history)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = next.(Model)

	next, _ = m.runSlash("/label 1")
	m = next.(Model)
	if !m.tree.editingLabel {
		t.Fatal("label mode did not open")
	}
	for _, r := range "task" {
		next, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(Model)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.tree.editingLabel {
		t.Fatal("label mode should close after save")
	}
	entries, err := store.Entries(id)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	facts, err := store.Facts(id)
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	found := false
	for _, f := range facts {
		if f.Kind == "label" && f.Key == entries[0].ID && f.Value == "task" {
			found = true
		}
	}
	if !found {
		t.Fatalf("label fact not persisted: facts=%+v", facts)
	}
}
