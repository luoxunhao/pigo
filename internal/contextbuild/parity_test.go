package contextbuild

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/smallnest/pigo/internal/agentcore"
	pigoruntime "github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
)

var updateParity = flag.Bool("update", false, "regenerate internal/contextbuild/testdata/parity fixtures")

// parityDeviation documents a deliberate pigo-vs-pi difference in the corpus.
type parityDeviation struct {
	Code     string `json:"code"`
	Scope    string `json:"scope"`
	PiCommit string `json:"piCommit"`
	Reason   string `json:"reason"`
}

type parityContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type paritySkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

type parityTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type parityProjector struct {
	Content string `json:"content"`
	Display bool   `json:"display"`
}

type parityInput struct {
	Model              string                     `json:"model,omitempty"`
	Provider           string                     `json:"provider,omitempty"`
	ThinkingLevel      string                     `json:"thinkingLevel,omitempty"`
	ActiveToolNames    []string                   `json:"activeToolNames,omitempty"`
	LeafID             string                     `json:"leafId,omitempty"`
	Entries            []session.V4Entry          `json:"entries,omitempty"`
	Projectors         map[string]parityProjector `json:"projectors,omitempty"`
	BaseInstruction    string                     `json:"baseInstruction,omitempty"`
	Cwd                string                     `json:"cwd,omitempty"`
	AppendInstructions []string                   `json:"appendInstructions,omitempty"`
	ContextFiles       []parityContextFile        `json:"contextFiles,omitempty"`
	Skills             []paritySkill              `json:"skills,omitempty"`
	Tools              []parityTool               `json:"tools,omitempty"`
	Reminders          []string                   `json:"reminders,omitempty"`
	BlockImages        bool                       `json:"blockImages,omitempty"`
}

type parityExpected struct {
	Messages     []json.RawMessage `json:"messages,omitempty"`
	SystemPrompt string            `json:"systemPrompt,omitempty"`
	Tools        []string          `json:"tools,omitempty"`
}

type parityFixture struct {
	Name       string            `json:"name"`
	PiCommit   string            `json:"piCommit"`
	Deviations []parityDeviation `json:"deviations"`
	Input      parityInput       `json:"input"`
	Expected   parityExpected    `json:"expected,omitempty"`
}

func (f parityFixture) path() string {
	return filepath.Join("testdata", "parity", f.Name+".json")
}

func TestParity(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "parity", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no parity fixtures: %v", err)
	}
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var fx parityFixture
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatal(err)
			}
			got, err := runParityFixture(fx)
			if err != nil {
				t.Fatalf("run fixture: %v", err)
			}
			if *updateParity {
				fx.Expected = got
				b, err := json.MarshalIndent(fx, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				b = append(b, '\n')
				if err := os.WriteFile(file, b, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			assertParityExpected(t, fx.Expected, got)
		})
	}
}

