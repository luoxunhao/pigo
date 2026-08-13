package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/sessionstore"
)

func TestSubAgentToolCreatesChildSession(t *testing.T) {
	reg := NewRegistry()
	child := &fauxProvider{
		name:   "faux-child",
		models: []provider.Model{{Provider: "faux-child", ID: "child"}},
		turns:  []fauxTurn{textTurn("child report <<DONE>>")},
	}
	tool := NewSubAgentTool(SubAgentSpec{
		Name:         "task",
		SystemPrompt: "sys",
		NewRunConfig: func() RunConfig { return newFauxRunCfg(child) },
	})
	tool.SetSubagentRegistry(reg)

	ctx := agentcore.WithSessionID(context.Background(), "parent-1")
	res, err := tool.Execute(ctx, "call-1", json.RawMessage(`{"prompt":"do it"}`), nil)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	childID := SessionID("parent-1", "call-1")
	if got := agentcore.ContentToText(res.Content); !strings.Contains(got, "[subagent session: "+childID+"]") {
		t.Fatalf("result missing child session reference: %q", got)
	}
	cs := reg.Get(childID)
	if cs == nil {
		t.Fatalf("child session %q not registered", childID)
	}
	if cs.ParentID != "parent-1" || cs.ToolCallID != "call-1" {
		t.Errorf("child relationship = parent %q tool %q", cs.ParentID, cs.ToolCallID)
	}
	if len(cs.Messages) != 2 {
		t.Errorf("child messages = %d, want user + assistant", len(cs.Messages))
	}
}

func TestChildSessionContinue(t *testing.T) {
	reg := NewRegistry()
	child := &fauxProvider{
		name:   "faux-child",
		models: []provider.Model{{Provider: "faux-child", ID: "child"}},
		turns: []fauxTurn{
			textTurn("first report <<DONE>>"),
			textTurn("second reply <<DONE>>"),
		},
	}
	tool := NewSubAgentTool(SubAgentSpec{
		Name:         "task",
		SystemPrompt: "sys",
		NewRunConfig: func() RunConfig { return newFauxRunCfg(child) },
	})
	tool.SetSubagentRegistry(reg)

	ctx := agentcore.WithSessionID(context.Background(), "parent-1")
	if _, err := tool.Execute(ctx, "call-1", json.RawMessage(`{"prompt":"do it"}`), nil); err != nil {
		t.Fatalf("initial Execute err = %v", err)
	}
	cs := reg.Get(SessionID("parent-1", "call-1"))
	if cs == nil {
		t.Fatal("child session missing after initial task")
	}
	if cs.Running() {
		t.Fatal("child should be idle after the delegated task settles")
	}
	text, _, err := cs.Continue(context.Background(), "follow up", nil, nil)
	if err != nil {
		t.Fatalf("Continue err = %v", err)
	}
	if text != "second reply" {
		t.Errorf("continue text = %q, want 'second reply'", text)
	}
}

func TestChildSessionPersistsSubagentMetadata(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	cleanupStores(t)
	reg := NewRegistry()
	reg.SetHome(home)
	factory := func() RunConfig { return RunConfig{} }
	child := reg.CreateOrGet("parent-1", "call-1", "task", "sys", nil, factory, nil, cwd)
	if child.Store == nil {
		t.Fatal("child session store was not opened")
	}
	meta, err := child.Store.LoadMetadata(child.ID)
	if err != nil {
		t.Fatalf("LoadMetadata err = %v", err)
	}
	if meta.SessionKind != sessionstore.SessionKindSubagent {
		t.Errorf("SessionKind = %q, want %q", meta.SessionKind, sessionstore.SessionKindSubagent)
	}
	if meta.ParentSessionID != "parent-1" || meta.ParentToolCallID != "call-1" || meta.SubagentType != "task" {
		t.Errorf("child relationship metadata = %+v", meta)
	}
	if _, err := os.Stat(filepath.Join(home, "subagents.json")); !os.IsNotExist(err) {
		t.Errorf("subagents.json index should be removed, stat err = %v", err)
	}
}

