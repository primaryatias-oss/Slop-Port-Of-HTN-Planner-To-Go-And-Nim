## Plans with a domain translated ahead of time into Nim: loads facts into a
## world state, decomposes the Human domain's top-level method and walks the
## resulting plan like a game would.
##
##   nim r examples/quickstart.nim
##
## human_generated.nim was generated from Domains/Test/human.domain (run from
## the repository root):
##
##   htn_translator Domains/Test/human.domain CreateHumanHTN nim/examples

import htn/[atom, callterm, integration, planner]
import human_generated

const facts = """
item "apple"
item "banana"
edible "apple"
edible "banana"
type "apple" "fruit"
type "banana" "fruit"
is_old "banana"
has_peel "apple"
"""

proc main() =
  # The world state the planner reads (owned by the database hook).
  let database = newDatabaseHook()
  if not database.parseWorldStateText(facts): quit "invalid world state"
  # Callterms the domain may call; the Human domain uses none.
  let registry = newRegistry()
  let hook = newPlannerHook(database.worldState, registry)
  if not hook.setGeneratedPlannerDefinition(CreateHumanHTN_GetDefinition()):
    quit "incompatible generated planner"
  let unit = newPlanningUnit(database, hook, "behave")
  unit.setBacktrackingMode(bmAll)

  let status = unit.decomposeTopLevelMethod(intern("behave"))
  if status != dsSucceeded: quit "no plan: " & $status
  echo "plan: ", toString(unit.lastDecomposition, true)

  # Execute the plan one primitive task at a time.
  while unit.resolveCurrentPrimitiveTask() == taskReady:
    let (task, _) = unit.currentPrimitiveTask()
    echo "executing ", toString(task, true)
    unit.completeCurrentPrimitiveTask()

main()
