package acp

import (
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
)

// CompactConfig carries the provider settings needed to run /compact through
// the ACP server.
type CompactConfig struct {
	Provider      provider.Provider
	ProviderName  string
	Model         string
	APIKey        string
	ContextWindow int
	ThinkingLevel agentcore.ThinkingLevel
	Tools         []agentcore.AgentTool
}
