package acp

import (
	"context"
	"io"

	"github.com/smallnest/pigo/internal/trust"
)

// ServeStdio runs an ACP server over newline-delimited JSON-RPC on in/out,
// the entry point used by external ACP clients such as Zed. The server lives
// until the client closes the transport or cancels ctx.
func ServeStdio(ctx context.Context, runner SessionRunner, pigoHome, model, sysPrompt, cwd string, mgr *trust.Manager, dreamCfg *DreamConfig, in io.Reader, out io.Writer) error {
	tr := NewStdioTransport(in, out)
	defer tr.Close()
	disp := newDispatcher(runner, tr, pigoHome, model, sysPrompt, cwd, mgr, dreamCfg)
	return NewServer(tr, disp).Serve(ctx)
}
