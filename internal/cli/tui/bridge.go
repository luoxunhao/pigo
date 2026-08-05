package tui

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"
)

// This file bridges the agent run seam (runtime.StartRun + runtime.DrainStream)
// to Bubble Tea (US-004, SPEC 5.1 bridge / 3.2). The agent loop runs on its own
// goroutine and emits AgentEvents; a Bubble Tea program consumes tea.Msg values
// one at a time from its Update loop. The bridge is a pump: a goroutine drains
// the run and converts every event into the matching tea.Msg (see msgs.go),
// sending it into a buffered channel; a tea.Cmd (waitForEvent) receives one msg
// per Update tick. The channel is the only synchronization point, so the
// producer never touches the model and the model never touches the run 鈥?all
// state transitions happen on the tea goroutine.
//
// Back-pressure is intentional: the channel blocks the draining goroutine when
// the buffer is full, so no event is ever dropped (the tea loop always catches
// up). Node #388 wires startRun into Model.Init/Update; this file only provides
// the reusable, unit-testable primitives.

// eventChanCap is the buffer size of the bridge channel. A modest buffer lets a
// burst of tool events queue without blocking the run's goroutine on every send,
// while still bounding memory (blocking, never dropping, past the cap).
const eventChanCap = 64

// newEventChan allocates the buffered channel the bridge pumps run events
// through.
func newEventChan() chan tea.Msg {
	return make(chan tea.Msg, eventChanCap)
}

// argsToMap coerces a tool call's untyped Args into a map[string]any. The event
// layer carries Args as an untyped any: the tool executor emits it as a
// json.RawMessage (the raw decoded JSON arguments), but a caller may also hand
// an already-decoded map. Both are supported here so the tool card can show the
// call's arguments; anything that is not a JSON object yields nil.
func argsToMap(args any) map[string]any {
	switch v := args.(type) {
	case map[string]any:
		return v
	case json.RawMessage:
		return unmarshalArgsMap(v)
	case []byte:
		return unmarshalArgsMap(v)
	case string:
		return unmarshalArgsMap([]byte(v))
	}
	return nil
}

// unmarshalArgsMap parses JSON object bytes into a map, returning nil for empty
// input or anything that is not a JSON object.
func unmarshalArgsMap(b []byte) map[string]any {
	if len(b) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// waitForEvent returns a tea.Cmd that blocks until the next bridge msg arrives.
// The Update loop re-issues it after handling each msg (except runEndMsg) to
// keep pulling events one at a time, so ordering is preserved and the tea
// goroutine never spins.
func waitForEvent(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}
