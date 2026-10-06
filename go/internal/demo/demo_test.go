package demo_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/demo"
)

// The goldens are recorded from the original HTNDemo sources driven by
// htn-demo-oracle (tools/demo/make_goldens.py).

func repositoryRoot(t *testing.T) string {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func compareGolden(t *testing.T, root, name, actual string) {
	t.Helper()
	golden, err := os.ReadFile(filepath.Join(root, "testdata", "demo", name))
	if err != nil {
		t.Fatal(err)
	}
	if actual == string(golden) {
		return
	}
	want, got := strings.Split(string(golden), "\n"), strings.Split(actual, "\n")
	for i := 0; i < len(want) || i < len(got); i++ {
		var w, g string
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w != g {
			t.Fatalf("%s: line %d differs\nwant: %s\ngot:  %s", name, i+1, w, g)
		}
	}
}

func TestDomainRunnerMatchesOriginal(t *testing.T) {
	root := repositoryRoot(t)
	for _, runtimeBacktracking := range []bool{false, true} {
		var output bytes.Buffer
		demo.RunnerTrace(&output, root, runtimeBacktracking)
		name := "runner.golden"
		if runtimeBacktracking {
			name = "runner_rt.golden"
		}
		compareGolden(t, root, name, output.String())
	}
}

func TestSimulationMatchesOriginal(t *testing.T) {
	root := repositoryRoot(t)
	for _, runtimeBacktracking := range []bool{false, true} {
		var output bytes.Buffer
		demo.SimulationTrace(&output, 8, 3600, 300, runtimeBacktracking)
		compareGolden(t, root, "simulate.golden", output.String())
	}
}
