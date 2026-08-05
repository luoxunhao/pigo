package acp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/trust"
)

type mockTransport struct {
	mu         sync.Mutex
	raw        json.RawMessage
	err        error
	calls      int
	lastParams json.RawMessage
	block      chan struct{}
}

func (m *mockTransport) SendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	m.mu.Lock()
	m.calls++
	m.lastParams, _ = json.Marshal(params)
	raw, err := m.raw, m.err
	m.mu.Unlock()
	if m.block != nil {
		select {
		case <-m.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (m *mockTransport) SendNotification(string, any) error { return nil }
func (m *mockTransport) Recv(context.Context) (IncomingMessage, error) {
	return IncomingMessage{}, ErrClosed
}
func (m *mockTransport) SendResponse(context.Context, RequestID, any, *Error) error { return nil }
func (m *mockTransport) Close() error                                               { return nil }

func newPermissionTest(t *testing.T) (*mockTransport, *trust.Manager, string) {
	t.Helper()
	m := &mockTransport{}
	mgr, err := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "project")
	return m, mgr, cwd
}

func bashCall() agentcore.AgentToolCall {
	return agentcore.AgentToolCall{
		ID:        "call-1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"echo hi"}`),
	}
}

func respond(m *mockTransport, optionID string) {
	m.mu.Lock()
	m.raw = json.RawMessage(`{"outcome":{"outcome":"selected","optionId":"` + optionID + `"}}`)
	m.mu.Unlock()
}

func TestPermissionAllowOnce(t *testing.T) {
	m, mgr, cwd := newPermissionTest(t)
	respond(m, "allow_once")
	broker := NewACPPermissionBroker(m, mgr, cwd, 0)
	dec := broker.BeforeToolCall("s1")(context.Background(), bashCall())
	if dec != nil {
		t.Fatalf("allow once blocked: %+v", dec)
	}
	res := mgr.NearestTrustDecision(cwd)
	if res.Found {
		t.Fatalf("allow once persisted trust: %+v", res)
	}
}

func TestPermissionAllowAlways(t *testing.T) {
	m, mgr, cwd := newPermissionTest(t)
	respond(m, "allow_always")
	broker := NewACPPermissionBroker(m, mgr, cwd, 0)
	hook := broker.BeforeToolCall("s1")
	if dec := hook(context.Background(), bashCall()); dec != nil {
		t.Fatalf("allow always blocked: %+v", dec)
	}
	if !mgr.IsTrusted(cwd) {
		t.Fatal("allow always did not grant session trust")
	}
	if dec := hook(context.Background(), bashCall()); dec != nil {
		t.Fatalf("second call after session trust still blocked: %+v", dec)
	}
	m.mu.Lock()
	calls := m.calls
	m.mu.Unlock()
	if calls != 1 {
		t.Fatalf("permission requests = %d, want 1", calls)
	}
}

func TestPermissionRejectOnce(t *testing.T) {
	m, mgr, cwd := newPermissionTest(t)
	respond(m, "reject_once")
	broker := NewACPPermissionBroker(m, mgr, cwd, 0)
	dec := broker.BeforeToolCall("s1")(context.Background(), bashCall())
	if dec == nil || !dec.Block {
		t.Fatalf("reject once did not block: %+v", dec)
	}
	if res := mgr.NearestTrustDecision(cwd); res.Found {
		t.Fatalf("reject once persisted: %+v", res)
	}
}

func TestPermissionRejectAlways(t *testing.T) {
	m, mgr, cwd := newPermissionTest(t)
	respond(m, "reject_always")
	broker := NewACPPermissionBroker(m, mgr, cwd, 0)
	dec := broker.BeforeToolCall("s1")(context.Background(), bashCall())
	if dec == nil || !dec.Block {
		t.Fatalf("reject always did not block: %+v", dec)
	}
	res := mgr.NearestTrustDecision(cwd)
	if !res.Found || res.Decision != trust.Untrusted {
		t.Fatalf("reject always trust state = %+v", res)
	}
}

func TestPermissionCancelledRejects(t *testing.T) {
	m, mgr, cwd := newPermissionTest(t)
	m.mu.Lock()
	m.raw = json.RawMessage(`{"outcome":{"outcome":"cancelled"}}`)
	m.mu.Unlock()
	broker := NewACPPermissionBroker(m, mgr, cwd, 0)
	dec := broker.BeforeToolCall("s1")(context.Background(), bashCall())
	if dec == nil || !dec.Block {
		t.Fatalf("cancelled did not block: %+v", dec)
	}
}

func TestPermissionTimeoutRejects(t *testing.T) {
	m, mgr, cwd := newPermissionTest(t)
	m.block = make(chan struct{})
	broker := NewACPPermissionBroker(m, mgr, cwd, 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dec := broker.BeforeToolCall("s1")(ctx, bashCall())
	if dec == nil || !dec.Block {
		t.Fatalf("timeout did not block: %+v", dec)
	}
}

func TestPermissionSkipsTrustedAndReadOnlyTools(t *testing.T) {
	m, mgr, cwd := newPermissionTest(t)
	broker := NewACPPermissionBroker(m, mgr, cwd, 0)
	hook := broker.BeforeToolCall("s1")
	if dec := hook(context.Background(), agentcore.AgentToolCall{ID: "c", Name: "read"}); dec != nil {
		t.Fatalf("read tool blocked: %+v", dec)
	}
	if err := mgr.SetDecision(cwd, trust.Trusted); err != nil {
		t.Fatal(err)
	}
	if dec := hook(context.Background(), bashCall()); dec != nil {
		t.Fatalf("trusted dir blocked: %+v", dec)
	}
	m.mu.Lock()
	calls := m.calls
	m.mu.Unlock()
	if calls != 0 {
		t.Fatalf("permission requests = %d, want 0", calls)
	}
}

func TestPermissionRequestParamsShape(t *testing.T) {
	m, mgr, cwd := newPermissionTest(t)
	respond(m, "allow_once")
	broker := NewACPPermissionBroker(m, mgr, cwd, 0)
	_ = broker.BeforeToolCall("s1")(context.Background(), bashCall())
	m.mu.Lock()
	raw := append(json.RawMessage{}, m.lastParams...)
	m.mu.Unlock()
	var params struct {
		SessionID string `json:"sessionId"`
		ToolCall  struct {
			ToolCallID string `json:"toolCallId"`
			Status     string `json:"status"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	if params.SessionID != "s1" || params.ToolCall.ToolCallID != "call-1" || params.ToolCall.Status != "pending" {
		t.Fatalf("params = %s", raw)
	}
	if len(params.Options) != 4 || params.Options[0].Kind != "allow_once" || params.Options[3].Kind != "reject_always" {
		t.Fatalf("options = %+v", params.Options)
	}
}

func TestPermissionRemoteConfirm(t *testing.T) {
	m, mgr, cwd := newPermissionTest(t)
	broker := NewACPPermissionBroker(m, mgr, cwd, 0)
	broker.SetRemoteConfirm(func(tool, summary string) (allow, always bool, ok bool) {
		return true, true, true
	})
	hook := broker.BeforeToolCall("s1")
	if dec := hook(context.Background(), bashCall()); dec != nil {
		t.Fatalf("remote approved but blocked: %+v", dec)
	}
	if !mgr.IsTrusted(cwd) {
		t.Fatal("remote always did not grant session trust")
	}
	m.mu.Lock()
	calls := m.calls
	m.mu.Unlock()
	if calls != 0 {
		t.Fatalf("transport consulted with remote confirm: %d calls", calls)
	}

	broker.SetRemoteConfirm(func(tool, summary string) (allow, always bool, ok bool) {
		return false, false, true
	})
	mgr.ClearSessionTrust(cwd)
	if dec := hook(context.Background(), bashCall()); dec == nil || !dec.Block {
		t.Fatalf("remote rejection did not block: %+v", dec)
	}
}
