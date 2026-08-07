package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/runtime"
)

type seamRunner struct{}

func (seamRunner) Run(ctx context.Context, prompt string, images []agentcore.Content, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error) {
	if hooks.InstallSeams != nil {
		var cfg runtime.RunConfig
		_ = hooks.InstallSeams(&cfg)
	}
	msg := agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent("done")},
		StopReason: agentcore.StopReasonEndTurn,
	}
	return nil, &msg, nil
}

// TestStartInProcessWithHooksInstallsPerSessionSeam verifies the CLI-supplied
// hook seam is bound to the live session id and workspace on every turn.
func TestStartInProcessWithHooksInstallsPerSessionSeam(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var gotSession, gotDir string
	seamCalled := false
	seam := func(cfg *runtime.RunConfig, sessionID, projectDir string) error {
		mu.Lock()
		defer mu.Unlock()
		seamCalled = true
		gotSession = sessionID
		gotDir = projectDir
		return nil
	}
	client, stop := StartInProcessWithHooks(&seamRunner{}, home, "openrouter/free", "sys", ws, nil, nil, seam)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Prompt(ctx, sessionID, "hi"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !seamCalled {
		t.Fatal("hook seam was not installed for the turn")
	}
	if gotSession != sessionID {
		t.Fatalf("seam session = %q, want %q", gotSession, sessionID)
	}
	if gotDir != ws {
		t.Fatalf("seam project dir = %q, want %q", gotDir, ws)
	}
}

type recordingBashTool struct {
	ran *bool
}

func (t *recordingBashTool) Name() string            { return "bash" }
func (t *recordingBashTool) Description() string     { return "recording bash" }
func (t *recordingBashTool) Schema() json.RawMessage { return nil }
func (t *recordingBashTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionSequential
}
func (t *recordingBashTool) Execute(ctx context.Context, id string, args json.RawMessage, onUpdate agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	*t.ran = true
	return agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("executed")}}, nil
}

type integrationRunner struct {
	mu              sync.Mutex
	permissionCalls int
	ran             bool
	seamSession     string
	seamDir         string
}

func (r *integrationRunner) Run(ctx context.Context, prompt string, images []agentcore.Content, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error) {
	if hooks.InstallSeams != nil {
		var cfg runtime.RunConfig
		cfg.Batch.ToolExecutorConfig.Registry = agenttool.NewToolRegistry()
		if err := cfg.Batch.ToolExecutorConfig.Registry.Register(&recordingBashTool{ran: &r.ran}); err != nil {
			return nil, nil, err
		}
		cfg.Batch.ToolExecutorConfig.BeforeToolCall = func(context.Context, agentcore.AgentToolCall) *agentcore.BeforeToolCallDecision {
			r.mu.Lock()
			r.permissionCalls++
			r.mu.Unlock()
			return nil
		}
		if err := hooks.InstallSeams(&cfg); err != nil {
			return nil, nil, err
		}
		call := agentcore.AgentToolCall{ID: "1", Name: "bash", Arguments: json.RawMessage(`{"command":"rm -rf /tmp/x"}`)}
		_, _ = agenttool.ExecuteToolCalls(ctx, cfg.Batch, []agentcore.AgentToolCall{call}, nil)
	}
	msg := agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent("done")},
		StopReason: agentcore.StopReasonEndTurn,
	}
	return nil, &msg, nil
}

// TestACPIntegrationHookBlocksBashBeforePermission drives a real hook config
// through the in-process ACP server: the bash call is blocked by PreToolUse,
// the permission seam is never consulted, and the seam receives the session
// workspace.
func TestACPIntegrationHookBlocksBashBeforePermission(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIGO_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	hookCfg := `{"hooks":{"PreToolUse":[{"matcher":"bash","hooks":[{"type":"command","command":"if grep -q 'rm -rf' ; then echo 'dangerous command blocked' 1>&2; exit 2; fi"}]}]}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(hookCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &integrationRunner{}
	seam := func(cfg *runtime.RunConfig, sessionID, projectDir string) error {
		runner.mu.Lock()
		runner.seamSession = sessionID
		runner.seamDir = projectDir
		runner.mu.Unlock()
		return run.SessionHookSeam()(cfg, sessionID, projectDir)
	}
	client, stop := StartInProcessWithHooks(runner, t.TempDir(), "openrouter/free", "sys", ws, nil, nil, seam)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Prompt(ctx, sessionID, "hi"); err != nil {
		t.Fatal(err)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.ran {
		t.Fatal("bash executed despite the PreToolUse block")
	}
	if runner.permissionCalls != 0 {
		t.Fatalf("permission calls = %d, want 0", runner.permissionCalls)
	}
	if runner.seamDir != ws {
		t.Fatalf("seam project dir = %q, want %q", runner.seamDir, ws)
	}
	if runner.seamSession != sessionID {
		t.Fatalf("seam session = %q, want %q", runner.seamSession, sessionID)
	}
}
