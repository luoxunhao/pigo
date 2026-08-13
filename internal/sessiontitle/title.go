// Package sessiontitle generates short LLM-based titles for unnamed sessions.
// It mirrors the pre-refactor ACP title generation: a lightweight provider
// call summarizes the first user prompt, then the title is persisted and
// optionally broadcast to clients. Failures are best-effort and leave the
// default session name intact.
package sessiontitle

import (
	"context"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/sessionstore"
)

const titleSystemPrompt = "You generate short coding-agent session titles. Reply with a single line, at most 40 characters, no quotes, no period."

const titleGenerationTimeout = 30 * time.Second

// Generate asks the provider to summarize firstPrompt into a short session
// title. It returns an empty title without error when the provider returns no
// usable text.
func Generate(ctx context.Context, stream provider.StreamFn, model provider.Model, firstPrompt string, cfg provider.StreamConfig) (string, error) {
	if stream == nil || strings.TrimSpace(firstPrompt) == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, titleGenerationTimeout)
	defer cancel()
	s, err := stream(ctx, model.ID, provider.LlmContext{
		SystemPrompt: titleSystemPrompt,
		Messages: agentcore.MessageList{
			agentcore.UserMessage{
				RoleField: agentcore.RoleUser,
				Content:   agentcore.ContentList{agentcore.NewTextContent("Summarize this task in one short title: " + firstPrompt)},
			},
		},
	}, cfg)
	if err != nil {
		return "", err
	}
	title := ""
	for ev := range s.Events() {
		switch e := ev.(type) {
		case provider.StreamTextEvent:
			title = agentcore.ContentToText(e.Partial.Content)
		case provider.StreamDoneEvent:
			title = agentcore.ContentToText(e.Message.Content)
		}
	}
	return strings.TrimSpace(title), nil
}

// AutoTitle generates and persists a title for sessionID when it still has the
// default "Session" name. publish, when non-nil, receives the saved title.
func AutoTitle(ctx context.Context, store *sessionstore.Store, sessionID, firstPrompt string, stream provider.StreamFn, model provider.Model, cfg provider.StreamConfig, publish func(title string)) error {
	if store == nil || sessionID == "" {
		return nil
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	if meta.SessionName != "" && meta.SessionName != "Session" {
		return nil
	}
	title, err := Generate(ctx, stream, model, firstPrompt, cfg)
	if err != nil || title == "" {
		return err
	}
	// Re-check before persisting so an explicit /name raced with generation is
	// never overwritten.
	meta, err = store.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	if meta.SessionName != "" && meta.SessionName != "Session" {
		return nil
	}
	meta.SessionName = title
	if err := store.SaveMetadata(meta); err != nil {
		return err
	}
	if publish != nil {
		publish(title)
	}
	return nil
}
