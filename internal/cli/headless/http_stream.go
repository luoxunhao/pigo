package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpclient"
	"github.com/smallnest/pigo/internal/sessionstore"
)

// RunHTTPStream runs one prompt through serve and emits the stream-json
// envelope shape consumed by --output-format stream-json. It returns a process
// exit code.
func RunHTTPStream(ctx context.Context, cfg httpapi.Config, prompt, resumeID string, out, errOut io.Writer) int {
	handler, err := httpapi.NewRouter(cfg)
	if err != nil {
		fmt.Fprintf(errOut, "pigo: %v\n", err)
		return 1
	}
	client, err := httpclient.InProcessClient(handler)
	if err != nil {
		fmt.Fprintf(errOut, "pigo: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(errOut, "pigo: %v\n", err)
		return 1
	}
	sessionID := ""
	if resumeID != "" {
		if home, homeErr := sessionstore.PigoHome(); homeErr == nil && home != "" {
			_ = EnsureProjectSession(home, cwd, resumeID)
		}
		limit := 200
		resp, err := client.LoadSessionWithResponse(ctx, resumeID, httpclient.LoadSessionJSONRequestBody{
			Directory: cwd,
			Limit:     &limit,
		})
		if err != nil {
			fmt.Fprintf(errOut, "pigo: %v\n", err)
			return 1
		}
		if resp.JSON200 == nil {
			fmt.Fprintln(errOut, "pigo: load session failed")
			return 1
		}
		sessionID = resp.JSON200.SessionId
	} else {
		resp, err := client.CreateSessionWithResponse(ctx, httpclient.CreateSessionJSONRequestBody{Directory: cwd})
		if err != nil {
			fmt.Fprintf(errOut, "pigo: %v\n", err)
			return 1
		}
		if resp.JSON200 == nil {
			fmt.Fprintln(errOut, "pigo: create session failed")
			return 1
		}
		sessionID = resp.JSON200.SessionId
	}

	writeJSON(out, map[string]any{"type": "agent_start", "sessionId": sessionID})
	resp, err := client.PromptSessionAsyncWithResponse(ctx, sessionID, httpclient.PromptSessionAsyncJSONRequestBody{
		Directory: cwd,
		Prompt:    []map[string]interface{}{{"type": "text", "text": prompt}},
	})
	if err != nil || resp.JSON202 == nil {
		if err == nil {
			err = fmt.Errorf("prompt was not accepted")
		}
		fmt.Fprintf(errOut, "pigo: %v\n", err)
		return 1
	}

	code := drainHTTPStream(ctx, client, sessionID, resp.JSON202.MessageId, cwd, out)
	if code != 0 {
		return code
	}
	return 0
}

func drainHTTPStream(ctx context.Context, client *httpclient.ClientWithResponses, sessionID, messageID, cwd string, out io.Writer) int {
	seenTools := make(map[string]bool)
	var text strings.Builder
	after := 0
	for {
		resp, err := client.GetEvents(ctx, &httpclient.GetEventsParams{SessionId: &sessionID, After: &after})
		if err != nil {
			fmt.Fprintf(out, `{"type":"agent_end","error":%q}`+"\n", err.Error())
			return 1
		}
		ended, code := drainHTTPEvents(ctx, client, resp, sessionID, messageID, cwd, out, seenTools, &text, &after)
		_ = resp.Body.Close()
		if ended {
			return code
		}
		if ctx.Err() != nil {
			writeJSON(out, map[string]any{"type": "agent_end", "stopReason": "aborted"})
			return 1
		}
		select {
		case <-ctx.Done():
			writeJSON(out, map[string]any{"type": "agent_end", "stopReason": "aborted"})
			return 1
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func drainHTTPEvents(ctx context.Context, client *httpclient.ClientWithResponses, resp *http.Response, sessionID, messageID, cwd string, out io.Writer, seenTools map[string]bool, text *strings.Builder, after *int) (bool, int) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	eventType := ""
	var dataBuf strings.Builder
	flush := func() (bool, int) {
		if eventType == "" {
			return false, 0
		}
		var envelope struct {
			ID   int64          `json:"id"`
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal([]byte(dataBuf.String()), &envelope)
		if envelope.ID > 0 {
			*after = int(envelope.ID)
		}
		typ := eventType
		eventType = ""
		dataBuf.Reset()
		return mapHTTPStreamEvent(ctx, client, sessionID, messageID, cwd, out, seenTools, text, envelope.Data, typ)
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			if ended, code := flush(); ended {
				return true, code
			}
		}
		if ctx.Err() != nil {
			return false, 0
		}
	}
	return false, 0
}

func mapHTTPStreamEvent(ctx context.Context, client *httpclient.ClientWithResponses, sessionID, messageID, cwd string, out io.Writer, seenTools map[string]bool, text *strings.Builder, data map[string]any, eventType string) (bool, int) {
	switch eventType {
	case "message.part.delta":
		if delta, ok := data["delta"].(string); ok && delta != "" {
			text.WriteString(delta)
			writeJSON(out, map[string]any{"type": "message_update", "text": text.String()})
		}
	case "tool.updated":
		id, _ := data["toolCallId"].(string)
		if id == "" {
			return false, 0
		}
		name, _ := data["title"].(string)
		status, _ := data["status"].(string)
		switch status {
		case "pending", "in_progress":
			if !seenTools[id] {
				seenTools[id] = true
				writeJSON(out, map[string]any{"type": "tool_execution_start", "toolCallId": id, "toolName": name})
			}
		case "completed", "failed":
			writeJSON(out, map[string]any{"type": "tool_execution_end", "toolCallId": id, "toolName": name, "isError": status == "failed"})
		}
	case "session.status":
		status, _ := data["status"].(string)
		switch status {
		case "telemetry":
			usage, _ := data["contextUsage"].(map[string]any)
			writeJSON(out, map[string]any{"type": "telemetry", "contextTokens": number(usage, "used"), "contextWindow": number(usage, "size")})
		case "compacting":
			writeJSON(out, map[string]any{"type": "compaction_start"})
		case "compacted", "compaction_failed":
			writeJSON(out, map[string]any{"type": "compaction"})
		case "error":
			writeJSON(out, map[string]any{"type": "turn_end", "stopReason": "error"})
			writeJSON(out, map[string]any{"type": "agent_end", "stopReason": "error"})
			return true, 1
		case "idle", "cancelled":
			if mid, ok := data["messageId"].(string); ok && mid != messageID {
				return false, 0
			}
			stopReason := "end_turn"
			if status == "cancelled" {
				stopReason = "aborted"
			}
			writeJSON(out, map[string]any{"type": "turn_end", "stopReason": stopReason, "text": text.String()})
			writeJSON(out, map[string]any{"type": "agent_end", "stopReason": stopReason})
			if status == "cancelled" {
				return true, 1
			}
			return true, 0
		}
	case "permission.asked":
		if pid, ok := data["permissionId"].(string); ok {
			_, _ = client.ReplyPermissionWithResponse(ctx, sessionID, pid, httpclient.ReplyPermissionJSONRequestBody{OptionId: "reject_once"})
		}
	}
	return false, 0
}

func number(data map[string]any, key string) int {
	switch v := data[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func writeJSON(out io.Writer, v map[string]any) {
	b, err := json.Marshal(v)
	if err == nil {
		_, _ = out.Write(append(b, '\n'))
	}
}
