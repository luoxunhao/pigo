package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/session"
)

func TestCommandServiceCoreSessionCommands(t *testing.T) {
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
	sessions := NewSessionServiceWithConfig(pigoHome, cfgPath)
	created, apiErr := sessions.Create(gen.NewSessionRequest{Directory: workspace})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	store, err := sessions.storeFor(workspace)
	if err != nil {
		t.Fatal(err)
	}
	header := session.SessionHeader{
		ID:        created.SessionId,
		Model:     "test/provider",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if _, err := store.AppendBranch(created.SessionId, header, "", agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hello")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("hi back")}},
	}); err != nil {
		t.Fatal(err)
	}

	svc := NewCommandService(sessions, nil, nil, nil, nil, nil)
	ctx := context.Background()

	tree, apiErr := svc.Execute(ctx, created.SessionId, gen.CommandRequest{Directory: workspace, Command: "tree"})
	if apiErr != nil || tree.Text == nil || !strings.Contains(*tree.Text, "session tree") {
		t.Fatalf("tree = %+v, err = %v", tree, apiErr)
	}
	copyResp, apiErr := svc.Execute(ctx, created.SessionId, gen.CommandRequest{Directory: workspace, Command: "copy"})
	if apiErr != nil || copyResp.Text == nil || !strings.Contains(*copyResp.Text, "hi back") {
		t.Fatalf("copy = %+v, err = %v", copyResp, apiErr)
	}
	cloneResp, apiErr := svc.Execute(ctx, created.SessionId, gen.CommandRequest{Directory: workspace, Command: "clone"})
	if apiErr != nil || cloneResp.Text == nil || !strings.Contains(*cloneResp.Text, "cloned session") {
		t.Fatalf("clone = %+v, err = %v", cloneResp, apiErr)
	}
	exportPath := filepath.Join(t.TempDir(), "session.jsonl")
	exportResp, apiErr := svc.Execute(ctx, created.SessionId, gen.CommandRequest{Directory: workspace, Command: "export", Arguments: &exportPath})
	if apiErr != nil || exportResp.Text == nil || !strings.Contains(*exportResp.Text, "exported 2 entries") {
		t.Fatalf("export = %+v, err = %v", exportResp, apiErr)
	}
	importResp, apiErr := svc.Execute(ctx, created.SessionId, gen.CommandRequest{Directory: workspace, Command: "import", Arguments: &exportPath})
	if apiErr != nil || importResp.Text == nil || !strings.Contains(*importResp.Text, "imported 2 entries") {
		t.Fatalf("import = %+v, err = %v", importResp, apiErr)
	}
	if _, apiErr := svc.Execute(ctx, created.SessionId, gen.CommandRequest{Directory: workspace, Command: "compact"}); apiErr == nil {
		t.Fatal("compact without backend should fail")
	}
}
