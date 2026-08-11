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

	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/httpapi"
	"github.com/smallnest/pigo/internal/plugin"
)

const defaultServePort = 4096

type serveOptions struct {
	hostname string
	port     int
	password string
	cors     []string
}

func runServe(args []string, version string, out, errOut io.Writer) int {
	opts, err := parseServeArgs(args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	applyServeEnv(&opts)

	if !isLoopback(opts.hostname) && opts.password == "" {
		fmt.Fprintln(errOut, "pigo serve: --hostname requires --password or PIGO_SERVER_PASSWORD")
		return 2
	}

	pluginMgr, err := plugin.Discover(run.PluginsDir(), errOut, errOut)
	if err != nil {
		fmt.Fprintf(errOut, "pigo serve: %v\n", err)
		return 1
	}
	defer pluginMgr.Close()

	handler, err := httpapi.NewRouter(httpapi.Config{
		Version:        version,
		Password:       opts.password,
		AllowedOrigins: opts.cors,
		PluginManager:  pluginMgr,
	})
	if err != nil {
		fmt.Fprintf(errOut, "pigo serve: %v\n", err)
		return 1
	}

	addr := net.JoinHostPort(opts.hostname, strconv.Itoa(opts.port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(out, "pigo serve listening on http://%s\n", addr)
		errCh <- srv.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(errOut, "pigo serve: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return 0
	}
}

func parseServeArgs(args []string) (serveOptions, error) {
	var opts serveOptions
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.hostname, "hostname", "127.0.0.1", "hostname to listen on")
	fs.IntVar(&opts.port, "port", defaultServePort, "port to listen on")
	fs.StringVar(&opts.password, "password", "", "HTTP Basic password")
	fs.Var(stringSliceValue{target: &opts.cors}, "cors", "additional allowed CORS origins")
	if err := fs.Parse(args); err != nil {
		return opts, fmt.Errorf("pigo serve: %w", err)
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("pigo serve: unexpected argument %q", fs.Arg(0))
	}
	return opts, nil
}

func applyServeEnv(opts *serveOptions) {
	if v := os.Getenv("PIGO_SERVER_HOSTNAME"); v != "" {
		opts.hostname = v
	}
	if v := os.Getenv("PIGO_SERVER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.port = n
		}
	}
	if v := os.Getenv("PIGO_SERVER_PASSWORD"); v != "" {
		opts.password = v
	}
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type stringSliceValue struct {
	target *[]string
}

func (v stringSliceValue) String() string {
	return fmt.Sprint(*v.target)
}

func (v stringSliceValue) Set(s string) error {
	*v.target = append(*v.target, s)
	return nil
}
