package repl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

// RunACP starts the line REPL through the in-process ACP server (ticket 09).
// It is the line counterpart of tui.RunACP: same server assembly, same client,
// a plain read-run-stream loop instead of Bubble Tea.
func RunACP(opts Options) error {
	home, err := sessionstore.PigoHome()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	mgr, err := trust.NewManager(trust.DefaultPath())
	if err != nil {
		return err
	}
	if opts.Approve {
		mgr.SetSessionTrust(cwd)
	}
	runner := &acp.RuntimeRunner{
		Provider:      opts.Provider,
		ProviderName:  opts.ProviderName,
		Model:         opts.Model,
		APIKey:        opts.APIKey,
		ThinkingLevel: opts.ThinkingLevel,
		Tools:         opts.Tools,
	}
	dreamCfg := &acp.DreamConfig{
		Model:         opts.Model,
		BaseURL:       opts.BaseURL,
		Protocol:      opts.Protocol,
		ProviderName:  opts.ProviderName,
		APIKey:        opts.APIKey,
		ThinkingLevel: opts.ThinkingLevel,
	}
	client, stop := acp.StartInProcess(runner, home, opts.Model, opts.SysPrompt, cwd, mgr, dreamCfg)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		return err
	}
	sessionID := opts.ResumeID
	if sessionID == "" {
		sessionID, err = client.NewSession(ctx, cwd)
	} else {
		sessionID, err = client.LoadSession(ctx, sessionID, cwd)
	}
	if err != nil {
		return err
	}
	return runACPInteractive(client, os.Stdin, os.Stdout, sessionID)
}

// runACPInteractive is the testable core loop: it reads prompts, streams
// session/update text as it arrives, answers permission requests with single
// keys, and runs /commands through pigo/command.
func runACPInteractive(client *acp.Client, in io.Reader, out io.Writer, sessionID string) error {
	reader := bufio.NewReader(in)
	var readMu sync.Mutex
	client.SetPermissionHandler(func(req acp.Request) (any, *acp.Error) {
		readMu.Lock()
		defer readMu.Unlock()
		fmt.Fprintf(out, "\n[permission] %s\n[y]es once / [a]lways / [n]o / [r]eject always / enter=cancel: ", toolSummary(req))
		line, _ := reader.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow_once"}}, nil
		case "a", "always":
			return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow_always"}}, nil
		case "n", "no":
			return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "reject_once"}}, nil
		case "r":
			return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "reject_always"}}, nil
		default:
			return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
		}
	})

	for {
		fmt.Fprintf(out, "> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil // EOF / interrupt
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" {
			return nil
		}
		if strings.HasPrefix(line, "/") {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			text, cmdErr := client.Command(ctx, sessionID, line)
			cancel()
			if cmdErr != nil {
				fmt.Fprintf(out, "error: %v\n", cmdErr)
				continue
			}
			fmt.Fprintln(out, text)
			continue
		}
		fmt.Fprintln(out)
		runACPPrompt(client, out, sessionID, line)
	}
}

func runACPPrompt(client *acp.Client, out io.Writer, sessionID, text string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.Prompt(ctx, sessionID, text)
		done <- err
	}()
	for {
		select {
		case err := <-done:
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
			}
			fmt.Fprintln(out)
			return
		case msg, ok := <-client.Notifications():
			if !ok {
				return
			}
			if msg.Notification != nil && msg.Notification.Method == acp.NotificationSessionUpdate {
				var payload struct {
					Update map[string]any `json:"update"`
				}
				if err := json.Unmarshal(msg.Notification.Params, &payload); err == nil {
					switch payload.Update["sessionUpdate"] {
					case "agent_message_chunk", "agent_thought_chunk":
						fmt.Fprint(out, acpUpdateText(payload.Update))
					case "tool_call":
						title, _ := payload.Update["title"].(string)
						fmt.Fprintf(out, "\n  ▶ %s\n", title)
					case "tool_call_update":
						status, _ := payload.Update["status"].(string)
						fmt.Fprintf(out, "  ✓ %s\n", status)
					}
				}
			}
		}
	}
}

func acpUpdateText(u map[string]any) string {
	content, _ := u["content"].(map[string]any)
	text, _ := content["text"].(string)
	return text
}

func toolSummary(req acp.Request) string {
	var params struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
	}
	_ = json.Unmarshal(req.Params, &params)
	if params.ToolCall.Title != "" {
		return params.ToolCall.Title
	}
	return "tool call"
}
