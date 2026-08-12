package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/cli/headless"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/cli/ui"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpclient"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

// RunHTTP runs the full-screen TUI through the serve HTTP API using an
// in-process client. It is the serve-backed counterpart to Run.
func RunHTTP(ctx context.Context, opts Options, cfg httpapi.Config) error {
	handler, err := httpapi.NewRouter(cfg)
	if err != nil {
		return err
	}
	client, err := httpclient.InProcessClient(handler)
	if err != nil {
		return err
	}
	s, history, err := newHTTPSession(ctx, opts, client)
	if err != nil {
		return err
	}
	p := tea.NewProgram(NewModel(opts).withSession(s, history))
	_, err = p.Run()
	return err
}

// newHTTPSession assembles the TUI session state from the serve API: it
// creates or loads the server-side session and keeps the local collaborators
// needed by the shell (slash registry, status bar, telemetry holder) without
// starting a direct runtime bridge.
func newHTTPSession(ctx context.Context, opts Options, client *httpclient.ClientWithResponses) (*runSession, []agentcore.Message, error) {
	cwd, _ := os.Getwd()
	store, err := headless.ProjectStore()
	if err != nil {
		return nil, nil, err
	}
	var (
		sessionID string
		history   agentcore.MessageList
		header    session.SessionHeader
	)
	if opts.ResumeID != "" {
		if home, homeErr := sessionstore.PigoHome(); homeErr == nil && home != "" {
			_ = headless.EnsureProjectSession(home, cwd, opts.ResumeID)
		}
		limit := 200
		resp, err := client.LoadSessionWithResponse(ctx, opts.ResumeID, httpclient.LoadSessionJSONRequestBody{
			Directory: cwd,
			Limit:     &limit,
		})
		if err != nil {
			return nil, nil, err
		}
		if resp.JSON200 == nil {
			return nil, nil, fmt.Errorf("tui: load session failed")
		}
		sessionID = resp.JSON200.SessionId
		history = domainMessagesToAgent(resp.JSON200.Messages)
		header = session.SessionHeader{
			ID:        sessionID,
			Model:     currentConfigOption(resp.JSON200.ConfigOptions, "model"),
			Provider:  opts.ProviderName,
			CreatedAt: time.Now().UTC(),
			Cwd:       cwd,
		}
	} else {
		resp, err := client.CreateSessionWithResponse(ctx, httpclient.CreateSessionJSONRequestBody{Directory: cwd})
		if err != nil {
			return nil, nil, err
		}
		if resp.JSON200 == nil {
			return nil, nil, fmt.Errorf("tui: create session failed")
		}
		sessionID = resp.JSON200.SessionId
		header = session.SessionHeader{
			ID:        sessionID,
			Model:     currentConfigOption(resp.JSON200.ConfigOptions, "model"),
			Provider:  opts.ProviderName,
			CreatedAt: time.Now().UTC(),
			Cwd:       cwd,
		}
	}
	if header.Model == "" {
		header.Model = opts.Model
	}
	if header.Provider == "" {
		header.Provider = opts.ProviderName
	}

	live := &cli.LiveConfig{
		Model:         header.Model,
		ProviderName:  opts.ProviderName,
		Provider:      opts.Provider,
		BaseURL:       opts.BaseURL,
		Protocol:      opts.Protocol,
		ThinkingLevel: opts.ThinkingLevel,
		ContextWindow: cli.DefaultContextWindow,
	}
	creds := provider.NewCredentialStore(nil)
	creds.SetOverride(opts.ProviderName, opts.APIKey)
	mgr, mgrErr := trust.NewManager(trust.DefaultPath())
	if mgrErr != nil {
		fmt.Fprintf(os.Stderr, "pigo: trust store unavailable, trust disabled: %v\n", mgrErr)
		mgr = nil
	}
	s := &runSession{
		store:      store,
		header:     header,
		agentCtx:   &agentcore.AgentContext{SystemPrompt: opts.SysPrompt, Messages: history, Tools: opts.Tools},
		live:       live,
		reg:        run.ToolRegistry(opts.Tools),
		reminders:  run.TodoReminders(opts.Tools),
		creds:      creds,
		cwd:        cwd,
		trust:      mgr,
		slash:      newSlashRegistry(opts, live),
		telemetry:  cli.NewTelemetryHolder(),
		persisted:  len(history),
		memoryRoot: run.MemoryRootFromTools(opts.Tools),
		memstore:   run.MemoryStoreFromTools(opts.Tools),
		httpClient: client,
		httpDir:    cwd,
	}
	if opts.Approve && mgr != nil {
		mgr.SetSessionTrust(cwd)
	}
	trust.RegisterCommand(s.slash, mgr, cwd)
	return s, history, nil
}

