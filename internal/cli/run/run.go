// Package run holds the run-assembly layer (US-005, #362): the shared setup that
// both the interactive REPL and the headless driver need — resolving the
// provider, building the tool set rooted at the working directory, discovering
// skills and plugins, and constructing the loop RunConfig. Pulling it out of
// cmd/pigo lets the subpackages assemble a run through one exported API instead
// of duplicating the wiring.
package run

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/builtinskills"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/contextbuild"
	"github.com/smallnest/pigo/internal/hooks"
	"github.com/smallnest/pigo/internal/memory"
	"github.com/smallnest/pigo/internal/plugin"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/trust"
)

// Env is the environment every run shares: the working directory, the tool set
// rooted at it, the resolved provider, and the system prompt. It is assembled
// once (SetupEnv) and consumed by whichever driver runs.
type Env struct {
	Cwd          string
	Tools        []agentcore.AgentTool
	Provider     provider.Provider
	ProviderName string
	APIKey       string
	SysPrompt    string
	// Model is the resolved wire model id used by sub-agent factories.
	Model string
	// AppendInstructions are the resolved --append-system-prompt texts so a
	// shared ACP process can rebuild per-session prompts without re-resolving.
	AppendInstructions []string
	// BaseInstruction is the raw --system-prompt base text (may be empty).
	// contextbuild layers tools/guidelines/context files/skills on top of it per
	// request; Env.SysPrompt remains the pre-assembled legacy prompt.
	BaseInstruction string

	// Skills is the discovered skill set (loaded once here, empty under
	// --no-skills). It is threaded into the REPL so each skill is registered as a
	// /skill-name command, and the model-invocable subset is already injected into
	// SysPrompt.
	Skills []*runtime.Skill

	// Plugins holds any loaded external plugins so the caller can Close them when
	// the run ends. It is nil when no plugins were discovered.
	Plugins *plugin.Manager

	// ContextFiles enables per-directory AGENTS/CLAUDE context-file injection.
	ContextFiles bool
	// PluginDirs are the resolved --plugin-dir / config plugin_dirs roots.
	PluginDirs []string

	// Memory is the persistent memory store opened once for the run (issue #481),
	// or nil when persistent memory is disabled (memory.enabled=false), tools are
	// disabled (--no-tools), or the store could not be opened (a non-fatal
	// failure). When non-nil the caller MUST Close it when the run ends. The store
	// is also handed to the memory_search tool (in Tools) and, through it, the
	// per-turn memory reminder provider, so this field exists mainly so the owner
	// can close the DB — downstream wiring reaches the store via the tool.
	Memory *memory.Store
}

