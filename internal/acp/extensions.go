package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/clipboard"
	"github.com/smallnest/pigo/internal/compaction"
	"github.com/smallnest/pigo/internal/dream"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

// commandFunc executes one pigo slash command against a session.
type commandFunc func(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error)

// buildCommands registers the pigo/command handlers available at the ACP layer.
// Commands whose full logic lands with the TUI migration (tickets 07/08)
// return a structured not-implemented error so the surface is stable.
func buildCommands() map[string]commandFunc {
	return map[string]commandFunc{
		"model":          cmdModel,
		"think":          cmdThink,
		"steering":       cmdSteering,
		"follow-up":      cmdFollowUp,
		"trust":          cmdTrust,
		"status":         cmdStatus,
		"compact":        cmdCompact,
		"session":        cmdSession,
		"help":           cmdHelp,
		"name":           cmdName,
		"changelog":      cmdChangelog,
		"copy":           cmdCopy,
		"export":         cmdExport,
		"import":         cmdImport,
		"rebuild":        cmdRebuild,
		"memory":         cmdMemory,
		"goal":           cmdGoal,
		"btw":            cmdBtw,
		"rewind":         cmdRewind,
		"fork":           cmdFork,
		"tree":           cmdTree,
		"dream":          cmdDream,
		"remote-control": cmdRemoteControl,
	}
}

func cmdModel(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "model: " + sess.Model, nil
	}
	sess.Model = args
	return "model set to " + args, nil
}

func cmdThink(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	args = strings.TrimSpace(args)
	if args == "" {
		if sess.Thinking == "" {
			return "thinking: default", nil
		}
		return "thinking: " + sess.Thinking, nil
	}
	sess.Thinking = args
	return "thinking set to " + args, nil
}

func cmdSteering(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	return setDeliveryMode(&sess.SteeringMode, "steering", args)
}

func cmdFollowUp(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	return setDeliveryMode(&sess.FollowUpMode, "follow-up", args)
}

func setDeliveryMode(mode *string, name, args string) (string, *Error) {
	if *mode == "" {
		*mode = "one-at-a-time"
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return name + " mode: " + *mode, nil
	}
	if args != "all" && args != "one-at-a-time" {
		return "", NewError(CodeInvalidParams, "usage: /"+name+" all|one-at-a-time")
	}
	*mode = args
	return name + " mode set to " + args, nil
}

func cmdName(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	name := strings.TrimSpace(args)
	if name == "" {
		return "usage: /name <name>", nil
	}
	meta, err := sess.Store.LoadMetadata(sess.ID)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	meta.SessionName = name
	if err := sess.Store.SaveMetadata(meta); err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	d.sendSessionUpdate(sess.ID, map[string]any{
		"sessionUpdate": "session_info_update",
		"title":         name,
		"updatedAt":     time.Now().UTC().Format(time.RFC3339),
	})
	return "Session name set: " + name, nil
}

