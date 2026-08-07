package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/trust"
)

func TestSessionHookSeamTrustGating(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIGO_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	global := `{"hooks":{"PreToolUse":[{"matcher":"bash","hooks":[{"type":"command","command":"true"}]}]}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}

	ws := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(ws, ".pigo"), 0o700); err != nil {
		t.Fatal(err)
	}
	project := `{"hooks":{"PreToolUse":[{"matcher":"bash","hooks":[{"type":"command","command":"echo project-blocked 1>&2; exit 2"}]}]}}`
	if err := os.WriteFile(filepath.Join(ws, ".pigo", "config.json"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}

	seam := SessionHookSeam()
	call := agentcore.AgentToolCall{ID: "1", Name: "bash", Arguments: json.RawMessage(`{"command":"ls"}`)}

	var untrusted runtime.RunConfig
	if err := seam(&untrusted, "s1", ws); err != nil {
		t.Fatal(err)
	}
	if untrusted.Batch.ToolExecutorConfig.BeforeToolCall == nil {
		t.Fatal("global hook seam missing for untrusted project")
	}
	if dec := untrusted.Batch.ToolExecutorConfig.BeforeToolCall(t.Context(), call); dec != nil {
		t.Fatalf("project hook must not run when untrusted, got %+v", dec)
	}

	mgr, err := trust.NewManager(trust.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetDecision(ws, trust.Trusted); err != nil {
		t.Fatal(err)
	}

	var trusted runtime.RunConfig
	if err := seam(&trusted, "s1", ws); err != nil {
		t.Fatal(err)
	}
	dec := trusted.Batch.ToolExecutorConfig.BeforeToolCall(t.Context(), call)
	if dec == nil || !dec.Block {
		t.Fatalf("project hook must block when trusted, got %+v", dec)
	}
	if dec.Content == nil || agentcore.ContentToText(*dec.Content) != "project-blocked" {
		t.Fatalf("block reason = %+v, want project-blocked", dec.Content)
	}
}

func TestSessionHookSeamFailClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIGO_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}

	seam := SessionHookSeam()
	var cfg runtime.RunConfig
	if err := seam(&cfg, "s1", t.TempDir()); err == nil {
		t.Fatal("expected error for malformed hook config")
	}
}
