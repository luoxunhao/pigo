package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/compaction"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/sessionstore"
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

func TestSessionMessagePaginationCoversAllEntries(t *testing.T) {
	cleanupStores(t)
	pigoHome := t.TempDir()
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
	store, err := svc.storeFor(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var msgs agentcore.MessageList
	for i := 0; i < 125; i++ {
		msgs = append(msgs, agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   agentcore.ContentList{agentcore.NewTextContent(fmt.Sprintf("msg-%03d", i))},
		})
	}
	if err := store.Append(created.SessionId, time.Now().UTC(), msgs); err != nil {
		t.Fatal(err)
	}

	limit := 50
	loaded, apiErr := svc.Load(created.SessionId, gen.LoadSessionRequest{
		Directory: workspace,
		Limit:     &limit,
	})
	if apiErr != nil {
		t.Fatalf("Load: %v", apiErr)
	}
	seen := make([]bool, len(msgs))
	mark := func(msgs []gen.Message) {
		for _, m := range msgs {
			if m.Seq != nil && *m.Seq >= 0 && *m.Seq < len(seen) {
				seen[*m.Seq] = true
			}
		}
	}
	mark(loaded.Messages)

	if loaded.NextCursor != nil {
		before := *loaded.NextCursor
		prev, apiErr := svc.Load(created.SessionId, gen.LoadSessionRequest{
			Directory: workspace,
			Before:    &before,
			Limit:     &limit,
		})
		if apiErr != nil {
			t.Fatalf("Load(before=%s): %v", before, apiErr)
		}
		mark(prev.Messages)
		if prev.NextCursor == nil || prev.HasMore != true {
			t.Fatalf("second page = %+v, want more pages", prev)
		}
	}

	cursor := loaded.NextCursor
	for cursor != nil {
		page, apiErr := svc.Messages(created.SessionId, workspace, *cursor, limit, false)
		if apiErr != nil {
			t.Fatalf("Messages(%s): %v", *cursor, apiErr)
		}
		mark(page.Messages)
		cursor = page.NextCursor
	}

	for i, ok := range seen {
		if !ok {
			t.Fatalf("entry %d was not returned by pagination", i)
		}
	}
}