func cmdChangelog(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	path := findChangelogPath()
	if path == "" {
		return "Changelog not found (couldn't locate pigo installation).", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	text := string(data)
	if len(text) > 20000 {
		text = text[:20000] + "\n\n...(truncated)..."
	}
	return text, nil
}

func findChangelogPath() string {
	if exe, err := os.Executable(); err == nil {
		candidates := []string{
			filepath.Join(filepath.Dir(exe), "CHANGELOG.md"),
			filepath.Join(filepath.Dir(exe), "..", "CHANGELOG.md"),
		}
		for _, p := range candidates {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
		}
	}
	for _, p := range []string{"CHANGELOG.md", "../CHANGELOG.md"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func cmdTrust(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	mgr := d.trustMgr
	if mgr == nil {
		return "trust is disabled", nil
	}
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "", "on":
		if err := mgr.SetDecision(sess.Cwd, trust.Trusted); err != nil {
			return "", NewError(CodeInternalError, err.Error())
		}
		return "trusted " + sess.Cwd + " (saved)", nil
	case "off":
		mgr.ClearSessionTrust(sess.Cwd)
		if err := mgr.SetDecision(sess.Cwd, trust.Untrusted); err != nil {
			return "", NewError(CodeInternalError, err.Error())
		}
		return "marked " + sess.Cwd + " untrusted (saved)", nil
	case "once":
		mgr.SetSessionTrust(sess.Cwd)
		return "trusted " + sess.Cwd + " for this session only", nil
	case "status":
		res := mgr.NearestTrustDecision(sess.Cwd)
		if !res.Found {
			return sess.Cwd + ": undecided", nil
		}
		return fmt.Sprintf("%s: %s (decision saved for %s)", sess.Cwd, res.Decision, res.Path), nil
	default:
		return "usage: /trust [on|off|once|status]", nil
	}
}

func cmdStatus(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	return sessionStatusText(sess), nil
}

func notImplementedCommand(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	return "", NewError(CodeNotImplemented, "command not implemented until the TUI migration")
}

// cmdRewind restores file snapshots (when the run journal has points) and
// truncates the session history by the requested number of user turns.
func cmdRewind(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	n := 1
	if args != "" {
		v, err := strconv.Atoi(strings.TrimSpace(args))
		if err != nil || v < 1 {
			return "", NewError(CodeInvalidParams, "usage: /rewind [n]")
		}
		n = v
	}
	restored := 0
	var warnings []string
	if d.snap != nil {
		points := d.snap.Points()
		if len(points) > 0 {
			idx := len(points) - n
			if idx < 0 {
				idx = 0
			}
			_, files, warns, err := d.snap.Restore(idx)
			if err != nil {
				return "", NewError(CodeInternalError, err.Error())
			}
			restored = len(files)
			warnings = warns
		}
	}
	truncateTurns(sess, n)
	if err := persistRewind(sess); err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	msg := fmt.Sprintf("rewound %d turn(s); restored %d file(s)", n, restored)
	if len(warnings) > 0 {
		msg += "; warnings: " + strings.Join(warnings, "; ")
	}
	return msg, nil
}

// cmdFork clones the current session branch into a new persisted session and
// registers it as live.
func cmdFork(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	_, entries, err := sess.Store.TranscriptStore().LoadEntries(sess.ID)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	leaf := ""
	if len(entries) > 0 {
		leaf = entries[len(entries)-1].ID
	}
	now := time.Now().UTC()
	newHeader, _, err := sess.Store.TranscriptStore().Fork(sess.ID, leaf, now)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	meta := sessionstore.NewMetadata(newHeader.ID, "Fork of "+sess.ID, "pigo", sess.Model, sess.Cwd)
	meta.ParentSessionID = sess.ID
	meta.CreatedAt = now
	meta.LastActiveAt = now
	if err := sess.Store.SaveMetadata(meta); err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	if _, err := d.manager.Load(sess.Cwd, newHeader.ID, sess.Model, sess.Store); err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	return "forked to " + newHeader.ID, nil
}

// cmdTree renders the session tree rooted at the current leaf.
func cmdTree(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	_, entries, err := sess.Store.TranscriptStore().LoadEntries(sess.ID)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	leaf := ""
	if len(entries) > 0 {
		leaf = entries[len(entries)-1].ID
	}
	lines := session.RenderTreeLines(entries, leaf)
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		texts = append(texts, line.Text)
	}
	return strings.Join(texts, "\n"), nil
}

func truncateTurns(sess *AcpSession, n int) {
	keep := len(sess.Messages)
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if _, ok := sess.Messages[i].(agentcore.UserMessage); ok {
			n--
			keep = i
			if n == 0 {
				break
			}
		}
	}
	sess.Messages = sess.Messages[:keep]
	if len(sess.Messages) < sess.Persisted {
		sess.Persisted = len(sess.Messages)
	}
}

func persistRewind(sess *AcpSession) error {
	sess.Header.UpdatedAt = time.Now().UTC()
	if err := sess.Store.TranscriptStore().Save(sess.Header, sess.Messages); err != nil {
		return err
	}
	meta, err := sess.Store.LoadMetadata(sess.ID)
	if err != nil {
		return err
	}
	meta.MessageCount = len(sess.Messages)
	return sess.Store.SaveMetadata(meta)
}

