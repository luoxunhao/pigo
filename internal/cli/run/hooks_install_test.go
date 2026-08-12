package run

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/hooks"
	"github.com/smallnest/pigo/internal/runtime"
)

func TestInstallHooksEmptyShortCircuits(t *testing.T) {
	var cfg runtime.RunConfig
	if d := InstallHooks(&cfg, nil, HookDeps{ProjectDir: t.TempDir()}); d != nil {
		t.Fatalf("expected nil dispatcher for empty hook set, got %v", d)
	}
	if d := InstallHooks(&cfg, hooks.HookSet{}, HookDeps{}); d != nil {
		t.Fatalf("expected nil dispatcher for empty map, got %v", d)
	}
}

func TestInstallHooksBuildsDispatcher(t *testing.T) {
	set := hooks.HookSet{
		"PreToolUse": {{Matcher: "*", Hooks: []hooks.HookConfig{{Command: "true"}}}},
	}
	var cfg runtime.RunConfig
	d := InstallHooks(&cfg, set, HookDeps{ProjectDir: t.TempDir()})
	if d == nil {
		t.Fatal("expected non-nil dispatcher for non-empty hook set")
	}
}

func TestChainBeforeToolCall(t *testing.T) {
	block := &agentcore.BeforeToolCallDecision{Block: true}
	allow := func(context.Context, agentcore.AgentToolCall) *agentcore.BeforeToolCallDecision { return nil }
	deny := func(context.Context, agentcore.AgentToolCall) *agentcore.BeforeToolCallDecision { return block }

	// nil operands act as identity.
	if got := chainBeforeToolCall(nil, deny); got == nil {
		t.Fatal("nil prev should return next")
	}
	if got := chainBeforeToolCall(allow, nil); got == nil {
		t.Fatal("nil next should return prev")
	}

	// prev blocks → short-circuit, next never runs.
	nextRan := false
	spy := func(context.Context, agentcore.AgentToolCall) *agentcore.BeforeToolCallDecision {
		nextRan = true
		return nil
	}
	if dec := chainBeforeToolCall(deny, spy)(context.Background(), agentcore.AgentToolCall{}); dec == nil || !dec.Block {
		t.Fatalf("expected block decision, got %v", dec)
	}
	if nextRan {
		t.Fatal("next should not run after prev blocks")
	}

	// prev allows → next runs and decides.
	if dec := chainBeforeToolCall(allow, deny)(context.Background(), agentcore.AgentToolCall{}); dec == nil || !dec.Block {
		t.Fatalf("expected next's block decision, got %v", dec)
	}
}

func TestChainAfterToolCall(t *testing.T) {
	content := agentcore.ContentList{}
	prevRes := &agentcore.AfterToolCallResult{Content: &content}
	nextRes := &agentcore.AfterToolCallResult{Content: &content}
	prev := func(context.Context, agentcore.AgentToolCall, agentcore.AgentToolResult, bool) *agentcore.AfterToolCallResult {
		return prevRes
	}
	nilNext := func(context.Context, agentcore.AgentToolCall, agentcore.AgentToolResult, bool) *agentcore.AfterToolCallResult {
		return nil
	}
	next := func(context.Context, agentcore.AgentToolCall, agentcore.AgentToolResult, bool) *agentcore.AfterToolCallResult {
		return nextRes
	}

	// next returns nil → prev's result preserved.
	if got := chainAfterToolCall(prev, nilNext)(context.Background(), agentcore.AgentToolCall{}, agentcore.AgentToolResult{}, false); got != prevRes {
		t.Fatalf("expected prev result when next is nil, got %v", got)
	}
	// next returns non-nil → next wins.
	if got := chainAfterToolCall(prev, next)(context.Background(), agentcore.AgentToolCall{}, agentcore.AgentToolResult{}, false); got != nextRes {
		t.Fatalf("expected next result to win, got %v", got)
	}
}

func TestChainShouldStop(t *testing.T) {
	yes := func(context.Context, *agentcore.AgentContext) bool { return true }
	no := func(context.Context, *agentcore.AgentContext) bool { return false }

	if got := chainShouldStop(no, no)(context.Background(), nil); got {
		t.Fatal("both false should be false")
	}
	if got := chainShouldStop(no, yes)(context.Background(), nil); !got {
		t.Fatal("next true should stop")
	}
	// prev true short-circuits without consulting next.
	nextRan := false
	spy := func(context.Context, *agentcore.AgentContext) bool { nextRan = true; return false }
	if got := chainShouldStop(yes, spy)(context.Background(), nil); !got {
		t.Fatal("prev true should stop")
	}
	if nextRan {
		t.Fatal("next should not run after prev returns true")
	}
}