// SetupEnv resolves the provider for model/baseURL, builds the tool set rooted
// at the working directory, and constructs the system prompt — the setup the
// REPL and headless drivers both need. systemPrompt, when non-empty, replaces
// the default base instruction (mirrors pi's --system-prompt); appendSystemPrompt
// entries are each resolved (a path to an existing file is read, otherwise the
// value is literal text) and layered onto the end of the prompt (mirrors pi's
// --append-system-prompt). apiKey is the resolved credential (CLI --api-key or
// config.toml) used as the override for sub-agent credential resolution so
// dispatched task children authenticate the same way the parent does. policy is
// the --allowed-tools/--disallowed-tools boundary; it is validated against the
// fully assembled tool set and then applied, so an unknown tool name is a usage
// error rather than a silently ineffective boundary. It returns an error rather
// than exiting so the caller owns exit-code mapping.
func SetupEnv(model, baseURL, protocol, providerName, apiKey string, noTools, noSkills, contextFiles bool, systemPrompt string, appendSystemPrompt []string, pluginDirs []string, memEnabled bool, policy ToolPolicy) (Env, error) {
	cwd, _ := os.Getwd()
	prov, resolvedName, resolvedKey, wireModel, err := resolveStartupProvider(model, baseURL, protocol, providerName, apiKey)
	if err != nil {
		return Env{}, err
	}
	model = wireModel
	appends, err := resolveAppendInstructions(appendSystemPrompt)
	if err != nil {
		return Env{}, err
	}
	tools := BuiltinTools(cwd, noTools)
	// Open the persistent memory store once (issue #481) and expose it as the
	// memory_search tool so the agent can recall earlier context. Memory is a
	// tool, so it is skipped under --no-tools; memory.enabled=false disables it
	// too. Opening is non-fatal: a failure logs and leaves memory off, matching
	// the "fall back to file-based auto-memory" contract.
	var memStore *memory.Store
	if !noTools {
		if store, err := OpenMemoryStore(memEnabled); err != nil {
			fmt.Fprintf(os.Stderr, "pigo: memory disabled: %v\n", err)
		} else if store != nil {
			memStore = store
			tools = append(tools, &agenttool.MemorySearchTool{Store: store})
		}
	}
	// Discover external plugins (US-016) and append their tools. Plugin loading
	// is fault-tolerant: a plugin that fails to start is logged and skipped, and
	// disabling tools (--no-tools) skips plugin discovery entirely.
	var mgr *plugin.Manager
	if !noTools {
		mgr = &plugin.Manager{}
		dirs := []string{PluginsDir()}
		for _, dir := range pluginDirs {
			if dir == "" {
				continue
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(cwd, dir)
			}
			dirs = append(dirs, filepath.Clean(dir))
		}
		seen := map[string]bool{}
		for _, dir := range dirs {
			if dir == "" || seen[dir] {
				continue
			}
			seen[dir] = true
			m, err := plugin.Discover(dir, os.Stderr, os.Stderr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "pigo: plugin discovery failed for %s: %v\n", dir, err)
				continue
			}
			mgr.Merge(m)
		}
		if m := mgr; m != nil && len(m.Tools()) > 0 {
			tools = append(tools, m.Tools()...)
			for _, spec := range m.Subagents() {
				tool, err := PluginSubagentTool(cwd, policy, model, resolvedName, prov, resolvedKey, spec, m.Tools())
				if err != nil {
					return Env{}, err
				}
				tools = append(tools, tool)
			}
		}
	}
	// Enforce the --allowed-tools/--disallowed-tools boundary now that the set is
	// complete. Validation must happen here rather than at flag-parse time: plugin
	// and memory tool names only exist at runtime, so an earlier check would reject
	// legitimate names. Filtering here — at the registration layer, before the
	// BeforeToolCall confirmation gate — is what makes the boundary structural: a
	// removed tool is never advertised and never dispatchable, so --approve cannot
	// widen it.
	if err := ValidateToolPolicy(tools, policy); err != nil {
		return Env{}, err
	}
	tools = ApplyToolPolicy(tools, policy)
	if len(tools) == 0 && !noTools && !policy.IsZero() {
		fmt.Fprintln(os.Stderr, "pigo: warning: the tool policy removed every tool; the model will run without tools")
	}
	// --no-tools already disables everything, so a tool policy alongside it has
	// no effect — and because the set is empty, ValidateToolPolicy above skipped
	// name validation, meaning a typo here would otherwise pass unnoticed. Say so
	// rather than letting the user believe a boundary is in force.
	if noTools && !policy.IsZero() {
		fmt.Fprintln(os.Stderr, "pigo: warning: --no-tools disables all tools; --allowed-tools/--disallowed-tools are ignored (and unvalidated)")
	}
	// Load skills once (shared between prompt injection and /skill-name
	// registration). A partial parse error still yields the skills that DID load,
	// so one malformed file is a non-fatal warning rather than a hard failure.
	skills, err := LoadSkills(noSkills)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pigo: skills: %v\n", err)
	}
	// The model can only load a skill's body when the read tool is present, so
	// advertise skills in the prompt only then (mirrors pi's selectedTools check).
	sysPrompt, err := runtime.BuildSystemPrompt(runtime.PromptConfig{
		BaseInstruction:    systemPrompt,
		WorkingDir:         cwd,
		Root:               cwd,
		AppendInstructions: appends,
		Skills:             skills,
		ReadToolAvailable:  HasReadTool(tools),
	})
	if err != nil {
		return Env{}, err
	}
	return Env{
		Cwd:                cwd,
		Tools:              tools,
		Provider:           prov,
		ProviderName:       resolvedName,
		APIKey:             resolvedKey,
		SysPrompt:          sysPrompt,
		Model:              model,
		AppendInstructions: appends,
		BaseInstruction:    systemPrompt,
		Skills:             skills,
		Plugins:            mgr,
		Memory:             memStore,
		ContextFiles:       contextFiles,
		PluginDirs:         pluginDirs,
	}, nil
}

