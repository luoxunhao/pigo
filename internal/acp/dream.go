package acp

import "github.com/smallnest/pigo/internal/agentcore"

// DreamConfig carries the provider settings needed to build a dream
// consolidator when the /dream command runs through ACP.
type DreamConfig struct {
	Model         string
	BaseURL       string
	Protocol      string
	ProviderName  string
	APIKey        string
	ThinkingLevel agentcore.ThinkingLevel
}
