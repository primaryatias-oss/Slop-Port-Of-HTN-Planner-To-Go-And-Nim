## Runtime used by generated HTN planners (the generated-planner ABI of the
## original framework, HTNGeneratedPlanner.h).
##
## A generated domain exports an immutable `Definition`; callers own the
## execution storage (`Exec`) and `decomposeCall` runs one decomposition.
## Generated code performs all domain-specific work; this module provides the
## generic machinery that the C generator emitted inline: variable slots, the
## pending-continuation stack with restore snapshots, explicit call frames
## with branch-retry state and the iterative dispatcher.

import atom, callterm, worldstate

const MaxFloat32 = 3.4028234663852886e38
  ## FLT_MAX.

const ABIVersion* = 0x4E540001'u32
  ## Generated-planner contract of this port. Generated definitions must
  ## report the same value.

type
  DecompositionStatus* = enum
    ## Why a planning request finished (values match HTNDecompositionStatus).
    dsSucceeded, dsNoPlan, dsBacktrackingCapacityExceeded, dsOutOfMemory, dsInvalidContext,
    dsInvalidCall, dsPreparationFailed, dsNotRun, dsCallFrameCapacityExceeded

  BacktrackingMode* = uint32
    ## Runtime backtracking flags (see the bm* constants).

  Features* = uint32

  Context* = object
    ## Per-invocation execution descriptor (HTNGeneratedPlannerContext). All
    ## referenced objects are borrowed.
    worldState*: WorldState
    bindings*: BindingContext
    backtrackingMode*: BacktrackingMode
    execution*: Exec
    prepared*: RootRef
    clientContext*: RootRef
    callTermErrorPolicy*: ErrorPolicy
    callTermErrorCallback*: ErrorCallback

  ExecutionInfo* = object
    ## Call-frame diagnostics of the last decomposition.
    callFrameCapacity*: uint32
    peakCallFrames*: uint32
    lastError*: string

  DecomposeCallFn* = proc (ctx: var Context, call: Atom, requireTopLevel: bool): (Atom, DecompositionStatus) {.nimcall.}

  Definition* = ref object
    ## The immutable descriptor exported by a generated domain.
    abiVersion*: uint32
    features*: Features
    domainID*: string
    sourceFile*: string
    newPreparedStorage*: proc (): RootRef {.nimcall.}
    newExecutionStorage*: proc (): Exec {.nimcall.}
    decomposeCall*: DecomposeCallFn
    factNames*: seq[string]
    callTermRequirements*: seq[Requirement]

  DefinitionGetter* = proc (): Definition {.nimcall.}
    ## The exported `<entry point>_GetDefinition` accessor of a generated
    ## planner.

  TaskFn* = proc (ex: Exec): int {.nimcall.}
    ## A generated method or task: returns 0 (failure), 1 (success) or 2
    ## (suspend: run `ex.next` as a child, then resume).

  PendingTask* = object
    ## One statically known continuation of a branch.
    fn*: TaskFn
    restore*: seq[uint32]
      ## Slots the immediately preceding sibling may mutate.

  BranchContinuations* = object
    ## A branch's tasks in push order (last task first).
    tasks*: seq[PendingTask]
    totalRestore*: int

  CallFrame* = object
    ## One suspended generated method/task invocation.
    parent: int
    function*: TaskFn
    resume*: uint32
    childResult*: int
    retryPlanSize*: int
    retryPendingBase*: int
    retryFrame*: uint64
    retryValues*: seq[Atom]

  PendingEntry = object
    fn: TaskFn
    snapStart: int
    snapCount: int
    frameID: uint64

  Config* = object
    ## Sizes execution storage; emitted by the code generator.
    variableCount*: int
    factCount*: int
    callTermCount*: int
    callFrameCapacity*: uint32
    fixedCapacity*: bool
    backtrackingCapacity*: int
    snapshotCapacity*: int
    capacityError*: string

  Exec* = ref object
    ## Mutable execution storage of one planner instance. It must not be
    ## shared by concurrent decompositions.
    ctx*: Context
    v*: seq[Atom]
    plan*: seq[Atom]
    failureState*: DecompositionStatus
    info*: ExecutionInfo
    next*: TaskFn
    currentFrameID*: uint64
    nextFrameID*: uint64
    factTables*: seq[FactTables]
    factWorld: WorldState
    factGeneration: uint64
    factsReady: bool
    callTerms*: seq[Slot]
    callBindings: BindingContext
    callsReady: bool
    pending: seq[PendingEntry]
    snapSlots: seq[uint32]
    snapValues: seq[Atom]
    frames: seq[CallFrame]
    current: int
    frameCount: uint32
    config: Config
    inv: Invocation

  AxiomScope* = object
    ## Caller state around one axiom invocation.
    saved*: seq[Atom]
    args*: seq[Atom]
    callerFrame*: uint64

