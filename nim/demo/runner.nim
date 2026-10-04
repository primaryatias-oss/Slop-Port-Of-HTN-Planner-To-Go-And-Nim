## The demo's Domain Runner and NPC Simulation, plus the deterministic traces
## in the format of the C++ htn-demo-oracle (tools/oracle/demo_oracle.cpp).

import std/[algorithm, os, strutils, tables]
import htn/[atom, callterm, debugger, integration, planner]
import ../tests/generated/registry
import agent, world

type
  Domain* = object
    ## One entry of the demo's domain table.
    name*: string
    variant*: string  ## planner variant; "RT" is appended for runtime backtracking
    source*: string   ## world-state stem preferred by the domain
    methods*: seq[string]

  Runner* = ref object
    ## One planner hook and planning unit for the selected domain and method
    ## over a shared database.
    registry*: Registry
    database*: DatabaseHook
    report: ErrorCallback
    debugger*: GeneratedDebugger
      ## When set before `select`, records every decomposition (planners
      ## built with -d:htnDebug only).
    hook: PlannerHook
    unit: PlanningUnit

  Simulation* = ref object
    ## A shared terrain and its Wanderer NPCs.
    terrain*: Terrain
    agents*: seq[Agent]
    age*: float32
    registry: Registry
    definition: Definition
    report: ErrorCallback
    nextID: uint32

let domains* = @[
  Domain(name: "AAACombatNPC", variant: "AAACombatNPC", source: "AAACombatNPC", methods: @["run"]),
  Domain(name: "AtomListDemo", variant: "AtomListDemo", source: "atom_list_demo", methods: @["show_atom_list",
    "split_list_basic", "split_list_single_element", "split_list_empty_fails", "split_list_bound_outputs",
    "split_list_rollback", "split_list_front_basic", "split_list_back_basic", "split_list_back_bound_outputs"]),
  Domain(name: "EliteNinja", variant: "EliteNinja", source: "EliteNinja", methods: @["run"]),
  Domain(name: "Grunt", variant: "Grunt", source: "Grunt", methods: @["run"]),
  Domain(name: "NormalNinja", variant: "NormalNinja", source: "NormalNinja", methods: @["run"]),
  Domain(name: "CallTermsDemo", variant: "Callterms", source: "callterms", methods: @["test_callterms",
    "callterm_creates_fact_visible_immediately", "callterm_world_state_mutation_survives_backtracking"]),
  Domain(name: "ComplexScenario", variant: "ComplexScenario", source: "complex_scenario", methods: @["run_scenario"]),
  Domain(name: "Human", variant: "Human", source: "human", methods: @["behave", "behave_upper_body"]),
  Domain(name: "NestedCallsDemo", variant: "NestedCalls", source: "nested_calls", methods: @["test_nested_calls"]),
  Domain(name: "IncludeDemo", variant: "IncludeDemo", source: "include_demo", methods: @["run_include_demo"]),
  Domain(name: "Wanderer", variant: "Wanderer", source: "Wanderer", methods: @["run"]),
  Domain(name: "BuiltinComparisonsDemo", variant: "BuiltinComparisonsDemo", source: "BuiltinComparisonsDemo",
    methods: @["run"]),
  Domain(name: "HierarchicalBacktracking", variant: "HierarchicalBacktracking", source: "hierarchical_backtracking",
    methods: @["validate_parent_guard", "validate_child_guard", "validate_fact_backtracking",
      "validate_axiom_backtracking"]),
  Domain(name: "RuntimeBacktrackingDemo", variant: "RuntimeBacktrackingDemo", source: "RuntimeBacktrackingDemo",
    methods: @["demo_fact_alternatives", "demo_axiom_alternatives", "demo_hierarchical_branches",
      "demo_direct_branch_fallback"]),
  Domain(name: "NumericExpressionsDemo", variant: "NumericExpressions", source: "numeric_expressions",
    methods: @["run", "division_by_zero", "invalid_operand_type"])]

const backtrackingModes* = [bmNone, bmFactsAndAxioms, bmBranches, bmAll]

proc findDomain*(name: string, domain: var Domain): bool =
  ## The domain named `name` (case-insensitively).
  for d in domains:
    if cmpIgnoreCase(d.name, name) == 0:
      domain = d
      return true
  false

