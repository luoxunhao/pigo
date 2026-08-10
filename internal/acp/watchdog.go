package acp

import (
	"os"
	"strconv"
	"time"
)

// defaultTurnIdleTimeout is the idle watchdog window for a running ACP turn:
// if no AgentEvent or tool-output heartbeat arrives within this window, the
// turn is force-released so queued prompts cannot block forever.
const defaultTurnIdleTimeout = 5 * time.Minute

// watchdogGrace is how long the watchdog waits for a canceled run to unwind
// before force-releasing the turn slot. Waiting avoids starting the next queued
// prompt while the previous run is still persisting the same session.
const watchdogGrace = 5 * time.Second

// turnIdleTimeout resolves the watchdog window from PIGO_TURN_IDLE_TIMEOUT.
// The value may be a Go duration ("90s", "5m") or plain seconds ("300");
// unset, empty, or invalid values fall back to the default.
func turnIdleTimeout() time.Duration {
	if v := os.Getenv("PIGO_TURN_IDLE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultTurnIdleTimeout
}
