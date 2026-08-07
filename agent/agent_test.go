package agent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/pigo/agent"
)

// hermetic points provider/skill/plugin discovery at throwaway dirs and writes a
// configured openrouter model into a throwaway config.toml, so New resolves
// fully without ever contacting a network. None of these tests call
// Prompt/Stream, so no real request is made.
func hermetic(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	path := filepath.Join(root, "pigo", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `
model = "openrouter/free"

[[models]]
provider = "openrouter"
model_id = "free"
name = "OpenRouter Free"
base_url = "https://openrouter.ai/api/v1"
api_key = "test-key"
protocol = "openai"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("PIGO_HOME", t.TempDir())
}

func contains(set []string, name string) bool {
	for _, n := range set {
		if n == name {
			return true
		}
	}
	return false
}

// TestNewDefaults is the configured-model path: the full built-in tool set is
// advertised and the configured model id resolves to the openrouter provider.
func TestNewDefaults(t *testing.T) {
	hermetic(t)
	sess, err := agent.New(agent.WithModel("openrouter/free"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sess.Close()

	if got := sess.Model(); got != "openrouter/free" {
		t.Errorf("Model() = %q, want %q", got, "openrouter/free")
	}
	if got := sess.Provider(); got != "openrouter" {
		t.Errorf("Provider() = %q, want %q", got, "openrouter")
	}
	for _, want := range []string{"read", "write", "edit", "grep", "find", "bash", "task"} {
		if !contains(sess.ToolNames(), want) {
			t.Errorf("default tool set missing %q: %q", want, sess.ToolNames())
		}
	}
}

// TestWithProviderSelectsConfiguredEntry confirms WithProvider supplies the
// provider prefix for the config lookup when the model id has none.
func TestWithProviderSelectsConfiguredEntry(t *testing.T) {
	hermetic(t)
	sess, err := agent.New(agent.WithModel("free"), agent.WithProvider("openrouter"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sess.Close()

	if got := sess.Model(); got != "free" {
		t.Errorf("Model() = %q, want %q", got, "free")
	}
	if got := sess.Provider(); got != "openrouter" {
		t.Errorf("Provider() = %q, want %q", got, "openrouter")
	}
}

// TestUnconfiguredModelIsError confirms an unresolvable model fails fast at
// construction instead of surfacing on the first prompt.
func TestUnconfiguredModelIsError(t *testing.T) {
	hermetic(t)
	_, err := agent.New(agent.WithModel("nope/nope"))
	if err == nil {
		t.Fatal("New = nil error, want a failure for an unconfigured model")
	}
}

// TestWithToolsAllowlist confirms an allowlist narrows the set to exactly the
// named tools, in order.
func TestWithToolsAllowlist(t *testing.T) {
	hermetic(t)
	sess, err := agent.New(
		agent.WithModel("openrouter/free"),
		agent.WithTools("read", "grep"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sess.Close()

	if got := strings.Join(sess.ToolNames(), ","); got != "read,grep" {
		t.Errorf("ToolNames() = %q, want %q", got, "read,grep")
	}
}

// TestDenyWinsOverAllow is the fail-closed guarantee at the SDK layer: a tool on
// both lists is removed.
func TestDenyWinsOverAllow(t *testing.T) {
	hermetic(t)
	sess, err := agent.New(
		agent.WithModel("openrouter/free"),
		agent.WithTools("read", "bash"),
		agent.WithDisallowedTools("bash"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sess.Close()

	if contains(sess.ToolNames(), "bash") {
		t.Errorf("bash was on both lists and must be removed: %q", sess.ToolNames())
	}
	if !contains(sess.ToolNames(), "read") {
		t.Errorf("read was allowed and not denied, so must survive: %q", sess.ToolNames())
	}
}

// TestWithoutTools yields an empty set — a pure text completion.
func TestWithoutTools(t *testing.T) {
	hermetic(t)
	sess, err := agent.New(
		agent.WithModel("openrouter/free"),
		agent.WithoutTools(),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sess.Close()

	if len(sess.ToolNames()) != 0 {
		t.Errorf("WithoutTools must leave no tools, got %q", sess.ToolNames())
	}
}

// TestUnknownToolIsError confirms a misspelled tool name fails construction
// rather than silently dropping the boundary.
func TestUnknownToolIsError(t *testing.T) {
	hermetic(t)
	_, err := agent.New(
		agent.WithModel("openrouter/free"),
		agent.WithTools("raed"),
	)
	if err == nil {
		t.Fatal("New = nil error, want a failure for the misspelled tool name")
	}
}

// TestInvalidThinkingLevelIsError confirms the level is validated up front.
func TestInvalidThinkingLevelIsError(t *testing.T) {
	hermetic(t)
	_, err := agent.New(
		agent.WithModel("openrouter/free"),
		agent.WithThinkingLevel("supersonic"),
	)
	if err == nil {
		t.Fatal("New = nil error, want a failure for the invalid thinking level")
	}
}

// TestValidThinkingLevels accepts every documented level.
func TestValidThinkingLevels(t *testing.T) {
	hermetic(t)
	for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		sess, err := agent.New(
			agent.WithModel("openrouter/free"),
			agent.WithThinkingLevel(level),
		)
		if err != nil {
			t.Errorf("WithThinkingLevel(%q): %v", level, err)
			continue
		}
		sess.Close()
	}
}

// TestCloseHermetic confirms Close is a no-op (nil) when the session holds no
// plugin manager or memory store, and is safe to call.
func TestCloseHermetic(t *testing.T) {
	hermetic(t)
	sess, err := agent.New(agent.WithModel("openrouter/free"), agent.WithoutTools())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}