// cmdRemoteControl starts or stops the remote-control server (D-04).
func cmdRemoteControl(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	if strings.TrimSpace(args) == "stop" {
		if err := d.stopRemoteControl(); err != nil {
			return "", NewError(CodeInternalError, err.Error())
		}
		return "remote control stopped", nil
	}
	url, rpcErr := d.startRemoteControl(sess)
	if rpcErr != nil {
		return "", rpcErr
	}
	return "remote control: " + url, nil
}

// cmdDream runs the memory-consolidation pipeline for the session project.
func cmdDream(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	if d.dreamCfg == nil {
		return "", NewError(CodeNotImplemented, "dream is not configured")
	}
	cons, err := dream.NewLLMConsolidator(
		d.dreamCfg.Model,
		d.dreamCfg.BaseURL,
		d.dreamCfg.Protocol,
		d.dreamCfg.ProviderName,
		d.dreamCfg.APIKey,
		d.dreamCfg.ThinkingLevel,
	)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	r := &dream.Runner{Consolidator: cons}
	report, err := r.Run(ctx, dream.RunOptions{
		DryRun:     strings.Contains(strings.ToLower(args), "dry"),
		ProjectDir: sess.Cwd,
	})
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	b, _ := json.Marshal(report)
	return string(b), nil
}

// cmdSession prints the live session summary (alias of /status for parity).
func cmdSession(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	return sessionStatusText(sess), nil
}

// cmdCompact runs manual compaction against the session history.
func cmdCompact(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	if d.compactCfg == nil {
		return "", NewError(CodeNotImplemented, "compact is not configured")
	}
	prov, providerName, apiKey, wireModel, err := d.providerForModel(sess)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	msgs := sess.Messages
	before := compaction.EstimateContextTokens(msgs).Tokens
	stream := provider.StreamFnFromProvider(prov)
	model := provider.Model{
		Provider:      providerName,
		ID:            wireModel,
		ContextWindow: d.compactCfg.ContextWindow,
	}
	res, err := compaction.Compact(
		ctx,
		stream,
		model,
		msgs,
		compaction.DefaultCompactionSettings,
		-1,
		nil,
		"",
		provider.StreamConfig{APIKey: apiKey},
	)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	if res == nil {
		return fmt.Sprintf("nothing to compact (%d tokens, %d messages)", before, len(msgs)), nil
	}
	rebuilt := res.RebuildContext(msgs, time.Now().UnixMilli())
	sess.Messages = rebuilt
	if err := persistRewind(sess); err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	after := compaction.EstimateContextTokens(rebuilt).Tokens
	return fmt.Sprintf("compacted: %d -> %d tokens, summarized %d messages", before, after, res.FirstKeptIndex), nil
}

// cmdHelp lists the commands routed through pigo/command.
func cmdHelp(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	cmds := availableCommandsPayload(d.commands, d.registry)
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		if name, _ := c["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "/%s\n", name)
	}
	return strings.TrimSpace(b.String()), nil
}

// cmdCopy copies the last assistant reply to the clipboard, degrading to
// printing when no clipboard utility is available.
func cmdCopy(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	text := ""
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if a, ok := sess.Messages[i].(agentcore.AssistantMessage); ok {
			if t := strings.TrimSpace(agentcore.ContentToText(a.Content)); t != "" {
				text = t
				break
			}
		}
	}
	if text == "" {
		return "nothing to copy — no assistant reply yet", nil
	}
	if err := clipboard.Copy(text); err != nil {
		return "no clipboard utility found; reply:\n" + text, nil
	}
	return "copied last reply to clipboard", nil
}

// cmdExport writes the session transcript to a file. It defaults to a
// self-contained HTML export and emits an ACP resource_link, matching pi-acp;
// an explicit .jsonl path keeps pigo's JSONL/import workflow.
func cmdExport(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	path := strings.TrimSpace(args)
	if path == "" {
		path = filepath.Join(sess.Cwd, sess.ID+".html")
	}
	n, err := sess.Store.TranscriptStore().Export(sess.ID, path)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	abs, _ := filepath.Abs(path)
	if strings.HasSuffix(strings.ToLower(path), ".html") || strings.HasSuffix(strings.ToLower(path), ".htm") {
		d.sendTextChunk(sess.ID, "Session exported: ")
		uri := "file://" + filepath.ToSlash(abs)
		if !strings.HasPrefix(filepath.ToSlash(abs), "/") {
			uri = "file:///" + filepath.ToSlash(abs)
		}
		d.sendSessionUpdate(sess.ID, map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{
				"type":     "resource_link",
				"name":     filepath.Base(path),
				"uri":      uri,
				"mimeType": "text/html",
				"title":    "Session exported",
			},
		})
	}
	return fmt.Sprintf("exported %d entries to %s", n, path), nil
}

