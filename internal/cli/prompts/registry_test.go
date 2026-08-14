package prompts

import (
	"testing"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli"
)

func TestSlashThinkPersistsConfig(t *testing.T) {
	live := &cli.LiveConfig{
		Model:         "test/model",
		ProviderName:  "test",
		ThinkingLevel: agentcore.ThinkingMedium,
	}
	calls := 0
	live.PersistConfig = func() { calls++ }
	reg, err := BuildSlashRegistry(live, nil, nil, PromptTemplateSources{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := reg.ResolveOutcome("/think high")
	if err != nil {
		t.Fatal(err)
	}
	if !out.Handled {
		t.Fatalf("/think not handled: %+v", out)
	}
	if live.ThinkingLevel != agentcore.ThinkingHigh {
		t.Fatalf("thinking = %q, want high", live.ThinkingLevel)
	}
	if calls != 1 {
		t.Fatalf("PersistConfig calls = %d, want 1", calls)
	}
}
