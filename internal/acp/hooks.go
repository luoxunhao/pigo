package acp

import "github.com/smallnest/pigo/internal/runtime"

// HookSeamFunc installs hook seams into a run config for one ACP turn. It is
// supplied by the CLI entry point, which owns hook resolution and trust
// gating; the ACP core only invokes it with the session identity. An error
// fails the turn closed so a malformed hook config cannot silently disable the
// command-level boundary.
type HookSeamFunc func(cfg *runtime.RunConfig, sessionID, projectDir string) error