// ContextBuildInput carries the session projection + run environment pieces
// frontends need to assemble one contextbuild session.
type ContextBuildInput struct {
	Project            *session.ProjectLeaf
	BaseInstruction    string
	Cwd                string
	ContextFiles       bool
	AppendInstructions []string
	Skills             []*runtime.Skill
	AllTools           []agentcore.AgentTool
	Plugins            *plugin.Manager
	Reminders          *runtime.ReminderRegistry
	Warn               io.Writer
}

// ContextBuild is the assembled per-session contextbuild state: the projected
// session context, the injected deps, and the per-request options.
type ContextBuild struct {
	Ctx  *contextbuild.SessionContext
	Deps contextbuild.BuildDeps
	Req  contextbuild.RequestOptions
}

// NewContextBuild projects a ProjectLeaf into a contextbuild session and wires
// the plugin declarations into a session-level registry. The returned
// RequestBuilder seam is installed into runtime.LoopConfig by frontends.
func NewContextBuild(in ContextBuildInput) (*ContextBuild, error) {
	reg := contextbuild.NewRegistry()
	if in.Plugins != nil {
		contextbuild.RegisterPluginContributions(reg, in.Plugins, in.Warn)
	}
	sess, err := contextbuild.BuildSessionContext(in.Project, contextbuild.SessionBuildOptions{Registry: reg})
	if err != nil {
		return nil, err
	}
	return &ContextBuild{
		Ctx: sess,
		Deps: contextbuild.BuildDeps{
			Registry:  reg,
			Reminders: in.Reminders,
			Convert:   contextbuild.ConvertToLlm,
		},
		Req: contextbuild.RequestOptions{
			Cwd:                 in.Cwd,
			BaseInstruction:     in.BaseInstruction,
			AppendInstructions:  in.AppendInstructions,
			ContextFilesEnabled: in.ContextFiles,
			Skills:              in.Skills,
			AllTools:            in.AllTools,
			GlobalAgentDir:      ConfigDir(),
			ReadFile:            os.ReadFile,
		},
	}, nil
}

// PluginSubagentTool builds a runtime sub-agent tool from a plugin manifest
// declaration. Tools are resolved against builtins plus all plugin tools;
// unknown names fail closed. Process isolation uses builtins only and requires
// a model id.
func PluginSubagentTool(cwd string, policy ToolPolicy, model, providerName string, prov provider.Provider, apiKey string, spec plugin.SubagentSpec, pluginTools []agentcore.AgentTool) (*runtime.SubAgentTool, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("plugin subagent: empty name")
	}
	all := append(BuiltinToolsExcept(cwd, false, "task"), pluginTools...)
	byName := map[string]agentcore.AgentTool{}
	for _, t := range all {
		byName[t.Name()] = t
	}
	var selected []agentcore.AgentTool
	if len(spec.Tools) == 0 {
		selected = append(selected, all...)
	} else {
		for _, name := range spec.Tools {
			t, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("plugin subagent %q: unknown tool %q", spec.Name, name)
			}
			selected = append(selected, t)
		}
	}
	childCreds := provider.NewCredentialStore(nil)
	childCreds.SetOverride(providerName, apiKey)
	factory := func() runtime.RunConfig {
		return runtime.RunConfig{
			LoopConfig: runtime.LoopConfig{
				Model:     model,
				Provider:  providerName,
				Stream:    provider.StreamFnFromProvider(prov),
				GetAPIKey: childCreds.GetAPIKey,
			},
			Batch: agenttool.BatchConfig{
				ToolExecutorConfig: agenttool.ToolExecutorConfig{Registry: ToolRegistry(selected)},
			},
		}
	}
	sub := runtime.SubAgentSpec{
		Name:         spec.Name,
		Description:  spec.Description,
		SystemPrompt: spec.SystemPrompt,
		Tools:        selected,
		NewRunConfig: factory,
		Cwd:          cwd,
	}
	if strings.EqualFold(spec.Isolation, "process") {
		if spec.Model == "" {
			return nil, fmt.Errorf("plugin subagent %q: process isolation requires model", spec.Name)
		}
		sub.Isolation = runtime.SubAgentIsolationProcess
		sub.Process = runtime.SubAgentProcessConfig{Model: spec.Model}
		sub.Tools = nil
		for _, t := range selected {
			sub.Process.ToolNames = append(sub.Process.ToolNames, t.Name())
		}
	}
	return runtime.NewSubAgentTool(sub), nil
}

