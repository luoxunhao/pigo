package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/runtime"
)

func TestWithACPSessionBindsBridge(t *testing.T) {
	client, server := acp.NewChannelPair()
	defer client.Close()
	defer server.Close()
	m := NewModel(Options{}).withACPSession(&runSession{}, nil, acp.NewClient(client), "s1")
	if m.startRunFn == nil {
		t.Fatal("startRunFn not bound")
	}
	if m.interruptFn == nil {
		t.Fatal("interruptFn not bound")
	}
}

func TestWithACPSessionRoutesSlashCommands(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	client, stop := acp.StartInProcess(&bridgeFakeRunner{}, home, "m", "sys", ws, nil, nil)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	live := &cli.LiveConfig{Model: "m"}
	m := NewModel(Options{}).withSession(&runSession{live: live, slash: runtime.NewSlashRegistry()}, nil)
	m.startRunFn = nil
	m.interruptFn = nil
	m = m.withACPSession(&runSession{live: live, slash: runtime.NewSlashRegistry()}, nil, client, sessionID)
	cmd, ok := m.slash.Lookup("model")
	if !ok {
		t.Fatal("/model not registered")
	}
	if text := cmd.Action("deepseek/deepseek-v4"); text != "model set to deepseek/deepseek-v4" {
		t.Fatalf("action = %q", text)
	}
	if live.Model != "deepseek/deepseek-v4" {
		t.Fatalf("live model = %q", live.Model)
	}
}
