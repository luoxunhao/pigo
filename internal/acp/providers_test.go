package acp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/provider"
)

func TestNormalizeDiscoverBaseURL(t *testing.T) {
	cases := []struct {
		base     string
		protocol string
		want     string
	}{
		{"https://gw.example/v1", "openai", "https://gw.example/v1/models"},
		{"https://gw.example/v1/chat/completions", "openai", "https://gw.example/v1/models"},
		{"https://gw.example/v1", "responses", "https://gw.example/v1/models"},
		{"https://api.anthropic.com", "anthropic", "https://api.anthropic.com/v1/models"},
		{"https://api.anthropic.com/v1", "anthropic", "https://api.anthropic.com/v1/models"},
		{"https://generativelanguage.googleapis.com", "gemini", "https://generativelanguage.googleapis.com/v1beta/models"},
		{"https://generativelanguage.googleapis.com/v1beta", "gemini", "https://generativelanguage.googleapis.com/v1beta/models"},
	}
	for _, c := range cases {
		if got := normalizeDiscoverBaseURL(c.base, c.protocol); got != c.want {
			t.Errorf("normalizeDiscoverBaseURL(%q, %q) = %q, want %q", c.base, c.protocol, got, c.want)
		}
	}
}

func TestNormalizeCustomProtocol(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "openai"},
		{"openai", "openai"},
		{"openai/chat", "openai"},
		{"responses", "openai/resp_api"},
		{"openai/resp_api", "openai/resp_api"},
		{"anthropic", "anthropic"},
		{"gemini", "gemini"},
	}
	for _, c := range cases {
		got, err := normalizeCustomProtocol(c.in)
		if err != nil || got != c.want {
			t.Errorf("normalizeCustomProtocol(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := normalizeCustomProtocol("bogus"); err == nil {
		t.Fatal("unknown protocol should error")
	}
}

func TestDiscoverModelList(t *testing.T) {
	tests := []struct {
		name       string
		protocol   string
		body       string
		wantPath   string
		wantHeader string
	}{
		{
			name:       "openai",
			protocol:   "openai",
			body:       `{"data":[{"id":"a","name":"A"},{"id":"b"}]}`,
			wantPath:   "/models",
			wantHeader: "Bearer sk-test",
		},
		{
			name:       "anthropic",
			protocol:   "anthropic",
			body:       `{"models":[{"id":"claude-x"}]}`,
			wantPath:   "/v1/models",
			wantHeader: "sk-test",
		},
		{
			name:       "gemini",
			protocol:   "gemini",
			body:       `{"models":[{"id":"gemini-x"}]}`,
			wantPath:   "/v1beta/models",
			wantHeader: "sk-test",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotAuth, gotXKey, gotGoog string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				gotXKey = r.Header.Get("x-api-key")
				gotGoog = r.Header.Get("x-goog-api-key")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			models, err := discoverModelList(srv.URL, "sk-test", tt.protocol)
			if err != nil {
				t.Fatal(err)
			}
			if len(models) == 0 {
				t.Fatal("no models returned")
			}
			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if tt.protocol == "openai" && gotAuth != tt.wantHeader {
				t.Errorf("auth = %q, want %q", gotAuth, tt.wantHeader)
			}
			if tt.protocol == "anthropic" && gotXKey != tt.wantHeader {
				t.Errorf("x-api-key = %q, want %q", gotXKey, tt.wantHeader)
			}
			if tt.protocol == "gemini" && gotGoog != tt.wantHeader {
				t.Errorf("x-goog-api-key = %q, want %q", gotGoog, tt.wantHeader)
			}
		})
	}
}

func TestDiscoverModelListHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := discoverModelList(srv.URL, "sk-test", "openai")
	if err == nil || strings.Contains(err.Error(), "sk-test") {
		t.Fatalf("error = %v, must not leak key", err)
	}
}