// httpStartRun starts a prompt through prompt_async and pumps the session's
// SSE domain events back into Bubble Tea messages.
func (s *runSession) httpStartRun(prompt string) (chan tea.Msg, tea.Cmd) {
	ch := newEventChan()
	content, err := ui.BuildUserContent(prompt)
	if err != nil {
		content = agentcore.ContentList{agentcore.NewTextContent(prompt)}
	}
	model := s.live.Model
	thinking := string(s.live.ThinkingLevel)
	ctx, cancel := context.WithCancel(context.Background())
	s.httpCancel = cancel
	resp, err := s.httpClient.PromptSessionAsyncWithResponse(ctx, s.header.ID, httpclient.PromptSessionAsyncJSONRequestBody{
		Directory:     s.httpDir,
		Prompt:        contentToDomainBlocks(content),
		Model:         &model,
		ThinkingLevel: &thinking,
	})
	if err != nil || resp.JSON202 == nil {
		cancel()
		if err == nil {
			err = fmt.Errorf("prompt was not accepted")
		}
		go func() { ch <- runEndMsg{err: err} }()
		return ch, waitForEvent(ch)
	}
	go s.httpPump(ctx, ch, resp.JSON202.MessageId)
	return ch, waitForEvent(ch)
}

// httpPump reads the SSE stream after the session cursor and converts each
// domain event into the matching tea.Msg until the turn reaches an idle,
// cancelled, or error status.
func (s *runSession) httpPump(ctx context.Context, ch chan tea.Msg, messageID string) {
	defer func() {
		if s.httpCancel != nil {
			s.httpCancel()
		}
		s.httpCancel = nil
	}()
	seenTools := make(map[string]bool)
	after := int(s.httpCursor)
	for {
		resp, err := s.httpClient.GetEvents(ctx, &httpclient.GetEventsParams{SessionId: &s.header.ID, After: &after})
		if err != nil {
			if ctx.Err() != nil {
				ch <- runEndMsg{}
			} else {
				ch <- runEndMsg{err: err}
			}
			return
		}
		ended := s.drainEventStream(ctx, resp, ch, messageID, seenTools, &after)
		_ = resp.Body.Close()
		if ended {
			return
		}
		if ctx.Err() != nil {
			ch <- runEndMsg{}
			return
		}
		select {
		case <-ctx.Done():
			ch <- runEndMsg{}
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *runSession) drainEventStream(ctx context.Context, resp *http.Response, ch chan tea.Msg, messageID string, seenTools map[string]bool, after *int) bool {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	eventType := ""
	var dataBuf strings.Builder
	flush := func() bool {
		if eventType == "" {
			return false
		}
		var envelope struct {
			ID   int64          `json:"id"`
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal([]byte(dataBuf.String()), &envelope)
		if envelope.ID > 0 {
			*after = int(envelope.ID)
			s.httpCursor = envelope.ID
		}
		ended := s.mapHTTPEvent(ctx, ch, messageID, seenTools, eventType, envelope.Data)
		eventType = ""
		dataBuf.Reset()
		return ended
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			if flush() {
				return true
			}
		}
		if ctx.Err() != nil {
			return false
		}
	}
	return false
}

func (s *runSession) mapHTTPEvent(ctx context.Context, ch chan tea.Msg, messageID string, seenTools map[string]bool, eventType string, data map[string]any) bool {
	switch eventType {
	case "message.part.delta":
		if delta, ok := data["delta"].(string); ok && delta != "" {
			ch <- textDeltaMsg{delta: delta}
		}
	case "tool.updated":
		id, _ := data["toolCallId"].(string)
		if id == "" {
			return false
		}
		title, _ := data["title"].(string)
		status, _ := data["status"].(string)
		output, _ := data["output"].(string)
		switch status {
		case "pending", "in_progress":
			if !seenTools[id] {
				seenTools[id] = true
				ch <- toolStartMsg{id: id, name: title, input: rawInputMap(data["rawInput"])}
			}
			if output != "" {
				ch <- toolUpdateMsg{id: id, partial: output}
			}
		case "completed", "failed":
			if !seenTools[id] {
				seenTools[id] = true
				ch <- toolStartMsg{id: id, name: title, input: rawInputMap(data["rawInput"])}
			}
			ch <- toolEndMsg{id: id, ok: status == "completed", result: output}
		}
	case "session.status":
		status, _ := data["status"].(string)
		switch status {
		case "telemetry":
			if usage, ok := data["contextUsage"].(map[string]any); ok {
				used, _ := usage["used"].(float64)
				size, _ := usage["size"].(float64)
				ch <- telemetryMsg{ev: agentcore.TelemetryEvent{ContextTokens: int(used), ContextWindow: int(size)}}
			}
		case "compacting":
			ch <- compactionStartMsg{}
		case "compacted", "compaction_failed":
			ch <- compactionMsg{}
		case "error":
			msg, _ := data["error"].(string)
			if msg == "" {
				msg = "prompt failed"
			}
			s.refreshHTTPMessages(ctx)
			ch <- runEndMsg{err: fmt.Errorf("%s", msg)}
			return true
		case "idle", "cancelled":
			if mid, ok := data["messageId"].(string); !ok || mid == messageID {
				s.refreshHTTPMessages(ctx)
				ch <- runEndMsg{}
				return true
			}
		}
	case "permission.asked":
		if pid, ok := data["permissionId"].(string); ok {
			_, _ = s.httpClient.ReplyPermissionWithResponse(ctx, s.header.ID, pid, httpclient.ReplyPermissionJSONRequestBody{OptionId: "reject_once"})
		}
	}
	return false
}

func (s *runSession) refreshHTTPMessages(ctx context.Context) {
	limit := 200
	resp, err := s.httpClient.GetSessionMessagesWithResponse(ctx, s.header.ID, &httpclient.GetSessionMessagesParams{
		Directory: s.httpDir,
		Limit:     &limit,
	})
	if err != nil || resp.JSON200 == nil {
		return
	}
	s.agentCtx.Messages = domainMessagesToAgent(resp.JSON200.Messages)
}

// resumeCandidates lists the saved sessions for the current workspace.
func (s *runSession) resumeCandidates() ([]sessionstore.Metadata, error) {
	home, err := sessionstore.PigoHome()
	if err != nil {
		return nil, err
	}
	store, err := sessionstore.OpenForWorkspace(home, s.httpDir)
	if err != nil {
		return nil, err
	}
	metas, err := store.List()
	if err != nil {
		return nil, err
	}
	filtered := make([]sessionstore.Metadata, 0, len(metas))
	for _, meta := range metas {
		if meta.SessionKind == sessionstore.SessionKindSubagent || strings.HasPrefix(meta.SessionID, "subagent-") {
			continue
		}
		filtered = append(filtered, meta)
	}
	return filtered, nil
}

// switchHTTPSession loads another persisted session through serve and replaces
// the live TUI session state with it.
func (s *runSession) switchHTTPSession(ctx context.Context, sessionID string) ([]agentcore.Message, error) {
	if s.httpClient == nil {
		return nil, fmt.Errorf("serve session is not available")
	}
	limit := 200
	resp, err := s.httpClient.LoadSessionWithResponse(ctx, sessionID, httpclient.LoadSessionJSONRequestBody{
		Directory: s.httpDir,
		Limit:     &limit,
	})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("load session failed")
	}
	history := domainMessagesToAgent(resp.JSON200.Messages)
	s.header = session.SessionHeader{
		ID:        resp.JSON200.SessionId,
		Model:     currentConfigOption(resp.JSON200.ConfigOptions, "model"),
		Provider:  s.live.ProviderName,
		CreatedAt: time.Now().UTC(),
		Cwd:       s.httpDir,
	}
	if s.header.Model == "" {
		s.header.Model = s.live.Model
	}
	s.agentCtx.Messages = history
	s.live.Model = s.header.Model
	s.persisted = len(history)
	s.curLeaf = ""
	s.compacted = false
	if s.telemetry != nil {
		s.telemetry.Reset()
	}
	return history, nil
}

func contentToDomainBlocks(content agentcore.ContentList) []map[string]interface{} {
	blocks := make([]map[string]interface{}, 0, len(content))
	for _, c := range content {
		switch b := c.(type) {
		case agentcore.TextContent:
			blocks = append(blocks, map[string]interface{}{"type": "text", "text": b.Text})
		case agentcore.ImageContent:
			blocks = append(blocks, map[string]interface{}{"type": "image", "data": b.Data, "mimeType": b.MimeType})
		}
	}
	return blocks
}

func domainMessagesToAgent(msgs []httpclient.Message) agentcore.MessageList {
	out := make(agentcore.MessageList, 0, len(msgs))
	for _, m := range msgs {
		content := domainBlocksToAgentContent(m.Content)
		switch m.Role {
		case agentcore.RoleUser:
			out = append(out, agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: content})
		case agentcore.RoleAssistant:
			out = append(out, agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: content})
		case agentcore.RoleToolResult:
			out = append(out, agentcore.ToolResultMessage{RoleField: agentcore.RoleToolResult, Content: content})
		}
	}
	return out
}

func domainBlocksToAgentContent(blocks []map[string]interface{}) agentcore.ContentList {
	out := make(agentcore.ContentList, 0, len(blocks))
	for _, b := range blocks {
		switch b["type"] {
		case "text":
			if s, ok := b["text"].(string); ok {
				out = append(out, agentcore.NewTextContent(s))
			}
		case "thinking":
			if s, ok := b["thinking"].(string); ok {
				out = append(out, agentcore.NewThinkingContent(s))
			}
		case "toolCall":
			id, _ := b["id"].(string)
			name, _ := b["name"].(string)
			args, _ := json.Marshal(b["arguments"])
			out = append(out, agentcore.NewToolCallContent(id, name, args))
		}
	}
	return out
}

func rawInputMap(raw any) map[string]any {
	switch v := raw.(type) {
	case map[string]any:
		return v
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(v), &m) == nil {
			return m
		}
	case []byte:
		var m map[string]any
		if json.Unmarshal(v, &m) == nil {
			return m
		}
	}
	return nil
}

func currentConfigOption(options []httpclient.ConfigOption, id string) string {
	for _, o := range options {
		if o.Id == id && o.CurrentValue != nil {
			return *o.CurrentValue
		}
	}
	return ""
}