// resolveStartupProvider resolves the startup provider, supporting a default
// model of the form custom-<slug>/<modelId> from the config provider registry.
func resolveStartupProvider(model, baseURL, protocol, providerName, apiKey string) (provider.Provider, string, string, string, error) {
	prov, name, resolvedKey, wireModel, err := provider.ResolveConfiguredModel(model, baseURL, protocol, providerName, apiKey, os.Getenv)
	if err != nil {
		if errors.Is(err, provider.ErrModelNotConfigured) {
			errMsg := "no configured model"
			if strings.TrimSpace(model) != "" {
				errMsg = fmt.Sprintf("model %q is not configured", model)
			}
			return provider.DeferredErrorProvider{Err: fmt.Errorf("%s", errMsg)}, "", "", model, nil
		}
		if errors.Is(err, provider.ErrModelDisabled) {
			return provider.DeferredErrorProvider{Err: err}, "", "", model, nil
		}
		return nil, "", "", "", err
	}
	return prov, name, resolvedKey, wireModel, nil
}

// localEndpoint reports whether a base URL points at a local, keyless endpoint.
func localEndpoint(baseURL string) bool {
	b := strings.ToLower(baseURL)
	return strings.Contains(b, "localhost") || strings.Contains(b, "127.0.0.1") || strings.HasPrefix(b, "http://")
}

// HasReadTool reports whether the read tool is present in the tool set. Skills
// are advertised in the system prompt only when it is, since the model needs the
// read tool to load a skill's body on demand.
func HasReadTool(tools []agentcore.AgentTool) bool {
	for _, t := range tools {
		if t.Name() == "read" {
			return true
		}
	}
	return false
}