func TestSessionLoadFullHistoryKeepsPreCompactionEntries(t *testing.T) {
	cleanupStores(t)
	pigoHome := t.TempDir()
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
	store, err := svc.storeFor(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var msgs agentcore.MessageList
	for i := 0; i < 6; i++ {
		msgs = append(msgs, agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   agentcore.ContentList{agentcore.NewTextContent(fmt.Sprintf("msg-%d", i))},
		})
	}
	if err := store.Append(created.SessionId, time.Now().UTC(), msgs); err != nil {
		t.Fatal(err)
	}
	header, err := store.Header(created.SessionId)
	if err != nil {
		t.Fatal(err)
	}
	res := &compaction.CompactionResult{
		Summary:        "summarized",
		FirstKeptIndex: 4,
		RetainedTail:   []agentcore.Message{msgs[4], msgs[5]},
		TokensBefore:   10,
	}
	if _, err := store.AppendCompaction(created.SessionId, header, res); err != nil {
		t.Fatal(err)
	}

	limit := 50
	full := true
	loaded, apiErr := svc.Load(created.SessionId, gen.LoadSessionRequest{
		Directory:   workspace,
		Limit:       &limit,
		FullHistory: &full,
	})
	if apiErr != nil {
		t.Fatalf("Load(fullHistory): %v", apiErr)
	}
	if len(loaded.Messages) != 7 {
		t.Fatalf("full history returned %d messages, want 7", len(loaded.Messages))
	}

	compacted, apiErr := svc.Load(created.SessionId, gen.LoadSessionRequest{
		Directory: workspace,
		Limit:     &limit,
	})
	if apiErr != nil {
		t.Fatalf("Load(compacted): %v", apiErr)
	}
	if len(compacted.Messages) != 3 {
		t.Fatalf("compacted projection returned %d messages, want 3", len(compacted.Messages))
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

func TestSessionUpdateConfigReflectsLaneConfigAndStatus(t *testing.T) {
	pigoHome := t.TempDir()
	cleanupStores(t)
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{
		Model: "test/old",
		Models: []config.ModelConfig{
			{
				Provider: "test", ModelID: "old", Name: "Old",
				BaseURL: "http://localhost", Protocol: "openai",
			},
			{
				Provider: "test", ModelID: "new", Name: "New",
				BaseURL: "http://localhost", Protocol: "openai",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewSessionServiceWithConfig(pigoHome, cfgPath)
	created, apiErr := svc.Create(gen.NewSessionRequest{Directory: workspace})
	if apiErr != nil {
		t.Fatalf("Create: %v", apiErr)
	}

	model := "test/new"
	thinking := "high"
	if _, apiErr := svc.UpdateConfig(created.SessionId, gen.UpdateSessionRequest{
		Directory:     workspace,
		Model:         &model,
		ThinkingLevel: &thinking,
	}); apiErr != nil {
		t.Fatalf("UpdateConfig: %v", apiErr)
	}

	status, apiErr := svc.Status(created.SessionId, workspace)
	if apiErr != nil {
		t.Fatalf("Status: %v", apiErr)
	}
	if status.Model == nil || *status.Model != "test/new" {
		got := "<nil>"
		if status.Model != nil {
			got = *status.Model
		}
		t.Fatalf("status model = %q, want test/new", got)
	}
	if status.ThinkingLevel == nil || *status.ThinkingLevel != "high" {
		got := "<nil>"
		if status.ThinkingLevel != nil {
			got = *status.ThinkingLevel
		}
		t.Fatalf("status thinking = %q, want high", got)
	}

	loaded, apiErr := svc.Load(created.SessionId, gen.LoadSessionRequest{Directory: workspace})
	if apiErr != nil {
		t.Fatalf("Load: %v", apiErr)
	}
	if loaded.LaneConfig == nil {
		t.Fatal("loaded laneConfig is nil")
	}
	if got := (*loaded.LaneConfig)["model"]; got != "test/new" {
		t.Fatalf("loaded lane model = %v, want test/new", got)
	}
	if got := (*loaded.LaneConfig)["thinkingLevel"]; got != "high" {
		t.Fatalf("loaded lane thinking = %v, want high", got)
	}
}

func TestUpdateConfigDoesNotPersistCustomThinking(t *testing.T) {
	pigoHome := t.TempDir()
	cleanupStores(t)
	workspace := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := config.SaveFileConfig(cfgPath, config.FileConfig{
		Model: "test/provider",
		Models: []config.ModelConfig{
			{
				Provider: "test", ModelID: "provider", Name: "Provider",
				BaseURL: "http://localhost", Protocol: "openai",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewSessionServiceWithConfig(pigoHome, cfgPath)
	created, apiErr := svc.Create(gen.NewSessionRequest{Directory: workspace})
	if apiErr != nil {
		t.Fatalf("Create: %v", apiErr)
	}
	thinking := "high"
	if _, apiErr := svc.UpdateConfig(created.SessionId, gen.UpdateSessionRequest{
		Directory:     workspace,
		ThinkingLevel: &thinking,
	}); apiErr != nil {
		t.Fatalf("UpdateConfig: %v", apiErr)
	}
	store, err := sessionstore.OpenForWorkspace(pigoHome, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	meta, err := store.LoadMetadata(created.SessionId)
	if err != nil {
		t.Fatal(err)
	}
	custom := map[string]any{}
	if len(meta.CustomMetadata) > 0 {
		if err := json.Unmarshal(meta.CustomMetadata, &custom); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := custom["thinkingLevel"]; ok {
		t.Fatalf("customMetadata still contains thinkingLevel: %s", meta.CustomMetadata)
	}
	lanes, err := store.Lanes(created.SessionId)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lanes {
		if l.Lane == "main" && (l.Config == nil || l.Config.ThinkingLevel != "high") {
			t.Fatalf("lane config thinking = %+v, want high", l.Config)
		}
	}
}
