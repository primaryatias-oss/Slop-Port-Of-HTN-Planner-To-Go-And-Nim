## Optional engine integration layer (HTNIntegration): a `DatabaseHook`
## owning a world state, a per-entity `PlannerHook` selecting a generated
## planner definition, and `PlanningUnit`s that own execution storage, the
## active plan and deferred-step expansion.

import atom, callterm, planner, worldstate

type
  DatabaseHook* = ref object
    ## Owns a world state loaded from world-state files.
    world: WorldState

  ExecutionContext* = object
    ## Per-execution descriptor (HTNPlannerExecutionContext). Planning units
    ## fill the world state, bindings, call and storage fields.
    worldState*: WorldState
    bindings*: BindingContext
    call*: Atom
    backtrackingMode*: BacktrackingMode
    execution*: Exec
    clientContext*: RootRef
    callTermErrorPolicy*: ErrorPolicy
    callTermErrorCallback*: ErrorCallback

  PlannerHook* = ref object
    ## Per-entity planner facade. The referenced world state must outlive it.
    world: WorldState
    definition: Definition
    prepared: RootRef
    bindings: BindingContext
    factRegistry: FactRegistry

  PrimitiveTaskResolution* = enum
    ## Outcome of `resolveCurrentPrimitiveTask`.
    taskReady, planCompleted, resolutionFailed

  PlanningUnit* = ref object
    ## Owns reusable execution storage, the last decomposition and the active
    ## plan of one agent/job. Not safe for concurrent use.
    options: ExecutionContext
    database: DatabaseHook
    hook: PlannerHook
    defaultTopLevelMethod: Symbol
    lastDecomposition: Atom
    currentPlan: seq[Atom]
    currentIndex: int
    execution: Exec
    executionDefinition: Definition

var logError*: proc (message: string) {.closure.}
  ## Receives integration errors (for example call-frame capacity
  ## diagnostics). Nil by default.

# ---------------------------------------------------------------------------
# Database hook

proc newDatabaseHook*(): DatabaseHook = DatabaseHook(world: newWorldState())

proc worldState*(d: DatabaseHook): WorldState = d.world

proc parseWorldStateFile*(d: DatabaseHook, path: string): bool =
  ## Replaces the facts with the contents of a world-state file. The world
  ## state object and its fact tables are retained so cached planner storage
  ## stays valid.
  d.world.removeAllFacts()
  d.world.parseFile(path)

proc parseWorldStateText*(d: DatabaseHook, text: string): bool =
  ## Replaces the facts with parsed world-state text.
  d.world.removeAllFacts()
  d.world.parseText(text)

# ---------------------------------------------------------------------------
# Planner hook

proc newPlannerHook*(world: WorldState, registry: Registry = nil): PlannerHook =
  ## A hook for `world` using the shared callterm registry (an empty registry
  ## when nil).
  PlannerHook(world: world, bindings: newBindingContext(registry), factRegistry: newFactRegistry())

proc setGeneratedPlannerDefinition*(h: PlannerHook, definition: Definition): bool {.discardable.} =
  ## Selects the generated backend. An incompatible definition is rejected and
  ## the previous one kept; nil clears it.
  if definition == nil:
    h.definition = nil
    h.prepared = nil
    h.factRegistry.reset()
    return true
  if not validateDefinition(definition): return false
  let prepared = definition.newPreparedStorage()
  if prepared == nil: return false
  h.prepared = prepared
  h.definition = definition
  h.factRegistry.reset()
  for name in definition.factNames: discard h.factRegistry.register(intern(name))
  true

proc generatedPlannerDefinition*(h: PlannerHook): Definition = h.definition
proc hasGeneratedPlannerDefinition*(h: PlannerHook): bool = h.definition != nil
proc preparedStorage*(h: PlannerHook): RootRef = h.prepared
proc bindings*(h: PlannerHook): BindingContext = h.bindings
proc worldState*(h: PlannerHook): WorldState = h.world
proc factRegistry*(h: PlannerHook): FactRegistry = h.factRegistry
proc findFactSlot*(h: PlannerHook, symbol: Symbol): FactSlot = h.factRegistry.findSlot(symbol)

proc decompose*(h: PlannerHook, ctx: ExecutionContext, requireTopLevel: bool): (Atom, DecompositionStatus) =
  ## Runs one generated decomposition of `ctx.call`. Failures return an empty
  ## list.
  if h.definition == nil or h.definition.decomposeCall == nil or not ctx.call.isBound:
    return (emptyList(), dsInvalidContext)
  var generated = Context(worldState: ctx.worldState, bindings: ctx.bindings,
    backtrackingMode: ctx.backtrackingMode, execution: ctx.execution, prepared: h.prepared,
    clientContext: ctx.clientContext, callTermErrorPolicy: ctx.callTermErrorPolicy,
    callTermErrorCallback: ctx.callTermErrorCallback)
  let (plan, status) = h.definition.decomposeCall(generated, ctx.call, requireTopLevel)
  if status == dsCallFrameCapacityExceeded and logError != nil and ctx.execution != nil and
      ctx.execution.info.lastError.len > 0:
    logError(ctx.execution.info.lastError)
  if status != dsSucceeded: return (emptyList(), status)
  (plan, status)

# ---------------------------------------------------------------------------
# Planning unit

proc newPlanningUnit*(database: DatabaseHook, hook: PlannerHook, defaultTopLevelMethod: string): PlanningUnit =
  ## A planning unit with a default top-level method.
  PlanningUnit(options: ExecutionContext(backtrackingMode: bmAll), database: database, hook: hook,
    defaultTopLevelMethod: intern(defaultTopLevelMethod), lastDecomposition: emptyList())

