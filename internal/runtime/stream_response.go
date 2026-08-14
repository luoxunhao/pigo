// This file implements streamAssistantResponse (US-003): it shapes the context
// into a provider request, resolves the API key dynamically, drives the
// provider stream, and back-fills the partial assistant message into the
// context while emitting message_start / message_update / message_end events.
package runtime

import (
	"context"
	"fmt"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/compaction"
	"github.com/smallnest/pigo/internal/provider"
)

// LoopConfig holds the pluggable behavior of the agent loop. Every hook is
// optional (nil = use the default). The pointer/func-field pattern mirrors pi's
// optional callbacks.
type LoopConfig struct {
	// Model is the model id passed to StreamFn.
	Model string
	// APIKey is the static fallback key when GetAPIKey is nil or returns "".
	APIKey string
	// ThinkingLevel is the reasoning effort for requests.
	ThinkingLevel agentcore.ThinkingLevel
	// Stream produces the provider stream. Required (defaults are wired by
	// callers/tests, e.g. a fake provider).
	Stream provider.StreamFn

	// TransformContext optionally rewrites the message list before conversion
	// (context trimming/injection). Contract: must not error; on failure return
	// a safe fallback. Runs first.
	TransformContext func(ctx context.Context, msgs agentcore.MessageList) agentcore.MessageList
	// ConvertToLlm optionally filters UI-only messages. Defaults to identity.
	// Contract: must not error.
	ConvertToLlm func(msgs agentcore.MessageList) agentcore.MessageList
	// GetAPIKey optionally resolves a fresh key per request (handles short-lived
	// token expiry). Falls back to APIKey when nil or empty.
	GetAPIKey func(ctx context.Context, provider string) string
	// Provider is the provider name passed to GetAPIKey.
	Provider string

	// ContextWindow is the model's total context-token budget, used to decide
	// automatic compaction. When <= 0 the window is unknown and auto-compaction
	// is disabled (ShouldCompact returns false), so the loop behaves exactly as
	// before for callers that do not plumb it through.
	ContextWindow int
	// Compaction holds the thresholds/retention knobs for auto-compaction. Its
	// Enabled flag gates the feature independently of ContextWindow.
	Compaction compaction.CompactionSettings
	// SummaryStream produces the provider stream used to generate compaction
	// summaries. Defaults to Stream when nil.
	SummaryStream provider.StreamFn
	// SummaryModel is the model used for summarization. When zero, a model is
	// synthesized from Model/ContextWindow.
	SummaryModel provider.Model

	// Extra is forwarded to StreamConfig.Extra.
	Extra map[string]any

	// RequestBuilder, when non-nil, replaces the manual system-prompt/tools
	// assembly below with the frontend's contextbuild pipeline. It receives the
	// live message list and returns the complete provider-visible request.
	// A returned error becomes a terminal assistant message, matching the
	// no-request-failure contract.
	RequestBuilder RequestBuilderFunc
}

// RequestBuilderFunc builds one provider-visible request from the live message
// list. It is the runtime seam frontends use to wire contextbuild into the loop
// without the runtime package depending on contextbuild.
type RequestBuilderFunc func(ctx context.Context, msgs agentcore.MessageList) (provider.LlmContext, error)

// streamAssistantResponse runs one assistant turn: it builds the request from
// agentCtx, streams the provider response, back-fills the partial into
// agentCtx.Messages, and returns the final assistant message. The sequence
// (transformContext → convertToLlm → resolve key → stream → drain) is kept
// identical to pi. It never returns an error for a request failure — such
// failures arrive as a terminal assistant message with stopReason error/aborted.
func streamAssistantResponse(ctx context.Context, agentCtx *agentcore.AgentContext, cfg LoopConfig, emit agentcore.EmitFunc) (agentcore.AssistantMessage, error) {
	// 0. contextbuild request seam: when set, the frontend owns system-prompt
	// assembly, tool resolution, transforms, and LLM conversion.
	if cfg.RequestBuilder != nil {
		llm, buildErr := cfg.RequestBuilder(ctx, agentCtx.Messages)
		if buildErr != nil {
			msg := newErrorAssistantMessage(cfg, buildErr)
			agentCtx.Messages = append(agentCtx.Messages, msg)
			if err := emit(ctx, agentcore.MessageEndEvent{Message: msg}); err != nil {
				return agentcore.AssistantMessage{}, err
			}
			return msg, nil
		}
		return streamShapedResponse(ctx, agentCtx, cfg, llm, emit)
	}
	// 1. transformContext (optional, must not error).
	msgs := agentCtx.Messages
	if cfg.TransformContext != nil {
		msgs = cfg.TransformContext(ctx, msgs)
	}
	// 2. convertToLlm (filter UI-only; default identity).
	if cfg.ConvertToLlm != nil {
		msgs = cfg.ConvertToLlm(msgs)
	}
	// 3. shape the LLM context.
	llm := provider.LlmContext{
		SystemPrompt: agentCtx.SystemPrompt,
		Messages:     msgs,
		Tools:        agentCtx.Tools,
	}
	return streamShapedResponse(ctx, agentCtx, cfg, llm, emit)
}

