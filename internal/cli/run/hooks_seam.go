package run

import (
	"fmt"
	"os"

	"github.com/smallnest/pigo/internal/runtime"
)

// SessionHookSeam returns the per-session hook installer shared by every
// ACP-backed driver (stdio acpcmd, TUI, REPL). Hooks are resolved against the
// session workspace; project hooks load only when that directory is trusted.
// A malformed hook config fails the turn (fail-closed) instead of silently
// disabling the command-level boundary.
func SessionHookSeam() func(cfg *runtime.RunConfig, sessionID, projectDir string) error {
	return func(cfg *runtime.RunConfig, sessionID, projectDir string) error {
		set, err := ResolveHookSet(projectDir, Trusted(projectDir))
		if err != nil {
			return fmt.Errorf("resolve hooks for %s: %w", projectDir, err)
		}
		deps := HookDeps{SessionID: sessionID, ProjectDir: projectDir, WarnLog: os.Stderr}
		d := BuildDispatcher(set, deps)
		InstallSeamsBefore(cfg, d, deps)
		return nil
	}
}
