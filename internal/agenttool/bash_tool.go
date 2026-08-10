// This file implements the bash tool (US-018): run a shell command, streaming
// stdout/stderr back as tool_execution_update partials, honoring a timeout and
// context cancellation (which kills the child process group). A non-zero exit
// is surfaced as an error (isError) whose message carries the captured output.
package agenttool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/smallnest/pigo/internal/agentcore"
)

// bashDefaultTimeout bounds a command that does not specify one.
const bashDefaultTimeout = 2 * time.Minute

// bashMaxTimeout caps any requested timeout.
const bashMaxTimeout = 10 * time.Minute

// bashWaitDelay bounds how long cmd.Wait may keep waiting on inherited
// stdout/stderr pipes after the direct shell has exited. On Windows a canceled
// shell can leave grandchildren holding the pipes open, so the delay turns an
// indefinite cmd.Run() hang into a bounded return.
const bashWaitDelay = 3 * time.Second

// bashMaxOutputBytes caps how many bytes of combined stdout/stderr the bash tool
// returns to the model. A single command can emit megabytes (build logs, a big
// cat), which — unlike the timeout cap — would otherwise flow into context whole
// and blow the window. Output past this size is truncated to a head + tail
// preview (see truncateBashOutput), mirroring search's searchMaxResults/"[truncated
// …]" convention. This is the tool's own inner cap; a later executor-layer budget
// may impose a stricter outer limit.
const bashMaxOutputBytes = 30_000

// truncateBashOutput caps s at bashMaxOutputBytes using the shared
// truncateToBudget idiom (head + "[truncated N bytes]" marker + tail, cut on
// UTF-8 rune boundaries). It is the bash tool's own inner cap; the executor
// layer applies a separate, uniform outer budget afterward.
func truncateBashOutput(s string) string {
	return truncateToBudget(s, bashMaxOutputBytes)
}

