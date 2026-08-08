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
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/trust"
)

func newContextDispatcher(t *testing.T, runner SessionRunner, factory SessionContextFactory) (*Dispatcher, Transport) {
	t.Helper()
	client, server := NewChannelPair()
	disp := NewDispatcher(NewSessionManager(runner), server, t.TempDir(), "openrouter/free", "startup", nil, nil)
	disp.SetRunner(runner)
	if factory != nil {
		disp.SetSessionContextFactory(factory)
	}
	srv := NewServer(server, disp)
	go func() { _ = srv.Serve(context.Background()) }()
	t.Cleanup(func() { _ = client.Close() })
	return disp, client
}

func mkWorkspace(t *testing.T) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestSessionNewBuildsSystemPromptPerCwd(t *testing.T) {
	wsA := mkWorkspace(t)
	wsB := mkWorkspace(t)
	factory := func(cwd string, _ []string) (SessionContext, error) {
		return SessionContext{SysPrompt: "prompt:" + cwd}, nil
	}
	disp, client := newContextDispatcher(t, &fakeRunner{}, factory)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ids := make([]string, 0, 2)
	for _, ws := range []string{wsA, wsB} {
		raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, resp.SessionID)
	}

	for i, ws := range []string{wsA, wsB} {
		sess := disp.manager.Get(ids[i])
		if sess == nil {
			t.Fatalf("session %q missing", ids[i])
		}
		if want := "prompt:" + ws; sess.Header.SystemPrompt != want {
			t.Fatalf("session %d system prompt = %q, want %q", i, sess.Header.SystemPrompt, want)
		}
	}
}

func TestSessionLoadRebuildsAndPersistsSystemPrompt(t *testing.T) {
	ws := mkWorkspace(t)
	factory := func(cwd string, _ []string) (SessionContext, error) {
		return SessionContext{SysPrompt: "old:" + cwd}, nil
	}
	disp, client := newContextDispatcher(t, &fakeRunner{}, factory)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

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
	if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "hello"}},
	}); err != nil {
		t.Fatal(err)
	}

	disp.SetSessionContextFactory(func(cwd string, _ []string) (SessionContext, error) {
		return SessionContext{SysPrompt: "new:" + cwd}, nil
	})
	if _, err := client.SendRequest(ctx, MethodSessionLoad, map[string]any{
		"sessionId": newResp.SessionID,
		"cwd":       ws,
	}); err != nil {
		t.Fatal(err)
	}

	sess := disp.manager.Get(newResp.SessionID)
	if sess == nil {
		t.Fatal("session missing after load")
	}
	if want := "new:" + ws; sess.Header.SystemPrompt != want {
		t.Fatalf("in-memory system prompt = %q, want %q", sess.Header.SystemPrompt, want)
	}
	_, header, _, err := sess.Store.Load(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if header.SystemPrompt != "new:"+ws {
		t.Fatalf("persisted system prompt = %q, want %q", header.SystemPrompt, "new:"+ws)
	}
}

func TestSessionEventMapperCwdIsolated(t *testing.T) {
	wsA := mkWorkspace(t)
	wsB := mkWorkspace(t)
	runner := &fakeRunner{
		events: []agentcore.AgentEvent{
			agentcore.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: "bash", Args: map[string]any{"command": "pwd"}},
			agentcore.ToolExecutionEndEvent{ToolCallID: "call-1", ToolName: "bash", Result: agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("ok")}}},
		},
	}
	_, client := newContextDispatcher(t, runner, nil)
	msgs, stop := startClientReader(t, client)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionIDs := make([]string, 0, 2)
	for _, ws := range []string{wsA, wsB} {
		raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		sessionIDs = append(sessionIDs, resp.SessionID)
		if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
			"sessionId": resp.SessionID,
			"prompt":    []map[string]any{{"type": "text", "text": "run"}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	cwdBySession := make(map[string]string)
	deadline := time.After(5 * time.Second)
	for len(cwdBySession) < 2 {
		select {
		case msg := <-msgs:
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				SessionID string         `json:"sessionId"`
				Update    map[string]any `json:"update"`
			}
			if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Update["sessionUpdate"] != "tool_call" {
				continue
			}
			meta, _ := payload.Update["_meta"].(map[string]any)
			info, _ := meta["terminal_info"].(map[string]any)
			if cwd, _ := info["cwd"].(string); cwd != "" {
				cwdBySession[payload.SessionID] = cwd
			}
		case <-deadline:
			t.Fatalf("timed out waiting for tool_call cwd: %+v", cwdBySession)
		}
	}
	if cwdBySession[sessionIDs[0]] != wsA || cwdBySession[sessionIDs[1]] != wsB {
		t.Fatalf("cwd by session = %+v, want %q and %q", cwdBySession, wsA, wsB)
	}
}

