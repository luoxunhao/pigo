package acp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/httpclient"
)

// TestSessionNewDefersCommandsUntilAfterResponse locks the ordering contract
// that some ACP clients (notably Zed) rely on: a notification for a brand-new
// session must not be sent before the session/new response has been delivered.
func TestSessionNewDefersCommandsUntilAfterResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessionId":     "s1",
				"directory":     `E:\ws`,
				"configOptions": []any{},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/commands":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"commands": []map[string]any{{"name": "ping", "description": "ping"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client, err := httpclient.NewClientWithResponses(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := NewChannelPair()
	adapter := NewHTTPAdapter(client, serverTransport, "test")

	ctx := context.Background()
	if _, apiErr := adapter.sessionNew(ctx, json.RawMessage(`{"cwd":"E:\\ws"}`)); apiErr != nil {
		t.Fatal(apiErr)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if msg, err := clientTransport.Recv(waitCtx); err == nil {
		t.Fatalf("notification sent before session/new response: %+v", msg.Notification)
	}

	adapter.AfterResponse(MethodSessionNew)
	msg, err := clientTransport.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
		t.Fatalf("expected session/update notification, got %+v", msg)
	}
	var payload struct {
		Update map[string]any `json:"update"`
	}
	if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Update["sessionUpdate"] != "available_commands_update" {
		t.Fatalf("update = %+v, want available_commands_update", payload.Update)
	}
}
