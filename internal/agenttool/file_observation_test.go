package agenttool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/pigo/internal/agentcore"
)

func observationErrorCode(res agentcore.AgentToolResult) string {
	if d, ok := res.Details.(map[string]any); ok {
		if code, ok := d["errorCode"].(string); ok {
			return code
		}
	}
	return ""
}

func TestEditRequiresRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("beta"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	obs := NewFileObservationRecorder()
	tool := &EditTool{Root: dir, Observe: obs}
	res := runEdit(t, tool, map[string]any{"path": "f.txt", "old_string": "beta", "new_string": "BETA"})
	text := resultText(res)
	if !res.IsError {
		t.Fatalf("expected error, got %q", text)
	}
	if !strings.Contains(text, `edit requires reading "f.txt" first`) ||
		!strings.Contains(text, "first — read the file, then retry") {
		t.Errorf("missing read-first remedy, got %q", text)
	}
	if code := observationErrorCode(res); code != "FS_NOT_OBSERVED" {
		t.Errorf("errorCode = %q, want FS_NOT_OBSERVED", code)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "beta" {
		t.Errorf("file changed despite rejection: %q", got)
	}
}

func TestReadThenEditAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("beta"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	obs := NewFileObservationRecorder()
	readRes, _ := runRead(t, &ReadTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt"})
	if readRes.IsError {
		t.Fatalf("read failed: %q", resultText(readRes))
	}
	res := runEdit(t, &EditTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt", "old_string": "beta", "new_string": "BETA"})
	if res.IsError {
		t.Fatalf("edit after read failed: %q", resultText(res))
	}
	got, _ := os.ReadFile(path)
	if string(got) != "BETA" {
		t.Errorf("content = %q, want BETA", got)
	}
}

func TestObservationsAreSessionIsolated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("beta"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sessionA := NewFileObservationRecorder()
	sessionB := NewFileObservationRecorder()
	readRes, _ := runRead(t, &ReadTool{Root: dir, Observe: sessionA}, map[string]any{"path": "f.txt"})
	if readRes.IsError {
		t.Fatalf("read failed: %q", resultText(readRes))
	}

	resB := runEdit(t, &EditTool{Root: dir, Observe: sessionB}, map[string]any{"path": "f.txt", "old_string": "beta", "new_string": "BETA"})
	if !resB.IsError || !strings.Contains(resultText(resB), "edit requires reading") {
		t.Fatalf("session B edit should be rejected without its own read, got %q", resultText(resB))
	}

	resA := runEdit(t, &EditTool{Root: dir, Observe: sessionA}, map[string]any{"path": "f.txt", "old_string": "beta", "new_string": "BETA"})
	if resA.IsError {
		t.Fatalf("session A edit after its own read failed: %q", resultText(resA))
	}
}

func TestEditStaleVersionRequiresReread(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	obs := NewFileObservationRecorder()
	readRes, _ := runRead(t, &ReadTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt"})
	if readRes.IsError {
		t.Fatalf("read failed: %q", resultText(readRes))
	}
	if err := os.WriteFile(path, []byte("ALPHA\nbeta\n"), 0o644); err != nil {
		t.Fatalf("external change: %v", err)
	}
	res := runEdit(t, &EditTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt", "old_string": "beta", "new_string": "BETA"})
	text := resultText(res)
	if !res.IsError {
		t.Fatalf("expected stale error, got %q", text)
	}
	if !strings.Contains(text, "changed since it was read") ||
		!strings.Contains(text, "re-read the file, then retry") {
		t.Errorf("missing stale remedy, got %q", text)
	}
	if code := observationErrorCode(res); code != "FS_STALE_VERSION" {
		t.Errorf("errorCode = %q, want FS_STALE_VERSION", code)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "ALPHA\nbeta\n" {
		t.Errorf("external content was clobbered: %q", got)
	}
}

func TestWriteRequiresReadForExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	obs := NewFileObservationRecorder()
	tool := &WriteTool{Root: dir, Observe: obs}
	res := runWrite(t, tool, map[string]any{"path": "f.txt", "content": "new"})
	text := resultText(res)
	if !res.IsError {
		t.Fatalf("expected error, got %q", text)
	}
	if !strings.Contains(text, `cannot overwrite existing "f.txt" without reading it first`) ||
		!strings.Contains(text, "first — read the file, then retry") {
		t.Errorf("missing read-first remedy, got %q", text)
	}
	if code := observationErrorCode(res); code != "FS_NOT_OBSERVED" {
		t.Errorf("errorCode = %q, want FS_NOT_OBSERVED", code)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old" {
		t.Errorf("file changed despite rejection: %q", got)
	}
}

func TestWriteUnobservedCreateAllowed(t *testing.T) {
	dir := t.TempDir()
	obs := NewFileObservationRecorder()
	tool := &WriteTool{Root: dir, Observe: obs}
	res := runWrite(t, tool, map[string]any{"path": "out.txt", "content": "hello"})
	if res.IsError {
		t.Fatalf("unexpected error creating file: %q", resultText(res))
	}
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil || string(got) != "hello" {
		t.Errorf("file = %q, err = %v", got, err)
	}
}

func TestReadThenWriteOverwriteAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	obs := NewFileObservationRecorder()
	readRes, _ := runRead(t, &ReadTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt"})
	if readRes.IsError {
		t.Fatalf("read failed: %q", resultText(readRes))
	}
	res := runWrite(t, &WriteTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt", "content": "new"})
	if res.IsError {
		t.Fatalf("write after read failed: %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "Overwrote") {
		t.Errorf("expected Overwrote, got %q", resultText(res))
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}
}

func TestWriteStaleVersionRequiresReread(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	obs := NewFileObservationRecorder()
	readRes, _ := runRead(t, &ReadTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt"})
	if readRes.IsError {
		t.Fatalf("read failed: %q", resultText(readRes))
	}
	if err := os.WriteFile(path, []byte("old-old"), 0o644); err != nil {
		t.Fatalf("external change: %v", err)
	}
	res := runWrite(t, &WriteTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt", "content": "new"})
	text := resultText(res)
	if !res.IsError {
		t.Fatalf("expected stale error, got %q", text)
	}
	if !strings.Contains(text, "changed since it was read") ||
		!strings.Contains(text, "re-read the file, then retry") {
		t.Errorf("missing stale remedy, got %q", text)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old-old" {
		t.Errorf("external content was clobbered: %q", got)
	}
}

func TestWriteAfterObservedAbsentCreates(t *testing.T) {
	dir := t.TempDir()
	obs := NewFileObservationRecorder()
	readRes, _ := runRead(t, &ReadTool{Root: dir, Observe: obs}, map[string]any{"path": "nope.txt"})
	if !readRes.IsError {
		t.Fatalf("expected missing-file read error, got %q", resultText(readRes))
	}
	res := runWrite(t, &WriteTool{Root: dir, Observe: obs}, map[string]any{"path": "nope.txt", "content": "created"})
	if res.IsError {
		t.Fatalf("create after observed absence failed: %q", resultText(res))
	}
	got, _ := os.ReadFile(filepath.Join(dir, "nope.txt"))
	if string(got) != "created" {
		t.Errorf("content = %q, want created", got)
	}
}

func TestWriteObservedAbsentCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	obs := NewFileObservationRecorder()
	readRes, _ := runRead(t, &ReadTool{Root: dir, Observe: obs}, map[string]any{"path": "a/b/c/deep.txt"})
	if !readRes.IsError {
		t.Fatalf("expected missing-file read error, got %q", resultText(readRes))
	}
	res := runWrite(t, &WriteTool{Root: dir, Observe: obs}, map[string]any{"path": "a/b/c/deep.txt", "content": "x"})
	if res.IsError {
		t.Fatalf("create after observed absence failed: %q", resultText(res))
	}
	got, err := os.ReadFile(filepath.Join(dir, "a", "b", "c", "deep.txt"))
	if err != nil || string(got) != "x" {
		t.Errorf("nested file = %q, err = %v", got, err)
	}
}

func TestWriteObservedAbsentThenExternalCreateRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	obs := NewFileObservationRecorder()
	readRes, _ := runRead(t, &ReadTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt"})
	if !readRes.IsError {
		t.Fatalf("expected missing-file read error, got %q", resultText(readRes))
	}
	if err := os.WriteFile(path, []byte("external"), 0o644); err != nil {
		t.Fatalf("external create: %v", err)
	}
	res := runWrite(t, &WriteTool{Root: dir, Observe: obs}, map[string]any{"path": "f.txt", "content": "mine"})
	text := resultText(res)
	if !res.IsError {
		t.Fatalf("expected rejection, got %q", text)
	}
	if !strings.Contains(text, "without reading it first") {
		t.Errorf("missing read-first rejection, got %q", text)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "external" {
		t.Errorf("concurrent creation was clobbered: %q", got)
	}
}

func TestEditObservedAbsentReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	obs := NewFileObservationRecorder()
	readRes, _ := runRead(t, &ReadTool{Root: dir, Observe: obs}, map[string]any{"path": "nope.txt"})
	if !readRes.IsError {
		t.Fatalf("expected missing-file read error, got %q", resultText(readRes))
	}
	res := runEdit(t, &EditTool{Root: dir, Observe: obs}, map[string]any{"path": "nope.txt", "old_string": "x", "new_string": "y"})
	text := resultText(res)
	if !res.IsError {
		t.Fatalf("expected not-found error, got %q", text)
	}
	if !strings.Contains(text, "cannot edit") || !strings.Contains(text, "not found") {
		t.Errorf("expected not-found edit error, got %q", text)
	}
	if code := observationErrorCode(res); code != "FS_NOT_FOUND" {
		t.Errorf("errorCode = %q, want FS_NOT_FOUND", code)
	}
}

func TestWriteObservedDirectoryStillReportsDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "adir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	obs := NewFileObservationRecorder()
	tool := &WriteTool{Root: dir, Observe: obs}
	res := runWrite(t, tool, map[string]any{"path": "adir", "content": "x"})
	if !strings.Contains(resultText(res), "is a directory") {
		t.Errorf("expected directory error, got %q", resultText(res))
	}
}

func TestWithFileObservationWiresSharedRecorder(t *testing.T) {
	obs := NewFileObservationRecorder()
	tools := []agentcore.AgentTool{
		&ReadTool{Root: "r"},
		&WriteTool{Root: "r"},
		&EditTool{Root: "r"},
	}
	cloned := WithFileObservation(tools, obs)
	if len(cloned) != len(tools) {
		t.Fatalf("len = %d, want %d", len(cloned), len(tools))
	}
	for _, tool := range cloned {
		switch tt := tool.(type) {
		case *ReadTool:
			if tt.Observe != obs {
				t.Errorf("read tool did not receive the recorder")
			}
		case *WriteTool:
			if tt.Observe != obs {
				t.Errorf("write tool did not receive the recorder")
			}
		case *EditTool:
			if tt.Observe != obs {
				t.Errorf("edit tool did not receive the recorder")
			}
		}
	}

	grep := &GrepTool{Root: "r"}
	reused := WithFileObservation([]agentcore.AgentTool{grep}, obs)
	if reused[0] != grep {
		t.Error("non-file tools should be reused, not cloned")
	}
}