func TestPigoModelsDiscoverWire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m1","name":"M1"}]}`))
	}))
	defer srv.Close()

	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetCredentialStore(provider.NewCredentialStore(nil))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodPigoModelsDiscover, map[string]any{
		"provider": "My Gateway",
		"baseUrl":  srv.URL,
		"apiKey":   "sk-test",
		"protocol": "openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"baseUrl"`
		Protocol string `json:"protocol"`
		Models   []struct {
			ModelID string `json:"modelId"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Provider != "My Gateway" || resp.BaseURL != srv.URL || resp.Protocol != "openai" {
		t.Fatalf("provider = %+v", resp)
	}
	if len(resp.Models) != 1 || resp.Models[0].ModelID != "m1" {
		t.Fatalf("models = %+v", resp.Models)
	}
}

func TestPigoConfigReadWrite(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetConfiguredModels(newConfiguredModels(t))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodPigoConfig, map[string]any{
		"model": "a/m1",
		"models": []map[string]any{
			{"provider": "a", "modelId": "m1", "baseUrl": "https://a.example/v1", "protocol": "openai", "apiKey": "secret"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "needsRestart") {
		t.Fatalf("config response leaked key or restart: %s", raw)
	}
	var resp struct {
		Model  string `json:"model"`
		Models []struct {
			ModelID          string `json:"modelId"`
			APIKeyConfigured bool   `json:"apiKeyConfigured"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Model != "a/m1" || len(resp.Models) != 1 || !resp.Models[0].APIKeyConfigured {
		t.Fatalf("config = %+v", resp)
	}

	if _, err := client.SendRequest(ctx, MethodPigoConfig, map[string]any{"deleteModel": "a/m1"}); err != nil {
		t.Fatal(err)
	}
	raw, err = client.SendRequest(ctx, MethodPigoConfig, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"model":""`) || strings.Contains(string(raw), `"a/m1"`) {
		t.Fatalf("config after delete = %s", raw)
	}
}

func TestPigoConfigDoesNotReturnKeyToAnyClient(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: "https://gw.example/v1", Protocol: "openai", APIKey: "sk-secret",
	}))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	if _, err := client.SendRequest(ctx, MethodInitialize, map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": "ash-workbench", "version": "0.1.0"},
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := client.SendRequest(ctx, MethodPigoConfig, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-secret") {
		t.Fatalf("config response leaked api key: %s", raw)
	}
	var resp struct {
		Models []struct {
			APIKeyConfigured bool `json:"apiKeyConfigured"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Models) != 1 || !resp.Models[0].APIKeyConfigured {
		t.Fatalf("config = %+v", resp.Models)
	}
}

func TestPigoConfigDoesNotReturnKeyToZed(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: "https://gw.example/v1", Protocol: "openai", APIKey: "sk-secret",
	}))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	if _, err := client.SendRequest(ctx, MethodInitialize, map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": "zed", "version": "0.1.0"},
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := client.SendRequest(ctx, MethodPigoConfig, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-secret") {
		t.Fatalf("config response leaked api key: %s", raw)
	}
}

func TestPigoConfigTestWire(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(customOpenAISSE))
	}))
	defer srv.Close()

	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: srv.URL, Protocol: "openai", APIKey: "sk-test",
	}))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodPigoConfigTest, map[string]any{
		"modelId": "custom-gw/m1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Success        bool   `json:"success"`
		ResponseTimeMs int64  `json:"response_time_ms"`
		ModelResponse  string `json:"model_response"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.ModelResponse != "Hello" || resp.ResponseTimeMs < 0 {
		t.Fatalf("test result = %s", raw)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q, want Bearer sk-test", gotAuth)
	}
}

func TestPigoConfigTestWireError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: srv.URL, Protocol: "openai", APIKey: "sk-secret",
	}))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodPigoConfigTest, map[string]any{
		"modelId": "custom-gw/m1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Success      bool   `json:"success"`
		ErrorDetails string `json:"error_details"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Success || resp.ErrorDetails == "" || strings.Contains(resp.ErrorDetails, "sk-secret") {
		t.Fatalf("error result = %s, must not leak key", raw)
	}
}

func TestPigoConfigTestUnknownModel(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetConfiguredModels(newConfiguredModels(t))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodPigoConfigTest, map[string]any{
		"modelId": "missing/model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "unknown modelId") {
		t.Fatalf("result = %s", raw)
	}
}