// trimUTF8Prefix drops trailing bytes of s that form an incomplete rune, so the
// returned prefix ends on a rune boundary.
func trimUTF8Prefix(s string) string {
	for len(s) > 0 {
		if r, size := utf8.DecodeLastRuneInString(s); r == utf8.RuneError && size <= 1 {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

// trimUTF8Suffix drops leading bytes of s that form an incomplete rune, so the
// returned suffix starts on a rune boundary.
func trimUTF8Suffix(s string) string {
	for len(s) > 0 {
		if r, size := utf8.DecodeRuneInString(s); r == utf8.RuneError && size <= 1 {
			s = s[1:]
			continue
		}
		break
	}
	return s
}

// BashTool runs shell commands. Dir bounds the working directory (empty = the
// process CWD). Shell selects the interpreter (empty = "bash -c").
type BashTool struct {
	// Dir is the working directory for commands. Empty uses the process CWD.
	Dir string
	// Shell is the interpreter path. Empty defaults to "bash".
	Shell string
	// Jobs holds background jobs launched with run_in_background. When nil,
	// run_in_background is rejected (the front-end did not wire a store).
	Jobs *BashJobStore
}

// bashToolArgs is the decoded argument shape for BashTool.
type bashToolArgs struct {
	// Command is the shell command line to run.
	Command string `json:"command"`
	// TimeoutMs optionally overrides the default timeout (milliseconds).
	TimeoutMs int `json:"timeout_ms,omitempty"`
	// RunInBackground detaches the command from the turn: it keeps running after
	// Execute returns, and its output is drained later via bash_output. A
	// background command has no default timeout (so dev servers/watchers run
	// indefinitely); timeout_ms still caps it if given.
	RunInBackground bool `json:"run_in_background,omitempty"`
}

// Name implements AgentTool.
func (t *BashTool) Name() string { return "bash" }

// Description implements AgentTool.
func (t *BashTool) Description() string {
	return "Run a shell command, streaming stdout/stderr. Supports a timeout " +
		"and cancellation. A non-zero exit code is reported as an error. " +
		"Set run_in_background=true for long-running commands (dev servers, " +
		"watchers): it returns immediately with a bash_id you drain with " +
		"bash_output and stop with kill_bash. " +
		"On Windows the command runs under bash if available (Git Bash/WSL), " +
		"else PowerShell, else cmd — prefer portable commands."
}

// Schema implements AgentTool.
func (t *BashTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "command":    {"type": "string", "description": "Shell command line to run."},
    "timeout_ms": {"type": "integer", "description": "Timeout in milliseconds (capped at 10 minutes). Ignored in background unless set.", "minimum": 0},
    "run_in_background": {"type": "boolean", "description": "Run detached and return immediately with a bash_id; drain output with bash_output, stop with kill_bash."}
  },
  "required": ["command"],
  "additionalProperties": false
}`)
}

// ExecutionMode implements AgentTool. Commands can have side effects → sequential.
func (t *BashTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionSequential
}

// shellLookPath resolves a program on PATH. It is a package var so tests can
// simulate a Windows box with or without bash installed.
var shellLookPath = exec.LookPath

// wslBashProbe reports whether the WSL bash relay at path can actually run a
// command. It is a package var so tests can simulate broken or healthy WSL.
var wslBashProbe = probeWSLBash

// wslProbeOnce caches the WSL relay probe so a broken relay is not re-probed
// on every bash call.
var (
	wslProbeOnce sync.Once
	wslProbeOK   bool
)

// resolveShell picks the interpreter and the flag that makes it read the command
// from the next argument. An explicit shell (BashTool.Shell) is always honored as
// a POSIX-style "<shell> -c <command>".
//
// On Windows with no explicit shell, the naive "bash -c" hardcode fails on stock
// machines that have no bash on PATH — the model then retries bash blindly and
// every call errors (issue #518). So we prefer a real bash when one is present
// (Git Bash / WSL / MSYS), since commands are authored in bash syntax, and fall
// back to PowerShell, then cmd, so a command still runs on a bare Windows box.
func resolveShell(explicit, goos string, lookPath func(string) (string, error)) (shell, flag string) {
	if explicit != "" {
		return explicit, "-c"
	}
	if goos == "windows" {
		if p := resolveWindowsBash(lookPath); p != "" {
			return p, "-c"
		}
		if p, err := lookPath("powershell"); err == nil {
			return p, "-Command"
		}
		return "cmd", "/C"
	}
	return "bash", "-c"
}

// resolveWindowsBash returns a usable bash on Windows, or "" when only a
// broken shell would be available. Git Bash install locations are preferred
// because PATH may resolve bash to C:\Windows\System32\bash.exe, the WSL
// relay. That relay fails on machines with no real Linux distro (for example
// only docker-desktop), so it is probed before being accepted.
func resolveWindowsBash(lookPath func(string) (string, error)) string {
	for _, p := range gitBashCandidates() {
		if isRegularFile(p) {
			return p
		}
	}
	// Git can live outside the standard install dirs (portable installs,
	// D:\Program Files\Git, ...). git.exe is at <root>\cmd\git.exe, so derive
	// the sibling <root>\bin\bash.exe from the git executable on PATH.
	if gitPath, err := lookPath("git"); err == nil {
		if p := gitBashCandidateFromGitPath(gitPath); isRegularFile(p) {
			return p
		}
	}
	p, err := lookPath("bash")
	if err != nil {
		return ""
	}
	if isWSLBashRelay(p) && !wslBashProbe(p) {
		return ""
	}
	return p
}

// gitBashCandidates lists the standard Git for Windows bash locations so a
// real bash is preferred over the WSL relay. It is a var so tests can
// substitute a deterministic list.
var gitBashCandidates = defaultGitBashCandidates

func defaultGitBashCandidates() []string {
	candidates := []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		candidates = append(candidates, filepath.Join(local, `Programs\Git\bin\bash.exe`))
	}
	return candidates
}

// gitBashCandidateFromGitPath maps a git.exe path to its sibling Git Bash:
// <root>\cmd\git.exe -> <root>\bin\bash.exe.
func gitBashCandidateFromGitPath(gitPath string) string {
	if strings.TrimSpace(gitPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Dir(gitPath)), "bin", "bash.exe")
}

// isWSLBashRelay reports whether path is the WSL bash relay in System32.
func isWSLBashRelay(path string) bool {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	wslBash := filepath.Join(root, `System32\bash.exe`)
	return strings.EqualFold(filepath.Clean(path), filepath.Clean(wslBash))
}

// isRegularFile reports whether path names an existing non-directory file.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// probeWSLBash runs a trivial command through the WSL relay to verify that a
// real distro is installed. docker-desktop-only machines fail here quickly.
func probeWSLBash(path string) bool {
	wslProbeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, "-c", ":")
		wslProbeOK = cmd.Run() == nil
	})
	return wslProbeOK
}

// streamWriter forwards each written chunk to onUpdate as a growing partial
// result while accumulating the full output. It is safe for concurrent use so
// stdout and stderr can share the same combined buffer.
type streamWriter struct {
	mu       *sync.Mutex
	buf      *bytes.Buffer
	onUpdate agentcore.ToolUpdateFunc
}

func (w streamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.buf.Write(p)
	snapshot := w.buf.String()
	w.mu.Unlock()
	if w.onUpdate != nil {
		w.onUpdate(agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent(snapshot)}})
	}
	return len(p), nil
}

// Execute implements AgentTool. It streams combined stdout/stderr via onUpdate,
// enforces a timeout, and kills the process on context cancellation. A non-zero
// exit returns a Go error (→ isError) carrying the exit code and output.
func (t *BashTool) Execute(ctx context.Context, id string, args json.RawMessage, onUpdate agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	a, bad := decodeArgs[bashToolArgs](args, "bash")
	if bad != nil {
		return *bad, nil
	}
	if a.Command == "" {
		return errorResult("bash: command is required"), nil
	}

	if a.RunInBackground {
		return t.startBackground(a)
	}

	timeout := bashDefaultTimeout
	if a.TimeoutMs > 0 {
		timeout = time.Duration(a.TimeoutMs) * time.Millisecond
	}
	if timeout > bashMaxTimeout {
		timeout = bashMaxTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell, flag := resolveShell(t.Shell, runtime.GOOS, shellLookPath)
	cmd := exec.CommandContext(runCtx, shell, flag, a.Command)
	cmd.WaitDelay = bashWaitDelay
	assignTreeKill, cleanupTreeKill := wireProcessTreeKill(cmd)
	defer cleanupTreeKill()
	if t.Dir != "" {
		cmd.Dir = t.Dir
	}

	var mu sync.Mutex
	var combined bytes.Buffer
	sw := streamWriter{mu: &mu, buf: &combined, onUpdate: onUpdate}
	cmd.Stdout = sw
	cmd.Stderr = sw

	err := cmd.Start()
	if err == nil {
		assignTreeKill(cmd.Process.Pid)
		err = cmd.Wait()
	}

	mu.Lock()
	output := combined.String()
	mu.Unlock()

	// Cap the output before it enters any ToolResult / error message, so a single
	// command's huge output cannot blow the model's context. Truncation keeps a
	// head + tail preview with a "[truncated N bytes]" marker in the middle.
	output = truncateBashOutput(output)

	// Context cancellation / timeout takes precedence in the message.
	if runCtx.Err() == context.DeadlineExceeded {
		return agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent(output)}},
			fmt.Errorf("bash: command timed out after %s\n%s", timeout, output)
	}
	if ctx.Err() == context.Canceled {
		return agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent(output)}},
			fmt.Errorf("bash: command canceled\n%s", output)
	}

	// A missing interpreter (no bash/powershell/cmd on PATH) surfaces as an
	// *exec.Error before the command ever runs. Report it with actionable
	// guidance instead of a bare "code -1", so the model stops retrying blindly.
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent(output)}},
			fmt.Errorf("bash: could not start shell %q: %v. On Windows install Git Bash or WSL (or configure a shell); commands are bash syntax", shell, execErr.Err)
	}

	if err != nil {
		exitCode := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		}
		return agentcore.AgentToolResult{
				Content: agentcore.ContentList{agentcore.NewTextContent(output)},
				Details: map[string]any{"exitCode": exitCode},
			},
			fmt.Errorf("bash: command exited with code %d\n%s", exitCode, output)
	}

	return agentcore.AgentToolResult{
		Content: agentcore.ContentList{agentcore.NewTextContent(output)},
		Details: map[string]any{"exitCode": 0},
	}, nil
}

// startBackground launches the command detached from the turn context and
// returns immediately with a bash_id. The job runs under its own cancelable
// context (rooted at context.Background(), not the turn ctx which is canceled
// when the turn ends), so it survives past Execute. A background command has no
// default timeout — a dev server or watcher is expected to run indefinitely —
// but an explicit timeout_ms still caps it. Its combined output accumulates in
// the job's buffer for bash_output to drain; kill_bash cancels its context.
func (t *BashTool) startBackground(a bashToolArgs) (agentcore.AgentToolResult, error) {
	if t.Jobs == nil {
		return errorResult("bash: run_in_background is not available in this environment"), nil
	}

	var jobCtx context.Context
	var cancel context.CancelFunc
	if a.TimeoutMs > 0 {
		timeout := time.Duration(a.TimeoutMs) * time.Millisecond
		if timeout > bashMaxTimeout {
			timeout = bashMaxTimeout
		}
		jobCtx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		jobCtx, cancel = context.WithCancel(context.Background())
	}

	shell, flag := resolveShell(t.Shell, runtime.GOOS, shellLookPath)
	cmd := exec.CommandContext(jobCtx, shell, flag, a.Command)
	cmd.WaitDelay = bashWaitDelay
	assignTreeKill, cleanupTreeKill := wireProcessTreeKill(cmd)
	if t.Dir != "" {
		cmd.Dir = t.Dir
	}

	job := t.Jobs.create(a.Command, cancel)
	w := job.writer()
	cmd.Stdout = w
	cmd.Stderr = w

	if err := cmd.Start(); err != nil {
		cancel()
		cleanupTreeKill()
		job.finish(-1, err.Error())
		return errorResult(fmt.Sprintf("bash: could not start background command: %v", err)), nil
	}
	assignTreeKill(cmd.Process.Pid)

	go func() {
		err := cmd.Wait()
		cleanupTreeKill()
		cancel()
		exitCode := 0
		errMsg := ""
		if err != nil {
			exitCode = -1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				exitCode = ee.ExitCode()
			}
			errMsg = err.Error()
		}
		job.finish(exitCode, errMsg)
	}()

	msg := fmt.Sprintf("started background command %s: %s\nuse bash_output %q to read its output, kill_bash %q to stop it", job.ID, a.Command, job.ID, job.ID)
	return agentcore.AgentToolResult{
		Content: agentcore.ContentList{agentcore.NewTextContent(msg)},
		Details: map[string]any{"bash_id": job.ID, "background": true},
	}, nil
}
