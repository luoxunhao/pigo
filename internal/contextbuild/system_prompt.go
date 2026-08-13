package contextbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	pigoruntime "github.com/smallnest/pigo/internal/runtime"
)

var defaultGuidelines = "# Guidelines\n" +
	"- Prefer precise, minimal changes and verify your work with the available tools.\n" +
	"- Keep the user informed of progress and never claim an action was taken unless it was."

type contextFile struct {
	path    string
	content string
}

// BuildSystemPrompt assembles the system prompt from base, active tools,
// guidelines, append instructions, project context files, skills, and the
// environment. Context file selection follows 04: per directory only one file
// wins (AGENTS.override.md > AGENTS.md > AGENTS.MD > CLAUDE.md > CLAUDE.MD),
// the global agent dir is injected first, ancestors run root-to-cwd, linked
// worktree roots skip their own context file, and Environment has no date.
func BuildSystemPrompt(cfg PromptBuildOptions) (string, error) {
	prompt, _, err := buildSystemPromptWithFingerprint(cfg)
	return prompt, err
}

func buildSystemPromptWithFingerprint(cfg PromptBuildOptions) (string, string, error) {
	files, err := resolveContextFiles(cfg)
	if err != nil {
		return "", "", err
	}
	var b strings.Builder
	base := cfg.BaseInstruction
	if base == "" {
		base = pigoruntime.DefaultBaseInstruction
	}
	b.WriteString(base)

	if len(cfg.Tools) > 0 {
		b.WriteString("\n\n# Available tools\n")
		for _, t := range cfg.Tools {
			fmt.Fprintf(&b, "- %s: %s\n", t.Name(), t.Description())
		}
	}
	b.WriteString("\n\n")
	b.WriteString(defaultGuidelines)

	for _, extra := range cfg.AppendInstructions {
		extra = strings.TrimSpace(extra)
		if extra == "" {
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(extra)
	}

	for _, f := range files {
		b.WriteString("\n\n<project_instructions path=\"")
		b.WriteString(f.path)
		b.WriteString("\">\n")
		b.WriteString(strings.TrimSpace(f.content))
		b.WriteString("\n</project_instructions>")
	}

	b.WriteString(pigoruntime.FormatSkillsForPrompt(cfg.Skills))

	wd := cfg.WorkingDir
	if wd == "" {
		if cwd, err := os.Getwd(); err == nil {
			wd = cwd
		}
	}
	b.WriteString("\n\nEnvironment:\n")
	fmt.Fprintf(&b, "- Working directory: %s\n", wd)
	fmt.Fprintf(&b, "- OS: %s/%s\n", runtime.GOOS, runtime.GOARCH)

	fp := fingerprint(cfg, files)
	return b.String(), fp, nil
}

func resolveContextFiles(cfg PromptBuildOptions) ([]contextFile, error) {
	if !cfg.ContextFilesEnabled {
		return nil, nil
	}
	readFile := cfg.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	seen := map[string]bool{}
	var out []contextFile
	add := func(dir string) error {
		canonical := canonicalPath(dir)
		if canonical == "" || seen[canonical] {
			return nil
		}
		path, content, err := firstContextFile(dir, readFile)
		if err != nil {
			return err
		}
		if path == "" {
			return nil
		}
		seen[canonical] = true
		out = append(out, contextFile{path: path, content: content})
		return nil
	}
	if cfg.GlobalAgentDir != "" {
		if err := add(cfg.GlobalAgentDir); err != nil {
			return nil, err
		}
	}
	isWorktree := cfg.IsWorktreeRoot
	if isWorktree == nil {
		isWorktree = isLinkedWorktreeRoot
	}
	for _, dir := range ancestorChain(cfg.WorkingDir) {
		if isWorktree(dir) {
			continue
		}
		if err := add(dir); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func firstContextFile(dir string, readFile func(string) ([]byte, error)) (string, string, error) {
	for _, name := range []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"} {
		path := filepath.Join(dir, name)
		data, err := readFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", "", fmt.Errorf("contextbuild: read %s: %w", path, err)
		}
		if strings.TrimSpace(string(data)) == "" {
			continue
		}
		return path, string(data), nil
	}
	return "", "", nil
}

// ancestorChain returns working-dir ancestors from the filesystem root down to
// cwd (root first, general-to-specific).
func ancestorChain(cwd string) []string {
	if cwd == "" {
		return nil
	}
	cur := filepath.Clean(cwd)
	var up []string
	for {
		up = append(up, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i, j := 0, len(up)-1; i < j; i, j = i+1, j-1 {
		up[i], up[j] = up[j], up[i]
	}
	return up
}

// isLinkedWorktreeRoot reports whether dir is a linked worktree root: its .git
// entry is a file pointing at another git dir rather than a real directory.
func isLinkedWorktreeRoot(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil || info.IsDir() {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(data)), "gitdir:")
}

func canonicalPath(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(dir)
}

func fingerprint(cfg PromptBuildOptions, files []contextFile) string {
	h := sha256.New()
	fmt.Fprintf(h, "base=%q\ncwd=%q\nglobal=%q\ncontext=%v\n", cfg.BaseInstruction, cfg.WorkingDir, cfg.GlobalAgentDir, cfg.ContextFilesEnabled)
	for _, a := range cfg.AppendInstructions {
		fmt.Fprintf(h, "append=%q\n", a)
	}
	for _, s := range cfg.Skills {
		if s != nil {
			fmt.Fprintf(h, "skill=%q/%q/%q\n", s.Frontmatter.Name, s.Frontmatter.Description, s.Path)
		}
	}
	for _, t := range cfg.Tools {
		fmt.Fprintf(h, "tool=%q\n", t.Name())
	}
	for _, f := range files {
		fmt.Fprintf(h, "file=%q\ncontent=%q\n", f.path, f.content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// promptCache is a small fingerprint-keyed system-prompt cache so identical
// per-request inputs reuse the same string (protecting provider prompt cache).
type promptCache struct {
	mu sync.Mutex
	m  map[string]string
}

func newPromptCache() *promptCache {
	return &promptCache{m: map[string]string{}}
}

func (c *promptCache) get(cfg PromptBuildOptions) (string, error) {
	files, err := resolveContextFiles(cfg)
	if err != nil {
		return "", err
	}
	key := fingerprint(cfg, files)
	c.mu.Lock()
	if p, ok := c.m[key]; ok {
		c.mu.Unlock()
		return p, nil
	}
	c.mu.Unlock()
	prompt, fp, err := buildSystemPromptWithFingerprint(cfg)
	if err != nil {
		return "", err
	}
	if fp == key {
		c.mu.Lock()
		c.m[key] = prompt
		c.mu.Unlock()
	}
	return prompt, nil
}
