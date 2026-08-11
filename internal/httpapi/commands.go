package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/smallnest/pigo/internal/httpapi/gen"
)

// CommandService exposes slash commands over HTTP.
type CommandService struct {
	sessions *SessionService
}

// NewCommandService builds a command service.
func NewCommandService(sessions *SessionService) *CommandService {
	return &CommandService{sessions: sessions}
}

func (c *CommandService) List() gen.CommandListResult {
	commands := []gen.AvailableCommand{
		{Name: "compact", Description: "Manually compact the session context", Input: &struct {
			Hint *string `json:"hint,omitempty"`
		}{Hint: strPtr("optional custom instructions")}},
		{Name: "help", Description: "List available slash commands"},
		{Name: "model", Description: "Show or set the session model", Input: &struct {
			Hint *string `json:"hint,omitempty"`
		}{Hint: strPtr("provider/model-id")}},
		{Name: "name", Description: "Set the session display name", Input: &struct {
			Hint *string `json:"hint,omitempty"`
		}{Hint: strPtr("<name>")}},
		{Name: "session", Description: "Show session stats"},
		{Name: "status", Description: "Show session status"},
		{Name: "think", Description: "Show or set the thinking level", Input: &struct {
			Hint *string `json:"hint,omitempty"`
		}{Hint: strPtr("off|minimal|low|medium|high|xhigh")}},
	}
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
	default:
		return gen.PromptResponse{}, &APIError{Status: http.StatusBadRequest, Code: CodeInvalidParams, Message: "unknown command: " + req.Command}
	}
}

func actionResponse(text string) gen.PromptResponse {
	return gen.PromptResponse{MessageId: newMessageID(), StopReason: "end_turn", Text: &text}
}

func strPtr(s string) *string {
	return &s
}
