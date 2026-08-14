package run

import (
	"testing"

	"github.com/smallnest/pigo/internal/agenttool"
)

func TestBuiltinToolsShareObservationRecorder(t *testing.T) {
	tools := BuiltinTools(t.TempDir(), false)
	var readObs, writeObs, editObs *agenttool.FileObservationRecorder
	for _, tool := range tools {
		switch tt := tool.(type) {
		case *agenttool.ReadTool:
			readObs = tt.Observe
		case *agenttool.WriteTool:
			writeObs = tt.Observe
		case *agenttool.EditTool:
			editObs = tt.Observe
		}
	}
	if readObs == nil || writeObs == nil || editObs == nil {
		t.Fatal("file tools must be wired with a shared observation recorder")
	}
	if readObs != writeObs || writeObs != editObs {
		t.Fatal("read/write/edit must share one session-scoped observation recorder")
	}
}
