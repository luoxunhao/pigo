package acp

import (
	"context"
	"io"

	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/trust"
)

// ServeStdio runs an ACP server over newline-delimited JSON-RPC on in/out,
// the entry point used by external ACP clients such as Zed. The server lives
// until the client closes the transport or cancels ctx.
func ServeStdio(ctx context.Context, runner SessionRunner, pigoHome, model, sysPrompt, cwd string, mgr *trust.Manager, dreamCfg *DreamConfig, in io.Reader, out io.Writer) error {
	return ServeStdioWithRegistry(ctx, runner, pigoHome, model, sysPrompt, cwd, mgr, dreamCfg, nil, nil, in, out)
}

// ServeStdioWithRegistry is ServeStdio with an optional full slash registry so
// external clients see and can invoke user templates, skills, and plugins.
func ServeStdioWithRegistry(ctx context.Context, runner SessionRunner, pigoHome, model, sysPrompt, cwd string, mgr *trust.Manager, dreamCfg *DreamConfig, reg *runtime.SlashRegistry, hookSeam HookSeamFunc, in io.Reader, out io.Writer) error {
	tr := NewStdioTransport(in, out)
	defer tr.Close()
	disp := newDispatcherWithHooks(runner, tr, pigoHome, model, sysPrompt, cwd, mgr, dreamCfg, hookSeam)
	disp.SetSlashRegistry(reg)
	return NewServer(tr, disp).Serve(ctx)
}

// ServeStdioWithSessionContext runs an ACP server with a per-session context
// factory. It is the entry point for shared pigo processes that serve multiple
// workspaces: the factory rebuilds system prompts, tool roots, and slash
// registries for each session cwd instead of using the process startup cwd.
func ServeStdioWithSessionContext(ctx context.Context, runner SessionRunner, pigoHome, model string, factory SessionContextFactory, mgr *trust.Manager, dreamCfg *DreamConfig, hookSeam HookSeamFunc, subagents *runtime.Registry, in io.Reader, out io.Writer) error {
	tr := NewStdioTransport(in, out)
	defer tr.Close()
	disp := newDispatcherWithHooks(runner, tr, pigoHome, model, "", "", mgr, dreamCfg, hookSeam)
	disp.SetSessionContextFactory(factory)
	disp.SetSubagentRegistry(subagents)
	return NewServer(tr, disp).Serve(ctx)
}
