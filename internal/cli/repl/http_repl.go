package repl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpclient"
)

// RunHTTP runs a serve-backed REPL through an in-process HTTP client.
func RunHTTP(ctx context.Context, cfg httpapi.Config, in io.Reader, out io.Writer) error {
	handler, err := httpapi.NewRouter(cfg)
	if err != nil {
		return err
	}
	client, err := httpclient.InProcessClient(handler)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	sessionResp, err := client.CreateSessionWithResponse(ctx, httpclient.CreateSessionJSONRequestBody{Directory: cwd})
	if err != nil || sessionResp.JSON200 == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("repl: create session failed")
	}
	sessionID := sessionResp.JSON200.SessionId
	_, _ = fmt.Fprintf(out, "session: %s\n", sessionID)

	scanner := bufio.NewScanner(in)
	for {
		_, _ = fmt.Fprint(out, "> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			return nil
		}
		if strings.HasPrefix(line, "/") {
			parts := strings.SplitN(line, " ", 2)
			command := strings.TrimPrefix(parts[0], "/")
			args := ""
			if len(parts) == 2 {
				args = parts[1]
			}
			resp, err := client.ExecuteCommandWithResponse(ctx, sessionID, httpclient.ExecuteCommandJSONRequestBody{
				Directory: cwd,
				Command:   command,
				Arguments: &args,
			})
			if err != nil {
				_, _ = fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			if resp.JSON200 != nil && resp.JSON200.Text != nil {
				_, _ = fmt.Fprintln(out, *resp.JSON200.Text)
			}
			continue
		}
		resp, err := client.PromptSessionWithResponse(ctx, sessionID, httpclient.PromptSessionJSONRequestBody{
			Directory: cwd,
			Prompt:    []map[string]interface{}{{"type": "text", "text": line}},
		})
		if err != nil {
			_, _ = fmt.Fprintf(out, "error: %v\n", err)
			continue
		}
		if resp.JSON200 == nil {
			_, _ = fmt.Fprintln(out, "no response")
			continue
		}
		if resp.JSON200.Text != nil {
			_, _ = fmt.Fprintln(out, *resp.JSON200.Text)
		} else {
			_, _ = fmt.Fprintln(out, resp.JSON200.StopReason)
		}
	}
}
