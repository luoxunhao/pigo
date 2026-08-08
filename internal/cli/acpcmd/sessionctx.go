package acpcmd

import (
	"path/filepath"
	"sync"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/cli/prompts"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/trust"
)

// sessionContextBuilder implements acp.SessionContextFactory for a shared
// pigo --acp process. It rebuilds the system prompt and tool roots for each
// session cwd and caches slash registries by (cwd, trust fingerprint) so a
// trust change invalidates project-scoped commands.
type sessionContextBuilder struct {
	opts   Options
	env    run.Env
	policy run.ToolPolicy
	mgr    *trust.Manager
	live   *cli.LiveConfig
	sem    chan struct{}

	mu    sync.Mutex
	cache map[string]*runtime.SlashRegistry
}

func newSessionContextBuilder(opts Options, env run.Env, policy run.ToolPolicy, mgr *trust.Manager) *sessionContextBuilder {
	return &sessionContextBuilder{
		opts:   opts,
		env:    env,
		policy: policy,
		mgr:    mgr,
		live: &cli.LiveConfig{
			Model:         opts.Model,
			ProviderName:  env.ProviderName,
			Provider:      env.Provider,
			BaseURL:       opts.BaseURL,
			Protocol:      opts.Protocol,
			ThinkingLevel: opts.ThinkingLevel,
			ContextWindow: cli.DefaultContextWindow,
		},
		sem:   runtime.NewSubagentSemaphore(),
		cache: make(map[string]*runtime.SlashRegistry),
	}
}

// Build creates the isolated context for one session. The factory is safe for
// concurrent use: the registry cache is mutex-guarded and tools are cloned per
// call.
func (b *sessionContextBuilder) Build(cwd string, additionalDirectories []string) (acp.SessionContext, error) {
	sysPrompt, err := runtime.BuildSystemPrompt(runtime.PromptConfig{
		BaseInstruction:    b.opts.SystemPrompt,
		WorkingDir:         cwd,
		Root:               cwd,
		AppendInstructions: b.env.AppendInstructions,
		Skills:             b.env.Skills,
		ReadToolAvailable:  run.HasReadTool(b.env.Tools),
	})
	if err != nil {
		return acp.SessionContext{}, err
	}
	tools := acp.CloneToolsForSession(b.env.Tools, cwd, additionalDirectories, func() *runtime.SubAgentTool {
		return run.SessionTaskTool(cwd, b.policy, b.env.Model, b.env.ProviderName, b.env.Provider, b.env.APIKey, b.sem)
	})
	registry, err := b.registryFor(cwd)
	if err != nil {
		return acp.SessionContext{}, err
	}
	return acp.SessionContext{
		SysPrompt: sysPrompt,
		Tools:     tools,
		Registry:  registry,
	}, nil
}

func (b *sessionContextBuilder) registryFor(cwd string) (*runtime.SlashRegistry, error) {
	fingerprint := "no-trust"
	if b.mgr != nil {
		fingerprint = b.mgr.Fingerprint()
	}
	key := cwd + "\x00" + fingerprint

	b.mu.Lock()
	if reg, ok := b.cache[key]; ok {
		b.mu.Unlock()
		return reg, nil
	}
	b.mu.Unlock()

	live := *b.live
	trusted := false
	if b.mgr != nil {
		trusted = b.mgr.IsTrusted(cwd)
	}
	reg, err := prompts.BuildSlashRegistry(&live, b.env.Skills, b.env.Plugins, prompts.PromptTemplateSources{
		Settings:       b.opts.ConfigPrompts,
		CLI:            b.opts.CliPrompts,
		Disable:        b.opts.NoPromptTemplates,
		ProjectDir:     filepath.Join(cwd, ".pigo", "prompts"),
		ProjectTrusted: trusted,
	})
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	b.cache[key] = reg
	b.mu.Unlock()
	return reg, nil
}
