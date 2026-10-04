//go:build htndebug

package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/generated"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/scenario"
)

// TestDebuggerScenarios runs testdata/debugger/*.scn, whose golden files were
// produced by htn-oracle-debug (the oracle built with HTN_DEBUG_DECOMPOSITION),
// and compares the generated event debugger dumps byte for byte.
func TestDebuggerScenarios(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repositoryRoot, "testdata", "debugger", "*.scn"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no debugger scenario files found: %v", err)
	}
	for _, file := range files {
		file := file
		t.Run(strings.TrimSuffix(filepath.Base(file), ".scn"), func(t *testing.T) {
			golden, err := os.ReadFile(strings.TrimSuffix(file, ".scn") + ".golden")
			if err != nil {
				t.Fatalf("missing golden output: %v", err)
			}
			output, err := scenario.RunFile(file, repositoryRoot, generated.Planners)
			if err != nil {
				t.Fatal(err)
			}
			compare(t, string(golden), output)
		})
	}
}