func TestInstallSeamsBeforeRunsHookBeforePermission(t *testing.T) {
	deps := HookDeps{ProjectDir: t.TempDir()}
	call := agentcore.AgentToolCall{ID: "1", Name: "bash", Arguments: json.RawMessage(`{"command":"rm -rf /tmp/x"}`)}

	permissionCalled := false
	permission := func(context.Context, agentcore.AgentToolCall) *agentcore.BeforeToolCallDecision {
		permissionCalled = true
		return nil
	}
	var cfg runtime.RunConfig
	cfg.Batch.ToolExecutorConfig.BeforeToolCall = permission
	blockSet := hooks.HookSet{
		"PreToolUse": {{Matcher: "*", Hooks: []hooks.HookConfig{{Command: "exit 2"}}}},
	}
	d := BuildDispatcher(blockSet, deps)
	InstallSeamsBefore(&cfg, d, deps)
	if dec := cfg.Batch.ToolExecutorConfig.BeforeToolCall(context.Background(), call); dec == nil || !dec.Block {
		t.Fatalf("hook should block, got %+v", dec)
	}
	if permissionCalled {
		t.Fatal("permission must not run after a hook block")
	}

	permissionCalled = false
	var allowCfg runtime.RunConfig
	allowCfg.Batch.ToolExecutorConfig.BeforeToolCall = permission
	allowSet := hooks.HookSet{
		"PreToolUse": {{Matcher: "*", Hooks: []hooks.HookConfig{{Command: "true"}}}},
	}
	d2 := BuildDispatcher(allowSet, deps)
	InstallSeamsBefore(&allowCfg, d2, deps)
	if dec := allowCfg.Batch.ToolExecutorConfig.BeforeToolCall(context.Background(), call); dec != nil {
		t.Fatalf("safe hook should allow, got %+v", dec)
	}
	if !permissionCalled {
		t.Fatal("safe command should reach the permission seam")
	}
}

func TestInstallSeamsPropagatesToSubAgentTool(t *testing.T) {
	tool := runtime.NewSubAgentTool(runtime.SubAgentSpec{Name: "task", NewRunConfig: func() runtime.RunConfig { return runtime.RunConfig{} }})
	reg := agenttool.NewToolRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	var cfg runtime.RunConfig
	cfg.Batch.ToolExecutorConfig.Registry = reg
	set := hooks.HookSet{
		"PreToolUse": {{Matcher: "*", Hooks: []hooks.HookConfig{{Command: "true"}}}},
	}
	deps := HookDeps{ProjectDir: t.TempDir()}
	d := BuildDispatcher(set, deps)
	InstallSeams(&cfg, d, deps)

	var child runtime.RunConfig
	found := false
	for _, tl := range reg.List() {
		if st, ok := tl.(*runtime.SubAgentTool); ok {
			st.ApplyConfigHook(&child)
			found = true
		}
	}
	if !found {
		t.Fatal("task tool not found in registry")
	}
	if child.Batch.ToolExecutorConfig.BeforeToolCall == nil {
		t.Fatal("child RunConfig did not receive the hook seam")
	}
}

func TestInstallSeamsBlocksChildBash(t *testing.T) {
	ran := false
	childTool := &recordingTool{name: "bash", ran: &ran}
	childReg := agenttool.NewToolRegistry()
	if err := childReg.Register(childTool); err != nil {
		t.Fatal(err)
	}
	childFactory := func() runtime.RunConfig {
		return runtime.RunConfig{Batch: agenttool.BatchConfig{ToolExecutorConfig: agenttool.ToolExecutorConfig{Registry: childReg}}}
	}
	taskTool := runtime.NewSubAgentTool(runtime.SubAgentSpec{Name: "task", NewRunConfig: childFactory})
	parentReg := agenttool.NewToolRegistry()
	if err := parentReg.Register(taskTool); err != nil {
		t.Fatal(err)
	}
	var cfg runtime.RunConfig
	cfg.Batch.ToolExecutorConfig.Registry = parentReg
	set := hooks.HookSet{
		"PreToolUse": {{Matcher: "*", Hooks: []hooks.HookConfig{{Command: "exit 2"}}}},
	}
	deps := HookDeps{ProjectDir: t.TempDir()}
	d := BuildDispatcher(set, deps)
	InstallSeams(&cfg, d, deps)

	child := childFactory()
	for _, tl := range parentReg.List() {
		if st, ok := tl.(*runtime.SubAgentTool); ok {
			st.ApplyConfigHook(&child)
		}
	}
	call := agentcore.AgentToolCall{ID: "1", Name: "bash", Arguments: json.RawMessage(`{"command":"rm -rf /tmp/x"}`)}
	msgs, _ := agenttool.ExecuteToolCalls(context.Background(), child.Batch, []agentcore.AgentToolCall{call}, nil)
	if ran {
		t.Fatal("child bash must not execute when the hook blocks")
	}
	if len(msgs) != 1 || !msgs[0].IsError {
		t.Fatalf("child blocked call should be an error result: %+v", msgs)
	}
}
