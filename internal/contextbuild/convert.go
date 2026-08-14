package contextbuild

import (
	"github.com/smallnest/pigo/internal/agentcore"
)

const (
	nonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
	nonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"
)

// ConvertOptions controls provider-independent conversion.
type ConvertOptions struct {
	BlockImages bool
}

// ConvertToLlm performs the provider-independent role conversion: compaction,
// branch-summary, and custom messages become user messages; standard messages
// pass through.
func ConvertToLlm(msgs agentcore.MessageList) agentcore.MessageList {
	return ConvertToLlmWithOptions(msgs, ConvertOptions{})
}

// ConvertToLlmWithOptions converts and optionally blocks images.
func ConvertToLlmWithOptions(msgs agentcore.MessageList, opts ConvertOptions) agentcore.MessageList {
	out := make(agentcore.MessageList, 0, len(msgs))
	for _, m := range msgs {
		switch msg := m.(type) {
		case agentcore.CompactionMessage:
			out = append(out, msg.AsUserMessage())
		case agentcore.BranchSummaryMessage:
			out = append(out, msg.AsUserMessage())
		case agentcore.CustomMessage:
			out = append(out, agentcore.UserMessage{
				RoleField: agentcore.RoleUser,
				Content:   msg.Content,
				Timestamp: msg.Timestamp,
			})
		case agentcore.AssistantMessage:
			if msg.StopReason == agentcore.StopReasonError || msg.StopReason == agentcore.StopReasonAborted {
				continue
			}
			out = append(out, msg)
		default:
			out = append(out, m)
		}
	}
	if opts.BlockImages {
		out = blockImages(out)
	}
	return out
}

func blockImages(msgs agentcore.MessageList) agentcore.MessageList {
	out := make(agentcore.MessageList, 0, len(msgs))
	for _, m := range msgs {
		switch msg := m.(type) {
		case agentcore.UserMessage:
			msg.Content = replaceImagesWithPlaceholder(msg.Content, nonVisionUserImagePlaceholder)
			out = append(out, msg)
		case agentcore.ToolResultMessage:
			msg.Content = replaceImagesWithPlaceholder(msg.Content, nonVisionToolImagePlaceholder)
			out = append(out, msg)
		default:
			out = append(out, m)
		}
	}
	return out
}

func replaceImagesWithPlaceholder(content agentcore.ContentList, placeholder string) agentcore.ContentList {
	out := make(agentcore.ContentList, 0, len(content))
	previousWasPlaceholder := false
	for _, block := range content {
		if _, ok := block.(agentcore.ImageContent); ok {
			if !previousWasPlaceholder {
				out = append(out, agentcore.NewTextContent(placeholder))
			}
			previousWasPlaceholder = true
			continue
		}
		out = append(out, block)
		if text, ok := block.(agentcore.TextContent); ok {
			previousWasPlaceholder = text.Text == placeholder
		} else {
			previousWasPlaceholder = false
		}
	}
	return out
}
