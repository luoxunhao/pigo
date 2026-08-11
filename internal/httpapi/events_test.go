package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/smallnest/pigo/internal/httpapi/gen"
)

func TestEventBrokerReplayAndLive(t *testing.T) {
	broker := NewEventBroker()
	broker.Publish("session.status", map[string]any{"sessionId": "a"})
	broker.Publish("message.part.delta", map[string]any{"sessionId": "a"})

	ch, err := broker.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Unsubscribe(ch)
	first := <-ch
	second := <-ch
	if first.ID != 1 || second.ID != 2 {
		t.Fatalf("ids = %d, %d", first.ID, second.ID)
	}

	broker.Publish("tool.updated", map[string]any{"sessionId": "a"})
	third := <-ch
	if third.Type != "tool.updated" {
		t.Fatalf("third type = %q", third.Type)
	}
}

func TestEventBrokerAfterCursor(t *testing.T) {
	broker := NewEventBroker()
	broker.Publish("session.status", map[string]any{})
	ch, err := broker.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Unsubscribe(ch)
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev)
	default:
	}
}

func TestSSEEndpointStreamsEvent(t *testing.T) {
	srv, err := NewServer(Config{})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler := gen.HandlerFromMux(srv, router)
	srv.events.Publish("session.status", map[string]any{"sessionId": "abc"})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	var buf strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		buf.WriteString(line)
		if line == "\n" {
			break
		}
	}
	if !strings.Contains(buf.String(), "event: session.status") {
		t.Fatalf("event stream = %q", buf.String())
	}
	cancel()
}
