package agenttool

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/smallnest/pigo/internal/agentcore"
)

const (
	fsNotObserved  = "FS_NOT_OBSERVED"
	fsStaleVersion = "FS_STALE_VERSION"
	fsNotFound     = "FS_NOT_FOUND"
)

// fileObservation is one recorded presence/absence state for a file. The zero
// value means the file was observed absent.
type fileObservation struct {
	Present bool
	Version string
}

// FileObservationRecorder is the read-before-edit state shared by the file
// tools within one session. Reads record presence with a version derived from
// the file's stat metadata; write/edit tools consult that state before mutating
// so an existing file cannot be changed until this session has read it (and
// re-read it after an external change). It mirrors the observed-state policy in
// deepseek-harness and is deliberately in-memory and session-scoped.
type FileObservationRecorder struct {
	mu     sync.Mutex
	byPath map[string]fileObservation
}

// NewFileObservationRecorder returns an empty recorder with no observations.
func NewFileObservationRecorder() *FileObservationRecorder {
	return &FileObservationRecorder{byPath: make(map[string]fileObservation)}
}

// RecordAbsent records that path was observed missing. A nil recorder is a
// no-op so tools can hold an optional handle.
func (r *FileObservationRecorder) RecordAbsent(path string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byPath[filepath.Clean(path)] = fileObservation{}
}

// RecordPresent records that path exists at its current version. A nil
// recorder is a no-op; a failed stat leaves the prior observation untouched.
func (r *FileObservationRecorder) RecordPresent(path string) {
	if r == nil {
		return
	}
	version, ok := currentFileVersion(path)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byPath[filepath.Clean(path)] = fileObservation{Present: true, Version: version}
}

// CheckEdit returns nil when an edit is authorized: the path was read by this
// session and still matches the observed version. Unread paths and paths whose
// recorded absence is stale are rejected with the deepseek-harness style
// recovery instruction. A nil recorder leaves edits unconstrained.
func (r *FileObservationRecorder) CheckEdit(path, display string) error {
	if r == nil {
		return nil
	}
	obs, seen := r.lookup(path)
	if !seen {
		return &observationError{
			code: fsNotObserved,
			msg:  fmt.Sprintf("edit requires reading %q first — read the file, then retry", display),
		}
	}
	if !obs.Present {
		return &observationError{
			code: fsNotFound,
			msg:  fmt.Sprintf("cannot edit %q: not found", display),
		}
	}
	if version, ok := currentFileVersion(path); !ok || version != obs.Version {
		return &observationError{
			code: fsStaleVersion,
			msg:  fmt.Sprintf("file %q changed since it was read — re-read the file, then retry", display),
		}
	}
	return nil
}

// CheckWrite returns nil when a write is authorized. When createOnly is true
// the caller must fail if the file already exists (unseen or observed-absent
// state); when false the file was observed present and may be replaced at the
// observed version. A nil recorder leaves writes unconstrained.
func (r *FileObservationRecorder) CheckWrite(path, display string) (createOnly bool, err error) {
	if r == nil {
		return false, nil
	}
	obs, seen := r.lookup(path)
	_, statErr := os.Stat(path)
	exists := statErr == nil

	if seen && obs.Present {
		if !exists {
			return false, &observationError{
				code: fsStaleVersion,
				msg:  fmt.Sprintf("file %q changed since it was read — re-read the file, then retry", display),
			}
		}
		if version, ok := currentFileVersion(path); !ok || version != obs.Version {
			return false, &observationError{
				code: fsStaleVersion,
				msg:  fmt.Sprintf("file %q changed since it was read — re-read the file, then retry", display),
			}
		}
		return false, nil
	}

	if exists {
		return false, &observationError{
			code: fsNotObserved,
			msg:  fmt.Sprintf("cannot overwrite existing %q without reading it first — read the file, then retry", display),
		}
	}
	return true, nil
}

func (r *FileObservationRecorder) lookup(path string) (fileObservation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	obs, seen := r.byPath[filepath.Clean(path)]
	return obs, seen
}

// observationError carries a stable machine-oriented code alongside the
// model-facing message, mirroring deepseek-harness's structured FS errors.
type observationError struct {
	code string
	msg  string
}

func (e *observationError) Error() string {
	return e.msg
}

// observationResult builds an error tool result from an observation policy
// failure, preserving the structured error code in Details.
func observationResult(err error) agentcore.AgentToolResult {
	res := errorResult(err.Error())
	if oe, ok := err.(*observationError); ok {
		res.Details = map[string]any{"errorCode": oe.code}
	}
	return res
}

// currentFileVersion derives an opaque version from a file's stat metadata.
// Size, nanosecond mtime, and permission bits are enough to catch external
// mutations between a session's read and its guarded write/edit.
func currentFileVersion(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%o", info.Size(), info.ModTime().UnixNano(), info.Mode().Perm()), true
}

// WithFileObservation returns a copy of tools with a session-scoped observation
// recorder installed on the read/write/edit tools. Other tools are returned as
// the same instances, matching how pigo shares stateful tools within a run.
func WithFileObservation(tools []agentcore.AgentTool, obs *FileObservationRecorder) []agentcore.AgentTool {
	if obs == nil {
		return tools
	}
	out := make([]agentcore.AgentTool, len(tools))
	for i, tool := range tools {
		switch tt := tool.(type) {
		case *ReadTool:
			cp := *tt
			cp.Observe = obs
			out[i] = &cp
		case *WriteTool:
			cp := *tt
			cp.Observe = obs
			out[i] = &cp
		case *EditTool:
			cp := *tt
			cp.Observe = obs
			out[i] = &cp
		default:
			out[i] = tool
		}
	}
	return out
}
