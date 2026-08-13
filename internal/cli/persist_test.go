package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

type titleFakeProvider struct {
	mainReply  string
	titleReply string
	titleCalls int
}

func (p *titleFakeProvider) Name() string { return "fake" }
func (p *titleFakeProvider) Models() []provider.Model {
	return []provider.Model{{Provider: "fake", ID: "m"}}
}

func (p *titleFakeProvider) StreamCompletion(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	reply := p.mainReply
	if len(req.Context.Messages) > 0 {
		if u, ok := req.Context.Messages[0].(agentcore.UserMessage); ok {
			if strings.Contains(agentcore.ContentToText(u.Content), "Summarize this task in one short title:") {
				p.titleCalls++
				reply = p.titleReply
			}
		}
	}
	msg := agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent(reply)},
		StopReason: agentcore.StopReasonEndTurn,
	}
	s := provider.NewAssistantMessageEventStream(0)
	go func() {
		defer s.Close()
		_ = s.Emit(ctx, provider.StreamStartEvent{Partial: msg})
		_ = s.Emit(ctx, provider.StreamDoneEvent{Message: msg})
	}()
	return s, nil
}

type fakePersistHost struct {
	Host
	store     *sessionstore.Store
	header    session.SessionHeader
	agentCtx  *agentcore.AgentContext
	live      *LiveConfig
	creds     *provider.CredentialStore
	persisted int
	curLeaf   string
}

func (h *fakePersistHost) Store() *sessionstore.Store        { return h.store }
func (h *fakePersistHost) Header() session.SessionHeader     { return h.header }
func (h *fakePersistHost) AgentCtx() *agentcore.AgentContext { return h.agentCtx }
func (h *fakePersistHost) Live() *LiveConfig                 { return h.live }
func (h *fakePersistHost) Creds() *provider.CredentialStore  { return h.creds }
func (h *fakePersistHost) Persisted() int                    { return h.persisted }
func (h *fakePersistHost) SetPersisted(n int)                { h.persisted = n }
func (h *fakePersistHost) CurLeaf() string                   { return h.curLeaf }
func (h *fakePersistHost) SetCurLeaf(id string)              { h.curLeaf = id }

func TestPersistTurnGeneratesTitleOnFirstTurn(t *testing.T) {
	t.Cleanup(sessionstore.CloseAll)
	home := t.TempDir()
	t.Setenv("PIGO_HOME", home)
	ws := filepath.Join(home, "ws")
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	id := session.NewID(now)
	header := session.SessionHeader{ID: id, CreatedAt: now, UpdatedAt: now, Model: "fake", Provider: "fake", Cwd: ws}
	if err := store.Create(sessionstore.NewMetadata(id, "Session", "pigo", "fake", ws), header, nil); err != nil {
		t.Fatal(err)
	}
	prov := &titleFakeProvider{mainReply: "ok", titleReply: "Fix login bug"}
	live := &LiveConfig{
		Model:         "fake",
		ProviderName:  "fake",
		Provider:      prov,
		ThinkingLevel: agentcore.ThinkingMedium,
		ContextWindow: 128000,
	}
	agentCtx := &agentcore.AgentContext{Messages: agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("fix login bug")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("done")}},
	}}
	host := &fakePersistHost{
		store:    store,
		header:   header,
		agentCtx: agentCtx,
		live:     live,
		creds:    provider.NewCredentialStore(nil),
	}
	var out bytes.Buffer
	PersistTurn(&out, host)
	meta, err := store.LoadMetadata(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionName != "Fix login bug" {
		t.Fatalf("sessionName = %q, want generated title", meta.SessionName)
	}
	if prov.titleCalls != 1 {
		t.Fatalf("title stream calls = %d, want 1", prov.titleCalls)
	}
}
