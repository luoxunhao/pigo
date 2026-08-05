package acp

import (
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/session"
)

// entryToACPMessage maps a persisted transcript entry to the ACP message shape
// pi-web consumes: stable id/parentId/timestamp plus role-discriminated
// content blocks.
func entryToACPMessage(e session.Entry) map[string]any {
	msg := map[string]any{
		"id":        e.ID,
		"parentId":  e.ParentID,
		"role":      e.Message.Role(),
		"timestamp": e.Timestamp,
	}
	switch m := e.Message.(type) {
	case agentcore.UserMessage:
		msg["content"] = contentBlocksToACP(m.Content)
	case agentcore.AssistantMessage:
		msg["content"] = contentBlocksToACP(m.Content)
		if m.Model != "" {
			msg["model"] = m.Model
		}
		if m.StopReason != "" {
			msg["stopReason"] = m.StopReason
		}
	case agentcore.ToolResultMessage:
		msg["toolCallId"] = m.ToolCallID
		msg["toolName"] = m.ToolName
		msg["isError"] = m.IsError
		msg["content"] = contentBlocksToACP(m.Content)
	case agentcore.CompactionMessage:
		msg["content"] = []any{
			map[string]any{"type": "text", "text": m.Summary},
		}
	}
	return msg
}

// contentBlocksToACP converts agentcore content blocks into the ACP wire
// blocks pi-web renders.
func contentBlocksToACP(list agentcore.ContentList) []any {
	blocks := make([]any, 0, len(list))
	for _, c := range list {
		switch b := c.(type) {
		case agentcore.TextContent:
			blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
		case agentcore.ThinkingContent:
			blocks = append(blocks, map[string]any{"type": "thinking", "thinking": b.Thinking})
		case agentcore.ToolCallContent:
			blocks = append(blocks, map[string]any{
				"type": "toolCall", "id": b.ID, "name": b.Name, "arguments": b.Arguments,
			})
		case agentcore.ImageContent:
			blocks = append(blocks, map[string]any{
				"type": "image", "data": b.Data, "mimeType": b.MimeType,
			})
		}
	}
	return blocks
}