proc definition*(d: Domain, runtimeBacktracking: bool): Definition =
  ## The generated planner, optionally the variant generated with runtime
  ## backtracking support.
  let variant = if runtimeBacktracking: d.variant & "RT" else: d.variant
  let getter = planners.getOrDefault(variant)
  if getter == nil: nil else: getter()

proc modeName*(mode: BacktrackingMode): string =
  case mode
  of bmNone: "None"
  of bmFactsAndAxioms: "Facts and axioms"
  of bmBranches: "Branches"
  of bmAll: "All"
  else: "Unknown"

proc comparePaths(a, b: string): int =
  ## Element-wise order of std::filesystem::path.
  let left = a.split('/')
  let right = b.split('/')
  for i in 0 ..< min(left.len, right.len):
    if left[i] != right[i]: return cmp(left[i], right[i])
  cmp(left.len, right.len)

proc findWorldStates*(root: string): seq[string] =
  ## The .worldstate files under root/WorldStates in the demo's order.
  let directory = root / "WorldStates"
  if not dirExists(directory): return @[]
  for path in walkDirRec(directory, yieldFilter = {pcFile, pcLinkToFile}):
    let name = path.extractFilename
    let dot = name.rfind('.')
    if dot <= 0 or name[dot .. ^1] != ".worldstate": continue
    if fileExists(path): result.add path
  result.sort(comparePaths)

proc bestWorldState*(d: Domain, worldStates: seq[string]): int =
  ## The index of the world state named after the domain's source (0, or -1
  ## when there are none).
  for i, path in worldStates:
    let stem = path.extractFilename.changeFileExt("")
    if stem == d.source or stem.startsWith(d.source & "_"): return i
  if worldStates.len == 0: -1 else: 0

proc formatPlanStep*(step: Atom): string =
  let head = callHead(step)
  result = if head == nil: "<invalid>" else: head.text
  for argument in callArguments(step): result.add " " & toString(argument, true)

proc newRunner*(report: ErrorCallback): Runner =
  ## A runner with the demo callterms; `report` receives callterm errors.
  let registry = newRegistry()
  bindCallTerms(registry)
  Runner(registry: registry, database: newDatabaseHook(), report: report)

proc select*(r: Runner, definition: Definition, methodName: string): bool =
  ## A fresh planner hook and planning unit (the demo's reload).
  r.hook = newPlannerHook(r.database.worldState, r.registry)
  r.unit = nil
  if definition == nil or not r.hook.setGeneratedPlannerDefinition(definition): return false
  r.unit = newPlanningUnit(r.database, r.hook, methodName)
  r.unit.executionContext.callTermErrorPolicy = epReport
  r.unit.executionContext.callTermErrorCallback = r.report
  if r.debugger != nil: r.unit.setGeneratedDebugger(r.debugger)
  true

proc run*(r: Runner, methodName: string, mode: BacktrackingMode): (DecompositionStatus, seq[string]) =
  ## Decomposes `methodName` on the loaded world state.
  r.unit.setBacktrackingMode(mode)
  let status = r.unit.decomposeTopLevelMethod(intern(methodName))
  var steps: seq[string]
  if status == dsSucceeded:
    for step in r.unit.lastDecomposition.elements: steps.add formatPlanStep(step)
  (status, steps)

proc repositoryRelative*(root, path: string): string =
  if path.startsWith(root & "/"): path[root.len + 1 .. ^1] else: path

proc runnerTrace*(write: proc (text: string) {.closure.}, root: string, runtimeBacktracking: bool) =
  ## Every demo domain and top-level method like the Domain Runner tab (each
  ## mode on the preferred world state, then every world state with all
  ## backtracking): the htn-demo-oracle "runner" output.
  let runner = newRunner(callTermErrorReporter(write))
  let worldStates = findWorldStates(root)
  for d in domains:
    let definition = d.definition(runtimeBacktracking)
    for methodName in d.methods:
      write("domain " & d.name & " method " & methodName & "\n")
      if not runner.select(definition, methodName):
        write("reload failed\n")
        continue
      let best = bestWorldState(d, worldStates)
      var runs: seq[(int, BacktrackingMode)]
      for mode in backtrackingModes: runs.add (best, mode)
      for i in 0 ..< worldStates.len: runs.add (i, bmAll)
      for (index, mode) in runs:
        write("run " & repositoryRelative(root, worldStates[index]) & " " & modeName(mode) & "\n")
        if not runner.database.parseWorldStateFile(worldStates[index]):
          write("load failed\n")
          continue
        let (status, steps) = runner.run(methodName, mode)
        write("status " & $status & "\n")
        for step in steps: write("step " & step & "\n")

