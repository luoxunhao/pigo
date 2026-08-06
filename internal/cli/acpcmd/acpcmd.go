// Package acpcmd runs pigo as a standalone Agent Client Protocol server over
// stdio. It is the CLI seam external clients (Zed, future desktop shells) use:
// the same run assembly and trust wiring as the TUI, but the wire transport
// instead of an in-process channel pair.
package acpcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/cli/prompts"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/sessionstore"
	"github.com/smallnest/pigo/internal/trust"
)

// Options mirrors the resolved CLI flags needed to assemble an ACP run. It is
// intentionally smaller than the interactive option structs: stdio mode owns
// no UI, slash registry, or plugin lifecycle surface beyond closing the
// shared run environment.
type Options struct {
	Model              string
	ProviderName       string
	BaseURL            string
	Protocol           string
	APIKey             string
	ThinkingLevel      agentcore.ThinkingLevel
	NoTools            bool
	NoSkills           bool
	SystemPrompt       string
	AppendSystemPrompt []string
	Approve            bool
	MemoryEnabled      bool
	ConfigPrompts      []string
	CliPrompts         []string
	NoPromptTemplates  bool
}

// Run assembles the run environment and serves ACP until stdin closes. It
// returns a process exit code: 0 for a clean close, 1 for startup or protocol
// failures.
func Run(ctx context.Context, opts Options, stdin io.Reader, stdout, stderr io.Writer) int {
	env, err := run.SetupEnv(opts.Model, opts.BaseURL, opts.Protocol, opts.ProviderName, opts.APIKey, opts.NoTools, opts.NoSkills, opts.SystemPrompt, opts.AppendSystemPrompt, opts.MemoryEnabled)
	if err != nil {
		fmt.Fprintf(stderr, "pigo acp: %v\n", err)
		return 1
	}
	if env.Plugins != nil {
		defer env.Plugins.Close()
	}
	if env.Memory != nil {
		defer env.Memory.Close()
	}

	home, err := sessionstore.PigoHome()
	if err != nil {
		fmt.Fprintf(stderr, "pigo acp: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "pigo acp: %v\n", err)
		return 1
	}
	mgr, err := trust.NewManager(trust.DefaultPath())
	if err != nil {
		fmt.Fprintf(stderr, "pigo acp: %v\n", err)
		return 1
	}
	if opts.Approve {
		mgr.SetSessionTrust(cwd)
	}

	live := &cli.LiveConfig{
		Model:         opts.Model,
		ProviderName:  env.ProviderName,
		Provider:      env.Provider,
		BaseURL:       opts.BaseURL,
		Protocol:      opts.Protocol,
		ThinkingLevel: opts.ThinkingLevel,
		ContextWindow: cli.DefaultContextWindow,
	}
	reg, err := prompts.BuildSlashRegistry(live, env.Skills, env.Plugins, prompts.PromptTemplateSources{
		Settings:       opts.ConfigPrompts,
		CLI:            opts.CliPrompts,
		Disable:        opts.NoPromptTemplates,
		ProjectDir:     filepath.Join(cwd, ".pigo", "prompts"),
		ProjectTrusted: run.Trusted(cwd),
	})
	if err != nil {
		fmt.Fprintf(stderr, "pigo acp: %v\n", err)
		return 1
	}

	runner := &acp.RuntimeRunner{
		Provider:      env.Provider,
		ProviderName:  env.ProviderName,
		Model:         opts.Model,
		APIKey:        env.APIKey,
		ThinkingLevel: opts.ThinkingLevel,
		Tools:         env.Tools,
	}
	configured := acp.NewConfiguredModels(config.FileConfigPath())
	_ = configured.Load()
	runner.ConfiguredModels = configured
	dreamCfg := &acp.DreamConfig{
		Model:         opts.Model,
		BaseURL:       opts.BaseURL,
		Protocol:      opts.Protocol,
		ProviderName:  env.ProviderName,
		APIKey:        env.APIKey,
		ThinkingLevel: opts.ThinkingLevel,
	}
	if _, _, ok := config.SplitModelID(opts.Model); ok {
		if entry, found := configured.Find(opts.Model); found {
			dreamCfg.BaseURL = entry.BaseURL
			dreamCfg.Protocol = entry.Protocol
			dreamCfg.ProviderName = entry.Provider
		}
	}

	if err := acp.ServeStdioWithRegistry(ctx, runner, home, opts.Model, env.SysPrompt, cwd, mgr, dreamCfg, reg, stdin, stdout); err != nil {
		if err == acp.ErrClosed {
			return 0
		}
		fmt.Fprintf(stderr, "pigo acp: %v\n", err)
		return 1
	}
	return 0
}
