package httpapi

import (
	"testing"
	"time"
)

func TestPermissionManagerAskAndReply(t *testing.T) {
	broker := NewEventBroker()
	mgr := NewPermissionManager(broker)
	type result struct {
		option string
		err    *APIError
	}
	done := make(chan result, 1)
	go func() {
		option, err := mgr.Ask("s1", map[string]any{"title": "bash"}, []map[string]any{{"optionId": "allow_once"}})
		done <- result{option: option, err: err}
	}()
	time.Sleep(20 * time.Millisecond)
	if apiErr := mgr.Reply("s1", "perm-unknown", "allow_once"); apiErr == nil {
		t.Fatal("expected unknown permission error")
	}
	// Find the real permission id from the broker event.
	ch, err := broker.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	ev := <-ch
	permissionID := ev.Data["permissionId"].(string)
	if apiErr := mgr.Reply("s1", permissionID, "allow_once"); apiErr != nil {
		t.Fatal(apiErr)
	}
	select {
	case res := <-done:
		if res.err != nil || res.option != "allow_once" {
			t.Fatalf("result = %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for permission reply")
	}
}