// streamShapedResponse runs the shared stream path once the provider-visible
// LlmContext is known: resolve the API key, build the provider stream, and
// back-fill the partial assistant message into the context.
func streamShapedResponse(ctx context.Context, agentCtx *agentcore.AgentContext, cfg LoopConfig, llm provider.LlmContext, emit agentcore.EmitFunc) (agentcore.AssistantMessage, error) {
	// 4. resolve API key dynamically, fall back to static.
	key := cfg.APIKey
	if cfg.GetAPIKey != nil {
		if dyn := cfg.GetAPIKey(ctx, cfg.Provider); dyn != "" {
			key = dyn
		}
	}
	// 5. build the provider stream.
	if !upstreamBreakerAllow(cfg.Provider) {
		msg := newErrorAssistantMessage(cfg, fmt.Errorf("upstream circuit breaker open for provider %q", cfg.Provider))
		agentCtx.Messages = append(agentCtx.Messages, msg)
		if err := emit(ctx, agentcore.MessageEndEvent{Message: msg}); err != nil {
			return agentcore.AssistantMessage{}, err
		}
		return msg, nil
	}
	stream, err := cfg.Stream(ctx, cfg.Model, llm, provider.StreamConfig{
		APIKey:        key,
		ThinkingLevel: cfg.ThinkingLevel,
		Extra:         cfg.Extra,
	})
	if err != nil {
		upstreamBreakerRecord(cfg.Provider, isRetryableUpstreamError(err.Error()))
		// Early "cannot build stream" failure: synthesize a terminal message so
		// the loop has a uniform assistant message to record.
		msg := newErrorAssistantMessage(cfg, err)
		agentCtx.Messages = append(agentCtx.Messages, msg)
		if err := emit(ctx, agentcore.MessageEndEvent{Message: msg}); err != nil {
			return agentcore.AssistantMessage{}, err
		}
		return msg, nil
	}

	// 6. drain the stream, back-filling the partial into the context.
	addedPartial := false
	backfill := func(partial agentcore.AssistantMessage) {
		if !addedPartial {
			agentCtx.Messages = append(agentCtx.Messages, partial)
			addedPartial = true
		} else {
			agentCtx.Messages[len(agentCtx.Messages)-1] = partial
		}
	}

	for ev := range stream.Events() {
		switch e := ev.(type) {
		case provider.StreamStartEvent:
			backfill(e.Partial)
			if err := emit(ctx, agentcore.MessageStartEvent{Message: e.Partial}); err != nil {
				return agentcore.AssistantMessage{}, err
			}
		case provider.StreamTextEvent:
			backfill(e.Partial)
			if err := emit(ctx, agentcore.MessageUpdateEvent{Message: e.Partial, AssistantMessageEvent: e}); err != nil {
				return agentcore.AssistantMessage{}, err
			}
		case provider.StreamThinkingEvent:
			backfill(e.Partial)
			if err := emit(ctx, agentcore.MessageUpdateEvent{Message: e.Partial, AssistantMessageEvent: e}); err != nil {
				return agentcore.AssistantMessage{}, err
			}
		case provider.StreamToolCallEvent:
			backfill(e.Partial)
			if err := emit(ctx, agentcore.MessageUpdateEvent{Message: e.Partial, AssistantMessageEvent: e}); err != nil {
				return agentcore.AssistantMessage{}, err
			}
		case provider.StreamDoneEvent:
			upstreamBreakerRecord(cfg.Provider, false)
			finalizeMessage(agentCtx, e.Message, &addedPartial)
			if err := emit(ctx, agentcore.MessageEndEvent{Message: e.Message}); err != nil {
				return agentcore.AssistantMessage{}, err
			}
			return e.Message, nil
		case provider.StreamErrorEvent:
			msg := e.Message.ErrorMessage
			if msg == "" && e.Err != nil {
				msg = e.Err.Error()
			}
			upstreamBreakerRecord(cfg.Provider, isRetryableUpstreamError(msg))
			finalizeMessage(agentCtx, e.Message, &addedPartial)
			if err := emit(ctx, agentcore.MessageEndEvent{Message: e.Message}); err != nil {
				return agentcore.AssistantMessage{}, err
			}
			return e.Message, nil
		}
	}

	// 7. stream ended without done/error: fall back to the stream result.
	upstreamBreakerRecord(cfg.Provider, false)
	final, resErr := stream.Result(ctx)
	if resErr != nil {
		return newErrorAssistantMessage(cfg, resErr), nil
	}
	finalizeMessage(agentCtx, final, &addedPartial)
	if err := emit(ctx, agentcore.MessageEndEvent{Message: final}); err != nil {
		return agentcore.AssistantMessage{}, err
	}
	return final, nil
}

// finalizeMessage replaces the placeholder partial with the final message, or
// appends it if the provider sent done/error without a prior start.
func finalizeMessage(agentCtx *agentcore.AgentContext, final agentcore.AssistantMessage, addedPartial *bool) {
	if *addedPartial {
		agentCtx.Messages[len(agentCtx.Messages)-1] = final
	} else {
		agentCtx.Messages = append(agentCtx.Messages, final)
		*addedPartial = true
	}
}

// newErrorAssistantMessage builds a terminal assistant message for an early
// failure that never produced a provider stream.
func newErrorAssistantMessage(cfg LoopConfig, err error) agentcore.AssistantMessage {
	return agentcore.AssistantMessage{
		RoleField:    agentcore.RoleAssistant,
		Model:        cfg.Model,
		Provider:     cfg.Provider,
		StopReason:   agentcore.StopReasonError,
		ErrorMessage: err.Error(),
	}
}
