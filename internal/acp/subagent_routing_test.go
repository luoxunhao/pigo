package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
)

func TestSessionPromptRoutesToChildSession(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	reg := runtime.NewRegistry()
	streamFn := func(ctx context.Context, model string, llm provider.LlmContext, cfg provider.StreamConfig) (*provider.AssistantMessageEventStream, error) {
		s := provider.NewAssistantMessageEventStream(0)
		go func() {
			_ = s.Emit(ctx, provider.StreamDoneEvent{Message: agentcore.AssistantMessage{
				RoleField:  agentcore.RoleAssistant,
				StopReason: agentcore.StopReasonEndTurn,
				Content:    agentcore.ContentList{agentcore.NewTextContent("ok <<DONE>>")},
			}})
			s.Close()
		}()
		return s, nil
	}
	factory := func() runtime.RunConfig {
		return runtime.RunConfig{LoopConfig: runtime.LoopConfig{Model: "fake", Stream: streamFn}}
	}
	child := reg.CreateOrGet("parent-1", "call-1", "task", "sys", nil, factory, nil, "")
	childID := child.ID

	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetSubagentRegistry(reg)
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": childID,
		"prompt":    []map[string]any{{"type": "text", "text": "follow up"}},
	})
	if err != nil {
		t.Fatalf("session/prompt to child err = %v", err)
	}
	var resp struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("stopReason = %q, want end_turn", resp.StopReason)
	}
	if got := len(child.Messages); got != 2 {
		t.Errorf("child messages = %d, want 2 after the follow-up turn", got)
	}
}