const
  bmNone* = BacktrackingMode(0)
  bmFactsAndAxioms* = BacktrackingMode(1)
  bmBranches* = BacktrackingMode(2)
  bmAll* = BacktrackingMode(3)
  featureNone* = Features(0)
  featureRuntimeBacktracking* = Features(1)

proc `$`*(s: DecompositionStatus): string =
  const names: array[DecompositionStatus, string] = ["SUCCEEDED", "NO_PLAN", "BACKTRACKING_CAPACITY_EXCEEDED",
    "OUT_OF_MEMORY", "INVALID_CONTEXT", "INVALID_CALL", "PREPARATION_FAILED", "NOT_RUN",
    "CALL_FRAME_CAPACITY_EXCEEDED"]
  names[s]

proc validateDefinition*(d: Definition): bool =
  ## Whether a definition matches this runtime's ABI and provides a complete
  ## lifecycle contract.
  d != nil and d.abiVersion == ABIVersion and d.newPreparedStorage != nil and
    d.newExecutionStorage != nil and d.decomposeCall != nil

proc newExec*(config: Config): Exec =
  ## Execution storage for a generated domain configuration.
  result = Exec(config: config, current: -1, currentFrameID: 1, nextFrameID: 2, failureState: dsNoPlan)
  result.v = newSeq[Atom](config.variableCount)
  result.factTables = newSeq[FactTables](config.factCount)
  result.callTerms = newSeq[Slot](config.callTermCount)
  result.info.callFrameCapacity = config.callFrameCapacity

proc frame*(ex: Exec): ptr CallFrame {.inline.} =
  ## The current call frame. Valid until the generated function returns to
  ## the dispatcher.
  addr ex.frames[ex.current]

proc pendingCount*(ex: Exec): int {.inline.} = ex.pending.len

proc begin*(ex: Exec, ctx: Context, factSymbols: openArray[Symbol], callNames: openArray[string]) =
  ## Resets execution state for a new decomposition and refreshes cached fact
  ## tables and callterm slots when their sources changed.
  ex.ctx = ctx
  ex.inv = Invocation(bindings: ctx.bindings, clientContext: ctx.clientContext,
    policy: ctx.callTermErrorPolicy, callback: ctx.callTermErrorCallback)
  ex.failureState = dsNoPlan
  let generation = ctx.worldState.generation
  let factsReady = ex.factsReady and ex.factWorld == ctx.worldState and ex.factGeneration == generation
  let callsReady = ex.callsReady and ex.callBindings == ctx.bindings
  for i in 0 ..< ex.v.len: ex.v[i] = Atom()
  ex.pending.setLen(0)
  ex.snapSlots.setLen(0)
  ex.snapValues.setLen(0)
  ex.plan.setLen(0)
  ex.currentFrameID = 1
  ex.nextFrameID = 2
  if not factsReady:
    for i, symbol in factSymbols: ex.factTables[i] = ctx.worldState.resolveGeneratedTables(symbol)
    ex.factWorld = ctx.worldState
    ex.factGeneration = generation
    ex.factsReady = true
  if not callsReady:
    for i, name in callNames: ex.callTerms[i] = ctx.bindings.resolveSlot(name)
    ex.callBindings = ctx.bindings
    ex.callsReady = true

proc resetDiagnostics*(ex: Exec) =
  ## Clears the execution info of the previous decomposition.
  ex.info.peakCallFrames = 0
  ex.info.lastError = ""

proc refreshFacts(ex: Exec, factSymbols: openArray[Symbol]) =
  ## Re-resolves cached fact tables after a callterm created a fact entry.
  let generation = ex.ctx.worldState.generation
  if generation == ex.factGeneration: return
  for i, symbol in factSymbols: ex.factTables[i] = ex.ctx.worldState.resolveGeneratedTables(symbol)
  ex.factGeneration = generation

proc invoke*(ex: Exec, slot: int, args: seq[Atom], source: ptr Source, factSymbols: openArray[Symbol]): (Atom, bool) =
  ## Runs the callterm cached in `slot` and refreshes the fact cache.
  let result0 = invokeEntry(ex.callTerms[slot].entry, ex.callTerms[slot].name, args, source, ex.inv)
  ex.refreshFacts(factSymbols)
  (result0, result0.isBound)

