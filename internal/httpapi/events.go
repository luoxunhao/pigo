package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/httpapi/gen"
)

const (
	eventRetentionCount = 10000
	eventRetentionAge   = 24 * time.Hour
	eventSubBuffer      = 12000
)

// ErrEventCursorGone reports that the requested event cursor is no longer retained.
var ErrEventCursorGone = errors.New("event cursor gone")

// DomainEvent is one persisted event in the SSE stream.
type DomainEvent struct {
	ID   int64          `json:"id"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
	Time time.Time      `json:"time"`
}

// EventBroker retains a bounded event history and fans events out to subscribers.
type EventBroker struct {
	mu     sync.Mutex
	events []DomainEvent
	nextID int64
	subs   map[chan DomainEvent]struct{}
}

// NewEventBroker builds an empty broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{subs: make(map[chan DomainEvent]struct{})}
}

// Publish appends an event and broadcasts it to live subscribers.
func (b *EventBroker) Publish(eventType string, data map[string]any) DomainEvent {
	b.mu.Lock()
	b.nextID++
	ev := DomainEvent{ID: b.nextID, Type: eventType, Data: data, Time: time.Now().UTC()}
	b.events = append(b.events, ev)
	cutoff := time.Now().UTC().Add(-eventRetentionAge)
	for len(b.events) > eventRetentionCount || (len(b.events) > 0 && b.events[0].Time.Before(cutoff)) {
		b.events = b.events[1:]
	}
	subs := make([]chan DomainEvent, 0, len(b.subs))
	for ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
	return ev
}

// Subscribe returns a channel that replays events after the cursor, then receives live events.
func (b *EventBroker) Subscribe(after int64) (chan DomainEvent, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if after > 0 {
		if len(b.events) == 0 || after < b.events[0].ID {
			return nil, ErrEventCursorGone
		}
	}
	ch := make(chan DomainEvent, eventSubBuffer)
	b.subs[ch] = struct{}{}
	replay := make([]DomainEvent, 0, len(b.events))
	for _, ev := range b.events {
		if ev.ID > after {
			replay = append(replay, ev)
		}
	}
	go func() {
		for _, ev := range replay {
			ch <- ev
		}
	}()
	return ch, nil
}

// Unsubscribe removes a subscriber channel.
func (b *EventBroker) Unsubscribe(ch chan DomainEvent) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// GetEvents implements GET /api/v1/events.
func (s *Server) GetEvents(w http.ResponseWriter, r *http.Request, params gen.GetEventsParams) {
	var after int64
	if params.After != nil {
		after = int64(*params.After)
	}
	ch, err := s.events.Subscribe(after)
	if err != nil {
		if errors.Is(err, ErrEventCursorGone) {
			WriteError(w, r, &APIError{Status: http.StatusGone, Code: CodeEventCursorGone, Message: "event cursor is no longer retained"})
			return
		}
		WriteError(w, r, Internal(err.Error()))
		return
	}
	defer s.events.Unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, r, Internal("streaming unsupported"))
		return
	}
	w.WriteHeader(http.StatusOK)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !eventMatches(ev, params) {
				continue
			}
			data, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, data)
			flusher.Flush()
		}
	}
}

func eventMatches(ev DomainEvent, params gen.GetEventsParams) bool {
	if params.Directory != nil && *params.Directory != "" {
		if dir, _ := ev.Data["directory"].(string); dir != *params.Directory {
			return false
		}
	}
	if params.SessionId != nil && *params.SessionId != "" {
		if id, _ := ev.Data["sessionId"].(string); id != *params.SessionId {
			return false
		}
	}
	if params.Types != nil && *params.Types != "" {
		types := strings.Split(*params.Types, ",")
		found := false
		for _, t := range types {
			if strings.TrimSpace(t) == ev.Type {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
