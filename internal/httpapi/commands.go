package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/smallnest/pigo/internal/httpapi/gen"
	"github.com/smallnest/pigo/internal/runtime"
)

// CommandService exposes slash commands over HTTP.
type CommandService struct {
	sessions *SessionService
	prompts  *PromptManager
	slash    *runtime.SlashRegistry
}

// NewCommandService builds a command service. slash carries the full pigo
// command registry (skills, plugins, prompt templates and built-ins); when nil
// the service falls back to a small static list.
func NewCommandService(sessions *SessionService, prompts *PromptManager, slash *runtime.SlashRegistry) *CommandService {
	return &CommandService{sessions: sessions, prompts: prompts, slash: slash}
}

func (c *CommandService) List() gen.CommandListResult {
	commands := []gen.AvailableCommand{
		{Name: "name", Description: "Set the session display name", Input: hintInput("<name>")},
	}
	if c.slash != nil {
		for _, cmd := range c.slash.List() {
			item := gen.AvailableCommand{Name: cmd.Name, Description: cmd.Description}
			if cmd.ArgumentHint != "" {
				item.Input = hintInput(cmd.ArgumentHint)
			}
			commands = append(commands, item)
		}
		return gen.CommandListResult{Commands: commands}
	}
	// Static fallback keeps the API usable when no registry was assembled.
	commands = append(commands,
		gen.AvailableCommand{Name: "compact", Description: "Manually compact the session context", Input: hintInput("optional custom instructions")},
		gen.AvailableCommand{Name: "help", Description: "List available slash commands"},
		gen.AvailableCommand{Name: "model", Description: "Show or set the session model", Input: hintInput("provider/model-id")},
		gen.AvailableCommand{Name: "session", Description: "Show session stats"},
		gen.AvailableCommand{Name: "status", Description: "Show session status"},
		gen.AvailableCommand{Name: "think", Description: "Show or set the thinking level", Input: hintInput("off|minimal|low|medium|high|xhigh")},
	)
	return gen.CommandListResult{Commands: commands}
}

func (c *CommandService) Execute(sessionID string, req gen.CommandRequest) (gen.PromptResponse, *APIError) {
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
	case "think":
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
		return actionResponse("compaction is not implemented in serve yet"), nil
	case "exit", "quit", "fork", "clone", "tree", "rewind", "export", "import", "copy", "goal", "btw", "dream", "remote-control":
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
