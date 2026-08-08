package acpcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/trust"
)

func TestSessionContextBuilderTrustFingerprintInvalidatesRegistry(t *testing.T) {
	cwd := t.TempDir()
	promptsDir := filepath.Join(cwd, ".pigo", "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "review.md"), []byte("Review: $ARGUMENTS"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr, err := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	builder := newSessionContextBuilder(Options{}, run.Env{
		ProviderName: "test",
	}, run.ToolPolicy{}, mgr)

	first, err := builder.Build(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Registry == nil {
		t.Fatal("first registry is nil")
	}
	if _, ok := first.Registry.Lookup("review"); ok {
		t.Fatal("untrusted workspace must not expose project prompts")
	}

	second, err := builder.Build(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Registry != first.Registry {
		t.Fatal("registry should be cached for the same cwd and trust fingerprint")
	}

	if err := mgr.SetDecision(cwd, trust.Trusted); err != nil {
		t.Fatal(err)
	}
	third, err := builder.Build(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if third.Registry == first.Registry {
		t.Fatal("registry must be rebuilt after a trust change")
	}
	if _, ok := third.Registry.Lookup("review"); !ok {
		t.Fatal("trusted workspace should expose project prompts after invalidation")
	}
}