func TestSessionSlashRegistryIsolated(t *testing.T) {
	wsA := mkWorkspace(t)
	wsB := mkWorkspace(t)
	regA := runtime.NewSlashRegistry()
	regA.AddBuiltin(runtime.SlashCommand{Name: "greet", Description: "greet", Action: func(string) string { return "hello-a" }})
	regB := runtime.NewSlashRegistry()
	regB.AddBuiltin(runtime.SlashCommand{Name: "greet", Description: "greet", Action: func(string) string { return "hello-b" }})
	factory := func(cwd string, _ []string) (SessionContext, error) {
		reg := regA
		if cwd == wsB {
			reg = regB
		}
		return SessionContext{Registry: reg}, nil
	}
	_, client := newContextDispatcher(t, &fakeRunner{}, factory)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	want := map[string]string{wsA: "hello-a", wsB: "hello-b"}
	for ws, expected := range want {
		raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		raw, err = client.SendRequest(ctx, MethodPigoCommand, map[string]any{
			"sessionId": resp.SessionID,
			"command":   "/greet",
		})
		if err != nil {
			t.Fatal(err)
		}
		var cmdResp struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &cmdResp); err != nil {
			t.Fatal(err)
		}
		if cmdResp.Text != expected {
			t.Fatalf("/greet for %s = %q, want %q", ws, cmdResp.Text, expected)
		}
	}
}

func TestPigoTrustSetInvalidatesLiveSessionRegistry(t *testing.T) {
	cwd := mkWorkspace(t)
	mgr, err := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	client, server := NewChannelPair()
	broker := NewACPPermissionBroker(server, mgr, cwd, 0)
	disp := NewDispatcher(NewSessionManager(&fakeRunner{}), server, t.TempDir(), "openrouter/free", "startup", broker, nil)
	disp.SetRunner(&fakeRunner{})
	disp.SetSessionContextFactory(func(ws string, _ []string) (SessionContext, error) {
		reg := runtime.NewSlashRegistry()
		if mgr.IsTrusted(ws) {
			reg.AddBuiltin(runtime.SlashCommand{Name: "review", Description: "review", Action: func(string) string { return "ok:" + ws }})
		}
		return SessionContext{Registry: reg}, nil
	})
	srv := NewServer(server, disp)
	go func() { _ = srv.Serve(context.Background()) }()
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	var newResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &newResp); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendRequest(ctx, MethodPigoCommand, map[string]any{
		"sessionId": newResp.SessionID,
		"command":   "/review",
	}); err == nil {
		t.Fatal("untrusted session should not expose project slash command")
	}

	if _, err := client.SendRequest(ctx, MethodPigoTrustSet, map[string]any{
		"path":     cwd,
		"decision": "trusted",
	}); err != nil {
		t.Fatal(err)
	}

	raw, err = client.SendRequest(ctx, MethodPigoCommand, map[string]any{
		"sessionId": newResp.SessionID,
		"command":   "/review",
	})
	if err != nil {
		t.Fatalf("live session registry not invalidated after trust change: %v", err)
	}
	var cmdResp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &cmdResp); err != nil {
		t.Fatal(err)
	}
	if cmdResp.Text != "ok:"+cwd {
		t.Fatalf("/review text = %q, want %q", cmdResp.Text, "ok:"+cwd)
	}
}

func TestSessionToolsRootedAtCwd(t *testing.T) {
	wsA := mkWorkspace(t)
	wsB := mkWorkspace(t)
	runner := &RuntimeRunner{Tools: []agentcore.AgentTool{&agenttool.ReadTool{Root: "template-root"}}}
	disp, client := newContextDispatcher(t, runner, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ids := make([]string, 0, 2)
	for _, ws := range []string{wsA, wsB} {
		raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, resp.SessionID)
	}
	for i, ws := range []string{wsA, wsB} {
		sess := disp.manager.Get(ids[i])
		if sess == nil {
			t.Fatalf("session %d missing", i)
		}
		read, ok := sess.Tools[0].(*agenttool.ReadTool)
		if !ok {
			t.Fatalf("session %d tool 0 = %T, want *ReadTool", i, sess.Tools[0])
		}
		if read.Root != ws {
			t.Fatalf("session %d read root = %q, want %q", i, read.Root, ws)
		}
	}
}