func runParityFixture(fx parityFixture) (parityExpected, error) {
	var out parityExpected
	lanes := []session.LaneState{{
		Lane: "main",
		Config: &session.LaneConfig{
			Model:           fx.Input.Model,
			Provider:        fx.Input.Provider,
			ThinkingLevel:   fx.Input.ThinkingLevel,
			ActiveToolNames: fx.Input.ActiveToolNames,
		},
	}}
	if fx.Input.LeafID != "" {
		lanes[0].LeafID = &fx.Input.LeafID
	}
	proj, err := session.BuildProjection(fx.Input.Entries, lanes, fx.Input.LeafID, nil)
	if err != nil {
		return out, err
	}
	projectors := make(map[string]EntryProjector, len(fx.Input.Projectors))
	for typ, spec := range fx.Input.Projectors {
		typ, spec := typ, spec
		projectors[typ] = func(entry session.V4Entry, _ int, _ []session.V4Entry) []agentcore.Message {
			content := spec.Content
			if entry.Content != "" && content == "" {
				content = entry.Content
			}
			return []agentcore.Message{agentcore.CustomMessage{
				RoleField:  agentcore.RoleCustom,
				CustomType: typ,
				Content:    agentcore.ContentList{agentcore.NewTextContent(content)},
				Display:    spec.Display,
				Timestamp:  entry.Timestamp.UnixMilli(),
			}}
		}
	}
	sess, err := BuildSessionContext(proj, SessionBuildOptions{EntryProjectors: projectors})
	if err != nil {
		return out, err
	}
	tools := make([]agentcore.AgentTool, 0, len(fx.Input.Tools))
	for _, tt := range fx.Input.Tools {
		tools = append(tools, newParityTool(tt))
	}
	skills := make([]*pigoruntime.Skill, 0, len(fx.Input.Skills))
	for _, s := range fx.Input.Skills {
		skills = append(skills, &pigoruntime.Skill{
			Frontmatter: pigoruntime.SkillFrontmatter{Name: s.Name, Description: s.Description},
			Path:        s.Path,
		})
	}
	reminders := pigoruntime.NewReminderRegistry()
	for _, body := range fx.Input.Reminders {
		body := body
		reminders.Register(pigoruntime.ReminderFunc{
			NameField: "parity",
			Fn: func(context.Context, agentcore.MessageList) (string, bool) {
				return body, body != ""
			},
		})
	}
	deps := BuildDeps{
		Registry:    NewRegistry(),
		Convert:     ConvertToLlm,
		Reminders:   reminders,
		BlockImages: fx.Input.BlockImages,
	}
	readFile := func(path string) ([]byte, error) {
		for _, cf := range fx.Input.ContextFiles {
			if cf.Path == path {
				return []byte(cf.Content), nil
			}
		}
		return nil, os.ErrNotExist
	}
	req := RequestOptions{
		Cwd:                 fx.Input.Cwd,
		BaseInstruction:     fx.Input.BaseInstruction,
		AppendInstructions:  fx.Input.AppendInstructions,
		ContextFilesEnabled: len(fx.Input.ContextFiles) > 0,
		Skills:              skills,
		AllTools:            tools,
		ReadFile:            readFile,
		IsWorktreeRoot:      func(string) bool { return false },
	}
	pr, err := BuildProviderContext(context.Background(), sess, deps, req)
	if err != nil {
		return out, err
	}
	out.SystemPrompt = pr.LlmContext.SystemPrompt
	for _, m := range pr.LlmContext.Messages {
		raw, err := json.Marshal(m)
		if err != nil {
			return out, err
		}
		out.Messages = append(out.Messages, raw)
	}
	for _, t := range pr.LlmContext.Tools {
		out.Tools = append(out.Tools, t.Name())
	}
	return out, nil
}

func assertParityExpected(t *testing.T, want, got parityExpected) {
	t.Helper()
	if want.SystemPrompt != got.SystemPrompt {
		t.Errorf("system prompt mismatch:\n--- want ---\n%s\n--- got ---\n%s", want.SystemPrompt, got.SystemPrompt)
	}
	if len(want.Messages) != len(got.Messages) {
		t.Fatalf("message count = %d, want %d", len(got.Messages), len(want.Messages))
	}
	for i := range want.Messages {
		wn := normalizeMessage(t, want.Messages[i])
		gn := normalizeMessage(t, got.Messages[i])
		if !reflect.DeepEqual(wn, gn) {
			t.Errorf("message[%d] mismatch:\nwant %s\ngot  %s", i, jsonString(wn), jsonString(gn))
		}
	}
	if !reflect.DeepEqual(want.Tools, got.Tools) {
		t.Errorf("tools = %v, want %v", got.Tools, want.Tools)
	}
}

// normalizeMessage strips metadata that is out of parity scope (id/timestamp,
// usage/cost) and returns a comparable generic shape.
func normalizeMessage(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	delete(m, "timestamp")
	delete(m, "usage")
	delete(m, "id")
	if content, ok := m["content"].([]any); ok {
		for _, block := range content {
			if bm, ok := block.(map[string]any); ok {
				delete(bm, "id")
			}
		}
	}
	return m
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

type fakeParityTool struct {
	spec parityTool
}

func newParityTool(spec parityTool) fakeParityTool { return fakeParityTool{spec: spec} }

func (t fakeParityTool) Name() string        { return t.spec.Name }
func (t fakeParityTool) Description() string { return t.spec.Description }
func (t fakeParityTool) Schema() json.RawMessage {
	if len(t.spec.Schema) > 0 {
		return t.spec.Schema
	}
	return json.RawMessage(`{"type":"object"}`)
}
func (t fakeParityTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionParallel
}
func (t fakeParityTool) Execute(context.Context, string, json.RawMessage, agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	return agentcore.AgentToolResult{}, nil
}
