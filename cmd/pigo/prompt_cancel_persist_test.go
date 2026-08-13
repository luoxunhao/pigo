package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi"
)

func TestCancelledPromptStillPersistsUserMessage(t *testing.T) {
	t.Setenv("PIGO_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cleanupStores(t)

	started := make(chan struct{}, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer provider.Close()

	cfgPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "pigo", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{
		Model: "test/fake",
		Models: []config.ModelConfig{{
			Provider: "test", ModelID: "fake", Name: "Fake",
			BaseURL: provider.URL, APIKey: "sk-fake", Protocol: "openai",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	httpCfg, err := httpServeConfig(cliOptions{
		model:         "test/fake",
		noTools:       true,
		noSkills:      true,
		thinkingLevel: "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewRouter(httpCfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()

	workspace := t.TempDir()
	created := postJSON(t, ts.URL+"/api/v1/session", map[string]any{"directory": workspace})
	sessionID, _ := created["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("create session returned %+v", created)
	}

	prompt := `E:\project\pigo 这是pigo源代码`
	postJSON(t, ts.URL+"/api/v1/session/"+sessionID+"/prompt_async", map[string]any{
		"directory": workspace,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	})

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was not called")
	}

	resp, err := http.Post(ts.URL+"/api/v1/session/"+sessionID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		msgs := getMessages(t, ts.URL, sessionID, workspace)
		if hasText(msgs, prompt) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancelled prompt was not persisted; messages=%+v", msgs)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestInterruptedTurnPersistsPartialAnswer(t *testing.T) {
	t.Setenv("PIGO_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cleanupStores(t)

	started := make(chan struct{}, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `data: {"id":"c1","model":"fake","choices":[{"delta":{"content":"部分回答"}}]}`+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer provider.Close()

	cfgPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "pigo", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{
		Model: "test/fake",
		Models: []config.ModelConfig{{
			Provider: "test", ModelID: "fake", Name: "Fake",
			BaseURL: provider.URL, APIKey: "sk-fake", Protocol: "openai",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	httpCfg, err := httpServeConfig(cliOptions{
		model:         "test/fake",
		noTools:       true,
		noSkills:      true,
		thinkingLevel: "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewRouter(httpCfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()

	workspace := t.TempDir()
	created := postJSON(t, ts.URL+"/api/v1/session", map[string]any{"directory": workspace})
	sessionID, _ := created["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("create session returned %+v", created)
	}

	prompt := "先给方案"
	postJSON(t, ts.URL+"/api/v1/session/"+sessionID+"/prompt_async", map[string]any{
		"directory": workspace,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	})

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was not called")
	}
	time.Sleep(500 * time.Millisecond)

	resp, err := http.Post(ts.URL+"/api/v1/session/"+sessionID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		msgs := getMessages(t, ts.URL, sessionID, workspace)
		if hasText(msgs, prompt) && hasText(msgs, "部分回答") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("interrupted prompt/answer was not persisted; messages=%+v", msgs)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestInterruptedTurnMessagesDropsDanglingToolCalls(t *testing.T) {
	tail := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("do it")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{
			agentcore.NewTextContent("partial"),
			agentcore.NewToolCallContent("c1", "bash", nil),
		}},
	}
	out := interruptedTurnMessages("do it", tail)
	if len(out) != 2 {
		t.Fatalf("interrupted turn = %d messages, want user + partial assistant", len(out))
	}
	if out[0].Role() != agentcore.RoleUser || out[1].Role() != agentcore.RoleAssistant {
		t.Fatalf("roles = %q, %q", out[0].Role(), out[1].Role())
	}
	a, ok := out[1].(agentcore.AssistantMessage)
	if !ok {
		t.Fatalf("second message = %T, want assistant", out[1])
	}
	if len(a.ToolCalls()) != 0 {
		t.Fatalf("dangling tool calls must be dropped, got %+v", a.ToolCalls())
	}
	if got := agentcore.ContentToText(a.Content); got != "partial" {
		t.Fatalf("partial text = %q, want %q", got, "partial")
	}
	if a.StopReason != agentcore.StopReasonAborted {
		t.Fatalf("stop reason = %q, want aborted", a.StopReason)
	}
}

func postJSON(t *testing.T, url string, body any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s -> %d: %s", url, resp.StatusCode, data)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func getMessages(t *testing.T, base, sessionID, workspace string) []map[string]any {
	t.Helper()
	u := base + "/api/v1/session/" + sessionID + "/messages?limit=50&fullHistory=true&directory=" + url.QueryEscape(workspace)
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("messages -> %d: %s", resp.StatusCode, data)
	}
	var out struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Messages
}

func hasText(msgs []map[string]any, want string) bool {
	for _, m := range msgs {
		content, _ := m["content"].([]any)
		for _, block := range content {
			b, ok := block.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := b["text"].(string); ok && text == want {
				return true
			}
		}
	}
	return false
}
