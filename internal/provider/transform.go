// This file ports pi's transformMessages: model-aware shaping of the
// provider-visible message list. It normalizes tool call ids across providers,
// downgrades images on non-vision models, drops redacted/empty thinking blocks,
// and synthesizes orphan tool results so wire APIs never see an unresolved call.
package provider

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/smallnest/pigo/internal/agentcore"
)

// TransformMessages shapes a message list for a target model. normalizeToolCallID
// may be nil (no cross-provider id normalization).
func TransformMessages(msgs agentcore.MessageList, model Model, normalizeToolCallID func(id string, source agentcore.AssistantMessage) string) agentcore.MessageList {
	imageAware := downgradeUnsupportedImages(msgs, model)
	toolCallIDMap := map[string]string{}
	transformed := make(agentcore.MessageList, 0, len(imageAware))
	for _, m := range imageAware {
		switch msg := m.(type) {
		case agentcore.UserMessage:
			transformed = append(transformed, msg)
		case agentcore.ToolResultMessage:
			if normalized := toolCallIDMap[msg.ToolCallID]; normalized != "" && normalized != msg.ToolCallID {
				msg.ToolCallID = normalized
			}
			transformed = append(transformed, msg)
		case agentcore.AssistantMessage:
			sameModel := msg.Provider == model.Provider && msg.Model == model.ID
			content := make(agentcore.ContentList, 0, len(msg.Content))
			for _, block := range msg.Content {
				switch b := block.(type) {
				case agentcore.ThinkingContent:
					if b.Redacted {
						if sameModel {
							content = append(content, b)
						}
						continue
					}
					if stringsTrim(b.Thinking) == "" {
						continue
					}
					if sameModel {
						content = append(content, b)
					} else {
						content = append(content, agentcore.NewTextContent(b.Thinking))
					}
				case agentcore.TextContent:
					content = append(content, b)
				case agentcore.ToolCallContent:
					tc := b
					if !sameModel && tc.ThoughtSignature != "" {
						tc.ThoughtSignature = ""
					}
					if !sameModel && normalizeToolCallID != nil {
						normalized := normalizeToolCallID(tc.ID, msg)
						if normalized != tc.ID {
							toolCallIDMap[tc.ID] = normalized
							tc.ID = normalized
						}
					}
					content = append(content, tc)
				default:
					content = append(content, block)
				}
			}
			msg.Content = content
			transformed = append(transformed, msg)
		default:
			transformed = append(transformed, m)
		}
	}

	out := make(agentcore.MessageList, 0, len(transformed))
	var pending []agentcore.ToolCallContent
	existing := map[string]bool{}
	flush := func() {
		if len(pending) == 0 {
			return
		}
		for _, tc := range pending {
			if !existing[tc.ID] {
				out = append(out, agentcore.ToolResultMessage{
					RoleField:  agentcore.RoleToolResult,
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Content:    agentcore.ContentList{agentcore.NewTextContent("No result provided")},
					IsError:    true,
				})
			}
		}
		pending = nil
		existing = map[string]bool{}
	}
	for _, m := range transformed {
		switch msg := m.(type) {
		case agentcore.AssistantMessage:
			flush()
			if msg.StopReason == agentcore.StopReasonError || msg.StopReason == agentcore.StopReasonAborted {
				continue
			}
			calls := msg.ToolCalls()
			if len(calls) > 0 {
				pending = calls
				existing = map[string]bool{}
			}
			out = append(out, msg)
		case agentcore.ToolResultMessage:
			existing[msg.ToolCallID] = true
			out = append(out, msg)
		case agentcore.UserMessage:
			flush()
			out = append(out, msg)
		default:
			out = append(out, msg)
		}
	}
	flush()
	return out
}

func downgradeUnsupportedImages(msgs agentcore.MessageList, model Model) agentcore.MessageList {
	if model.SupportsImages {
		return msgs
	}
	out := make(agentcore.MessageList, 0, len(msgs))
	for _, m := range msgs {
		switch msg := m.(type) {
		case agentcore.UserMessage:
			msg.Content = replaceImagesWithPlaceholder(msg.Content, "(image omitted: model does not support images)")
			out = append(out, msg)
		case agentcore.ToolResultMessage:
			msg.Content = replaceImagesWithPlaceholder(msg.Content, "(tool image omitted: model does not support images)")
			out = append(out, msg)
		default:
			out = append(out, m)
		}
	}
	return out
}

func replaceImagesWithPlaceholder(content agentcore.ContentList, placeholder string) agentcore.ContentList {
	out := make(agentcore.ContentList, 0, len(content))
	previous := false
	for _, block := range content {
		if _, ok := block.(agentcore.ImageContent); ok {
			if !previous {
				out = append(out, agentcore.NewTextContent(placeholder))
			}
			previous = true
			continue
		}
		out = append(out, block)
		if text, ok := block.(agentcore.TextContent); ok {
			previous = text.Text == placeholder
		} else {
			previous = false
		}
	}
	return out
}

func stringsTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// modelForRequest resolves the target model metadata for wire shaping. An
// unknown model is treated permissively (images are not downgraded).
func modelForRequest(providerName, model string, models []Model) Model {
	for _, m := range models {
		if m.ID == model {
			return m
		}
	}
	return Model{Provider: providerName, ID: model, SupportsImages: true}
}

// normalizeAnthropicToolCallID sanitizes tool call ids for the Anthropic wire
// (^[a-zA-Z0-9_-]+$, max 64).
func normalizeAnthropicToolCallID(id string, _ agentcore.AssistantMessage) string {
	return sanitizeToolCallID(id, 64)
}

// normalizeResponsesToolCallID sanitizes long Responses API ids for providers
// that reject special characters (kept up to 128 chars).
func normalizeResponsesToolCallID(id string, _ agentcore.AssistantMessage) string {
	return sanitizeToolCallID(id, 128)
}

func sanitizeToolCallID(id string, max int) string {
	if id == "" {
		return "tool-" + shortHash(id)
	}
	valid := true
	for _, r := range id {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			valid = false
			break
		}
	}
	if valid && len(id) <= max {
		return id
	}
	var b []byte
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b = append(b, byte(r))
		} else {
			b = append(b, '-')
		}
	}
	out := string(b)
	if len(out) > max {
		out = out[:max]
	}
	if out == "" {
		out = "tool-" + shortHash(id)
	}
	return out
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}
