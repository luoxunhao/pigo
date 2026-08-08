package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/trust"
)

// permissionDecision is the decision extracted from a permission response.
type permissionDecision int

const (
	permissionReject permissionDecision = iota
	permissionAllowOnce
	permissionAllowAlways
	permissionRejectAlways
)

// ACPPermissionBroker bridges pigo's trust gating to ACP request_permission.
// Side-effect tools in an untrusted directory trigger a permission request
// with the four standard options; the user's choice is mapped back onto the
// trust manager with the same semantics as the CLI confirmation prompt.
type ACPPermissionBroker struct {
	transport Transport
	trust     *trust.Manager
	cwd       string
	timeout   time.Duration // 0 = wait forever
	// remoteConfirm, when set, lets a paired remote browser answer the
	// permission request before the ACP client is consulted.
	remoteConfirm func(tool, summary string) (allow, always bool, ok bool)
}

// NewACPPermissionBroker builds a broker. mgr may be nil to disable gating.
func NewACPPermissionBroker(transport Transport, mgr *trust.Manager, cwd string, timeout time.Duration) *ACPPermissionBroker {
	return &ACPPermissionBroker{transport: transport, trust: mgr, cwd: cwd, timeout: timeout}
}

// BeforeToolCall returns the per-session tool gating hook for cwd. Sessions
// may target different workspaces, so trust and permission decisions are made
// against the session's own directory rather than the server startup cwd.
func (b *ACPPermissionBroker) BeforeToolCall(sessionID, cwd string) agentcore.BeforeToolCallFunc {
	if b.trust == nil {
		return nil
	}
	if cwd == "" {
		cwd = b.cwd
	}
	return func(ctx context.Context, call agentcore.AgentToolCall) *agentcore.BeforeToolCallDecision {
		if !trust.SideEffectTools[call.Name] {
			return nil
		}
		if b.trust.IsTrusted(cwd) {
			return nil
		}
		if b.remoteConfirm != nil {
			if allow, always, ok := b.remoteConfirm(call.Name, toolCallSummary(call)); ok {
				if !allow {
					return blockDecision(fmt.Sprintf("tool %q blocked by remote decision", call.Name))
				}
				if always {
					if err := b.trust.SetDecision(cwd, trust.Trusted); err != nil {
						return blockDecision(fmt.Sprintf("tool %q blocked: persist trust failed: %v", call.Name, err))
					}
				}
				return nil
			}
		}
		decision, err := b.request(ctx, sessionID, call)
		if err != nil {
			return blockDecision(fmt.Sprintf("tool %q blocked: permission request failed: %v", call.Name, err))
		}
		switch decision {
		case permissionAllowOnce:
			return nil
		case permissionAllowAlways:
			if err := b.trust.SetDecision(cwd, trust.Trusted); err != nil {
				return blockDecision(fmt.Sprintf("tool %q blocked: persist trust failed: %v", call.Name, err))
			}
			return nil
		case permissionRejectAlways:
			_ = b.trust.SetDecision(cwd, trust.Untrusted)
			return blockDecision(fmt.Sprintf("tool %q blocked: %s is untrusted", call.Name, cwd))
		default:
			return blockDecision(fmt.Sprintf("tool %q blocked: %s is not trusted", call.Name, cwd))
		}
	}
}

// TrustManager exposes the underlying trust manager for slash commands.
func (b *ACPPermissionBroker) TrustManager() *trust.Manager { return b.trust }

// SetRemoteConfirm installs an optional remote approval path.
func (b *ACPPermissionBroker) SetRemoteConfirm(f func(tool, summary string) (allow, always bool, ok bool)) {
	b.remoteConfirm = f
}

// toolCallSummary renders a one-line preview of a tool call.
func toolCallSummary(call agentcore.AgentToolCall) string {
	var in struct {
		Command string `json:"command"`
		Path    string `json:"path"`
	}
	_ = json.Unmarshal(call.Arguments, &in)
	if in.Command != "" {
		return "command: " + in.Command
	}
	if in.Path != "" {
		return "path: " + in.Path
	}
	return string(call.Arguments)
}

func (b *ACPPermissionBroker) request(ctx context.Context, sessionID string, call agentcore.AgentToolCall) (permissionDecision, error) {
	options := []map[string]any{
		{"optionId": "allow_once", "name": "Allow once", "kind": "allow_once"},
		{"optionId": "allow_always", "name": "Always allow", "kind": "allow_always"},
		{"optionId": "reject_once", "name": "Reject once", "kind": "reject_once"},
		{"optionId": "reject_always", "name": "Always reject", "kind": "reject_always"},
	}
	params := map[string]any{
		"sessionId": sessionID,
		"toolCall": map[string]any{
			"toolCallId": call.ID,
			"title":      call.Name,
			"kind":       inferToolKind(call.Name),
			"status":     "pending",
			"rawInput":   json.RawMessage(call.Arguments),
		},
		"options": options,
	}

	var raw json.RawMessage
	var err error
	if b.timeout > 0 {
		reqCtx, cancel := context.WithTimeout(ctx, b.timeout)
		defer cancel()
		raw, err = b.transport.SendRequest(reqCtx, MethodRequestPermission, params)
	} else {
		raw, err = b.transport.SendRequest(ctx, MethodRequestPermission, params)
	}
	if err != nil {
		return permissionReject, err
	}

	var resp struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return permissionReject, fmt.Errorf("parse permission response: %w", err)
	}
	if resp.Outcome.Outcome != "selected" {
		return permissionReject, nil
	}
	switch resp.Outcome.OptionID {
	case "allow_once":
		return permissionAllowOnce, nil
	case "allow_always":
		return permissionAllowAlways, nil
	case "reject_once":
		return permissionReject, nil
	case "reject_always":
		return permissionRejectAlways, nil
	default:
		return permissionReject, nil
	}
}

func blockDecision(reason string) *agentcore.BeforeToolCallDecision {
	content := agentcore.ContentList{agentcore.NewTextContent(reason)}
	return &agentcore.BeforeToolCallDecision{Block: true, Content: &content}
}