// cmdImport materializes a JSONL export as a new session.
func cmdImport(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	path := strings.TrimSpace(args)
	if path == "" {
		return "", NewError(CodeInvalidParams, "usage: /import <path.jsonl>")
	}
	newHeader, entries, err := sess.Store.TranscriptStore().Import(path, time.Now().UTC())
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	meta := sessionstore.NewMetadata(newHeader.ID, "Import of "+path, "pigo", sess.Model, sess.Cwd)
	if err := sess.Store.SaveMetadata(meta); err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	if _, err := d.manager.Load(sess.Cwd, newHeader.ID, sess.Model, sess.Store); err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	return fmt.Sprintf("imported %d entries from %s → session %s", len(entries), path, newHeader.ID), nil
}

// cmdRebuild reconstructs the session context from a checkpoint, falling back
// to compaction when no checkpoint exists.
func cmdRebuild(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	if d.compactCfg == nil {
		return "", NewError(CodeNotImplemented, "rebuild is not configured")
	}
	prov, providerName, apiKey, wireModel, err := d.providerForModel(sess)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	msgs := sess.Messages
	before := compaction.EstimateContextTokens(msgs).Tokens
	cfg := runtime.RunConfig{
		LoopConfig: runtime.LoopConfig{
			Model:         wireModel,
			Provider:      providerName,
			ThinkingLevel: d.compactCfg.ThinkingLevel,
			APIKey:        apiKey,
			Stream:        provider.StreamFnFromProvider(prov),
			ContextWindow: d.compactCfg.ContextWindow,
			Compaction:    compaction.DefaultCompactionSettings,
		},
	}
	res, err := runtime.RebuildFromCheckpoint(ctx, msgs, sess.ID, "", &cfg, nil)
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	if res.NoOp {
		return fmt.Sprintf("nothing to rebuild (%d tokens, %d messages)", before, len(msgs)), nil
	}
	sess.Messages = res.Messages
	if err := persistRewind(sess); err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	source := "checkpoint"
	if !res.FromCheckpoint {
		source = "compaction (no checkpoint)"
	}
	return fmt.Sprintf("rebuilt from %s: %d -> %d tokens", source, res.TokensBefore, res.TokensAfter), nil
}

// cmdMemory reports the persistent memory store status and per-scope counts.
func cmdMemory(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	if d.memoryCfg == nil || d.memoryCfg.Store == nil {
		return "memory is disabled", nil
	}
	counts, err := d.memoryCfg.Store.CountByScope()
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	return fmt.Sprintf("memory root: %s\nentries: %d", d.memoryCfg.Store.Root(), total), nil
}

// cmdGoal sets, clears, or shows the session goal. An active goal is prepended
// to every subsequent prompt by applyGoal.
func cmdGoal(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	args = strings.TrimSpace(args)
	switch {
	case args == "":
		if sess.Goal == "" {
			return "no active goal", nil
		}
		return "goal: " + sess.Goal, nil
	case strings.EqualFold(args, "off"):
		sess.Goal = ""
		return "goal cleared", nil
	default:
		sess.Goal = args
		return "goal set: " + args, nil
	}
}

// cmdBtw runs a non-persisted side question against a copy of the session
// history and returns the reply.
func cmdBtw(ctx context.Context, d *Dispatcher, sess *AcpSession, args string) (string, *Error) {
	if d.runner == nil {
		return "", NewError(CodeNotImplemented, "btw is not configured")
	}
	prompt := strings.TrimSpace(args)
	if prompt == "" {
		return "usage: /btw <prompt>", nil
	}
	history := append(agentcore.MessageList{}, sess.Messages...)
	_, last, err := d.runner.Run(ctx, prompt, nil, history, sess.Header.SystemPrompt, sess.Model, sess.Thinking, nil, nil, TurnHooks{})
	if err != nil {
		return "", NewError(CodeInternalError, err.Error())
	}
	if last == nil {
		return "no reply", nil
	}
	return agentcore.ContentToText(last.Content), nil
}

