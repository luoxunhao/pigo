package acp

import (
	"context"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/agenttool"
	"github.com/smallnest/pigo/internal/memory"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/trust"
)

func memoryStoreFromTools(tools []agentcore.AgentTool) *memory.Store {
	for _, tool := range tools {
		if mt, ok := tool.(*agenttool.MemorySearchTool); ok {
			return mt.Store
		}
	}
	return nil
}

// newDispatcher assembles the shared ACP dispatcher wiring for any transport:
// permission broker, rewind snapshot, dream/compact/memory extensions. It is
// used by the in-process TUI path and the stdio server so both frontends see
// the same behavior.
func newDispatcher(runner SessionRunner, transport Transport, pigoHome, model, sysPrompt, cwd string, mgr *trust.Manager, dreamCfg *DreamConfig) *Dispatcher {
	return newDispatcherWithHooks(runner, transport, pigoHome, model, sysPrompt, cwd, mgr, dreamCfg, nil)
}

func newDispatcherWithHooks(runner SessionRunner, transport Transport, pigoHome, model, sysPrompt, cwd string, mgr *trust.Manager, dreamCfg *DreamConfig, hookSeam HookSeamFunc) *Dispatcher {
	broker := NewACPPermissionBroker(transport, mgr, cwd, 0)
	var snap *agenttool.FileSnapshotRecorder
	if rr, ok := runner.(*RuntimeRunner); ok {
		snap = rr.snapshotRecorder()
	}
	disp := NewDispatcher(NewSessionManager(runner), transport, pigoHome, model, sysPrompt, broker, snap)
	disp.SetHookSeam(hookSeam)
	disp.SetRunner(runner)
	disp.SetDreamConfig(dreamCfg)
	if rr, ok := runner.(*RuntimeRunner); ok {
		if rr.ConfiguredModels != nil {
			disp.SetConfiguredModels(rr.ConfiguredModels)
		}
		disp.SetProviderName(rr.ProviderName)
		creds := provider.NewCredentialStore(nil)
		if rr.APIKey != "" {
			creds.SetOverride(rr.ProviderName, rr.APIKey)
		}
		disp.SetCredentialStore(creds)
		disp.SetCompactConfig(&CompactConfig{
			Provider:      rr.Provider,
			ProviderName:  rr.ProviderName,
			Model:         rr.Model,
			APIKey:        rr.APIKey,
			ThinkingLevel: rr.ThinkingLevel,
			Tools:         rr.Tools,
		})
		disp.SetMemoryConfig(&MemoryConfig{Store: memoryStoreFromTools(rr.Tools)})
	}
	return disp
}

// StartInProcess starts an ACP server in the current process over an
// in-process channel transport and returns the client half plus a stop
// function. It is the seam the TUI (and later the REPL) uses to talk to the
// agent core through ACP without spawning a subprocess.
func StartInProcess(runner SessionRunner, pigoHome, model, sysPrompt, cwd string, mgr *trust.Manager, dreamCfg *DreamConfig) (*Client, func()) {
	return StartInProcessWithHooks(runner, pigoHome, model, sysPrompt, cwd, mgr, dreamCfg, nil)
}

// StartInProcessWithHooks is StartInProcess with a per-session hook seam for
// tests and embedded drivers that want command-level policy enforcement.
func StartInProcessWithHooks(runner SessionRunner, pigoHome, model, sysPrompt, cwd string, mgr *trust.Manager, dreamCfg *DreamConfig, hookSeam HookSeamFunc) (*Client, func()) {
	clientT, serverT := NewChannelPair()
	disp := newDispatcherWithHooks(runner, serverT, pigoHome, model, sysPrompt, cwd, mgr, dreamCfg, hookSeam)
	reg := runtime.NewRegistry()
	reg.SetHome(pigoHome)
	if rr, ok := runner.(*RuntimeRunner); ok {
		for _, tool := range rr.Tools {
			if st, ok := tool.(*runtime.SubAgentTool); ok {
				st.SetSubagentRegistry(reg)
				st.SetSubagentCwd(cwd)
			}
		}
	}
	disp.SetSubagentRegistry(reg)
	srv := NewServer(serverT, disp)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = srv.Serve(ctx)
	}()
	cl := NewClient(clientT)
	return cl, func() {
		cl.Close()
		cancel()
	}
}
