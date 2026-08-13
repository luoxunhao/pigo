package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/smallnest/pigo/internal/acp"
	"github.com/smallnest/pigo/internal/cli/config"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/httpclient"
	"github.com/smallnest/pigo/internal/plugin"
)

type acpOptions struct {
	hostname string
	port     int
	password string
}

func runACP(args []string, version string, in io.Reader, out io.Writer, errOut io.Writer) int {
	opts, err := parseAcpArgs(args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	pluginMgr, err := plugin.Discover(run.PluginsDir(), errOut, errOut)
	if err != nil {
		fmt.Fprintf(errOut, "pigo acp: %v\n", err)
		return 1
	}
	defer pluginMgr.Close()

	acpOpts := cliOptions{}
	if cfg, cfgErr := config.LoadFileConfig(config.FileConfigPath()); cfgErr == nil {
		applyFileConfig(&acpOpts, cfg, func(string) bool { return false })
	}
	if level, levelErr := run.ResolveThinkingLevel(acpOpts.thinkingLevel); levelErr == nil {
		acpOpts.thinkingLevel = string(level)
	}
	httpCfg, err := httpServeConfigWithAutoReject(acpOpts, false)
	if err != nil {
		fmt.Fprintf(errOut, "pigo acp: %v\n", err)
		return 1
	}
	httpCfg.Version = version
	httpCfg.Password = opts.password
	httpCfg.PluginManager = pluginMgr
	handler, err := httpapi.NewRouter(httpCfg)
	if err != nil {
		fmt.Fprintf(errOut, "pigo acp: %v\n", err)
		return 1
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(opts.hostname, strconv.Itoa(opts.port)))
	if err != nil {
		fmt.Fprintf(errOut, "pigo acp: %v\n", err)
		return 1
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	var clientOpts []httpclient.ClientOption
	if opts.password != "" {
		password := opts.password
		clientOpts = append(clientOpts, httpclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.SetBasicAuth("pigo", password)
			return nil
		}))
	}
	client, err := httpclient.NewClientWithResponses("http://"+ln.Addr().String(), clientOpts...)
	if err != nil {
		fmt.Fprintf(errOut, "pigo acp: %v\n", err)
		_ = srv.Close()
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	transport := acp.NewStdioTransport(in, out)
	defer transport.Close()
	adapter := acp.NewHTTPAdapter(client, transport, version)
	server := acp.NewServer(transport, adapter)
	server.SetVersion(version)
	err = server.Serve(ctx)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	if err != nil && err != acp.ErrClosed {
		fmt.Fprintf(errOut, "pigo acp: %v\n", err)
		return 1
	}
	return 0
}

func parseAcpArgs(args []string) (acpOptions, error) {
	var opts acpOptions
	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.hostname, "hostname", "127.0.0.1", "hostname to listen on")
	fs.IntVar(&opts.port, "port", 0, "port to listen on (0 = random)")
	fs.StringVar(&opts.password, "password", "", "HTTP Basic password")
	if err := fs.Parse(args); err != nil {
		return opts, fmt.Errorf("pigo acp: %w", err)
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("pigo acp: unexpected argument %q", fs.Arg(0))
	}
	return opts, nil
}