func sessionStatusText(sess *AcpSession) string {
	turns := 0
	for _, m := range sess.Messages {
		if _, ok := m.(agentcore.UserMessage); ok {
			turns++
		}
	}
	thinking := sess.Thinking
	if thinking == "" {
		thinking = "default"
	}
	steering := sess.SteeringMode
	if steering == "" {
		steering = "one-at-a-time"
	}
	followUp := sess.FollowUpMode
	if followUp == "" {
		followUp = "one-at-a-time"
	}
	return fmt.Sprintf(
		"session: %s\nmodel: %s\nthinking: %s\nsteering: %s\nfollow-up: %s\ncwd: %s\nmessages: %d\nturns: %d",
		sess.ID, sess.Model, thinking, steering, followUp, sess.Cwd, len(sess.Messages), turns,
	)
}

// agentEventEnvelope renders an agentcore event as a JSON-safe map with a
// "type" discriminant. It is the payload shape of the pigo/event extension
// channel and mirrors the stream-json envelope so TUI rendering keeps working.
func agentEventEnvelope(ev agentcore.AgentEvent) map[string]any {
	env := map[string]any{"type": ev.EventType()}
	switch e := ev.(type) {
	case agentcore.AgentStartEvent:
		if e.SessionID != "" {
			env["sessionId"] = e.SessionID
		}
	case agentcore.AgentEndEvent:
		env["messageCount"] = len(e.Messages)
	case agentcore.TurnEndEvent:
		env["stopReason"] = e.Message.StopReason
		if text := agentcore.ContentToText(e.Message.Content); text != "" {
			env["text"] = text
		}
		if calls := e.Message.ToolCalls(); len(calls) > 0 {
			names := make([]string, len(calls))
			for i, c := range calls {
				names[i] = c.Name
			}
			env["toolCalls"] = names
		}
	case agentcore.MessageUpdateEvent:
		if a, ok := e.Message.(agentcore.AssistantMessage); ok {
			if text := agentcore.ContentToText(a.Content); text != "" {
				env["text"] = text
			}
		}
	case agentcore.ToolExecutionStartEvent:
		env["toolCallId"] = e.ToolCallID
		env["toolName"] = e.ToolName
	case agentcore.ToolExecutionEndEvent:
		env["toolCallId"] = e.ToolCallID
		env["toolName"] = e.ToolName
		env["isError"] = e.IsError
	case agentcore.CompactionStartEvent:
		env["reason"] = e.Reason
		env["tokensBefore"] = e.TokensBefore
	case agentcore.CompactionEvent:
		env["reason"] = e.Reason
		env["tokensBefore"] = e.TokensBefore
		env["tokensAfter"] = e.TokensAfter
		env["summarizedCount"] = e.SummarizedCount
		env["keptCount"] = e.KeptCount
		if e.ErrorMessage != "" {
			env["error"] = e.ErrorMessage
		}
	case agentcore.SubAgentProgressEvent:
		env["toolCallId"] = e.ToolCallID
		env["description"] = e.Description
		env["activity"] = e.Activity
		env["tokens"] = e.Tokens
	case agentcore.TelemetryEvent:
		env["turns"] = e.Turns
		env["compactionCount"] = e.CompactionCount
		env["contextUtilization"] = e.ContextUtilization
		env["contextTokens"] = e.ContextTokens
		env["contextWindow"] = e.ContextWindow
	}
	return env
}

// pigoEventPayload wraps a raw agent event for the pigo/event notification.
func pigoEventPayload(sessionID string, ev agentcore.AgentEvent) map[string]any {
	return map[string]any{"sessionId": sessionID, "event": agentEventEnvelope(ev)}
}

// parseCommandLine splits "/name arg1 arg2" into name and raw args.
func parseCommandLine(line string) (name, args string) {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "/")
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return line[:i], strings.TrimSpace(line[i+1:])
	}
	return line, ""
}

// rawJSONValue returns v as a JSON value for embedding in request params.
func rawJSONValue(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
