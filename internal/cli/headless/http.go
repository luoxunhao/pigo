package headless

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpclient"
)

// RunHTTPOnce runs a single prompt through serve using the in-process HTTP client.
func RunHTTPOnce(ctx context.Context, cfg httpapi.Config, prompt string, out io.Writer) error {
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
		return fmt.Errorf("headless: create session failed")
	}
	resp, err := client.PromptSessionWithResponse(ctx, sessionResp.JSON200.SessionId, httpclient.PromptSessionJSONRequestBody{
		Directory: cwd,
		Prompt:    []map[string]interface{}{{"type": "text", "text": prompt}},
	})
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return fmt.Errorf("headless: prompt failed")
	}
	if resp.JSON200.Text != nil {
		_, _ = fmt.Fprintln(out, *resp.JSON200.Text)
	} else {
		_, _ = fmt.Fprintln(out, resp.JSON200.StopReason)
	}
	return nil
}
