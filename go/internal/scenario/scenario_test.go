package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/generated"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/scenario"
)

const repositoryRoot = "../../.."

// TestScenarios runs every testdata/scenarios/*.scn file and compares the
// output with the golden file produced by the C++ oracle.
func TestScenarios(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repositoryRoot, "testdata", "scenarios", "*.scn"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenario files found: %v", err)
	}
	for _, file := range files {
		file := file
		name := strings.TrimSuffix(filepath.Base(file), ".scn")
		t.Run(name, func(t *testing.T) {
			goldenBytes, err := os.ReadFile(strings.TrimSuffix(file, ".scn") + ".golden")
			if err != nil {
				t.Fatalf("missing golden output: %v", err)
			}
			output, err := scenario.RunFile(file, repositoryRoot, generated.Planners)
			if err != nil {
				t.Fatal(err)
			}
			compare(t, string(goldenBytes), output)
		})
	}
}

func compare(t *testing.T, golden, output string) {
	t.Helper()
	if golden == output {
		return
	}
	want := strings.Split(golden, "\n")
	got := strings.Split(output, "\n")
	scenarioName := ""
	for i := 0; i < len(want) || i < len(got); i++ {
		var w, g string
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if strings.HasPrefix(w, "scenario ") {
			scenarioName = w
		}
		if w != g {
			start := i - 8
			if start < 0 {
				start = 0
			}
			context := strings.Join(want[start:i], "\n")
			t.Fatalf("%s: first difference at line %d\n--- context\n%s\n--- want: %q\n--- got:  %q", scenarioName, i+1, context, w, g)
		}
	}
}