proc pushBranch*(ex: Exec, b: ptr BranchContinuations): bool =
  ## Schedules a branch's tasks. Fails when a fixed-capacity planner cannot
  ## store them.
  if ex.config.fixedCapacity:
    let capacity = ex.config.backtrackingCapacity
    let snapshots = ex.config.snapshotCapacity
    if b.tasks.len > capacity or b.totalRestore > snapshots or ex.pending.len > capacity - b.tasks.len or
        ex.snapSlots.len > snapshots - b.totalRestore:
      ex.failureState = dsBacktrackingCapacityExceeded
      return false
  let frameID = ex.currentFrameID
  for i in 0 ..< b.tasks.len:
    let start = ex.snapSlots.len
    for slot in b.tasks[i].restore:
      ex.snapSlots.add slot
      ex.snapValues.add ex.v[slot]
    ex.pending.add PendingEntry(fn: b.tasks[i].fn, snapStart: start, snapCount: b.tasks[i].restore.len, frameID: frameID)
  true

proc popPending*(ex: Exec): TaskFn =
  ## Removes the most recent continuation and restores its snapshot.
  let n = ex.pending.len
  if n == 0: return nil
  let entry = ex.pending[n - 1]
  ex.pending.setLen(n - 1)
  for i in entry.snapStart ..< entry.snapStart + entry.snapCount:
    ex.v[ex.snapSlots[i]] = ex.snapValues[i]
    ex.snapValues[i] = Atom()
  ex.snapSlots.setLen(entry.snapStart)
  ex.snapValues.setLen(entry.snapStart)
  ex.currentFrameID = entry.frameID
  entry.fn

proc saveRetry*(ex: Exec, frame: ptr CallFrame, slots: openArray[uint32]) =
  ## Captures a method's retry state before a branch that can fall back to a
  ## later branch.
  frame.retryPlanSize = ex.plan.len
  frame.retryPendingBase = ex.pending.len
  frame.retryFrame = ex.currentFrameID
  frame.retryValues.setLen(slots.len)
  for i, slot in slots: frame.retryValues[i] = ex.v[slot]

proc releaseRetry*(ex: Exec, frame: ptr CallFrame) =
  ## Drops the captured retry values.
  for i in 0 ..< frame.retryValues.len: frame.retryValues[i] = Atom()

proc restoreRetry*(ex: Exec, frame: ptr CallFrame, slots: openArray[uint32]) =
  ## Rolls back a failed branch subtree: discards its pending continuations,
  ## truncates the partial plan and restores the method slots.
  while ex.pending.len > frame.retryPendingBase: discard ex.popPending()
  if ex.plan.len > frame.retryPlanSize: ex.plan.setLen(frame.retryPlanSize)
  for i, slot in slots:
    ex.v[slot] = frame.retryValues[i]
    frame.retryValues[i] = Atom()
  ex.currentFrameID = frame.retryFrame

proc enterFrame*(ex: Exec) {.inline.} =
  ## Starts a fresh logical variable frame for a compound call.
  ex.currentFrameID = ex.nextFrameID
  inc ex.nextFrameID

proc run*(ex: Exec, fn: TaskFn): int =
  ## Drives `fn` and every function it suspends into with explicit call
  ## frames (the generated _RUN dispatcher). It never recurses natively.
  if ex.frames.len == 0: ex.frames.add CallFrame()
  ex.frameCount = 1
  if ex.info.peakCallFrames == 0: ex.info.peakCallFrames = 1
  ex.current = 0
  ex.frames[0].parent = -1
  ex.frames[0].resume = 0
  ex.frames[0].function = fn
  while ex.current >= 0:
    let result0 = ex.frames[ex.current].function(ex)
    result = result0
    if result0 == 2:
      if ex.frameCount == ex.config.callFrameCapacity:
        ex.failureState = dsCallFrameCapacityExceeded
        ex.info.lastError = ex.config.capacityError
        ex.frames[ex.current].childResult = 0
        continue
      let index = int(ex.frameCount)
      inc ex.frameCount
      if ex.frameCount > ex.info.peakCallFrames: ex.info.peakCallFrames = ex.frameCount
      if index >= ex.frames.len: ex.frames.add CallFrame()
      ex.frames[index].resume = 0
      ex.frames[index].parent = ex.current
      ex.frames[index].function = ex.next
      ex.current = index
    else:
      let parent = ex.frames[ex.current].parent
      ex.frames[ex.current].function = nil
      dec ex.frameCount
      ex.current = parent
      if parent >= 0: ex.frames[parent].childResult = result0

