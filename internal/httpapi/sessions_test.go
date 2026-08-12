package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi/gen"
)

func TestSessionCreateAndList(t *testing.T) {
	pigoHome := t.TempDir()
	cleanupStores(t)
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
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
	svc := NewSessionServiceWithConfig(pigoHome, cfgPath)
	created, apiErr := svc.Create(gen.NewSessionRequest{Directory: workspace})
	if apiErr != nil {
		t.Fatalf("Create: %v", apiErr)
	}
	if created.SessionId == "" || created.Directory != workspace {
		t.Fatalf("created = %+v", created)
	}
	list, apiErr := svc.List(workspace, "", 50, false)
	if apiErr != nil {
		t.Fatalf("List: %v", apiErr)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].SessionId != created.SessionId {
		t.Fatalf("list = %+v", list)
	}
}

func TestSessionCreateMissingDefaultModel(t *testing.T) {
	pigoHome := t.TempDir()
	cleanupStores(t)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{}); err != nil {
		t.Fatal(err)
	}
	svc := NewSessionServiceWithConfig(pigoHome, cfgPath)
	_, apiErr := svc.Create(gen.NewSessionRequest{Directory: t.TempDir()})
	if apiErr == nil || apiErr.Code != CodeModelNotConfigured || apiErr.Status != http.StatusConflict {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

func TestSessionCreateRejectsRelativeDirectory(t *testing.T) {
	cleanupStores(t)
	svc := NewSessionService(t.TempDir())
	_, apiErr := svc.Create(gen.NewSessionRequest{Directory: "relative"})
	if apiErr == nil || apiErr.Code != CodeInvalidParams {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

func TestSessionHTTPCreateAndList(t *testing.T) {
	pigoHome := t.TempDir()
	cleanupStores(t)
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
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
	handler, err := NewRouter(Config{PigoHome: pigoHome, ConfigPath: cfgPath})
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(gen.NewSessionRequest{Directory: workspace})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created gen.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/session?directory="+url.QueryEscape(workspace), nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list gen.SessionListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].SessionId != created.SessionId {
		t.Fatalf("list = %+v", list)
	}
}

func TestSessionLoadCloseDeleteStatus(t *testing.T) {
	pigoHome := t.TempDir()
	cleanupStores(t)
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
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
	svc := NewSessionServiceWithConfig(pigoHome, cfgPath)
	created, apiErr := svc.Create(gen.NewSessionRequest{Directory: workspace})
	if apiErr != nil {
		t.Fatal(apiErr)
	}

	loaded, apiErr := svc.Load(created.SessionId, gen.LoadSessionRequest{Directory: workspace})
	if apiErr != nil {
		t.Fatalf("Load: %v", apiErr)
	}
	if loaded.SessionId != created.SessionId || loaded.HasMore {
		t.Fatalf("loaded = %+v", loaded)
	}

	status, apiErr := svc.Status(created.SessionId, workspace)
	if apiErr != nil {
		t.Fatalf("Status: %v", apiErr)
	}
	if status.Status != "idle" || status.SessionId != created.SessionId {
		t.Fatalf("status = %+v", status)
	}

	if apiErr := svc.Close(created.SessionId, workspace); apiErr != nil {
		t.Fatalf("Close: %v", apiErr)
	}
	if apiErr := svc.Delete(created.SessionId, workspace); apiErr != nil {
		t.Fatalf("Delete: %v", apiErr)
	}
	if _, apiErr := svc.Status(created.SessionId, workspace); apiErr == nil || apiErr.Code != CodeSessionNotFound {
		t.Fatalf("Status after delete: %v", apiErr)
	}
	if apiErr := svc.Delete(created.SessionId, workspace); apiErr != nil {
		t.Fatalf("Delete again should be idempotent: %v", apiErr)
	}
}

func TestSessionUpdateConfigAndMode(t *testing.T) {
	pigoHome := t.TempDir()
	cleanupStores(t)
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
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
	svc := NewSessionServiceWithConfig(pigoHome, cfgPath)
	created, apiErr := svc.Create(gen.NewSessionRequest{Directory: workspace})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	high := "high"
	build := "build"
	cfgResp, apiErr := svc.UpdateConfig(created.SessionId, gen.UpdateSessionRequest{
		Directory:     workspace,
		ThinkingLevel: &high,
		Mode:          &build,
	})
	if apiErr != nil {
		t.Fatalf("UpdateConfig: %v", apiErr)
	}
	if len(cfgResp.ConfigOptions) != 3 {
		t.Fatalf("config options = %+v", cfgResp.ConfigOptions)
	}
	modeResp, apiErr := svc.SetMode(created.SessionId, gen.SetModeRequest{Directory: workspace, ModeId: "build"})
	if apiErr != nil {
		t.Fatalf("SetMode: %v", apiErr)
	}
	if modeResp.CurrentModeId != "build" || len(modeResp.AvailableModes) != 1 {
		t.Fatalf("mode response = %+v", modeResp)
	}
	if _, apiErr := svc.SetMode(created.SessionId, gen.SetModeRequest{Directory: workspace, ModeId: "plan"}); apiErr == nil || apiErr.Code != CodeModeNotFound {
		t.Fatalf("unknown mode error = %v", apiErr)
	}
}
