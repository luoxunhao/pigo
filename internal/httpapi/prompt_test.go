package httpapi

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/httpapi/gen"
)

func TestPromptManagerSync(t *testing.T) {
	broker := NewEventBroker()
	mgr := NewPromptManager(func(_ context.Context, _ PromptRun) (gen.PromptResponse, error) {
		return gen.PromptResponse{MessageId: "msg-1", StopReason: "end_turn"}, nil
	}, broker)
	resp, apiErr := mgr.SubmitSync("s1", gen.PromptRequest{
		Directory: "E:/project/foo",
		Prompt:    []map[string]interface{}{{"type": "text", "text": "hi"}},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if resp.StopReason != "end_turn" || resp.MessageId == "" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestPromptManagerCancelClearsQueue(t *testing.T) {
	broker := NewEventBroker()
	started := make(chan struct{})
	mgr := NewPromptManager(func(ctx context.Context, _ PromptRun) (gen.PromptResponse, error) {
		close(started)
		<-ctx.Done()
		return gen.PromptResponse{}, context.Canceled
	}, broker)

	asyncResp, apiErr := mgr.SubmitAsync("s1", gen.PromptRequest{Directory: "E:/project/foo", Prompt: []map[string]interface{}{{"type": "text", "text": "a"}}})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	<-started

	var wg sync.WaitGroup
	wg.Add(1)
	var syncResp gen.PromptResponse
	var syncErr *APIError
	go func() {
		defer wg.Done()
		syncResp, syncErr = mgr.SubmitSync("s1", gen.PromptRequest{Directory: "E:/project/foo", Prompt: []map[string]interface{}{{"type": "text", "text": "b"}}})
	}()
	time.Sleep(20 * time.Millisecond)
	if apiErr := mgr.Cancel("s1"); apiErr != nil {
		t.Fatal(apiErr)
	}
	wg.Wait()
	if syncErr != nil {
		t.Fatal(syncErr)
	}
	if syncResp.StopReason != "cancelled" {
		t.Fatalf("sync resp = %+v", syncResp)
	}
	if asyncResp.MessageId == "" {
		t.Fatal("missing async message id")
	}
}

func TestPromptManagerQueueFull(t *testing.T) {
	broker := NewEventBroker()
	block := make(chan struct{})
	mgr := NewPromptManager(func(ctx context.Context, _ PromptRun) (gen.PromptResponse, error) {
		<-block
		return gen.PromptResponse{StopReason: "end_turn"}, nil
	}, broker)
	_, apiErr := mgr.SubmitAsync("s1", gen.PromptRequest{Directory: "E:/project/foo", Prompt: []map[string]interface{}{}})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for i := 0; i < promptQueueLimit; i++ {
		_, apiErr = mgr.SubmitAsync("s1", gen.PromptRequest{Directory: "E:/project/foo", Prompt: []map[string]interface{}{}})
		if apiErr != nil {
			t.Fatalf("submit %d: %v", i, apiErr)
		}
	}
	_, apiErr = mgr.SubmitAsync("s1", gen.PromptRequest{Directory: "E:/project/foo", Prompt: []map[string]interface{}{}})
	if apiErr == nil || apiErr.Code != CodeQueueFull {
		t.Fatalf("apiErr = %v", apiErr)
	}
	close(block)
}