proc appendPlanStep*(ex: Exec, head: Symbol, args: openArray[Atom]): bool =
  ## Appends a plan step built from `head` and `args`. Fails when an argument
  ## is unbound (the original reports OUT_OF_MEMORY in that case).
  var elems = newSeqOfCap[Atom](args.len + 1)
  elems.add newSymbol(head)
  for arg in args:
    if not arg.isBound:
      ex.failureState = dsOutOfMemory
      return false
    elems.add arg
  ex.plan.add newListOwned(elems)
  true

proc planAtom*(ex: Exec): Atom =
  ## The completed plan as a list atom.
  newListOwned(ex.plan)

proc setIfChanged*(ex: Exec, slot: uint32, value: Atom) {.inline.} =
  ## Binds `slot` to `value` when it is bound and differs from the current
  ## binding (EmitGeneratedSetCopyIfChanged).
  if value.isBound:
    if not ex.v[slot].isBound or not equal(ex.v[slot], value):
      ex.v[slot] = value

proc arith*(op: uint32, operands: openArray[Atom]): Atom =
  ## Evaluates a generated arithmetic expression with the original semantics:
  ## int32 arithmetic with overflow checks unless an operand is a float, float
  ## arithmetic in double precision stored as float32. Unbound when invalid.
  let count = operands.len
  if count == 0: return Atom()
  var useFloat = false
  for operand in operands:
    case operand.kind
    of akInt: discard
    of akFloat: useFloat = true
    else: return Atom()
  if op == 4 and (count != 2 or useFloat): return Atom()
  if op == 5 or op == 6:
    if count != 1: return Atom()
    let delta = if op == 6: -1'i64 else: 1'i64
    if not useFloat:
      let incremented = int64(operands[0].intValue) + delta
      if incremented < int64(low(int32)) or incremented > int64(high(int32)): return Atom()
      return newInt(int32(incremented))
    let value = float64(operands[0].floatValue) + float64(delta)
    if value != value or value < -MaxFloat32 or
        value > MaxFloat32:
      return Atom()
    return newFloat(float32(value))
  if not useFloat:
    var accumulated: int64
    var i = 1
    if op == 2:
      accumulated = 1
      i = 0
    else:
      accumulated = int64(operands[0].intValue)
    if op == 1 and count == 1:
      accumulated = -int64(operands[0].intValue)
      i = count
    while i < count:
      let value = int64(operands[i].intValue)
      case op
      of 0: accumulated += value
      of 1: accumulated -= value
      of 2: accumulated *= value
      of 3:
        if value == 0: return Atom()
        accumulated = accumulated div value
      of 4:
        if value == 0: return Atom()
        accumulated = accumulated mod value
      else: return Atom()
      if accumulated < int64(low(int32)) or accumulated > int64(high(int32)): return Atom()
      inc i
    return newInt(int32(accumulated))
  proc number(a: Atom): float64 =
    if a.kind == akInt: float64(a.intValue) else: float64(a.floatValue)
  var accumulated: float64
  var i = 1
  if op == 2:
    accumulated = 1.0
    i = 0
  else:
    accumulated = number(operands[0])
  if op == 1 and count == 1:
    accumulated = -accumulated
    i = count
  while i < count:
    let value = number(operands[i])
    case op
    of 0: accumulated += value
    of 1: accumulated -= value
    of 2: accumulated *= value
    of 3:
      if value == 0: return Atom()
      accumulated /= value
    else: return Atom()
    if accumulated != accumulated or accumulated < -MaxFloat32 or
        accumulated > MaxFloat32:
      return Atom()
    inc i
  newFloat(float32(accumulated))

proc compare*(left, right: Atom, op: uint32): bool =
  ## The generated built-in comparison: unbound operands fail, == and !=
  ## compare numbers numerically and other atoms structurally, ordering
  ## requires two numbers.
  if not left.isBound or not right.isBound: return false
  let leftNumeric = left.kind in {akInt, akFloat}
  let rightNumeric = right.kind in {akInt, akFloat}
  var l, r: float64
  case left.kind
  of akInt: l = float64(left.intValue)
  of akFloat: l = float64(left.floatValue)
  else: discard
  case right.kind
  of akInt: r = float64(right.intValue)
  of akFloat: r = float64(right.floatValue)
  else: discard
  if op == 0 or op == 1:
    let equalValues = if leftNumeric and rightNumeric: l == r else: equal(left, right)
    return if op == 0: equalValues else: not equalValues
  if not leftNumeric or not rightNumeric: return false
  case op
  of 2: l < r
  of 3: l <= r
  of 4: l > r
  of 5: l >= r
  else: false
