// Command quickstart plans with a domain translated ahead of time into Go:
// it loads facts into a world state, decomposes the Human domain's top-level
// method and walks the resulting plan like a game would.
//
//	go run ./examples/quickstart
//
// The planner in ./human was generated from Domains/Test/human.domain:
//
//go:generate go run ../../cmd/htn-translator ../../../Domains/Test/human.domain CreateHumanHTN human --package=human
package main

import (
	"fmt"
	"os"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/examples/quickstart/human"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/callterm"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/integration"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/planner"
)

const facts = `
item "apple"
item "banana"
edible "apple"
edible "banana"
type "apple" "fruit"
type "banana" "fruit"
is_old "banana"
has_peel "apple"
`

func main() {
	// The world state the planner reads (owned by the database hook).
	database := integration.NewDatabaseHook()
	if !database.ParseWorldStateText(facts) {
		fmt.Fprintln(os.Stderr, "invalid world state")
		os.Exit(1)
	}
	// Callterms the domain may call; the Human domain uses none.
	registry := callterm.NewRegistry()
	hook := integration.NewPlannerHook(database.WorldState(), registry)
	if !hook.SetGeneratedPlannerDefinition(human.CreateHumanHTN_GetDefinition()) {
		fmt.Fprintln(os.Stderr, "incompatible generated planner")
		os.Exit(1)
	}
	unit := integration.NewPlanningUnit(database, hook, "behave")
	unit.SetBacktrackingMode(planner.BacktrackingAll)

	if status := unit.DecomposeTopLevelMethod(atom.Intern("behave")); status != planner.Succeeded {
		fmt.Fprintln(os.Stderr, "no plan:", status)
		os.Exit(1)
	}
	fmt.Println("plan:", atom.ToString(unit.LastDecomposition(), true))

	// Execute the plan one primitive task at a time.
	for unit.ResolveCurrentPrimitiveTask() == integration.TaskReady {
		task, _ := unit.CurrentPrimitiveTask()
		fmt.Println("executing", atom.ToString(task, true))
		unit.CompleteCurrentPrimitiveTask()
	}
}
