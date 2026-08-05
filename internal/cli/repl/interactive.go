// This file wires the line-based REPL (US-003) and session persistence
// (US-024, #43) into the pigo command. When invoked without a prompt on a
// terminal, pigo starts the REPL loop (see repl.go); each run's messages are
// persisted to a local JSONL session so the conversation can be listed, resumed
// and replayed later.
package repl

import (
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/headless"
	"github.com/smallnest/pigo/internal/dream"
	"github.com/smallnest/pigo/internal/plugin"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
)

// sessionStore returns the session store for the interactive REPL. It is a thin
// alias for headless.SessionStore so the REPL and headless runs share one store
// rooted at ~/.pigo/sessions (or PIGO_HOME).
func sessionStore() (*session.Store, error) {
	return headless.SessionStore()
}

// Options carries the resolved run configuration plus optional
// resume state into Run.
type Options struct {
	Model        string
	ProviderName string
	Provider     provider.Provider
	BaseURL      string
	APIKey       string
	Protocol     string
	// ThinkingLevel is the resolved reasoning-effort level (US-023): it seeds the
	// live run config so every REPL turn requests it, until a control command
	// changes it.
	ThinkingLevel agentcore.ThinkingLevel
	Tools         []agentcore.AgentTool
	SysPrompt     string

	// ResumeID, when non-empty, resumes an existing session: its messages seed
	// the context and replayed transcript. Otherwise a fresh session is created.
	ResumeID string

	// Approve, when true, grants the launch directory session trust before the
	// run so the first-launch trust prompt is skipped and side-effect tools run
	// without per-call confirmation (mirrors pi's --approve/-a).
	Approve bool
	// Skills is the pre-loaded skill set (loaded once by setupAgentEnv, shared
	// with prompt injection). Each is registered as a /skill-name command. Empty
	// under --no-skills, so nothing is registered.
	Skills []*runtime.Skill

	// Plugins holds the loaded plugin manager so the REPL can deliver lifecycle
	// events to subscribed plugins (US-017, #133). It may be nil (no plugins).
	Plugins *plugin.Manager

	// ConfigPrompts holds prompt-template paths from the config.toml `prompts`
	// array (settings tier); each is a file or dir loaded non-recursively.
	ConfigPrompts []string
	// CliPrompts holds --prompt-template paths (CLI tier, repeatable).
	CliPrompts []string
	// NoPromptTemplates disables all prompt-template discovery (global, project,
	// settings, CLI); built-in slash commands are unaffected. Independent of
	// --no-skills.
	NoPromptTemplates bool

	// Dream is the resolved [dream] configuration (US-008). Run uses it to decide
	// whether to launch the startup background consolidation; a zero value
	// (Enabled false) disables the auto-trigger entirely.
	Dream dream.Config
}

// Run starts the line-based REPL over a persisted session. It keeps
// a single growing AgentContext across prompts (so turns share history) and
// saves the session's messages after each run completes (see runREPL/streamRun
// in repl.go).
func Run(opts Options) error {
	// ACP is the only frontend entry: the line REPL drives the agent core
	// through the in-process ACP server (ticket 10 contract).
	return RunACP(opts)
}

// formatHelpLine renders one slash-command line for /help as
// "/name <argument-hint> - description (source: <tier>)", omitting the hint
// segment when absent. It is the plain, testable form of the /help line; the
// /help Action applies color on top of the same structure.
func formatHelpLine(c runtime.SlashCommand) string {
	s := "/" + c.Name
	if c.ArgumentHint != "" {
		s += " " + c.ArgumentHint
	}
	if c.Description != "" {
		s += " - " + c.Description
	}
	s += " (source: " + c.Tier.String() + ")"
	return s
}
