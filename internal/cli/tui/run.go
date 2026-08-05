package tui

// Run starts the full-screen TUI and blocks until the user quits (Ctrl+C /
// Ctrl+D) or the program errors. It is the alt-screen counterpart to repl.Run:
// cmd/pigo's dispatch calls it on the (no prompt + TTY + no --no-tui) path and
// maps its error to the process exit code. The alt-screen is entered/left via
// the View returned by the root Model, so a clean return here restores the
// terminal to the user's prior scrollback.
func Run(opts Options) error {
	// ACP is the only frontend entry: the TUI drives the agent core through the
	// in-process ACP server (ticket 10 contract).
	return RunACP(opts)
}
