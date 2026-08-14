package acp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
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
	if got := r.effectiveContextWindow("fake-model", fakeProvider{}); got != 128000 {
		t.Fatalf("context window = %d, want 128000", got)
	}
}

func TestRuntimeRunnerCustomProviderUsesRegistryEndpoint(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, customOpenAISSE)
	}))
	defer srv.Close()

	models := newConfiguredModels(t, config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: srv.URL, APIKey: "sk-custom", Protocol: "openai",
	})
	runner := &RuntimeRunner{
		Provider:         fakeProvider{},
		ProviderName:     "fake",
		Model:            "custom-gw/m1",
		ConfiguredModels: models,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, last, err := runner.Run(ctx, "hi", nil, nil, "sys", "custom-gw/m1", "", nil, func(agentcore.AgentEvent) {}, TurnHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if last == nil || agentcore.ContentToText(last.Content) != "Hello" {
		t.Fatalf("last = %+v", last)
	}
	if gotAuth != "Bearer sk-custom" {
		t.Errorf("auth = %q, want Bearer sk-custom", gotAuth)
	}
	if gotModel != "m1" {
		t.Errorf("wire model = %q, want m1", gotModel)
	}
}

func TestRuntimeRunnerForwardsConfiguredMaxTokens(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, customOpenAISSE)
	}))
	defer srv.Close()

	models := newConfiguredModels(t, config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: srv.URL, APIKey: "sk-custom", Protocol: "openai",
		MaxTokens: 32000,
	})
	runner := &RuntimeRunner{
		Provider:         fakeProvider{},
		ProviderName:     "fake",
		Model:            "custom-gw/m1",
		ConfiguredModels: models,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := runner.Run(ctx, "hi", nil, nil, "sys", "custom-gw/m1", "", nil, func(agentcore.AgentEvent) {}, TurnHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if got := gotBody["max_tokens"]; got != float64(32000) {
		t.Fatalf("max_tokens = %v, want 32000", got)
	}
}

const customOpenAISSE = `data: {"id":"c1","model":"m1","choices":[{"delta":{"content":"Hello"}}]}

data: {"id":"c1","model":"m1","choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}

data: [DONE]
`
