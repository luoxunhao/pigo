package httpclient_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/httpclient"
)

func TestInProcessClientHealth(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"healthy":true,"version":"test"}`))
	})
	client, err := httpclient.InProcessClient(handler)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.GetHealthWithResponse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.JSON200 == nil || !resp.JSON200.Healthy || resp.JSON200.Version != "test" {
		t.Fatalf("resp = %+v", resp.JSON200)
	}
}

func TestInProcessClientStreamsSSE(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: session.status\ndata: {\"status\":\"running\"}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	client, err := httpclient.InProcessClient(handler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := client.GetEvents(ctx, &httpclient.GetEventsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("resp = %+v", resp)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "session.status") {
		t.Fatalf("first SSE line = %q", line)
	}
	cancel()
	select {
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stream did not close after cancel")
	default:
	}
}
