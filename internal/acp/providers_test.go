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

func TestCustomProvidersUpsertListDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	reg := NewCustomProviders(path)
	entry, err := reg.Upsert(config.ProviderConfig{
		Name:     "DeepSeek Proxy",
		BaseURL:  "https://api.deepseek.com/v1",
		APIKey:   "sk-secret",
		Protocol: "openai",
		Models:   []config.ProviderModelConfig{{ModelID: "deepseek-v4", Name: "DeepSeek V4"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != "custom-deepseek-proxy" {
		t.Fatalf("id = %q", entry.ID)
	}
	updated, err := reg.Upsert(config.ProviderConfig{
		ID:       entry.ID,
		Name:     "DeepSeek Proxy 2",
		BaseURL:  "https://api.deepseek.com/v1",
		Protocol: "openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.APIKey != "sk-secret" {
		t.Fatalf("empty apiKey should keep existing key, got %q", updated.APIKey)
	}
	if len(reg.List()) != 1 {
		t.Fatalf("list = %+v", reg.List())
	}
	if err := reg.Delete(entry.ID); err != nil {
		t.Fatal(err)
	}
	if len(reg.List()) != 0 {
		t.Fatalf("list after delete = %+v", reg.List())
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
		"name":     "My Gateway",
		"baseUrl":  srv.URL,
		"apiKey":   "sk-test",
		"protocol": "openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		ProviderID   string `json:"providerId"`
		ProviderName string `json:"providerName"`
		Models       []struct {
			ModelID string `json:"modelId"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ProviderID != "custom-my-gateway" || resp.ProviderName != "My Gateway" {
		t.Fatalf("provider = %+v", resp)
	}
	if len(resp.Models) != 1 || resp.Models[0].ModelID != "m1" {
		t.Fatalf("models = %+v", resp.Models)
	}
}

func TestPigoProvidersWireLifecycle(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetCredentialStore(provider.NewCredentialStore(nil))
	reg := NewCustomProviders(filepath.Join(t.TempDir(), "config.toml"))
	disp.SetCustomProviders(reg)
	srvACP := NewServer(server, disp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srvACP.Serve(ctx) }()

	raw, err := client.SendRequest(ctx, MethodPigoProvidersUpsert, map[string]any{
		"name":     "GW",
		"baseUrl":  "https://gw.example/v1",
		"apiKey":   "sk-secret",
		"protocol": "openai",
		"models":   []map[string]any{{"modelId": "m1", "name": "M1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var upsert struct {
		ProviderID string `json:"providerId"`
	}
	if err := json.Unmarshal(raw, &upsert); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(upsert.ProviderID, "custom-") {
		t.Fatalf("providerId = %q", upsert.ProviderID)
	}

	raw, err = client.SendRequest(ctx, MethodPigoProvidersList, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-secret") {
		t.Fatalf("list leaked api key: %s", raw)
	}
	var listed struct {
		Providers []struct {
			ProviderID       string `json:"providerId"`
			APIKeyConfigured bool   `json:"apiKeyConfigured"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Providers) != 1 || !listed.Providers[0].APIKeyConfigured {
		t.Fatalf("list = %+v", listed.Providers)
	}

	if _, err := client.SendRequest(ctx, MethodPigoProvidersDelete, map[string]any{"providerId": upsert.ProviderID}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendRequest(ctx, MethodPigoProvidersDelete, map[string]any{"providerId": upsert.ProviderID}); err != nil {
		t.Fatalf("second delete should be idempotent: %v", err)
	}
	raw, err = client.SendRequest(ctx, MethodPigoProvidersList, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"providers":[]`) {
		t.Fatalf("providers after delete = %s", raw)
	}
}

func TestPigoModelsFilteringIncludesCustom(t *testing.T) {
	client, server := NewChannelPair()
	defer client.Close()
	defer server.Close()
	disp, _ := newTestDispatcher(t, &fakeRunner{}, server)
	disp.SetCredentialStore(provider.NewCredentialStore(nil))
	reg := NewCustomProviders(filepath.Join(t.TempDir(), "config.toml"))
	_, _ = reg.Upsert(config.ProviderConfig{
		ID:       "custom-gw",
		Name:     "GW",
		BaseURL:  "https://gw.example/v1",
		Protocol: "openai",
		Models:   []config.ProviderModelConfig{{ModelID: "m1", Name: "M1"}},
	})
	disp.SetCustomProviders(reg)
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
	disp.SetCredentialStore(provider.NewCredentialStore(nil))
	reg := NewCustomProviders(filepath.Join(t.TempDir(), "config.toml"))
	_, _ = reg.Upsert(config.ProviderConfig{
		ID:       "custom-gw",
		Name:     "GW",
		BaseURL:  "https://gw.example/v1",
		Protocol: "openai",
		Models:   []config.ProviderModelConfig{{ModelID: "m1", Name: "M1"}},
	})
	disp.SetCustomProviders(reg)
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

	reg := NewCustomProviders(filepath.Join(t.TempDir(), "config.toml"))
	if _, err := reg.Upsert(config.ProviderConfig{
		ID:       "custom-gw",
		Name:     "GW",
		BaseURL:  srv.URL,
		APIKey:   "sk-custom",
		Protocol: "openai",
		Models:   []config.ProviderModelConfig{{ModelID: "m1", Name: "M1"}},
	}); err != nil {
		t.Fatal(err)
	}
	runner := &RuntimeRunner{
		Provider:        fakeProvider{},
		ProviderName:    "fake",
		Model:           "openrouter/free",
		CustomProviders: reg,
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