func TestPigoConfigEnabledFilter(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetConfiguredModels(newConfiguredModels(t))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	if _, err := client.SendRequest(ctx, MethodPigoConfig, map[string]any{
		"models": []map[string]any{
			{"provider": "a", "modelId": "m1", "baseUrl": "https://a.example/v1", "protocol": "openai", "enabled": false},
			{"provider": "a", "modelId": "m2", "baseUrl": "https://a.example/v1", "protocol": "openai"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := client.SendRequest(ctx, MethodPigoModels, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var modelsResp struct {
		Models []struct {
			ModelID string `json:"modelId"`
			Enabled bool   `json:"enabled"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &modelsResp); err != nil {
		t.Fatal(err)
	}
	if len(modelsResp.Models) != 2 {
		t.Fatalf("models = %+v", modelsResp.Models)
	}
	for _, m := range modelsResp.Models {
		if m.ModelID == "m1" && m.Enabled {
			t.Fatalf("m1 should be disabled: %+v", m)
		}
		if m.ModelID == "m2" && !m.Enabled {
			t.Fatalf("m2 should be enabled: %+v", m)
		}
	}

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err = client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "a/m2") || strings.Contains(string(raw), "a/m1") {
		t.Fatalf("session/new should only expose enabled models: %s", raw)
	}
}

func TestPigoModelsFilteringIncludesCustom(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: "https://gw.example/v1", Protocol: "openai",
	}))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodPigoModels, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Models []struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	var sawCustom bool
	for _, m := range resp.Models {
		if m.Provider == "openrouter" {
			t.Fatalf("unconfigured built-in leaked: %+v", m)
		}
		if m.Provider == "custom-gw" && m.ModelID == "m1" {
			sawCustom = true
		}
	}
	if !sawCustom {
		t.Fatalf("custom model missing: %+v", resp.Models)
	}
}

func TestSessionNewIncludesCustomModels(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetConfiguredModels(newConfiguredModels(t, config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: "https://gw.example/v1", Protocol: "openai",
	}))
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": filepath.Join(t.TempDir(), "ws")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "custom-gw/m1") {
		t.Fatalf("session/new missing custom model: %s", raw)
	}
}

func TestSessionNewAndLoadIncludeAvailableCommands(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := client.SendRequest(ctx, MethodSessionNew, map[string]any{"cwd": ws})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		SessionID         string           `json:"sessionId"`
		AvailableCommands []map[string]any `json:"availableCommands"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if !availableCommandsContain(created.AvailableCommands, "session") {
		t.Fatalf("session/new availableCommands missing /session: %s", raw)
	}

	raw, err = client.SendRequest(ctx, MethodSessionLoad, map[string]any{
		"sessionId": created.SessionID,
		"cwd":       ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	var loaded struct {
		AvailableCommands []map[string]any `json:"availableCommands"`
	}
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if !availableCommandsContain(loaded.AvailableCommands, "session") {
		t.Fatalf("session/load availableCommands missing /session: %s", raw)
	}
}

func availableCommandsContain(cmds []map[string]any, name string) bool {
	for _, c := range cmds {
		if c["name"] == name {
			return true
		}
	}
	return false
}

func TestSetModelThenPromptUsesCustomEndpoint(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(customOpenAISSE))
	}))
	defer srv.Close()

	models := newConfiguredModels(t, config.ModelConfig{
		Provider: "custom-gw", ModelID: "m1", Name: "M1",
		BaseURL: srv.URL, APIKey: "sk-custom", Protocol: "openai",
	})
	runner := &RuntimeRunner{
		Provider:         fakeProvider{},
		ProviderName:     "fake",
		Model:            "openrouter/free",
		ConfiguredModels: models,
	}
	home := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	client, stop := StartInProcess(runner, home, "openrouter/free", "sys", ws, nil, nil)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.NewSession(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetModel(ctx, sessionID, "custom-gw/m1"); err != nil {
		t.Fatal(err)
	}
	stopReason, err := client.Prompt(ctx, sessionID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if stopReason != "end_turn" {
		t.Fatalf("stop reason = %q", stopReason)
	}
	if gotAuth != "Bearer sk-custom" {
		t.Errorf("auth = %q, want Bearer sk-custom", gotAuth)
	}
	if gotModel != "m1" {
		t.Errorf("wire model = %q, want m1", gotModel)
	}
}
