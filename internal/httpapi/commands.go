package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/runtime"
)

// serveUnsupportedCommands are real pigo slash commands that still require
// REPL/core-side state and are not executable through the serve API yet. They
// are intentionally not advertised to clients so Zed never sees a command that
// would only return a placeholder.
var serveUnsupportedCommands = map[string]bool{
	"exit": true, "quit": true,
	"rewind": true,
}

// CommandService exposes slash commands over HTTP.
type CommandService struct {
	sessions *SessionService
	prompts  *PromptManager
	slash    *runtime.SlashRegistry
	compact  func(ctx context.Context, sessionID, directory string) (string, error)
	dream    func(ctx context.Context, args string) (string, error)
	goal     GoalFunc
	remote   *RemoteControlService
	broker   *EventBroker
}

// NewCommandService builds a command service. slash carries the full pigo
// command registry (skills, plugins, prompt templates and built-ins); when nil
// the service falls back to a small static list.
func NewCommandService(sessions *SessionService, prompts *PromptManager, slash *runtime.SlashRegistry, compact func(ctx context.Context, sessionID, directory string) (string, error), dream func(ctx context.Context, args string) (string, error), goal GoalFunc, remote *RemoteControlService, broker *EventBroker) *CommandService {
	return &CommandService{sessions: sessions, prompts: prompts, slash: slash, compact: compact, dream: dream, goal: goal, remote: remote, broker: broker}
}

