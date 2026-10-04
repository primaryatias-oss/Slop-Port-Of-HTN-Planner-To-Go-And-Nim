//go:build !htndebug

package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/generated"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/scenario"
)

// TestDebuggerNeedsDebugBuild checks that release builds, whose generated
// planners carry no debug events, reject the debugger command like the
// release oracle (htn-oracle).
func TestDebuggerNeedsDebugBuild(t *testing.T) {
	file := filepath.Join(t.TempDir(), "debugger.scn")
	if err := os.WriteFile(file, []byte("scenario s\nplanner Human\ndebugger on\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := scenario.RunFile(file, repositoryRoot, generated.Planners)
	if err == nil || !strings.Contains(err.Error(), "htndebug") {
		t.Fatalf("expected the debugger command to need an htndebug build, got %v", err)
	}
}
