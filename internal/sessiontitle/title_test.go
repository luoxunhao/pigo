package sessiontitle

import (
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

type titleStreamRecorder struct {
	reply string
	calls int
	model string
	llm   provider.LlmContext
	cfg   provider.StreamConfig
}

func (r *titleStreamRecorder) stream(ctx context.Context, model string, llm provider.LlmContext, cfg provider.StreamConfig) (*provider.AssistantMessageEventStream, error) {
	r.calls++
	r.model = model
	r.llm = llm
	r.cfg = cfg
	s := provider.NewAssistantMessageEventStream(0)
	msg := agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent(r.reply)},
		StopReason: agentcore.StopReasonEndTurn,
	}
	go func() {
		defer s.Close()
		_ = s.Emit(ctx, provider.StreamStartEvent{Partial: msg})
		_ = s.Emit(ctx, provider.StreamDoneEvent{Message: msg})
	}()
	return s, nil
}

func openTitleStore(t *testing.T) (*sessionstore.Store, string) {
	t.Helper()
	t.Cleanup(sessionstore.CloseAll)
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, ws
}

func createTitleSession(t *testing.T, store *sessionstore.Store, ws, name string) string {
	t.Helper()
	now := time.Now().UTC()
	id := session.NewID(now)
	header := session.SessionHeader{ID: id, CreatedAt: now, UpdatedAt: now, Model: "m", Provider: "p", Cwd: ws}
	if err := store.Create(sessionstore.NewMetadata(id, name, "pigo", "m", ws), header, nil); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return id
}

func TestGenerateTitleUsesFirstPrompt(t *testing.T) {
	rec := &titleStreamRecorder{reply: "Fix lint warnings"}
	title, err := Generate(context.Background(), rec.stream, provider.Model{Provider: "p", ID: "m"}, "fix the lint warnings", provider.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if title != "Fix lint warnings" {
		t.Fatalf("title = %q, want generated reply", title)
	}
	if rec.calls != 1 {
		t.Fatalf("stream calls = %d, want 1", rec.calls)
	}
	if !strings.Contains(rec.llm.SystemPrompt, "short coding-agent session titles") {
		t.Fatalf("system prompt = %q, want title instructions", rec.llm.SystemPrompt)
	}
	text := agentcore.ContentToText(rec.llm.Messages[0].(agentcore.UserMessage).Content)
	if !strings.Contains(text, "fix the lint warnings") {
		t.Fatalf("user prompt = %q, want first prompt", text)
	}
}

func TestAutoTitleSavesAndPublishes(t *testing.T) {
	store, ws := openTitleStore(t)
	id := createTitleSession(t, store, ws, "Session")
	rec := &titleStreamRecorder{reply: "Fix login bug"}
	var published string
	if err := AutoTitle(context.Background(), store, id, "fix login bug", rec.stream, provider.Model{Provider: "p", ID: "m"}, provider.StreamConfig{}, func(title string) {
		published = title
	}); err != nil {
		t.Fatal(err)
	}
	meta, err := store.LoadMetadata(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionName != "Fix login bug" {
		t.Fatalf("sessionName = %q, want generated title", meta.SessionName)
	}
	if published != "Fix login bug" {
		t.Fatalf("published = %q, want generated title", published)
	}
}

func TestAutoTitleSkipsNamedSession(t *testing.T) {
	store, ws := openTitleStore(t)
	id := createTitleSession(t, store, ws, "My Task")
	rec := &titleStreamRecorder{reply: "Should not run"}
	published := false
	if err := AutoTitle(context.Background(), store, id, "hello", rec.stream, provider.Model{Provider: "p", ID: "m"}, provider.StreamConfig{}, func(string) {
		published = true
	}); err != nil {
		t.Fatal(err)
	}
	if rec.calls != 0 {
		t.Fatalf("stream calls = %d, want 0", rec.calls)
	}
	if published {
		t.Fatal("published must not fire for a named session")
	}
}

func TestAutoTitleSkipsEmptyTitle(t *testing.T) {
	store, ws := openTitleStore(t)
	id := createTitleSession(t, store, ws, "Session")
	rec := &titleStreamRecorder{reply: "   "}
	published := false
	if err := AutoTitle(context.Background(), store, id, "hello", rec.stream, provider.Model{Provider: "p", ID: "m"}, provider.StreamConfig{}, func(string) {
		published = true
	}); err != nil {
		t.Fatal(err)
	}
	meta, err := store.LoadMetadata(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionName != "Session" {
		t.Fatalf("sessionName = %q, want unchanged default", meta.SessionName)
	}
	if published {
		t.Fatal("published must not fire for an empty title")
	}
}
