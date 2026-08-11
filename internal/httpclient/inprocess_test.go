package httpclient_test

import (
	"context"
	"net/http"
	"testing"

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
