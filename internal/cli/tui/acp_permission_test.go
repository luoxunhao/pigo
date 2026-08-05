package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/acp"
)

func TestFullModelPermissionFlow(t *testing.T) {
	permCh := make(chan tea.Msg, 8)
	m := Model{permissionCh: permCh}
	respond := make(chan any, 1)
	req := acp.Request{
		ID:     acp.NumID(1),
		Method: acp.MethodRequestPermission,
		Params: json.RawMessage(`{"sessionId":"s1","toolCall":{"title":"bash"}}`),
	}

	updated, cmd := m.Update(permissionRequestedMsg{req: req, respond: respond})
	if cmd == nil {
		t.Fatal("expected waitForPermission cmd")
	}
	m2 := updated.(Model)
	if m2.permission == nil || m2.permission.summary != "bash" {
		t.Fatalf("permission not pending: %+v", m2.permission)
	}
	if view := m2.permissionView(); !strings.Contains(view, "[permission]") {
		t.Fatalf("permission view = %q", view)
	}

	m3, _ := m2.handleKey(tea.KeyPressMsg(tea.Key{Text: "y"}))
	if m3.(Model).permission != nil {
		t.Fatal("permission not cleared after y")
	}
	select {
	case v := <-respond:
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != `{"optionId":"allow_once","outcome":"selected"}` {
			t.Fatalf("decision = %s", raw)
		}
	default:
		t.Fatal("no decision sent")
	}
}

func TestFullModelPermissionCancel(t *testing.T) {
	permCh := make(chan tea.Msg, 8)
	m := Model{permissionCh: permCh}
	respond := make(chan any, 1)
	req := acp.Request{
		ID:     acp.NumID(2),
		Method: acp.MethodRequestPermission,
		Params: json.RawMessage(`{"sessionId":"s1","toolCall":{"title":"write"}}`),
	}
	updated, _ := m.Update(permissionRequestedMsg{req: req, respond: respond})
	m2 := updated.(Model)
	m2.respondPermission("")
	if m2.permission != nil {
		t.Fatal("permission not cleared")
	}
	select {
	case v := <-respond:
		raw, _ := json.Marshal(v)
		if string(raw) != `{"outcome":"cancelled"}` {
			t.Fatalf("decision = %s", raw)
		}
	default:
		t.Fatal("no cancelled decision sent")
	}
}
