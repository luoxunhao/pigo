// This file implements the write tool (US-016): create or overwrite a file at a
// given path, creating parent directories as needed. Overwrites are reported so
// the caller/model knows an existing file was replaced (parity with pi's write
// behavior). Paths resolve against a Root and are rejected if they escape it.
package agenttool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smallnest/pigo/internal/agentcore"
)

// WriteTool writes text files under Root, creating parent directories as needed.
type WriteTool struct {
	// Root bounds all writes; a path resolving outside Root is rejected. Empty
	// Root defaults to the current working directory.
	Root string
	// ExtraRoots are additional trusted directories a write may target even though
	// they lie outside Root. It exists for the skills directory so the model can
	// author or update skills (create a new SKILL.md, edit an existing one) that
	// live outside the workspace.
	ExtraRoots []string
	// Snap, when non-nil, records the file's prior content before it is written so
	// the /rewind command can roll the change back. It is shared with the edit tool.
	Snap *FileSnapshotRecorder
	// Observe, when non-nil, enforces read-before-overwrite: an existing file
	// must have been read by this session at the current version before it can
	// be replaced, while unseen or observed-absent paths may only be created.
	// It is shared with the read/edit tools.
	Observe *FileObservationRecorder
}

// writeToolArgs is the decoded argument shape for WriteTool.
type writeToolArgs struct {
	// Path is the file to write, relative to Root (or absolute within Root).
	Path string `json:"path"`
	// Content is the full file contents to write (overwrites any existing file).
	Content string `json:"content"`
}

// Name implements AgentTool.
func (t *WriteTool) Name() string { return "write" }

// Description implements AgentTool.
func (t *WriteTool) Description() string {
	return "Create or overwrite a file at the given path, creating parent " +
		"directories as needed. Overwriting an existing file is reported; read it first."
}

// Schema implements AgentTool.
func (t *WriteTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path":    {"type": "string", "description": "File path to write, relative to the workspace root."},
    "content": {"type": "string", "description": "Full file contents to write."}
  },
  "required": ["path", "content"],
  "additionalProperties": false
}`)
}

// ExecutionMode implements AgentTool. Writes mutate the filesystem → sequential
// so a batch does not race concurrent writes to the same tree.
func (t *WriteTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionSequential
}

// resolvePath resolves p against Root (or any ExtraRoots) via the shared
// resolveWithin boundary policy, so every file tool enforces the same
// workspace-escape guard while writes can also reach trusted extra roots.
func (t *WriteTool) resolvePath(p string) (string, error) {
	if len(t.ExtraRoots) == 0 {
		return resolveWithin(t.Root, p)
	}
	return resolveWithinAny(append([]string{t.Root}, t.ExtraRoots...), p)
}

// Execute implements AgentTool. Write failures are encoded as error results;
// the returned Go error is reserved for nothing here (argument decode also
// degrades to a result), matching the read tool's contract.
func (t *WriteTool) Execute(ctx context.Context, id string, args json.RawMessage, onUpdate agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	a, bad := decodeArgs[writeToolArgs](args, "write")
	if bad != nil {
		return *bad, nil
	}
	if a.Path == "" {
		return errorResult("write: path is required"), nil
	}
	full, err := t.resolvePath(a.Path)
	if err != nil {
		return errorResult("write: " + err.Error()), nil
	}

	// Detect overwrite before writing so the result can report it. A path that
	// points at a directory is an error, not an overwrite.
	overwrote := false
	if info, statErr := os.Stat(full); statErr == nil {
		if info.IsDir() {
			return errorResult(fmt.Sprintf("write: %q is a directory, not a file", a.Path)), nil
		}
		overwrote = true
	}

	// Read-before-write gate: reject overwriting an unread existing file and
	// reject replacing a file whose version changed since this session's read.
	createOnly, obsErr := t.Observe.CheckWrite(full, a.Path)
	if obsErr != nil {
		return observationResult(obsErr), nil
	}

	// Create parent directories as needed.
	if dir := filepath.Dir(full); dir != "" {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return errorResult(fmt.Sprintf("write: cannot create parent directories for %q: %v", a.Path, err)), nil
		}
	}

	// Snapshot the prior state before mutating so /rewind can restore it.
	t.Snap.Record(full)
	// Re-check after directory creation so an external change between the first
	// check and the mutation is still caught; the create path also uses O_EXCL
	// so a concurrent creator is never silently overwritten.
	createOnly, obsErr = t.Observe.CheckWrite(full, a.Path)
	if obsErr != nil {
		return observationResult(obsErr), nil
	}
	if createOnly {
		f, openErr := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
		if openErr != nil {
			if os.IsExist(openErr) {
				return observationResult(&observationError{
					code: fsNotObserved,
					msg:  fmt.Sprintf("cannot overwrite existing %q without reading it first — read the file, then retry", a.Path),
				}), nil
			}
			return errorResult(fmt.Sprintf("write: cannot create %q: %v", a.Path, openErr)), nil
		}
		_, werr := f.WriteString(a.Content)
		cerr := f.Close()
		if werr != nil {
			return errorResult(fmt.Sprintf("write: cannot write %q: %v", a.Path, werr)), nil
		}
		if cerr != nil {
			return errorResult(fmt.Sprintf("write: cannot close %q: %v", a.Path, cerr)), nil
		}
	} else if err := os.WriteFile(full, []byte(a.Content), filePerm); err != nil {
		return errorResult(fmt.Sprintf("write: cannot write %q: %v", a.Path, err)), nil
	}
	t.Observe.RecordPresent(full)
	verb := "Created"
	if overwrote {
		verb = "Overwrote"
	}
	msg := fmt.Sprintf("%s %s (%d bytes)", verb, a.Path, len(a.Content))
	return agentcore.AgentToolResult{
		Content: agentcore.ContentList{agentcore.NewTextContent(msg)},
		Details: map[string]any{"path": a.Path, "bytes": len(a.Content), "overwrote": overwrote},
	}, nil
}