func TestChildSessionAdvertisesRegistryTools(t *testing.T) {
	reg := NewRegistry()
	var gotTools []agentcore.AgentTool
	capturing := provider.StreamFn(func(ctx context.Context, model string, llm provider.LlmContext, cfg provider.StreamConfig) (*provider.AssistantMessageEventStream, error) {
		gotTools = llm.Tools
		child := &fauxProvider{
			name:   "faux-child",
			models: []provider.Model{{Provider: "faux-child", ID: "child"}},
			turns:  []fauxTurn{textTurn("done <<DONE>>")},
		}
		return provider.StreamFnFromProvider(child)(ctx, model, llm, cfg)
	})
	toolReg := agenttool.NewToolRegistry()
	_ = toolReg.Register(echoTool("read", agentcore.ToolExecutionParallel, false))
	_ = toolReg.Register(echoTool("bash", agentcore.ToolExecutionParallel, false))
	factory := func() RunConfig {
		return RunConfig{
			LoopConfig: LoopConfig{Model: "child", Stream: capturing},
			Batch:      agenttool.BatchConfig{ToolExecutorConfig: agenttool.ToolExecutorConfig{Registry: toolReg}},
		}
	}
	tool := NewSubAgentTool(SubAgentSpec{
		Name:         "task",
		SystemPrompt: "sys",
		NewRunConfig: factory,
	})
	tool.SetSubagentRegistry(reg)

	ctx := agentcore.WithSessionID(context.Background(), "parent-1")
	if _, err := tool.Execute(ctx, "call-1", json.RawMessage(`{"prompt":"go"}`), nil); err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if len(gotTools) != 2 {
		t.Fatalf("child was advertised %d tools, want 2 from the registry", len(gotTools))
	}
}

func TestChildSessionErrorWithContentRecovered(t *testing.T) {
	reg := NewRegistry()
	report := strings.Repeat("architecture report content. ", 20)
	child := &fauxProvider{
		name:   "faux-child",
		models: []provider.Model{{Provider: "faux-child", ID: "child"}},
		turns:  []fauxTurn{errorContentTurn(report)},
	}
	tool := NewSubAgentTool(SubAgentSpec{
		Name:         "task",
		SystemPrompt: "sys",
		NewRunConfig: func() RunConfig { return newFauxRunCfg(child) },
	})
	tool.SetSubagentRegistry(reg)

	ctx := agentcore.WithSessionID(context.Background(), "parent-1")
	res, err := tool.Execute(ctx, "call-1", json.RawMessage(`{"prompt":"go"}`), nil)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if res.IsError {
		t.Fatalf("child error stop with substantial content must be recovered: %+v", res)
	}
	if got := agentcore.ContentToText(res.Content); !strings.Contains(got, strings.TrimSpace(report)) {
		t.Errorf("result missing the streamed report: %q", got)
	}
	if cs := reg.Get(SessionID("parent-1", "call-1")); cs == nil {
		t.Fatal("child session missing after recovered result")
	}
}

func TestChildSessionErrorWithContentSurfacesDiagnostic(t *testing.T) {
	reg := NewRegistry()
	report := strings.Repeat("architecture report content. ", 20)
	child := &fauxProvider{
		name:   "faux-child",
		models: []provider.Model{{Provider: "faux-child", ID: "child"}},
		turns: []fauxTurn{errorContentWithMessageTurn(
			report,
			"run stopped after repeated truncated responses despite short-reply guidance (output token limit)",
		)},
	}
	tool := NewSubAgentTool(SubAgentSpec{
		Name:         "task",
		SystemPrompt: "sys",
		NewRunConfig: func() RunConfig { return newFauxRunCfg(child) },
	})
	tool.SetSubagentRegistry(reg)

	ctx := agentcore.WithSessionID(context.Background(), "parent-1")
	_, err := tool.Execute(ctx, "call-1", json.RawMessage(`{"prompt":"go"}`), nil)
	if err == nil {
		t.Fatal("expected error for child run with ErrorMessage and content, got nil")
	}
	if !strings.Contains(err.Error(), "repeated truncated responses despite short-reply guidance") {
		t.Errorf("error %q hides the length-breaker diagnostic", err.Error())
	}
	if !strings.Contains(err.Error(), "architecture report content") {
		t.Errorf("error %q omits the trailing streamed snippet", err.Error())
	}
}

