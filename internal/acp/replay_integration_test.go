package acp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/httpclient"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
)

func TestHTTPAdapterSessionLoadReplaysThinkingAndToolCalls(t *testing.T) {
	cleanupStores(t)
	pigoHome := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := sessionstore.OpenForWorkspace(pigoHome, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "replay-sess", CreatedAt: now, UpdatedAt: now, Model: "m", Provider: "p", Cwd: workspace}
	msgs := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hi")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{
			agentcore.NewThinkingContent("let me check"),
			agentcore.NewToolCallContent("call-bash", "bash", json.RawMessage(`{"command":"ls E:/ws"}`)),
		}},
		agentcore.ToolResultMessage{
			RoleField: agentcore.RoleToolResult, ToolCallID: "call-bash", ToolName: "bash",
			Content: agentcore.ContentList{agentcore.NewTextContent("src\n")},
		},
	}
	meta := sessionstore.NewMetadata(header.ID, "Replay", "pigo", header.Model, workspace)
	if err := store.Create(meta, header, msgs); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{
		Model: "test/provider",
		Models: []config.ModelConfig{{
			Provider: "test", ModelID: "provider", Name: "Provider",
			BaseURL: "http://localhost", Protocol: "openai",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewRouter(httpapi.Config{
		Version:    "test",
		PigoHome:   pigoHome,
		ConfigPath: cfgPath,
		TrustPath:  filepath.Join(pigoHome, "trust.json"),
		PromptRunner: func(_ context.Context, _ httpapi.PromptRun) (gen.PromptResponse, error) {
			return gen.PromptResponse{MessageId: "msg-1", StopReason: "end_turn"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()
	httpClient, err := httpclient.NewClientWithResponses(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	clientTransport, serverTransport := acp.NewChannelPair()
	adapter := acp.NewHTTPAdapter(httpClient, serverTransport, "test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = acp.NewServer(serverTransport, adapter).Serve(ctx) }()
	client := acp.NewClient(clientTransport)
	defer client.Close()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.LoadSession(ctx, header.ID, workspace); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var sawThought, sawBashStart, sawBashEnd bool
	for time.Now().Before(deadline) {
		select {
		case msg := <-client.Notifications():
			if msg.Notification == nil || msg.Notification.Method != acp.NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
				continue
			}
			u := payload.Update
			switch u["sessionUpdate"] {
			case "agent_thought_chunk":
				sawThought = true
			case "tool_call":
				if u["title"] == "ls E:/ws" {
					sawBashStart = true
				}
			case "tool_call_update":
				if u["status"] == "completed" {
					meta, _ := u["_meta"].(map[string]any)
					term, _ := meta["terminal_output"].(map[string]any)
					if term["data"] == "src\n" {
						sawBashEnd = true
					}
				}
			}
		case <-ctx.Done():
			t.Fatal("context done while waiting for replay notifications")
		case <-time.After(time.Second):
			t.Fatalf("no replay notifications within 1s; thought=%v bashStart=%v bashEnd=%v", sawThought, sawBashStart, sawBashEnd)
		}
		if sawThought && sawBashStart && sawBashEnd {
			break
		}
	}
	if !sawThought {
		t.Error("thinking was not replayed")
	}
	if !sawBashStart {
		t.Error("bash tool_call was not replayed")
	}
	if !sawBashEnd {
		t.Error("bash tool_call_update was not replayed")
	}
}

func TestHTTPAdapterSessionLoadReplaysAllPagesInOrder(t *testing.T) {
	cleanupStores(t)
	pigoHome := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := sessionstore.OpenForWorkspace(pigoHome, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "replay-pages", CreatedAt: now, UpdatedAt: now, Model: "m", Provider: "p", Cwd: workspace}
	var msgs agentcore.MessageList
	for i := 0; i < 125; i++ {
		msgs = append(msgs, agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   agentcore.ContentList{agentcore.NewTextContent(fmt.Sprintf("msg-%03d", i))},
		})
	}
	meta := sessionstore.NewMetadata(header.ID, "ReplayPages", "pigo", header.Model, workspace)
	if err := store.Create(meta, header, msgs); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{
		Model: "test/provider",
		Models: []config.ModelConfig{{
			Provider: "test", ModelID: "provider", Name: "Provider",
			BaseURL: "http://localhost", Protocol: "openai",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewRouter(httpapi.Config{
		Version:    "test",
		PigoHome:   pigoHome,
		ConfigPath: cfgPath,
		TrustPath:  filepath.Join(pigoHome, "trust.json"),
		PromptRunner: func(_ context.Context, _ httpapi.PromptRun) (gen.PromptResponse, error) {
			return gen.PromptResponse{MessageId: "msg-1", StopReason: "end_turn"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()
	httpClient, err := httpclient.NewClientWithResponses(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	clientTransport, serverTransport := acp.NewChannelPair()
	adapter := acp.NewHTTPAdapter(httpClient, serverTransport, "test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = acp.NewServer(serverTransport, adapter).Serve(ctx) }()
	client := acp.NewClient(clientTransport)
	defer client.Close()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.LoadSession(ctx, header.ID, workspace); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var texts []string
replayLoop:
	for time.Now().Before(deadline) {
		select {
		case msg := <-client.Notifications():
			if msg.Notification == nil || msg.Notification.Method != acp.NotificationSessionUpdate {
				continue
			}
			var payload struct {
				Update map[string]any `json:"update"`
			}
			if err := json.Unmarshal(msg.Notification.Params, &payload); err != nil {
				continue
			}
			u := payload.Update
			if u["sessionUpdate"] == "user_message_chunk" {
				content, _ := u["content"].(map[string]any)
				text, _ := content["text"].(string)
				texts = append(texts, text)
			}
			if len(texts) == len(msgs) {
				break replayLoop
			}
		case <-ctx.Done():
			t.Fatal("context done while waiting for replay notifications")
		case <-time.After(time.Second):
			t.Fatalf("got %d/%d replayed messages before timeout", len(texts), len(msgs))
		}
	}
	if len(texts) != len(msgs) {
		t.Fatalf("replayed %d messages, want %d", len(texts), len(msgs))
	}
	for i, text := range texts {
		want := fmt.Sprintf("msg-%03d", i)
		if text != want {
			head := texts
			if len(head) > 10 {
				head = head[:10]
			}
			t.Fatalf("replayed[%d] = %q, want %q; head=%v", i, text, want, head)
		}
	}
}
