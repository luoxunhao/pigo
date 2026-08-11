package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/httpapi/gen"
)

func TestCommandServiceListAndExecute(t *testing.T) {
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
	svc := NewCommandService(sessions)
	list := svc.List()
	if len(list.Commands) == 0 {
		t.Fatal("empty command list")
	}
	name := "My Session"
	resp, apiErr := svc.Execute(created.SessionId, gen.CommandRequest{Directory: workspace, Command: "name", Arguments: &name})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if resp.Text == nil || !strings.Contains(*resp.Text, "My Session") {
		t.Fatalf("resp = %+v", resp)
	}
	status, apiErr := svc.Execute(created.SessionId, gen.CommandRequest{Directory: workspace, Command: "status"})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if status.Text == nil || !strings.Contains(*status.Text, created.SessionId) {
		t.Fatalf("status = %+v", status)
	}
	if _, apiErr := svc.Execute(created.SessionId, gen.CommandRequest{Directory: workspace, Command: "nope"}); apiErr == nil {
		t.Fatal("expected unknown command error")
	}
}
