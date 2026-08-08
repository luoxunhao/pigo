package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

type titleProvider struct{}

func (titleProvider) Name() string { return "fake" }
func (titleProvider) Models() []provider.Model {
	return []provider.Model{{Provider: "fake", ID: "fake-model"}}
}

func (titleProvider) StreamCompletion(ctx context.Context, _ provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	s := provider.NewAssistantMessageEventStream(0)
	go func() {
		defer s.Close()
		msg := agentcore.AssistantMessage{
			RoleField: agentcore.RoleAssistant,
			Content:   agentcore.ContentList{agentcore.NewTextContent("Generated Title")},
		}
		_ = s.Emit(ctx, provider.StreamStartEvent{Partial: msg})
		_ = s.Emit(ctx, provider.StreamDoneEvent{Message: msg})
	}()
	return s, nil
}

func TestPromptGeneratesTitleAndNotifies(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()

	runner := &RuntimeRunner{Provider: titleProvider{}, ProviderName: "fake", Model: "fake-model"}
	disp, home := newTestDispatcher(t, runner, server)
	disp.SetRunner(runner)
	srv := NewServer(server, disp)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	msgs, stopReader := startClientReader(t, client)
	defer stopReader()

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var newResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &newResp); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendRequest(ctx, MethodSessionPrompt, map[string]any{
		"sessionId": newResp.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "fix the login bug"}},
	}); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(10 * time.Second)
	for {
		select {
		case msg := <-msgs:
			if msg.Notification == nil || msg.Notification.Method != NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
				continue
			}
			if payload.Update["sessionUpdate"] != "session_info_update" {
				continue
			}
			if payload.Update["title"] != "Generated Title" {
				t.Fatalf("title update = %+v", payload.Update)
			}
			store, err := sessionstore.OpenForWorkspace(home, ws)
			if err != nil {
				t.Fatal(err)
			}
			meta, err := store.LoadMetadata(newResp.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if meta.SessionName != "Generated Title" {
				t.Fatalf("persisted title = %q", meta.SessionName)
			}
			return
		case <-timeout:
			t.Fatal("timed out waiting for generated title update")
		}
	}
}

func TestPigoTrustRPCListAndSet(t *testing.T) {
	mgr, err := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	broker := NewACPPermissionBroker(&mockTransport{}, mgr, t.TempDir(), 0)
	disp := NewDispatcher(NewSessionManager(&fakeRunner{}), nil, t.TempDir(), "model", "prompt", broker, nil)
	cwd := filepath.Join(t.TempDir(), "project")

	raw, rpcErr := disp.HandleRequest(context.Background(), RequestID{}, MethodPigoTrustList, json.RawMessage(`{}`))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	listRaw, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var listResp struct {
		Entries []struct {
			Path     string `json:"path"`
			Decision string `json:"decision"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(listRaw, &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Entries) != 0 {
		t.Fatalf("initial entries = %+v", listResp.Entries)
	}

	setParams, err := json.Marshal(map[string]any{"path": cwd, "decision": "trusted"})
	if err != nil {
		t.Fatal(err)
	}
	raw, rpcErr = disp.HandleRequest(context.Background(), RequestID{}, MethodPigoTrustSet, setParams)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}

	raw, rpcErr = disp.HandleRequest(context.Background(), RequestID{}, MethodPigoTrustList, json.RawMessage(`{}`))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	listRaw, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(listRaw, &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Entries) != 1 || listResp.Entries[0].Path != cwd || listResp.Entries[0].Decision != "trusted" {
		t.Fatalf("entries after set = %+v", listResp.Entries)
	}

	forgetParams, err := json.Marshal(map[string]any{"path": cwd, "decision": "forget"})
	if err != nil {
		t.Fatal(err)
	}
	if _, rpcErr = disp.HandleRequest(context.Background(), RequestID{}, MethodPigoTrustSet, forgetParams); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	raw, rpcErr = disp.HandleRequest(context.Background(), RequestID{}, MethodPigoTrustList, json.RawMessage(`{}`))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	listRaw, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(listRaw, &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Entries) != 0 {
		t.Fatalf("entries after forget = %+v", listResp.Entries)
	}
}
