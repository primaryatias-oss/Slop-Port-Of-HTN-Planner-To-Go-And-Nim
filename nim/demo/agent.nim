## A long-lived Wanderer NPC (AIHTNDemoWandererAgent): gameplay state,
## daemons, world state, planner hook and planning unit, executing primitive
## tasks over simulated time. Also the demo's callterm bindings and error
## report.

import std/[math, monotimes, times]
import htn/[atom, callterm, integration, planner, worldstate]
import daemons, world

type
  HistoryEntry* = object
    ageSeconds*: float32
    text*: string

  Agent* = ref object
    id: uint32
    definition: Definition
    initialWaypoint: int
    registry: Registry
    report: ErrorCallback
    database: DatabaseHook
    hook: PlannerHook
    unit: PlanningUnit
    terrain: Terrain
    wanderer: Wanderer
    wandererDaemon: WandererDaemon
    pathfinder: Pathfinder
    terrainDaemon: TerrainDaemon
    initialized: bool
    age: float32
    remaining: float32
    taskStarted: bool
    lastPlanSucceeded: bool
    planCount: uint64
    completedTasks: uint64
    lastPlanner, totalPlanner, maxPlanner: Duration
    history: seq[HistoryEntry]

let
  taskWalkSegment = intern("!walk_segment")
  taskPlayContextualAnimation = intern("!play_contextual_animation")
  taskWaitForPathfindingQuery = intern("!wait_for_pathfinding_query")
  taskWandererIdle = intern("!wanderer_idle")
  taskSay = intern("!say")

const
  MaxImmediatePlanningPasses = 4
  WaitTaskDurationSeconds = 0.15'f32
  IdleTaskDurationSeconds = 0.4'f32
  SayTaskDurationSeconds = 0.8'f32
  MaxHistoryEntries = 160

proc reasonText(reason: ErrorReason): string =
  case reason
  of erNotRegistered: "not registered"
  of erMissingBinding: "no callable bound"
  of erMissingInstance: "daemon instance missing"
  of erArgumentCountMismatch: "argument count mismatch"
  of erArgumentTypeMismatch: "incompatible argument type"
  of erArgumentConversionFailed: "argument conversion failed"
  of erReturnConversionFailed: "return conversion failed"
  of erNone: "unknown reason"

proc callTermErrorReporter*(write: proc (text: string) {.closure.}): ErrorCallback =
  ## A callterm error callback printing the demo's report
  ## (ReportGeneratedDemoCallTermError) through `write`.
  proc orDefault(text, fallback: string): string = (if text.len == 0: fallback else: text)
  result = proc (clientContext: RootRef, info: ErrorInfo) =
    write("[Generated] Callterm '" & orDefault(info.name, "<unknown>") & "' failed: " & reasonText(info.reason) &
      " (daemon: " & orDefault(info.daemonID, "-") & ", domain: " & orDefault(info.source.domain, "<unknown>") &
      ", source: " & orDefault(info.source.file, "<unavailable>") & ":" & $info.source.line & ":" &
      $info.source.column & ")\n")
    if info.argumentIndex != NoIndex:
      write("  argument " & $(info.argumentIndex + 1) & ": expected atom type " & $info.expectedAtomType & " (" &
        orDefault(info.expectedTypeName, "see atom type") & "), received " & $info.actualAtomType & "\n")

proc increment(value: int32): int32 = value + 1
proc bothCoordinatesEven(c: Cell): bool = c.x mod 2 == 0 and c.y mod 2 == 0
proc bothCoordinatesOdd(c: Cell): bool = c.x mod 2 != 0 and c.y mod 2 != 0
proc requestPathFromTo(p: Pathfinder, source, target: Cell): int32 = p.requestPath(source, target)
proc sameLocation(source, target: Cell): bool = source == target
proc addTargetAvailableCallTerm(d: DaemonDemoTest): bool = d.addTargetAvailable()

proc intArgument(a: var Arguments, i: int, value: var int32): bool =
  value = a.values[i].intValue
  a.values[i].isKind(akInt)

proc bindCallTerms*(r: Registry) =
  ## The demo's callterms (BindCalls of the original demo).
  bindListCallTerms(r)
  # AIHTNDemoWandererAgent::BindCallTerms
  r.bindFunc("inc", increment)
  r.bindFunc("both_coordinates_even", bothCoordinatesEven)
  r.bindFunc("both_coordinates_odd", bothCoordinatesOdd)
  doAssert r.bindMemberFunc("request_path_from_to", PathfinderDaemonID, requestPathFromTo)
  r.bindFunc("same_location", sameLocation)
  # AIHtnDaemonDemoTest::BindCallTerms
  doAssert r.bindMemberFunc("add_target_available", DaemonDemoTestID, addTargetAvailableCallTerm)
  r.bindRaw("binded_function_with_args", proc (a: var Arguments): Atom = newBool(a.len == 1))
  r.bindRaw("get_health", proc (a: var Arguments): Atom = newInt(50))
  r.bindRaw("get_max_speed", proc (a: var Arguments): Atom = newFloat(1))
  r.bindRaw("lt", proc (a: var Arguments): Atom =
    var left, right: int32
    newBool(a.len == 2 and intArgument(a, 0, left) and intArgument(a, 1, right) and left < right))
  r.bindRaw("inc", proc (a: var Arguments): Atom =
    var value: int32
    if a.len == 1 and intArgument(a, 0, value): newInt(value + 1) else: newInt(0))
  r.bindRaw("add", proc (a: var Arguments): Atom =
    var left, right: int32
    if a.len == 2 and intArgument(a, 0, left) and intArgument(a, 1, right): newInt(left + right) else: newInt(0))
  r.bindRaw("mul", proc (a: var Arguments): Atom =
    var left, right: int32
    if a.len == 2 and intArgument(a, 0, left) and intArgument(a, 1, right): newInt(left * right) else: newInt(0))

