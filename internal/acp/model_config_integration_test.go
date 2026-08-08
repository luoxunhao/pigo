package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/sessionstore"
)

func TestPromptMissingModelErrors(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	models := newConfiguredModels(t)
	runner := &RuntimeRunner{
		Provider:         fakeProvider{},
		ProviderName:     "fake",
		Model:            "openrouter/free",
		ConfiguredModels: models,
	}
	disp := NewDispatcher(NewSessionManager(runner), server, t.TempDir(), "openrouter/free", "sys", nil, nil)
	disp.SetConfiguredModels(models)
	srv := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var newResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &newResp); err != nil {
		t.Fatal(err)
	}
	_, err = client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("prompt error = %v", err)
	}
}

func TestGeminiRuntimeNotImplemented(t *testing.T) {
	models := newConfiguredModels(t, config.ModelConfig{
		Provider: "g", ModelID: "m", BaseURL: "https://gemini.example", Protocol: "gemini",
	})
	runner := &RuntimeRunner{
		Provider:         fakeProvider{},
		ProviderName:     "fake",
		Model:            "g/m",
		ConfiguredModels: models,
	}
	_, _, _, _, err := runner.ResolveForModel("g/m")
	if err == nil || !strings.Contains(err.Error(), "gemini runtime is not implemented") {
		t.Fatalf("error = %v", err)
	}
}

func TestSessionThinkingPersistsAndRestores(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := NewSessionManager(&fakeRunner{})
	store, err := sessionstore.OpenForWorkspace(home, ws)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.New(ws, "a/m", SessionContext{SysPrompt: "sys"}, store)
	if err != nil {
		t.Fatal(err)
	}
	sess.Thinking = "high"
	if err := persistSessionThinking(sess); err != nil {
		t.Fatal(err)
	}
	meta, err := store.LoadMetadata(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := readSessionThinking(meta); got != "high" {
		t.Fatalf("thinking = %q, want high", got)
	}
	loaded, err := mgr.Load(ws, sess.ID, "", SessionContext{SysPrompt: "sys"}, store)
	if err != nil {
		t.Fatal(err)
	}
	meta, err = store.LoadMetadata(loaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Thinking = readSessionThinking(meta)
	if loaded.Thinking != "high" {
		t.Fatalf("restored thinking = %q", loaded.Thinking)
	}
}