proc c_snprintf(buffer: cstring, size: csize_t, format: cstring): cint {.importc: "snprintf",
    header: "<stdio.h>", varargs.}

proc formatFloat*(v: float32): string =
  ## printf("%.9g").
  var buffer: array[64, char]
  let count = c_snprintf(cast[cstring](addr buffer[0]), csize_t(buffer.len), "%.9g", float64(v))
  for i in 0 ..< count: result.add buffer[i]

proc spawn*(s: Simulation, count: int) =
  ## Adds `count` NPCs, each starting at waypoint (id - 1) mod 12.
  for _ in 0 ..< count:
    let npc = newAgent(s.nextID, s.definition, int(s.nextID - 1) mod WaypointCount, s.terrain, s.registry, s.report)
    inc s.nextID
    npc.initialize()
    s.agents.add npc

proc newSimulation*(agents: int, runtimeBacktracking: bool, report: ErrorCallback): Simulation =
  ## The default terrain with `agents` NPCs.
  let registry = newRegistry()
  bindCallTerms(registry)
  var wanderer: Domain
  doAssert findDomain("Wanderer", wanderer)
  result = Simulation(terrain: newTerrain(), registry: registry, definition: wanderer.definition(runtimeBacktracking),
    report: report, nextID: 1)
  result.spawn(agents)

proc update*(s: Simulation, deltaTime: float32) =
  ## Advances every NPC by `deltaTime` seconds.
  let deltaTime = max(0'f32, deltaTime)
  s.age += deltaTime
  for npc in s.agents: npc.update(deltaTime)

proc simulationTrace*(write: proc (text: string) {.closure.}, agents, steps, snapshotEvery: int,
    runtimeBacktracking: bool) =
  ## The simulation with a fixed 1/60 s step: the htn-demo-oracle "simulate"
  ## output.
  let s = newSimulation(agents, runtimeBacktracking, callTermErrorReporter(write))
  let t = s.terrain
  write("terrain " & $t.width & " " & $t.height & " interactables " & $t.interactables.len & "\n")
  for y in countdown(t.height - 1, 0):
    var row = newString(t.width)
    for x in 0 ..< t.width:
      row[x] = case t.cellType(Cell(x: int32(x), y: int32(y)))
        of cellBlocked: '#'
        of cellInteractable: 'o'
        of cellWalkable: '.'
    write("map " & align($y, 2) & " " & row & "\n")
  for interactable in t.interactables:
    write("interactable " & interactable.id & " " & interactable.kind.name & " " & $interactable.location & " " &
      interactable.contextAnimation & " " & formatFloat(interactable.usageTimeSeconds) & "\n")
  if s.agents.len > 0:
    var waypoints = ""
    for i in 0 ..< WaypointCount: waypoints.add " " & $s.agents[0].wanderer.waypoint(i)
    write("waypoints" & waypoints & "\n")
  const deltaTime = 1.0'f32 / 60.0'f32
  var age = 0'f32
  proc snapshot(step: int) =
    write("frame " & $step & " age " & formatFloat(age) & "\n")
    for npc in s.agents:
      let w = npc.wanderer
      write("npc " & $npc.id & " " & w.stateName & " at " & $w.location & " to " & $w.destination & " task " &
        npc.currentTaskName & " remaining " & formatFloat(npc.remainingSeconds) & " plans " & $npc.planCount &
        " completed " & $npc.completedTaskCount & " journeys " & $w.journeys & " ok " &
        $(if npc.lastPlanSucceeded: 1 else: 0) & "\n")
      let plan = npc.currentPlan
      for i, step in plan:
        write("  " & (if i == npc.currentTaskIndex: "> " else: "  ") & npc.formatTask(step) & "\n")
  snapshot(0)
  for step in 1 .. steps:
    age += deltaTime
    for npc in s.agents: npc.update(deltaTime)
    if snapshotEvery > 0 and step mod snapshotEvery == 0: snapshot(step)
  for npc in s.agents:
    write("history " & $npc.id & "\n")
    for entry in npc.history: write("  " & formatFloat(entry.ageSeconds) & " " & entry.text & "\n")