proc newAgent*(id: uint32, definition: Definition, initialWaypoint: int, terrain: Terrain, registry: Registry,
    report: ErrorCallback): Agent =
  ## An NPC that starts at waypoint `initialWaypoint`; `report` receives
  ## callterm errors.
  let wanderer = newWanderer(terrain)
  Agent(id: id, definition: definition, initialWaypoint: initialWaypoint, registry: registry, report: report,
    terrain: terrain, wanderer: wanderer, wandererDaemon: WandererDaemon(wanderer: wanderer),
    pathfinder: newPathfinder(terrain), terrainDaemon: TerrainDaemon(terrain: terrain))

proc addHistory(a: Agent, text: string) =
  a.history.add HistoryEntry(ageSeconds: a.age, text: text)
  if a.history.len > MaxHistoryEntries: a.history.delete(0)

proc formatTask*(a: Agent, task: Atom): string =
  ## "head arg..." with quoted strings.
  let head = callHead(task)
  result = if head == nil: "<invalid task>" else: head.text
  for argument in callArguments(task): result.add " " & toString(argument, true)

proc writeWorldState(a: Agent) =
  if a.database == nil: return
  let w = a.database.worldState
  a.terrainDaemon.writeWorldState(w)
  a.wandererDaemon.writeWorldState(w)
  a.pathfinder.writeWorldState(w)

proc initialize*(a: Agent): bool {.discardable.} =
  ## Creates the persistent planner and execution storage.
  if a.initialized: return true
  if a.definition == nil:
    a.addHistory("Initialization failed: missing generated Wanderer definition")
    return false
  a.database = newDatabaseHook()
  a.hook = newPlannerHook(a.database.worldState, a.registry)
  discard a.hook.bindings.setDaemon(PathfinderDaemonID, a.pathfinder)
  a.wanderer.reset(a.initialWaypoint)
  a.wandererDaemon.initialize(a.hook)
  a.pathfinder.initialize(a.hook)
  a.terrainDaemon.initialize(a.hook)
  if not a.hook.setGeneratedPlannerDefinition(a.definition):
    a.addHistory("Initialization failed: incompatible generated planner ABI")
    return false
  a.unit = newPlanningUnit(a.database, a.hook, "run")
  a.unit.executionContext.callTermErrorPolicy = epReport
  a.unit.executionContext.callTermErrorCallback = a.report
  a.writeWorldState()
  a.initialized = true
  a.addHistory("NPC spawned; persistent planner/execution storage created")
  true

proc taskArgument(task: Atom, index: int, cell: var Cell): bool =
  if callArgumentCount(task) <= index: return false
  parseCell(callArgument(task, index)[0], cell)

proc tryPlan(a: Agent)

proc finishPlan(a: Agent) =
  if a.unit != nil: a.unit.clearCurrentPlan()
  a.taskStarted = false
  a.remaining = 0
  # The planner is synchronous: request the next plan immediately.
  a.tryPlan()

