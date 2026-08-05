package tui

import (
	"encoding/json"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/acp"
)

// pendingPermission is an in-flight session/request_permission awaiting the
// user's decision in the full TUI.
type pendingPermission struct {
	req     acp.Request
	respond chan<- any
	summary string
}

// permissionRequestedMsg routes an ACP permission request into the tea loop.
type permissionRequestedMsg struct {
	req     acp.Request
	respond chan<- any
}

// waitForPermission returns a tea.Cmd that blocks until the next ACP
// permission request arrives. Update re-issues it after every permission msg.
func waitForPermission(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func permissionSummary(req acp.Request) string {
	var params struct {
		ToolCall struct {
			Title string `json:"title"`
			Kind  string `json:"kind"`
		} `json:"toolCall"`
	}
	_ = json.Unmarshal(req.Params, &params)
	if params.ToolCall.Title != "" {
		return params.ToolCall.Title
	}
	return "tool call"
}

// permissionView renders the permission decision line just above the input.
func (m Model) permissionView() string {
	if m.permission == nil {
		return ""
	}
	return fmt.Sprintf("[permission] %s  y=once a=always n=reject r=reject-always esc=cancel", m.permission.summary)
}

// respondPermission sends the ACP outcome for the pending permission request
// and clears the pending state. optionID "" maps to the cancelled outcome.
func (m *Model) respondPermission(optionID string) {
	if m.permission == nil || m.permission.respond == nil {
		return
	}
	if optionID == "" {
		m.permission.respond <- map[string]any{"outcome": "cancelled"}
	} else {
		m.permission.respond <- map[string]any{"outcome": "selected", "optionId": optionID}
	}
	m.permission = nil
}
