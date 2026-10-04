//go:build htndebug

package demo_test

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/debugger"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/planner"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/demo"
)

// TestRunnerDebuggerText checks the Domain Runner's debugger tree (htn-demo
// run --debugger) on the Human domain.
func TestRunnerDebuggerText(t *testing.T) {
	root := repositoryRoot(t)
	d, ok := demo.FindDomain("Human")
	if !ok {
		t.Fatal("Human domain not found")
	}
	runner := demo.NewRunner(demo.CallTermErrorReporter(io.Discard))
	runner.Debugger = debugger.New()
	runner.Debugger.SetEnabled(true)
	if !runner.Select(d.Definition(false), "behave") {
		t.Fatal("could not select the Human planner")
	}
	if err := runner.Database.ParseWorldStateFile(filepath.Join(root, "WorldStates", "Test", "human.worldstate")); err != nil {
		t.Fatal(err)
	}
	if status, _ := runner.Run("behave", planner.BacktrackingAll); status != planner.Succeeded {
		t.Fatalf("decomposition failed: %v", status)
	}
	text := runner.Debugger.Text(false)
	for _, want := range []string{
		"[OK]   (behave)  (line 21)\n",
		"      [OK]   (eat_food @fruit)  (line 26)\n             constants: @fruit = fruit\n",
		"                    [FAIL] (edible ?out_item)  (line 16)\n",
		"               food: <unbound> -> \"apple\"\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("debugger text lacks %q:\n%s", want, text)
		}
	}
	if verbose := runner.Debugger.Text(true); len(verbose) <= len(text) || !strings.Contains(verbose, "[--]") {
		t.Errorf("verbose text should add unreached nodes:\n%s", verbose)
	}
}
