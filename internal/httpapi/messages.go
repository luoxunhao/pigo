package httpapi

import (
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/session"
)

func entryToDomainMessage(e session.Entry) gen.Message {
	msg := gen.Message{
		Id:        e.ID,
		Role:      e.Message.Role(),
		Timestamp: e.Timestamp.Format(time.RFC3339),
		Content:   []map[string]interface{}{},
	}
	switch m := e.Message.(type) {
	case agentcore.UserMessage:
		msg.Content = contentBlocksToDomain(m.Content)
	case agentcore.AssistantMessage:
		msg.Content = contentBlocksToDomain(m.Content)
		if m.Model != "" {
			msg.Model = &m.Model
		}
	case agentcore.ToolResultMessage:
		msg.Content = contentBlocksToDomain(m.Content)
	case agentcore.CompactionMessage:
		msg.Content = []map[string]interface{}{{"type": "text", "text": m.Summary}}
	}
	return msg
}

func contentBlocksToDomain(content agentcore.ContentList) []map[string]interface{} {
	blocks := make([]map[string]interface{}, 0, len(content))
	for _, c := range content {
		switch b := c.(type) {
		case agentcore.TextContent:
			blocks = append(blocks, map[string]interface{}{"type": "text", "text": b.Text})
		case agentcore.ThinkingContent:
			blocks = append(blocks, map[string]interface{}{"type": "thinking", "thinking": b.Thinking})
		case agentcore.ToolCallContent:
			blocks = append(blocks, map[string]interface{}{
				"type": "toolCall", "id": b.ID, "name": b.Name, "arguments": b.Arguments,
			})
		case agentcore.ImageContent:
			blocks = append(blocks, map[string]interface{}{
				"type": "image", "data": b.Data, "mimeType": b.MimeType,
			})
		}
	}
	return blocks
}
