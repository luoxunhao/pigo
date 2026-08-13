// This file retains the sub-agent concurrency and progress helpers used by
// plugin-declared sub-agent tools. The built-in generic `task` tool has been
// removed; sub-agents are registered declaratively by plugins.
package runtime

import (
	"os"
	"strconv"
	"strings"

	"github.com/smallnest/pigo/internal/agentcore"
)

// DefaultMaxSubagents is the concurrency cap applied when PIGO_MAX_SUBAGENTS is
// unset or invalid. It bounds how many task sub-agents run at once so a fan-out
// cannot overwhelm the provider rate limit.
const DefaultMaxSubagents = 4

// MaxSubagents resolves the concurrency cap for task sub-agents from
// PIGO_MAX_SUBAGENTS: absent or unparseable yields DefaultMaxSubagents (4), and
// a parsed value below 1 is floored to 1 so the semaphore always admits at least
// one runner.
func MaxSubagents() int {
	v := strings.TrimSpace(os.Getenv("PIGO_MAX_SUBAGENTS"))
	if v == "" {
		return DefaultMaxSubagents
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return DefaultMaxSubagents
	}
	if n < 1 {
		return 1
	}
	return n
}

// NewSubagentSemaphore builds the shared concurrency semaphore for task
// sub-agents, sized by MaxSubagents. One instance per run is created and shared
// across every task call so the cap is enforced run-wide.
func NewSubagentSemaphore() chan struct{} {
	return make(chan struct{}, MaxSubagents())
}

// activityOf maps a child sub-agent event to the display verb surfaced in a
// SubAgentProgressEvent (D-8: tool name / phase, no argument summary). A child
// ToolExecutionStartEvent maps by tool name; a TurnStartEvent (a fresh turn with
// no tool in progress) maps to "Thinking". Every other event maps to "" so the
// caller emits nothing — progress is reported only at these activity boundaries,
// keeping event volume proportional to the child's tool calls rather than its
// text deltas (D-7).
func activityOf(ev agentcore.AgentEvent) string {
	switch e := ev.(type) {
	case agentcore.ToolExecutionStartEvent:
		switch e.ToolName {
		case "read":
			return "Reading"
		case "edit", "write":
			return "Editing"
		case "bash":
			return "Running bash"
		case "grep", "find", "ls":
			return "Searching"
		case "webfetch":
			return "Fetching"
		default:
			return ""
		}
	case agentcore.TurnStartEvent:
		return "Thinking"
	default:
		return ""
	}
}

// estimateTokens gives a coarse output-token estimate from a running character
// count of the child's streamed text (~4 chars per token). It rides along on
// each progress event as a rough "↓ tokens" figure; 0 means unknown (no text
// streamed yet).
func estimateTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return chars / 4
}
