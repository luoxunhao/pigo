package acp

import (
	"context"
	"strings"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/remotecontrol"
)

// RemoteBridge adapts the existing remote-control HTTP/WS server to the ACP
// layer (D-04: the server lives on the agent side). Remote input becomes
// session prompts and remote confirmations answer permission requests through
// the broker, so a paired browser behaves like a second ACP client.
type RemoteBridge struct {
	server *remotecontrol.Server
	bridge *remotecontrol.Bridge
}

// NewRemoteBridge builds a bridge bound to host/port (""/0 auto).
func NewRemoteBridge(host string, port int, onConnect func(remoteAddr string), onDisconnect func()) (*RemoteBridge, error) {
	server := remotecontrol.NewServer(remotecontrol.Config{
		Host:               host,
		Port:               port,
		OnClientConnect:    onConnect,
		OnClientDisconnect: onDisconnect,
	}, nil)
	bridge := remotecontrol.NewBridge(server)
	server.SetHandler(bridge)
	return &RemoteBridge{server: server, bridge: bridge}, nil
}

// Start begins serving and returns the pairing URL.
func (r *RemoteBridge) Start() (string, error) { return r.server.Start() }

// Stop shuts the server down.
func (r *RemoteBridge) Stop(ctx context.Context) error { return r.server.Stop(ctx) }

// Enabled reports whether a browser is paired and connected.
func (r *RemoteBridge) Enabled() bool { return r.bridge.Enabled() }

// RemoteInput exposes remote-submitted prompts.
func (r *RemoteBridge) RemoteInput() <-chan string { return r.bridge.RemoteInput() }

// Confirm asks the remote client to approve a risky tool call.
func (r *RemoteBridge) Confirm(ctx context.Context, tool, summary string) (remotecontrol.Decision, bool) {
	return r.bridge.Confirm(ctx, tool, summary)
}

// SendOutput streams session text to the paired browser.
func (r *RemoteBridge) SendOutput(text string) { r.server.SendOutput(text) }

// SendEvent forwards a compact, non-duplicating line for observable events.
func (r *RemoteBridge) SendEvent(ev agentcore.AgentEvent) {
	var text string
	switch e := ev.(type) {
	case agentcore.ToolExecutionStartEvent:
		text = "▶ " + e.ToolName
	case agentcore.ToolExecutionEndEvent:
		text = "✓ " + e.ToolName
	case agentcore.CompactionEvent:
		text = "· context compacted"
	case agentcore.TurnEndEvent:
		if r := e.Message.StopReason; r != "" {
			text = "· turn " + r
		}
	}
	if strings.TrimSpace(text) != "" {
		r.SendOutput(text + "\n")
	}
}