// resolveAppendInstructions maps each --append-system-prompt value to the text
// to append. Following pi, a value that names an existing regular file is read
// and its contents are appended; any other value (a non-existent path, or a
// directory) is treated as literal text. Only a value that stats as a regular
// file but then fails to read (e.g. a permission error) is reported, so a
// genuinely broken file path is not silently appended verbatim.
func resolveAppendInstructions(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		info, statErr := os.Stat(v)
		if statErr == nil && !info.IsDir() {
			data, err := os.ReadFile(v)
			if err != nil {
				return nil, fmt.Errorf("read --append-system-prompt file %q: %w", v, err)
			}
			out = append(out, string(data))
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// BuiltinTools returns the default file/shell tool set rooted at cwd, or nil
// when tools are disabled. The todo tool is stateful: a single TodoStore is
// created here and held by the one TodoTool instance, so the task list persists
// across calls within a run (a later write replaces the plan).
func BuiltinTools(cwd string, disabled bool) []agentcore.AgentTool {
	if disabled {
		return nil
	}
	// A single recorder is shared by the write and edit tools so /rewind can roll
	// back every mutation from a turn regardless of which tool made it.
	snap := agenttool.NewFileSnapshotRecorder()
	// A single observation recorder is shared by read/write/edit so a session's
	// read authorizes later guarded mutations of the same file.
	obs := agenttool.NewFileObservationRecorder()
	return []agentcore.AgentTool{
		&agenttool.ReadTool{Root: cwd, ExtraRoots: ReadableExtraRoots(), Observe: obs},
		&agenttool.WriteTool{Root: cwd, ExtraRoots: ReadableExtraRoots(), Snap: snap, Observe: obs},
		&agenttool.EditTool{Root: cwd, ExtraRoots: ReadableExtraRoots(), Snap: snap, Observe: obs},
		&agenttool.GrepTool{Root: cwd},
		&agenttool.FindTool{Root: cwd},
		&agenttool.BashTool{Dir: cwd},
		&agenttool.TodoTool{Store: agenttool.NewTodoStore()},
		&agenttool.WebFetchTool{},
		&agenttool.WebSearchTool{},
	}
}

// BuiltinToolsExcept returns the default builtin tool set (BuiltinTools) with
// any tool whose name matches one of the except names removed. It backs the
// nesting guard for the generic task tool: a child sub-agent's registry is built
// with "task" excluded so a child can never spawn further sub-agents, capping
// delegation depth at one. With no except names it is equivalent to BuiltinTools.
func BuiltinToolsExcept(cwd string, disabled bool, except ...string) []agentcore.AgentTool {
	all := BuiltinTools(cwd, disabled)
	if len(except) == 0 || len(all) == 0 {
		return all
	}
	skip := make(map[string]struct{}, len(except))
	for _, n := range except {
		skip[n] = struct{}{}
	}
	out := make([]agentcore.AgentTool, 0, len(all))
	for _, t := range all {
		if _, ok := skip[t.Name()]; ok {
			continue
		}
		out = append(out, t)
	}
	return out
}

// ReadableExtraRoots returns trusted directories the file tools may reach beyond
// the workspace root. The skills directory is included so the model can load the
// absolute SKILL.md paths pigo advertises in the system prompt, and author or
// update skills there (they otherwise resolve outside the workspace and are
// rejected). An empty skills dir is dropped, so this stays a no-op when the home
// directory cannot be resolved.
func ReadableExtraRoots() []string {
	if dir := SkillsDir(); dir != "" {
		return []string{dir}
	}
	return nil
}

// ToolRegistry builds a registry from the given tools (skipping any that fail to
// register, e.g. a bad schema, which should not happen for built-ins).
func ToolRegistry(tools []agentcore.AgentTool) *agenttool.ToolRegistry {
	reg := agenttool.NewToolRegistry()
	for _, t := range tools {
		_ = reg.Register(t)
	}
	return reg
}

// TodoReminders builds the per-turn system-reminder registry for a tool set
// (US-002): it locates the stateful TodoTool and registers a TodoReminderProvider
// over its shared store, so the model is reminded of unfinished tasks each turn.
// It also registers a MemoryReminderProvider over the memory_search tool's store
// (issue #481) when present, so relevant persisted memory is recalled each turn
// (this is the recall channel used after auto-compaction/rebuild). Returns nil
// when neither provider applies (e.g. --no-tools), leaving injection disabled.
func TodoReminders(tools []agentcore.AgentTool) *runtime.ReminderRegistry {
	var providers []runtime.ReminderProvider
	for _, t := range tools {
		switch tool := t.(type) {
		case *agenttool.TodoTool:
			if tool.Store != nil {
				providers = append(providers, &runtime.TodoReminderProvider{Store: tool.Store})
			}
		case *agenttool.MemorySearchTool:
			if tool.Store != nil {
				providers = append(providers, &runtime.MemoryReminderProvider{Store: tool.Store})
			}
		}
	}
	if len(providers) == 0 {
		return nil
	}
	return runtime.NewReminderRegistry(providers...)
}

// MemoryRootFromTools returns the persistent memory root the run's memory_search
// tool is backed by (its Store.Root()), or "" when persistent memory is not wired
// into this tool set (memory.enabled=false, --no-tools, or the store failed to
// open). It is the canonical source of the memory root for checkpoint persistence
// and context rebuild from SQLite compaction entries: callers resolve the
// root through the opened store rather than re-deriving it from the session store.
func MemoryRootFromTools(tools []agentcore.AgentTool) string {
	for _, t := range tools {
		if mt, ok := t.(*agenttool.MemorySearchTool); ok && mt.Store != nil {
			return mt.Store.Root()
		}
	}
	return ""
}

// MemoryStoreFromTools returns the persistent memory Store backing the run's
// memory_search tool, or nil when persistent memory is not wired into this tool
// set. It lets status commands (/memory) inspect the live store without
// re-opening the database.
func MemoryStoreFromTools(tools []agentcore.AgentTool) *memory.Store {
	for _, t := range tools {
		if mt, ok := t.(*agenttool.MemorySearchTool); ok && mt.Store != nil {
			return mt.Store
		}
	}
	return nil
}

// SnapshotRecorderFromTools returns the shared FileSnapshotRecorder backing the
// run's write/edit tools, or nil when file tools are disabled (--no-tools). The
// REPL uses it to commit a per-turn restore point and to serve /rewind.
func SnapshotRecorderFromTools(tools []agentcore.AgentTool) *agenttool.FileSnapshotRecorder {
	for _, t := range tools {
		switch tool := t.(type) {
		case *agenttool.WriteTool:
			if tool.Snap != nil {
				return tool.Snap
			}
		case *agenttool.EditTool:
			if tool.Snap != nil {
				return tool.Snap
			}
		}
	}
	return nil
}

// BashJobStoreFromTools returns the shared BashJobStore backing the run's bash /
// bash_output / kill_bash tools, or nil when the shell tool is disabled. The
// REPL uses it to kill any still-running background jobs on exit so they are not
// orphaned.
func BashJobStoreFromTools(tools []agentcore.AgentTool) *agenttool.BashJobStore {
	for _, t := range tools {
		if bt, ok := t.(*agenttool.BashTool); ok && bt.Jobs != nil {
			return bt.Jobs
		}
	}
	return nil
}

// MemoryDir returns the persistent memory root directory: $PIGO_HOME/memory, or
// ~/.pigo/memory by default (a single global store so cross-project "global"
// memories are searchable, mirroring the session store's ~/.pigo base). It
// returns "" when the home directory cannot be resolved and no override is set.
func MemoryDir() string {
	dir := os.Getenv("PIGO_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".pigo")
	}
	return filepath.Join(dir, "memory")
}

// OpenMemoryStore opens the persistent memory store under MemoryDir() (index DB
// at <root>/index.db). It returns (nil, nil) — not an error — when persistent
// memory is disabled (memEnabled=false) or the home dir is unresolvable, so the
// caller degrades to file-based auto-memory without treating the off state as a
// failure. A genuine open failure is returned as an error for the caller to log
// non-fatally.
func OpenMemoryStore(memEnabled bool) (*memory.Store, error) {
	if !memEnabled {
		return nil, nil
	}
	root := MemoryDir()
	if root == "" {
		return nil, nil
	}
	dbPath := filepath.Join(root, "index.db")
	return memory.Open(dbPath, root, "")
}

// SkillsDir returns the directory skills are loaded from. It defaults to
// ~/.agents/skills, overridable via PIGO_SKILLS_DIR. An empty string is returned
// when the home directory cannot be resolved and no override is set.
func SkillsDir() string {
	if dir := os.Getenv("PIGO_SKILLS_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agents", "skills")
}

// LoadSkills discovers skills from SkillsDir() once, for both prompt injection
// and /skill-name registration. Under --no-skills it is a no-op. Built-in skills
// are bootstrapped into the skills dir first, then the directory is loaded.
func LoadSkills(noSkills bool) ([]*runtime.Skill, error) {
	if noSkills {
		return nil, nil
	}
	var blog io.Writer
	if os.Getenv("PIGO_DEBUG") != "" {
		blog = os.Stderr
	}
	builtinskills.Bootstrap(ConfigDir(), SkillsDir(), blog)
	dir := SkillsDir()
	if dir == "" {
		return nil, nil
	}
	return runtime.LoadSkillsDir(dir)
}

// PluginsDir returns the directory external plugins are discovered from:
// $PIGO_HOME/plugins, or ~/.pigo/plugins by default. An empty string is returned
// when the home directory cannot be resolved and no override is set (Discover
// then treats it as "no plugins").
func PluginsDir() string {
	dir := os.Getenv("PIGO_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".pigo")
	}
	return filepath.Join(dir, "plugins")
}

// ConfigDir returns the directory pigo reads its global config layer from:
// $PIGO_HOME, or ~/.pigo by default. An empty string is returned when the home
// directory cannot be resolved and no override is set (the caller then treats
// the global layer as absent).
func ConfigDir() string {
	dir := os.Getenv("PIGO_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".pigo")
	}
	return dir
}

// ResolveThinkingLevel resolves the effective reasoning-effort level through the
// layered config chain (US-023): default < global < project < env < CLI flag.
// The global layer is $PIGO_HOME/config.json (or ~/.pigo/config.json); the
// project layer is ./.pigo/config.json in the working directory. A malformed
// layer file or an invalid resolved value is a hard error, surfaced to the
// caller for exit-code mapping. cliLevel is the raw --thinking-level flag ("" =
// unset, so lower layers show through).
func ResolveThinkingLevel(cliLevel string) (agentcore.ThinkingLevel, error) {
	def := runtime.DefaultConfigLayer()
	layers := []*runtime.ConfigLayer{&def}

	if dir := ConfigDir(); dir != "" {
		global, err := runtime.LoadConfigLayer(filepath.Join(dir, "config.json"))
		if err != nil {
			return "", err
		}
		layers = append(layers, global)
	}
	project, err := runtime.LoadConfigLayer(filepath.Join(".pigo", "config.json"))
	if err != nil {
		return "", err
	}
	layers = append(layers, project)

	env := runtime.EnvConfigLayer(os.Getenv)
	layers = append(layers, &env)

	if v := strings.TrimSpace(cliLevel); v != "" {
		cli := runtime.ConfigLayer{ThinkingLevel: &v}
		layers = append(layers, &cli)
	}

	cfg, err := runtime.ResolveConfig(layers...)
	if err != nil {
		return "", err
	}
	return cfg.ThinkingLevel, nil
}

// ResolveHookSet resolves the effective hook set through the same layered config
// chain as ResolveThinkingLevel (default < global < project < env), with one
// difference required by FR-14: the project layer (./.pigo/config.json under
// cwd) is only merged when the directory is trusted. An untrusted directory
// therefore contributes no hooks, so a checked-out repo cannot run arbitrary
// commands until the user trusts it. A malformed layer file is a hard error,
// surfaced to the caller. The returned set is empty (len 0) when no layer
// defines hooks, which InstallHooks treats as "no hooks" (FR-18).
func ResolveHookSet(cwd string, trusted bool) (hooks.HookSet, error) {
	def := runtime.DefaultConfigLayer()
	layers := []*runtime.ConfigLayer{&def}

	if dir := ConfigDir(); dir != "" {
		global, err := runtime.LoadConfigLayer(filepath.Join(dir, "config.json"))
		if err != nil {
			return nil, err
		}
		layers = append(layers, global)
	}
	if trusted {
		project, err := runtime.LoadConfigLayer(filepath.Join(cwd, ".pigo", "config.json"))
		if err != nil {
			return nil, err
		}
		layers = append(layers, project)
	}
	env := runtime.EnvConfigLayer(os.Getenv)
	layers = append(layers, &env)

	cfg, err := runtime.ResolveConfig(layers...)
	if err != nil {
		return nil, err
	}
	return cfg.Hooks, nil
}

// Trusted reports whether cwd is a trusted directory per the shared trust store
// ($PIGO_HOME/trust.json). It is the trust gate for the non-interactive drivers
// (headless / TUI / sub-agent) that have no live trust.Manager to consult, so
// ResolveHookSet can honor FR-14 uniformly. A missing or unreadable store is
// treated as untrusted (fail closed): a directory only runs project-layer hooks
// after the user has explicitly trusted it.
func Trusted(cwd string) bool {
	m, err := trust.NewManager(trust.DefaultPath())
	if err != nil || m == nil {
		return false
	}
	return m.IsTrusted(cwd)
}

// NewConfig builds the loop configuration shared by every driver: the provider
// stream, the dynamic API-key resolver, and the tool registry. It is the single
// definition of "how a run is wired", so the REPL (streamRun) and the headless
// driver cannot drift apart.
func NewConfig(model, providerName string, thinking agentcore.ThinkingLevel, prov provider.Provider, creds *provider.CredentialStore, reg *agenttool.ToolRegistry, reminders *runtime.ReminderRegistry) runtime.RunConfig {
	return runtime.RunConfig{
		LoopConfig: runtime.LoopConfig{
			Model:         model,
			Provider:      providerName,
			ThinkingLevel: thinking,
			Stream:        provider.StreamFnFromProvider(prov),
			GetAPIKey:     creds.GetAPIKey,
		},
		Batch: agenttool.BatchConfig{
			ToolExecutorConfig: agenttool.ToolExecutorConfig{Registry: reg},
		},
		Reminders: reminders,
	}
}

// WireModel returns the provider-less model id that must be sent to the
// upstream API. Interactive drivers keep the full "provider/model_id" key in
// LiveConfig for display and /model commands, but the endpoint was already
// resolved by provider selection, so the wire request must use only the model
// id (headless and ACP already do this via env.Model / ResolveForModel).
func WireModel(model string) string {
	_, modelID, ok := config.SplitModelID(model)
	if !ok || modelID == "" {
		return model
	}
	return modelID
}
