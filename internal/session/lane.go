package session

// LaneConfig is the authoritative per-lane runtime configuration. It replaces
// the old entry-derived model/thinking/active-tools state: entries remain
// history, while the current state lives in lane.config (mirrors pi's
// lane.config register). ActiveToolNames == nil means "all tools".
type LaneConfig struct {
	Model          string   `json:"model,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	ThinkingLevel  string   `json:"thinkingLevel,omitempty"`
	ActiveToolNames []string `json:"activeToolNames,omitempty"`
}
