package acp

import (
	"path/filepath"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/runtime"
)

// SessionContext is the per-session run context a shared pigo process builds
// for a workspace. Each ACP session gets its own system prompt, tool set, and
// slash registry so one process can serve multiple projects without leaking
// cwd-derived state across sessions.
type SessionContext struct {
	SysPrompt             string
	Tools                 []agentcore.AgentTool
	Registry              *runtime.SlashRegistry
	AdditionalDirectories []string
}

// SessionContextFactory builds a SessionContext for a session workspace. It is
// the seam between the protocol dispatcher and the CLI run assembly: stdio
// servers provide a factory that rebuilds prompts, roots, and slash commands
// per cwd, while in-process single-project drivers use the default factory.
type SessionContextFactory func(cwd string, additionalDirectories []string) (SessionContext, error)

// CloneToolsForSession returns a per-session copy of the template tool set with
// file and shell roots pointed at cwd. Additional directories are merged into
// the read/write/edit extra roots. rebuildTask, when non-nil, replaces the
// generic task tool with a cwd-bound clone; it is nil for single-project
// in-process servers where the template cwd is already correct.
func CloneToolsForSession(template []agentcore.AgentTool, cwd string, additionalDirectories []string, rebuildTask func() *runtime.SubAgentTool) []agentcore.AgentTool {
	if len(template) == 0 {
		return nil
	}
	extra := normalizeRoots(additionalDirectories)
	out := make([]agentcore.AgentTool, 0, len(template))
	// Per-session state stores: rewind snapshots and background bash jobs must
	// never leak across workspaces, while the memory store stays process-global
	// by design (persistent memory is shared pigo data).
	snap := agenttool.NewFileSnapshotRecorder()
	jobs := agenttool.NewBashJobStore()
	for _, tool := range template {
		if st, ok := tool.(*runtime.SubAgentTool); ok && st.Name() == "task" && rebuildTask != nil {
			out = append(out, rebuildTask())
			continue
		}
		out = append(out, cloneToolForSession(tool, cwd, extra, snap, jobs))
	}
	return out
}

func cloneToolForSession(tool agentcore.AgentTool, cwd string, extra []string, snap *agenttool.FileSnapshotRecorder, jobs *agenttool.BashJobStore) agentcore.AgentTool {
	switch t := tool.(type) {
	case *agenttool.ReadTool:
		return &agenttool.ReadTool{Root: cwd, ExtraRoots: mergeRoots(t.ExtraRoots, extra)}
	case *agenttool.WriteTool:
		return &agenttool.WriteTool{Root: cwd, ExtraRoots: mergeRoots(t.ExtraRoots, extra), Snap: snap}
	case *agenttool.EditTool:
		return &agenttool.EditTool{Root: cwd, ExtraRoots: mergeRoots(t.ExtraRoots, extra), Snap: snap}
	case *agenttool.GrepTool:
		return &agenttool.GrepTool{Root: cwd}
	case *agenttool.FindTool:
		return &agenttool.FindTool{Root: cwd}
	case *agenttool.LsTool:
		return &agenttool.LsTool{Root: cwd}
	case *agenttool.BashTool:
		return &agenttool.BashTool{Dir: cwd, Shell: t.Shell, Jobs: jobs}
	case *agenttool.BashOutputTool:
		return &agenttool.BashOutputTool{Jobs: jobs}
	case *agenttool.BashKillTool:
		return &agenttool.BashKillTool{Jobs: jobs}
	case *agenttool.TodoTool:
		return &agenttool.TodoTool{Store: agenttool.NewTodoStore()}
	case *agenttool.MemorySearchTool:
		return &agenttool.MemorySearchTool{Store: t.Store}
	default:
		// Custom/plugin tools have no cwd or per-session state to rebind; the
		// shared instance is intentionally reused across sessions.
		return tool
	}
}

func normalizeRoots(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		out = appendUniquePath(out, abs)
	}
	return out
}

func mergeRoots(base, extra []string) []string {
	out := append([]string(nil), base...)
	for _, dir := range extra {
		out = appendUniquePath(out, dir)
	}
	return out
}

func sessionContextOf(sess *AcpSession) SessionContext {
	return SessionContext{
		SysPrompt:             sess.Header.SystemPrompt,
		Tools:                 sess.Tools,
		Registry:              sess.Registry,
		AdditionalDirectories: sess.AdditionalDirectories,
	}
}
