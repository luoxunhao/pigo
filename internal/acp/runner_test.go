package acp

import (
	"context"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
)

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Models() []provider.Model {
	return []provider.Model{{Provider: "fake", ID: "fake-model", ContextWindow: 128000}}
}

func (fakeProvider) StreamCompletion(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	s := provider.NewAssistantMessageEventStream(0)
	go func() {
		defer s.Close()
		_ = s.Emit(ctx, provider.StreamStartEvent{Partial: agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}})
		partial := agentcore.AssistantMessage{
			RoleField: agentcore.RoleAssistant,
			Content:   agentcore.ContentList{agentcore.NewTextContent("hi from fake")},
		}
		_ = s.Emit(ctx, provider.StreamTextEvent{Partial: partial})
		_ = s.Emit(ctx, provider.StreamDoneEvent{Message: partial})
	}()
	return s, nil
}

func TestRuntimeRunnerRunsPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var events []agentcore.AgentEvent
	runner := &RuntimeRunner{
		Provider:     fakeProvider{},
		ProviderName: "fake",
		Model:        "fake-model",
	}
	msgs, last, err := runner.Run(ctx, "hello", nil, nil, "you are pigo", "fake-model", "", nil, func(ev agentcore.AgentEvent) {
		events = append(events, ev)
	}, TurnHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if last == nil || agentcore.ContentToText(last.Content) != "hi from fake" {
		t.Fatalf("last = %+v", last)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want user + assistant", len(msgs))
	}
	var sawText bool
	for _, ev := range events {
		if me, ok := ev.(agentcore.MessageUpdateEvent); ok {
			if _, ok := me.AssistantMessageEvent.(provider.StreamTextEvent); ok {
				sawText = true
			}
		}
	}
	if !sawText {
		t.Fatalf("no stream text event observed: %+v", events)
	}
}

func TestRuntimeRunnerDefaultsAutoCompaction(t *testing.T) {
	r := &RuntimeRunner{
		Provider:     fakeProvider{},
		ProviderName: "fake",
		Model:        "fake-model",
	}
	if !r.effectiveCompaction().Enabled {
		t.Fatal("default auto-compaction must be enabled")
	}
	if got := r.effectiveContextWindow("fake-model"); got != 128000 {
		t.Fatalf("context window = %d, want 128000", got)
	}
}