func (c *CommandService) List() gen.CommandListResult {
	commands := []gen.AvailableCommand{
		{Name: "name", Description: "Set the session display name", Input: hintInput("<name>")},
	}
	seen := map[string]bool{"name": true}
	if c.slash != nil {
		for _, cmd := range c.slash.List() {
			if serveUnsupportedCommands[cmd.Name] {
				continue
			}
			name := cmd.Name
			desc := cmd.Description
			if cmd.Source == runtime.SourceSkill && !strings.HasPrefix(name, "skill:") {
				name = "skill:" + cmd.Name
			}
			if cmd.Source == runtime.SourceSkill && desc == "" {
				desc = "(skill)"
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			item := gen.AvailableCommand{Name: name, Description: desc}
			if cmd.ArgumentHint != "" {
				item.Input = hintInput(cmd.ArgumentHint)
			}
			commands = append(commands, item)
		}
		return gen.CommandListResult{Commands: commands}
	}
	// Static fallback keeps the API usable when no registry was assembled.
	commands = append(commands,
		gen.AvailableCommand{Name: "help", Description: "List available slash commands"},
		gen.AvailableCommand{Name: "model", Description: "Show or set the session model", Input: hintInput("provider/model-id")},
		gen.AvailableCommand{Name: "session", Description: "Show session stats"},
		gen.AvailableCommand{Name: "status", Description: "Show session status"},
		gen.AvailableCommand{Name: "think", Description: "Show or set the thinking level", Input: hintInput("off|minimal|low|medium|high|xhigh")},
	)
	return gen.CommandListResult{Commands: commands}
}

func (c *CommandService) Execute(ctx context.Context, sessionID string, req gen.CommandRequest) (gen.PromptResponse, *APIError) {
	args := ""
	if req.Arguments != nil {
		args = strings.TrimSpace(*req.Arguments)
	}
	switch req.Command {
	case "help":
		list := c.List()
		names := make([]string, 0, len(list.Commands))
		for _, cmd := range list.Commands {
			names = append(names, "/"+cmd.Name)
		}
		return actionResponse(strings.Join(names, "\n")), nil
	case "status", "session":
		status, apiErr := c.sessions.Status(sessionID, req.Directory)
		if apiErr != nil {
			return gen.PromptResponse{}, apiErr
		}
		model := ""
		if status.Model != nil {
			model = *status.Model
		}
		mode := ""
		if status.Mode != nil {
			mode = *status.Mode
		}
		return actionResponse(fmt.Sprintf("Session: %s\nmodel: %s\nmode: %s\nstatus: %s", sessionID, model, mode, status.Status)), nil
	case "name":
		if args == "" {
			return gen.PromptResponse{}, InvalidParams("usage: /name <name>")
		}
		if apiErr := c.sessions.Rename(sessionID, req.Directory, args); apiErr != nil {
			return gen.PromptResponse{}, apiErr
		}
		return actionResponse("Session name set: " + args), nil
	case "resume":
		if args == "" {
			text, err := c.sessions.ResumeList(req.Directory)
			if err != nil {
				return gen.PromptResponse{}, Internal(err.Error())
			}
			return actionResponse(text), nil
		}
		n, convErr := strconv.Atoi(args)
		if convErr != nil || n < 1 {
			return gen.PromptResponse{}, InvalidParams("usage: /resume <n> (run /resume to list sessions)")
		}
		text, err := c.sessions.ResumeSelect(req.Directory, n)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse(text), nil
	case "model":
		if args == "" {
			status, apiErr := c.sessions.Status(sessionID, req.Directory)
			if apiErr != nil {
				return gen.PromptResponse{}, apiErr
			}
			model := ""
			if status.Model != nil {
				model = *status.Model
			}
			return actionResponse("model: " + model), nil
		}
		if _, apiErr := c.sessions.UpdateConfig(sessionID, gen.UpdateSessionRequest{Directory: req.Directory, Model: &args}); apiErr != nil {
			return gen.PromptResponse{}, apiErr
		}
		return actionResponse("model set to " + args), nil
	case "think", "effect":
		if args == "" {
			status, apiErr := c.sessions.Status(sessionID, req.Directory)
			if apiErr != nil {
				return gen.PromptResponse{}, apiErr
			}
			level := ""
			if status.ThinkingLevel != nil {
				level = *status.ThinkingLevel
			}
			return actionResponse("thinking: " + level), nil
		}
		if _, apiErr := c.sessions.UpdateConfig(sessionID, gen.UpdateSessionRequest{Directory: req.Directory, ThinkingLevel: &args}); apiErr != nil {
			return gen.PromptResponse{}, apiErr
		}
		return actionResponse("thinking set to " + args), nil
	case "compact":
		if c.compact == nil {
			return gen.PromptResponse{}, Internal("compaction backend is not configured")
		}
		text, err := c.compact(ctx, sessionID, req.Directory)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse(text), nil
	case "dream":
		if c.dream == nil {
			return gen.PromptResponse{}, Internal("dream backend is not configured")
		}
		text, err := c.dream(ctx, args)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse(text), nil
	case "fork":
		if args == "" {
			list, err := c.sessions.ForkChoices(sessionID, req.Directory)
			if err != nil {
				return gen.PromptResponse{}, Internal(err.Error())
			}
			return actionResponse(list), nil
		}
		n, convErr := strconv.Atoi(args)
		if convErr != nil || n < 1 {
			return gen.PromptResponse{}, InvalidParams("usage: /fork <n> (run /fork to list choices)")
		}
		id, err := c.sessions.Fork(sessionID, req.Directory, n)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse("forked session " + id), nil
	case "clone":
		id, err := c.sessions.Clone(sessionID, req.Directory)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse("cloned session " + id), nil
	case "tree":
		n := 0
		if args != "" {
			parsed, convErr := strconv.Atoi(args)
			if convErr != nil || parsed < 1 {
				return gen.PromptResponse{}, InvalidParams("usage: /tree [n]")
			}
			n = parsed
		}
		text, err := c.sessions.Tree(sessionID, req.Directory, n)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse(text), nil
	case "export":
		text, err := c.sessions.Export(sessionID, req.Directory, args)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse(text), nil
	case "import":
		if args == "" {
			return gen.PromptResponse{}, InvalidParams("usage: /import <path.jsonl>")
		}
		text, err := c.sessions.Import(req.Directory, args)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse(text), nil
	case "copy":
		text, err := c.sessions.CopyLast(sessionID, req.Directory)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse(text), nil
	case "btw":
		if args == "" {
			return actionResponse("usage: /btw <question>"), nil
		}
		if c.prompts == nil {
			return gen.PromptResponse{}, Internal("prompt runner is not configured")
		}
		created, apiErr := c.sessions.Create(gen.NewSessionRequest{Directory: req.Directory})
		if apiErr != nil {
			return gen.PromptResponse{}, apiErr
		}
		model, thinking := c.sessionOptions(created.SessionId, req.Directory)
		resp, apiErr := c.prompts.SubmitSync(created.SessionId, gen.PromptRequest{
			Directory:     req.Directory,
			Prompt:        []map[string]interface{}{{"type": "text", "text": args}},
			Model:         &model,
			ThinkingLevel: &thinking,
		})
		if apiErr != nil {
			return gen.PromptResponse{}, apiErr
		}
		text := "btw (side session " + created.SessionId + ")"
		if resp.Text != nil && *resp.Text != "" {
			text += ":\n" + *resp.Text
		}
		return actionResponse(text), nil
	case "remote-control":
		if c.remote == nil {
			return gen.PromptResponse{}, Internal("remote control backend is not configured")
		}
		text, err := c.remote.Run(sessionID, args)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse(text), nil
	case "goal":
		if c.goal == nil {
			return gen.PromptResponse{}, Internal("goal backend is not configured")
		}
		var out goalOutput
		out.broker = c.broker
		out.sessionID = sessionID
		steering := func() []string {
			if c.prompts == nil {
				return nil
			}
			return c.prompts.DrainSteering(sessionID)
		}
		text, err := c.goal(ctx, sessionID, req.Directory, args, &out, c.prompts.beforeToolCall(sessionID, req.Directory), steering)
		if err != nil {
			return gen.PromptResponse{}, Internal(err.Error())
		}
		return actionResponse(text), nil
	case "exit", "quit", "rewind":
		return actionResponse("/" + req.Command + " is not available over serve yet"), nil
	}
	if c.slash == nil {
		return gen.PromptResponse{}, &APIError{Status: http.StatusBadRequest, Code: CodeInvalidParams, Message: "unknown command: " + req.Command}
	}
	line := "/" + req.Command
	if args != "" {
		line += " " + args
	}
	outcome, err := c.slash.ResolveOutcome(line)
	if err != nil && strings.HasPrefix(req.Command, "skill:") {
		// Backward-compatible fallback for registries that still use the bare
		// skill name: resolve /skill:<name> as /<name>.
		outcome, err = c.slash.ResolveOutcome("/" + strings.TrimPrefix(req.Command, "skill:"))
	}
	if err != nil {
		return gen.PromptResponse{}, &APIError{Status: http.StatusBadRequest, Code: CodeInvalidParams, Message: err.Error()}
	}
	if !outcome.Handled {
		return gen.PromptResponse{}, &APIError{Status: http.StatusBadRequest, Code: CodeInvalidParams, Message: "unknown command: " + req.Command}
	}
	if outcome.Kind == runtime.SlashAction || outcome.Prompt == "" {
		return actionResponse(outcome.Message), nil
	}
	if c.prompts == nil {
		return gen.PromptResponse{}, Internal("prompt runner is not configured")
	}
	model, thinking := c.sessionOptions(sessionID, req.Directory)
	resp, apiErr := c.prompts.SubmitSync(sessionID, gen.PromptRequest{
		Directory:     req.Directory,
		Prompt:        []map[string]interface{}{{"type": "text", "text": outcome.Prompt}},
		Model:         &model,
		ThinkingLevel: &thinking,
	})
	if apiErr != nil {
		return gen.PromptResponse{}, apiErr
	}
	if outcome.Message != "" {
		if resp.Text == nil {
			resp.Text = &outcome.Message
		} else {
			text := outcome.Message + "\n" + *resp.Text
			resp.Text = &text
		}
	}
	return resp, nil
}

func (c *CommandService) sessionOptions(sessionID, directory string) (string, string) {
	status, apiErr := c.sessions.Status(sessionID, directory)
	if apiErr != nil {
		return "", ""
	}
	model := ""
	if status.Model != nil {
		model = *status.Model
	}
	thinking := ""
	if status.ThinkingLevel != nil {
		thinking = *status.ThinkingLevel
	}
	return model, thinking
}

func hintInput(hint string) *struct {
	Hint *string `json:"hint,omitempty"`
} {
	return &struct {
		Hint *string `json:"hint,omitempty"`
	}{Hint: &hint}
}

func actionResponse(text string) gen.PromptResponse {
	return gen.PromptResponse{MessageId: newMessageID(), StopReason: "end_turn", Text: &text}
}

func strPtr(s string) *string {
	return &s
}

// goalOutput buffers goal-loop progress while publishing it to the session's
// SSE event stream so ACP clients see the autonomous run live.
type goalOutput struct {
	broker    *EventBroker
	sessionID string
	b         strings.Builder
}

func (w *goalOutput) Write(p []byte) (int, error) {
	n, err := w.b.Write(p)
	if w.broker != nil && len(p) > 0 {
		w.broker.Publish("message.part.delta", map[string]any{
			"sessionId": w.sessionID,
			"partId":    "goal",
			"delta":     string(p),
		})
	}
	return n, err
}