func TestSessionStatefulToolsIsolated(t *testing.T) {
	wsA := mkWorkspace(t)
	wsB := mkWorkspace(t)
	templateSnap := agenttool.NewFileSnapshotRecorder()
	templateJobs := agenttool.NewBashJobStore()
	templateTodo := agenttool.NewTodoStore()
	runner := &RuntimeRunner{Tools: []agentcore.AgentTool{
		&agenttool.TodoTool{Store: templateTodo},
		&agenttool.WriteTool{Root: "template-root", Snap: templateSnap},
		&agenttool.BashTool{Dir: "template-root", Jobs: templateJobs},
	}}
	disp, client := newContextDispatcher(t, runner, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ids := make([]string, 0, 2)
	for _, ws := range []string{wsA, wsB} {
		raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, resp.SessionID)
	}

	sessA := disp.manager.Get(ids[0])
	sessB := disp.manager.Get(ids[1])
	if sessA == nil || sessB == nil {
		t.Fatal("sessions missing")
	}
	find := func(sess *AcpSession, want string) any {
		for _, tool := range sess.Tools {
			switch tt := tool.(type) {
			case *agenttool.TodoTool:
				if want == "todo" {
					return tt.Store
				}
			case *agenttool.WriteTool:
				if want == "snap" {
					return tt.Snap
				}
			case *agenttool.BashTool:
				if want == "jobs" {
					return tt.Jobs
				}
			}
		}
		return nil
	}
	if find(sessA, "todo") == find(sessB, "todo") {
		t.Fatal("sessions must not share a TodoStore")
	}
	if find(sessA, "snap") == find(sessB, "snap") {
		t.Fatal("sessions must not share a FileSnapshotRecorder")
	}
	if find(sessA, "jobs") == find(sessB, "jobs") {
		t.Fatal("sessions must not share a BashJobStore")
	}
	if find(sessA, "todo") == templateTodo || find(sessA, "snap") == templateSnap || find(sessA, "jobs") == templateJobs {
		t.Fatal("session tools must not reuse the process template state stores")
	}
}

type promptSysPromptRunner struct {
	mu   sync.Mutex
	seen map[string]string
}

func (r *promptSysPromptRunner) Run(ctx context.Context, prompt string, images []agentcore.Content, history agentcore.MessageList, sysPrompt, model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error) {
	r.mu.Lock()
	r.seen[prompt] = sysPrompt
	r.mu.Unlock()
	msg := agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent("done")},
		StopReason: agentcore.StopReasonEndTurn,
	}
	msgs := append(append(agentcore.MessageList{}, history...),
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent(prompt)}},
		msg,
	)
	return msgs, &msg, nil
}

func TestConcurrentSessionIsolation(t *testing.T) {
	wsA := mkWorkspace(t)
	wsB := mkWorkspace(t)
	runner := &promptSysPromptRunner{seen: make(map[string]string)}
	factory := func(cwd string, _ []string) (SessionContext, error) {
		return SessionContext{SysPrompt: "prompt:" + cwd}, nil
	}
	disp, client := newContextDispatcher(t, runner, factory)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	newSession := func(ws string) string {
		raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		return resp.SessionID
	}
	idA := newSession(wsA)
	idB := newSession(wsB)

	prompt := func(sessionID, text string) {
		if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": text}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Alternating turns must not leak prompts or system prompts across sessions.
	prompt(idA, "run-a-1")
	prompt(idB, "run-b-1")
	prompt(idA, "run-a-2")
	prompt(idB, "run-b-2")

	// Concurrent turns on both sessions must stay isolated too.
	var wg sync.WaitGroup
	for _, call := range []struct {
		id   string
		text string
	}{
		{idA, "run-a-3"},
		{idB, "run-b-3"},
	} {
		wg.Add(1)
		go func(id, text string) {
			defer wg.Done()
			prompt(id, text)
		}(call.id, call.text)
	}
	wg.Wait()

	runner.mu.Lock()
	seen := make(map[string]string, len(runner.seen))
	for k, v := range runner.seen {
		seen[k] = v
	}
	runner.mu.Unlock()
	for _, text := range []string{"run-a-1", "run-a-2", "run-a-3"} {
		if got := seen[text]; got != "prompt:"+wsA {
			t.Fatalf("%s saw sysPrompt %q, want %q", text, got, "prompt:"+wsA)
		}
	}
	for _, text := range []string{"run-b-1", "run-b-2", "run-b-3"} {
		if got := seen[text]; got != "prompt:"+wsB {
			t.Fatalf("%s saw sysPrompt %q, want %q", text, got, "prompt:"+wsB)
		}
	}

	sessA := disp.manager.Get(idA)
	sessB := disp.manager.Get(idB)
	if sessA == nil || sessB == nil {
		t.Fatal("sessions missing after concurrent runs")
	}
	if sessA.Header.SystemPrompt != "prompt:"+wsA || sessB.Header.SystemPrompt != "prompt:"+wsB {
		t.Fatalf("headers crossed: A=%q B=%q", sessA.Header.SystemPrompt, sessB.Header.SystemPrompt)
	}
	if sessA.Mapper.cwdValue() != wsA || sessB.Mapper.cwdValue() != wsB {
		t.Fatalf("mapper cwds crossed: A=%q B=%q", sessA.Mapper.cwdValue(), sessB.Mapper.cwdValue())
	}
}