func TestChildSessionLengthRecoversWithGuidance(t *testing.T) {
	reg := NewRegistry()
	child := &fauxProvider{
		name:   "faux-child",
		models: []provider.Model{{Provider: "faux-child", ID: "child"}},
		turns: []fauxTurn{
			lengthToolTurn("c1", "write", `{"path":"report.md"`),
			lengthToolTurn("c2", "write", `{"path":"report.md"`),
			lengthToolTurn("c3", "write", `{"path":"report.md"`),
			textTurn("concise summary <<DONE>>"),
		},
	}
	tool := NewSubAgentTool(SubAgentSpec{
		Name:         "task",
		SystemPrompt: "sys",
		NewRunConfig: func() RunConfig {
			return newFauxRunCfg(child, echoTool("write", agentcore.ToolExecutionParallel, false))
		},
	})
	tool.SetSubagentRegistry(reg)

	ctx := agentcore.WithSessionID(context.Background(), "parent-1")
	res, err := tool.Execute(ctx, "call-1", json.RawMessage(`{"prompt":"go"}`), nil)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if res.IsError {
		t.Fatalf("child must recover after short-reply guidance: %+v", res)
	}
	if got := agentcore.ContentToText(res.Content); !strings.Contains(got, "concise summary") {
		t.Errorf("result = %q, want the concise summary", got)
	}
}

func TestChildSessionDegenerateLoopStops(t *testing.T) {
	reg := NewRegistry()
	child := &fauxProvider{
		name:   "faux-child",
		models: []provider.Model{{Provider: "faux-child", ID: "child"}},
		turns: []fauxTurn{
			toolCallTurn("c1", "write", `{}`),
			toolCallTurn("c2", "write", `{}`),
			toolCallTurn("c3", "write", `{}`),
			toolCallTurn("c4", "write", `{}`),
			toolCallTurn("c5", "write", `{}`),
			toolCallTurn("c6", "write", `{}`),
		},
	}
	tool := NewSubAgentTool(SubAgentSpec{
		Name:         "task",
		SystemPrompt: "sys",
		NewRunConfig: func() RunConfig { return newFauxRunCfg(child, invalidArgsTool("write")) },
	})
	tool.SetSubagentRegistry(reg)

	ctx := agentcore.WithSessionID(context.Background(), "parent-1")
	_, err := tool.Execute(ctx, "call-1", json.RawMessage(`{"prompt":"go"}`), nil)
	if err == nil {
		t.Fatal("expected degenerate child loop to stop with an error, got nil")
	}
	if !strings.Contains(err.Error(), "degenerate tool calls") {
		t.Errorf("error %q does not explain the degenerate loop", err.Error())
	}
}

func TestChildSessionPersistsBeforeErrorReturn(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	cleanupStores(t)
	reg := NewRegistry()
	reg.SetHome(home)
	child := &fauxProvider{
		name:   "faux-child",
		models: []provider.Model{{Provider: "faux-child", ID: "child"}},
		turns:  []fauxTurn{errorTurn("boom")},
	}
	tool := NewSubAgentTool(SubAgentSpec{
		Name:         "task",
		SystemPrompt: "sys",
		Cwd:          cwd,
		NewRunConfig: func() RunConfig { return newFauxRunCfg(child) },
	})
	tool.SetSubagentRegistry(reg)

	ctx := agentcore.WithSessionID(context.Background(), "parent-1")
	if _, err := tool.Execute(ctx, "call-1", json.RawMessage(`{"prompt":"go"}`), nil); err == nil {
		t.Fatal("expected child error to surface, got nil")
	}
	cs := reg.Get(SessionID("parent-1", "call-1"))
	if cs == nil {
		t.Fatal("child session missing after error")
	}
	if len(cs.Messages) < 2 {
		t.Errorf("child messages = %d, want the user prompt and the error assistant persisted", len(cs.Messages))
	}
}

func TestConcurrentChildSessionsShareStore(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	cleanupStores(t)
	reg := NewRegistry()
	reg.SetHome(home)
	factory := func() RunConfig { return RunConfig{} }

	var wg sync.WaitGroup
	children := make([]*ChildSession, 3)
	for i := range children {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			children[i] = reg.CreateOrGet("parent-1", "call-"+string(rune('a'+i)), "task", "sys", nil, factory, nil, cwd)
		}(i)
	}
	wg.Wait()

	for i, cs := range children {
		if cs == nil {
			t.Fatalf("child %d missing", i)
		}
		if cs.Store == nil {
			t.Fatalf("child %d store was not opened (concurrent persistence race)", i)
		}
	}
	if len(reg.stores) != 1 {
		t.Errorf("registry stores = %d, want one shared store per cwd", len(reg.stores))
	}
}