proc executionContext*(u: PlanningUnit): var ExecutionContext = u.options
  ## Runtime options (backtracking mode, client context, callterm error
  ## policy/callback). Configure them while idle.

proc setBacktrackingMode*(u: PlanningUnit, mode: BacktrackingMode) = u.options.backtrackingMode = mode
proc backtrackingMode*(u: PlanningUnit): BacktrackingMode = u.options.backtrackingMode
proc setClientContext*(u: PlanningUnit, clientContext: RootRef) = u.options.clientContext = clientContext
proc databaseHook*(u: PlanningUnit): DatabaseHook = u.database
proc plannerHook*(u: PlanningUnit): PlannerHook = u.hook
proc defaultTopLevelMethod*(u: PlanningUnit): Symbol = u.defaultTopLevelMethod
proc lastDecomposition*(u: PlanningUnit): Atom = u.lastDecomposition
  ## The plan of the last top-level decomposition (empty after failures).
proc executionStorage*(u: PlanningUnit): Exec = u.execution

proc ensureExecutionStorage(u: PlanningUnit): bool =
  let definition = u.hook.generatedPlannerDefinition
  if definition == nil: return true
  if u.execution != nil and u.executionDefinition == definition: return true
  u.execution = definition.newExecutionStorage()
  u.executionDefinition = definition
  if u.execution == nil:
    u.executionDefinition = nil
    return false
  true

proc executeCall(u: PlanningUnit, call: Atom, requireTopLevel: bool): (Atom, DecompositionStatus) =
  if not u.ensureExecutionStorage(): return (emptyList(), dsOutOfMemory)
  var ctx = u.options
  ctx.worldState = u.hook.worldState
  ctx.bindings = u.hook.bindings
  ctx.call = call
  ctx.execution = u.execution
  u.hook.decompose(ctx, requireTopLevel)

proc clearCurrentPlan*(u: PlanningUnit) =
  ## Discards the active plan.
  u.currentPlan.setLen(0)
  u.currentIndex = 0

proc decomposeCall*(u: PlanningUnit, call: Atom): DecompositionStatus =
  ## Decomposes a caller-owned top-level call (head arg...) and installs the
  ## resulting plan.
  let (plan, status) = u.executeCall(call, true)
  u.lastDecomposition = plan
  if status == dsSucceeded:
    u.currentPlan = plan.elements
    u.currentIndex = 0
  else:
    u.clearCurrentPlan()
  status

proc decomposeTopLevelMethod*(u: PlanningUnit, methodSymbol: Symbol, args: varargs[Atom]): DecompositionStatus =
  ## Decomposes `methodSymbol` with atom arguments.
  if methodSymbol == nil: return dsInvalidCall
  let (call, ok) = makeCall(methodSymbol, args)
  if not ok: return dsOutOfMemory
  u.decomposeCall(call)

proc decompose*(u: PlanningUnit): DecompositionStatus =
  ## Decomposes the default top-level method without arguments.
  u.decomposeTopLevelMethod(u.defaultTopLevelMethod)

proc resolveCurrentPrimitiveTask*(u: PlanningUnit): PrimitiveTaskResolution =
  ## Expands deferred calls at the current position until a primitive task is
  ## ready, the plan completes or an expansion fails.
  while u.currentIndex < u.currentPlan.len:
    let step = u.currentPlan[u.currentIndex]
    let kind = getPlanStepKind(step)
    if kind == planStepPrimitiveTask: return taskReady
    if kind != planStepDeferredCall:
      u.clearCurrentPlan()
      return resolutionFailed
    let (call, ok) = makeCallFromDeferredPlanStep(step)
    if not ok or not isValidCall(call):
      u.clearCurrentPlan()
      return resolutionFailed
    let (plan, status) = u.executeCall(call, false)
    if status != dsSucceeded:
      u.clearCurrentPlan()
      return resolutionFailed
    let replacement = plan.elements
    let rest = u.currentPlan[u.currentIndex + 1 .. ^1]
    u.currentPlan.setLen(u.currentIndex)
    u.currentPlan.add replacement
    u.currentPlan.add rest
  planCompleted

proc currentPrimitiveTask*(u: PlanningUnit): (Atom, bool) =
  ## The primitive task at the current position.
  if u.currentIndex >= u.currentPlan.len: return (Atom(), false)
  let step = u.currentPlan[u.currentIndex]
  if getPlanStepKind(step) != planStepPrimitiveTask: return (Atom(), false)
  (step, true)

proc completeCurrentPrimitiveTask*(u: PlanningUnit) =
  ## Advances past the current primitive task.
  if u.currentPrimitiveTask()[1]: inc u.currentIndex

proc currentPlan*(u: PlanningUnit): seq[Atom] = u.currentPlan
proc currentPrimitiveTaskIndex*(u: PlanningUnit): int = u.currentIndex

# ---------------------------------------------------------------------------
# Daemons (AIHtnDaemonBase)

type
  Daemon* = ref object of RootObj
    ## Feeds a planner's world state. Override `update` and `onWriteWorldState`.
    factRegistry*: FactRegistry
    world*: WorldState

method update*(d: Daemon, deltaTime: float32) {.base.} = discard
method onWriteWorldState*(d: Daemon, world: WorldState) {.base.} = discard

proc initialize*(d: Daemon, hook: PlannerHook) =
  ## Binds the daemon to a planner hook.
  if hook == nil:
    d.factRegistry = nil
    d.world = nil
    return
  d.factRegistry = hook.factRegistry
  d.world = hook.worldState

proc writeWorldState*(d: Daemon, world: WorldState) =
  ## Associates `world` with the daemon's fact registry and lets the daemon
  ## write its knowledge.
  world.setFactRegistry(d.factRegistry)
  d.onWriteWorldState(world)