proc startCurrentTask(a: Agent) =
  if a.unit == nil: return
  case a.unit.resolveCurrentPrimitiveTask()
  of planCompleted:
    a.finishPlan()
    return
  of resolutionFailed:
    a.lastPlanSucceeded = false
    a.addHistory("Plan resolution failed")
    a.unit.clearCurrentPlan()
    a.taskStarted = false
    return
  of taskReady: discard
  let (task, ok) = a.unit.currentPrimitiveTask()
  if not ok:
    a.lastPlanSucceeded = false
    a.addHistory("Plan contains no executable primitive task")
    a.unit.clearCurrentPlan()
    a.taskStarted = false
    return
  let head = callHead(task)
  if head == taskWalkSegment:
    var distance = 1.0'f32
    var target: Cell
    if taskArgument(task, 0, target):
      let current = a.wanderer.location
      let deltaX = float32(target.x - current.x)
      let deltaY = float32(target.y - current.y)
      let squared = float32(deltaX * deltaX) + float32(deltaY * deltaY)
      distance = float32(sqrt(float64(squared)))
    let speed = max(0.01'f32, a.wanderer.speed)
    a.remaining = max(0.01'f32, distance / speed)
  elif head == taskPlayContextualAnimation:
    var location: Cell
    var interactable = -1
    if taskArgument(task, 1, location): interactable = a.terrain.interactableAt(location)
    if interactable >= 0:
      a.remaining = max(0.01'f32, a.terrain.interactable(interactable).usageTimeSeconds)
    else:
      a.remaining = 0.05
      a.addHistory("Warning: contextual animation has no matching interactable")
  elif head == taskWaitForPathfindingQuery: a.remaining = WaitTaskDurationSeconds
  elif head == taskWandererIdle: a.remaining = IdleTaskDurationSeconds
  elif head == taskSay: a.remaining = SayTaskDurationSeconds
  else: a.remaining = 0.05
  a.taskStarted = true
  a.addHistory("Start: " & a.formatTask(task))

proc tryPlan(a: Agent) =
  if a.unit == nil: return
  # A successful decomposition may contain no primitive task (a callterm
  # started a pathfinding request): replan at once, within a small budget.
  for pass in 0 ..< MaxImmediatePlanningPasses:
    let start = getMonoTime()
    a.lastPlanSucceeded = a.unit.decompose() == dsSucceeded
    a.lastPlanner = getMonoTime() - start
    a.totalPlanner += a.lastPlanner
    a.maxPlanner = max(a.maxPlanner, a.lastPlanner)
    inc a.planCount
    if not a.lastPlanSucceeded:
      a.addHistory("Planning failed; retrying next update")
      return
    a.taskStarted = false
    if a.unit.currentPlan.len == 0:
      a.addHistory("Plan completed immediately (side effect); replanning now")
      a.writeWorldState()
      continue
    a.addHistory("New plan: " & $a.unit.currentPlan.len & " plan step(s)")
    a.startCurrentTask()
    return
  a.addHistory("Warning: immediate replanning budget exhausted with no executable task")

proc completeCurrentTask(a: Agent) =
  var task: Atom
  var ok = false
  if a.unit != nil: (task, ok) = a.unit.currentPrimitiveTask()
  if not ok:
    a.finishPlan()
    return
  let head = callHead(task)
  if head == taskWalkSegment:
    var location: Cell
    if taskArgument(task, 0, location): a.wanderer.notifyWalkSegmentCompleted(location)
  elif head == taskPlayContextualAnimation:
    a.wanderer.notifyContextualAnimationCompleted()
  inc a.completedTasks
  a.addHistory("Complete: " & a.formatTask(task))
  a.unit.completeCurrentPrimitiveTask()
  a.taskStarted = false
  a.remaining = 0
  a.writeWorldState()
  a.startCurrentTask()

proc update*(a: Agent, deltaTime: float32) =
  ## Advances the NPC by `deltaTime` seconds.
  if not a.initialized: return
  let deltaTime = max(0'f32, deltaTime)
  a.age += deltaTime
  a.wandererDaemon.update(deltaTime)
  a.terrainDaemon.update(deltaTime)
  a.pathfinder.update(deltaTime)
  a.writeWorldState()
  if a.unit == nil or a.unit.currentPlan.len == 0:
    # Planning is synchronous: ask for the next piece of work at once.
    a.tryPlan()
    if a.unit == nil or a.unit.currentPlan.len == 0: return
  if not a.taskStarted: a.startCurrentTask()
  if not a.taskStarted: return
  a.remaining -= deltaTime
  if a.remaining <= 0: a.completeCurrentTask()

proc id*(a: Agent): uint32 = a.id
proc wanderer*(a: Agent): Wanderer = a.wanderer
proc isInitialized*(a: Agent): bool = a.initialized
proc lastPlanSucceeded*(a: Agent): bool = a.lastPlanSucceeded
proc planCount*(a: Agent): uint64 = a.planCount
proc completedTaskCount*(a: Agent): uint64 = a.completedTasks
proc remainingSeconds*(a: Agent): float32 = a.remaining
proc history*(a: Agent): seq[HistoryEntry] = a.history
proc plannerTimes*(a: Agent): (Duration, Duration, Duration) = (a.lastPlanner, a.totalPlanner, a.maxPlanner)

proc currentPlan*(a: Agent): seq[Atom] =
  if a.unit == nil: @[] else: a.unit.currentPlan

proc currentTaskIndex*(a: Agent): int =
  if a.unit == nil: 0 else: a.unit.currentPrimitiveTaskIndex

proc currentTaskName*(a: Agent): string =
  var task: Atom
  var ok = false
  if a.unit != nil: (task, ok) = a.unit.currentPrimitiveTask()
  if not ok: return (if a.lastPlanSucceeded: "<no task>" else: "<plan failed>")
  let head = callHead(task)
  if head == nil: "<invalid task>" else: head.text

proc navigationPath*(a: Agent, path: var seq[Cell]): bool =
  ## The remaining resolved path (for display).
  a.pathfinder.remainingPath(a.wanderer.location, a.wanderer.destination, path)

proc worldState*(a: Agent): WorldState =
  if a.database == nil: nil else: a.database.worldState
