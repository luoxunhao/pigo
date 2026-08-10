//go:build !windows

package agenttool

import "os/exec"

// wireProcessTreeKill is a no-op on platforms where exec.CommandContext already
// terminates the child process on context cancellation.
func wireProcessTreeKill(cmd *exec.Cmd) (assign func(pid int), cleanup func()) {
	return func(int) {}, func() {}
}
