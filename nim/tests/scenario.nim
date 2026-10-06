## Runs the shared HTN scenario files (testdata/scenarios) on the Nim port.
## The scenario language and its canonical output are defined by the C++
## oracle (tools/oracle/oracle.cpp); golden files produced by the oracle are
## compared with this interpreter's output byte for byte.

import std/[algorithm, os, strutils, tables]
import htn/[atom, callterm, debugger, integration, planner, worldstate]

type
  WorldStateDaemon = ref object of RootObj
    world: WorldState

  AgentDaemon = ref object of RootObj
    value: int32

  ScenarioState = ref object
    spec: seq[string]
    definition: Definition
    database: DatabaseHook
    registry: Registry
    hook: PlannerHook
    unit: PlanningUnit
    worldDaemon: WorldStateDaemon
    agent: AgentDaemon
    mode: BacktrackingMode
    policy: ErrorPolicy
    rawPrepared: RootRef
    rawExec: Exec
    # The generated event debugger (-d:htnDebug builds only).
    debuggerEnabled: bool
    debugger: GeneratedDebugger
    dumpedRevision: uint64

  Runner = ref object
    output: string
    root: string
    planners: Table[string, DefinitionGetter]

  ScenarioError* = object of CatchableError

proc emit(r: Runner, line: string) =
  r.output.add line
  r.output.add '\n'

proc formatAtom*(a: Atom): string =
  ## HTNAtomToString with quoted strings, "?" for unbound atoms.
  if not a.isBound: "?" else: toString(a, true)

proc formatStep*(step: Atom): string =
  ## One call-shaped plan step: "head arg...".
  let head = callHead(step)
  result = if head != nil: head.text else: "<invalid>"
  for i in 0 ..< callArgumentCount(step):
    let (argument, ok) = callArgument(step, i)
    result.add ' '
    result.add(if ok: formatAtom(argument) else: "?")

proc emitPlan(r: Runner, plan: Atom) =
  if not plan.isKind(akList):
    r.emit("plan -")
    return
  let elements = plan.elements
  r.emit("plan " & $elements.len)
  for step in elements: r.emit("step " & formatStep(step))

proc index(value: uint32): string = (if value == NoIndex: "-" else: $value)
proc text(value: string): string = (if value.len == 0: "-" else: value)

proc onError(r: Runner): ErrorCallback =
  proc (clientContext: RootRef, info: ErrorInfo) =
    r.emit("error " & $info.reason & " name=" & text(info.name) & " daemon=" & text(info.daemonID) &
      " source=" & text(info.source.domain) & "|" & text(info.source.file) & ":" & $info.source.line & ":" &
      $info.source.column & " arg=" & index(info.argumentIndex) & " expected_count=" &
      index(info.expectedArgumentCount) & " actual_count=" & index(info.actualArgumentCount) & " expected_type=" &
      index(info.expectedAtomType) & " actual_type=" & index(info.actualAtomType) & " type_name=" &
      text(info.expectedTypeName))

proc trace(r: Runner, name: string, args: Arguments, result: Atom) =
  var line = "trace " & name & "("
  for i in 0 ..< args.len:
    if i != 0: line.add ", "
    line.add formatAtom(args[i])
  line.add ") -> " & formatAtom(result)
  r.emit(line)

proc isInt(a: Arguments, i: int): bool = a[i].isKind(akInt)
proc intAt(a: Arguments, i: int): int32 = a[i].intValue

proc readCell(a: Atom): (int32, int32, bool) =
  if not a.isKind(akList) or a.len != 2: return (0'i32, 0'i32, false)
  let x = a.at(0)[0]
  let y = a.at(1)[0]
  if not x.isKind(akInt) or not y.isKind(akInt): return (0'i32, 0'i32, false)
  (x.intValue, y.intValue, true)

type StandardFunction = proc (a: Arguments): Atom {.nimcall.}

## Mirrors kStandardCallTerms of the oracle.
let standardCallTerms: seq[(string, StandardFunction)] = @[
  ("binded_function_with_args", (proc (a: Arguments): Atom {.nimcall.} =
    newBool(a.len == 1 and a[0].isKind(akString)))),
  ("get_health", (proc (a: Arguments): Atom {.nimcall.} =
    newInt(if a.len == 1 and isInt(a, 0): 50'i32 else: 0'i32))),
  ("get_max_speed", (proc (a: Arguments): Atom {.nimcall.} =
    newFloat(if a.len == 1 and isInt(a, 0): 1.0'f32 else: 0.0'f32))),
  ("lt", (proc (a: Arguments): Atom {.nimcall.} =
    newBool(a.len == 2 and isInt(a, 0) and isInt(a, 1) and intAt(a, 0) < intAt(a, 1)))),
  ("inc", (proc (a: Arguments): Atom {.nimcall.} =
    newInt(if a.len == 1 and isInt(a, 0): intAt(a, 0) + 1 else: 0'i32))),
  ("add", (proc (a: Arguments): Atom {.nimcall.} =
    newInt(if a.len == 2 and isInt(a, 0) and isInt(a, 1): intAt(a, 0) + intAt(a, 1) else: 0'i32))),
  ("mul", (proc (a: Arguments): Atom {.nimcall.} =
    newInt(if a.len == 2 and isInt(a, 0) and isInt(a, 1): intAt(a, 0) * intAt(a, 1) else: 0'i32))),
  ("assignment_probe", (proc (a: Arguments): Atom {.nimcall.} =
    newInt(if a.len == 1 and isInt(a, 0): intAt(a, 0) + 1 else: 0'i32))),
  ("get_entity_position", (proc (a: Arguments): Atom {.nimcall.} =
    if a.len != 1 or not isInt(a, 0): return newInt(0)
    let entity = intAt(a, 0)
    newInt(if entity == 1: 7'i32 elif entity == 42: 20'i32 else: entity + 100))),
  ("get_distance_from_to", (proc (a: Arguments): Atom {.nimcall.} =
    if a.len != 2 or not isInt(a, 0) or not isInt(a, 1): return newFloat(0)
    newFloat(float32(abs(intAt(a, 1) - intAt(a, 0))) / 100.0'f32))),
  ("distance", (proc (a: Arguments): Atom {.nimcall.} = newFloat(0.1'f32))),
  ("identity", (proc (a: Arguments): Atom {.nimcall.} = (if a.len == 1: a[0] else: Atom()))),
  ("probe", (proc (a: Arguments): Atom {.nimcall.} = newBool(true))),
  ("axiom_trace", (proc (a: Arguments): Atom {.nimcall.} = newBool(true))),
  ("axiom_value", (proc (a: Arguments): Atom {.nimcall.} = newInt(42))),
  ("visit", (proc (a: Arguments): Atom {.nimcall.} = newBool(true))),
  ("same_location", (proc (a: Arguments): Atom {.nimcall.} =
    if a.len != 2: return newBool(false)
    let (fromX, fromY, fromOK) = readCell(a[0])
    if not fromOK: return newBool(false)
    let (toX, toY, toOK) = readCell(a[1])
    newBool(toOK and fromX == toX and fromY == toY))),
  ("both_coordinates_even", (proc (a: Arguments): Atom {.nimcall.} =
    if a.len != 1: return newBool(false)
    let (x, y, ok) = readCell(a[0])
    newBool(ok and x mod 2 == 0 and y mod 2 == 0))),
  ("both_coordinates_odd", (proc (a: Arguments): Atom {.nimcall.} =
    if a.len != 1: return newBool(false)
    let (x, y, ok) = readCell(a[0])
    newBool(ok and x mod 2 != 0 and y mod 2 != 0)))]

proc findStandard(name: string): StandardFunction =
  for (callName, fn) in standardCallTerms:
    if callName == name: return fn
  nil

proc fail(message: string) {.noreturn.} =
  raise newException(ScenarioError, message)

proc parseSignatureType(name: string): SignatureType =
  case name
  of "any": anyType()
  of "bool": kindType(akBool)
  of "int": kindType(akInt)
  of "float": kindType(akFloat)
  of "string": kindType(akString)
  of "symbol": kindType(akSymbol)
  of "list": kindType(akList)
  else: fail("unknown signature type: " & name)

proc tracedFunction(r: Runner, name: string, fn: StandardFunction): proc (args: var Arguments): Atom {.closure.} =
  proc (args: var Arguments): Atom =
    result = fn(args)
    r.trace(name, args, result)

proc memberFunction(r: Runner, name: string): CallTermFunction =
  proc (daemon: RootRef, args: var Arguments): Atom =
    result = newInt(AgentDaemon(daemon).value)
    r.trace(name, args, result)

proc constantFunction(r: Runner, name: string, value: int32): proc (args: var Arguments): Atom {.closure.} =
  proc (args: var Arguments): Atom =
    result = newInt(value)
    r.trace(name, args, result)

proc bindCallTerms(r: Runner, s: ScenarioState) =
  let registry = s.registry
  var excluded: seq[string]
  var standard = false
  for item in s.spec:
    if item == "standard": standard = true
    elif item == "none": standard = false
    elif item.startsWith("-"): excluded.add item[1 .. ^1]
  if standard:
    if "list" notin excluded: registry.bindListCallTerms()
    for (name, fn) in standardCallTerms:
      if name in excluded: continue
      registry.bindRaw(name, r.tracedFunction(name, fn))
    if "add_target_available" notin excluded:
      discard registry.bindMember("add_target_available", "WorldStateDaemon",
        proc (daemon: RootRef, args: var Arguments): Atom =
          discard WorldStateDaemon(daemon).world.addFact("target_available", newString("enemy0"))
          result = newBool(true)
          r.trace("add_target_available", args, result),
        @[])
  for item in s.spec:
    let parts = item.split(':')
    if parts.len < 2: continue
    let (kind, name) = (parts[0], parts[1])
    case kind
    of "typed":
      let fn = findStandard(name)
      if fn == nil or parts.len != 3: fail("typed callterm needs a standard name and a signature: " & item)
      var signature: Signature
      if parts[2] != "none":
        for typeName in parts[2].split(','): signature.add parseSignatureType(typeName)
      registry.bindWithSignature(name, r.tracedFunction(name, fn), signature)
    of "member":
      discard registry.bindMember(name, "agent", r.memberFunction(name), @[])
    of "empty":
      discard registry.bindMember(name, "agent", nil, @[])
    else:
      fail("unknown callterm modifier: " & item)

proc createPlanner(r: Runner, s: ScenarioState, variant: string) =
  if variant notin r.planners: fail("unknown planner variant " & variant)
  s.definition = r.planners[variant]()
  s.database = newDatabaseHook()
  s.registry = newRegistry()
  r.bindCallTerms(s)
  s.hook = newPlannerHook(s.database.worldState, s.registry)
  s.worldDaemon = WorldStateDaemon(world: s.database.worldState)
  discard s.hook.bindings.setDaemon("WorldStateDaemon", s.worldDaemon)
  if not s.hook.setGeneratedPlannerDefinition(s.definition): fail("definition rejected: " & variant)
  s.unit = newPlanningUnit(s.database, s.hook, "run")
  s.unit.setBacktrackingMode(s.mode)
  s.unit.executionContext.callTermErrorPolicy = s.policy
  s.unit.executionContext.callTermErrorCallback = r.onError()
  if s.debuggerEnabled: s.unit.setGeneratedDebugger(s.debugger)

proc dumpDebugger(r: Runner, s: ScenarioState) =
  ## Writes every recorded node of the generated event debugger after a new
  ## capture (the debugger is reset by every decomposition).
  if not s.debuggerEnabled or s.debugger.revision == s.dumpedRevision: return
  s.dumpedRevision = s.debugger.revision
  for line in s.debugger.dump(): r.emit(line)

proc makeScenarioCall*(callText: string): (Atom, bool) =
  ## Parses "name arg..." with the world-state syntax and builds (name arg...).
  let temporary = newWorldState()
  if not temporary.parseText(callText): return (Atom(), false)
  let facts = temporary.facts
  if facts.len != 1: return (Atom(), false)
  let tables = temporary.findTables(facts[0])
  for arity in 0 ..< FactArgumentsSize:
    if tables.tables[arity].rowCount == 0: continue
    return makeCall(facts[0], tables.tables[arity].rows[0])
  (Atom(), false)

proc runRaw(r: Runner, s: ScenarioState, callText: string, requireTopLevel: bool) =
  let d = s.definition
  if s.rawExec == nil:
    s.rawPrepared = d.newPreparedStorage()
    s.rawExec = d.newExecutionStorage()
    if s.rawPrepared == nil or s.rawExec == nil: fail("storage initialization failed")
  let (call, ok) = makeScenarioCall(callText)
  if not ok: fail("invalid call: " & callText)
  var ctx = Context(worldState: s.database.worldState, bindings: s.hook.bindings, backtrackingMode: s.mode,
    execution: s.rawExec, prepared: s.rawPrepared, callTermErrorPolicy: s.policy,
    callTermErrorCallback: r.onError())
  if s.debuggerEnabled:
    ctx.debugger = s.debugger
    s.debugger.reset(if d.debugMetadata == nil: "" else: d.debugMetadata.sourceFile)
  let (plan, status) = d.decomposeCall(ctx, call, requireTopLevel)
  r.emit("status " & $status)
  let info = s.rawExec.info
  let lastError = if info.lastError.len == 0: "-" else: info.lastError
  r.emit("info peak=" & $info.peakCallFrames & " capacity=" & $info.callFrameCapacity & " error=" & lastError)
  r.emitPlan(plan)
  r.dumpDebugger(s)

proc runLines(r: Runner, path: string) =
  var current: ScenarioState = nil
  proc finish() =
    if current != nil:
      r.emit("end")
      current = nil
  for rawLine in lines(path):
    let trimmed = rawLine.strip(chars = {' ', '\t', '\r'})
    if trimmed.len == 0 or trimmed[0] == '#': continue
    var command = trimmed
    var argument = ""
    let space = trimmed.find(' ')
    if space >= 0:
      command = trimmed[0 ..< space]
      argument = trimmed[space + 1 .. ^1].strip(chars = {' ', '\t', '\r'})
    if command == "scenario":
      finish()
      current = ScenarioState(spec: @["standard"], mode: bmAll, policy: epFailSilently, agent: AgentDaemon(value: 1),
        debugger: newGeneratedDebugger())
      r.emit("scenario " & argument)
      continue
    if current == nil: fail("command outside of a scenario")
    let s = current
    if command notin ["callterms", "planner", "end"] and s.unit == nil:
      fail("'" & command & "' requires a planner")
    case command
    of "callterms":
      if s.unit != nil: fail("callterms must precede planner")
      s.spec = argument.split(' ')
    of "planner":
      r.createPlanner(s, argument)
    of "worldstate":
      if not s.database.parseWorldStateFile(r.root / argument):
        r.emit("worldstate failed " & argument)
    of "fact":
      if not s.database.worldState.parseText(argument):
        r.emit("fact failed " & argument)
    of "remove_fact":
      let parts = argument.split(' ')
      if parts.len != 3: fail("remove_fact <name> <arity> <index>")
      s.database.worldState.removeFact(parts[0], parseInt(parts[1]), parseInt(parts[2]))
    of "clear_facts":
      s.database.worldState.removeAllFacts()
    of "mode":
      s.mode = case argument
        of "none": bmNone
        of "facts_and_axioms": bmFactsAndAxioms
        of "branches": bmBranches
        of "all": bmAll
        else: fail("unknown mode " & argument)
      s.unit.setBacktrackingMode(s.mode)
    of "policy":
      s.policy = case argument
        of "unset": epUnset
        of "silent": epFailSilently
        of "report": epReport
        else: fail("unknown policy " & argument)
      s.unit.executionContext.callTermErrorPolicy = s.policy
    of "daemon":
      case argument
      of "on": discard s.hook.bindings.setDaemon("agent", s.agent)
      of "off": discard s.hook.bindings.setDaemon("agent", nil)
      else: fail("daemon on|off")
    of "rebind":
      let parts = argument.split(' ')
      if parts.len != 2: fail("rebind <name> <int>")
      s.registry.bindRaw(parts[0], r.constantFunction(parts[0], int32(parseInt(parts[1]))))
    of "debugger":
      when not htnDebugEnabled:
        fail("the debugger command needs a build with -d:htnDebug")
      else:
        if argument != "on" and argument != "off": fail("debugger expects on or off")
        s.debuggerEnabled = argument == "on"
        s.debugger.setEnabled(s.debuggerEnabled)
        s.dumpedRevision = s.debugger.revision
        s.unit.setGeneratedDebugger(if s.debuggerEnabled: s.debugger else: nil)
    of "call":
      r.emit("call " & argument)
      let (call, ok) = makeScenarioCall(argument)
      if not ok: fail("invalid call: " & argument)
      let status = s.unit.decomposeCall(call)
      r.emit("status " & $status)
      r.emitPlan(s.unit.lastDecomposition)
      r.dumpDebugger(s)
    of "resolve":
      r.emit("resolve")
      while true:
        let resolution = s.unit.resolveCurrentPrimitiveTask()
        r.dumpDebugger(s)
        if resolution == taskReady:
          let (task, _) = s.unit.currentPrimitiveTask()
          r.emit("exec " & formatStep(task))
          s.unit.completeCurrentPrimitiveTask()
          continue
        r.emit(if resolution == planCompleted: "resolution completed" else: "resolution failed")
        break
    of "raw", "rawdeferred":
      r.emit(command & " " & argument)
      r.runRaw(s, argument, command == "raw")
    of "end":
      finish()
    else:
      fail("unknown command " & command)
  finish()

proc runScenarioFile*(path, root: string, planners: Table[string, DefinitionGetter]): (string, string) =
  ## Executes one scenario file. Returns (output, error); paths inside the
  ## scenario are resolved against `root` (the repository root).
  let r = Runner(root: root, planners: planners)
  try:
    r.runLines(path)
  except ScenarioError as e:
    return (r.output, path & ": " & e.msg)
  (r.output, "")

proc checkGoldenDirectory*(root, directory: string, planners: Table[string, DefinitionGetter]): bool =
  ## Runs every `<root>/<directory>/*.scn` file and compares its output with
  ## the `.golden` file next to it, printing one line per file and the first
  ## difference. Returns whether every file matches.
  var files: seq[string]
  for file in walkFiles(root / directory / "*.scn"): files.add file
  files.sort()
  if files.len == 0:
    echo "no scenario files found in ", directory
    return false
  var failures = 0
  for file in files:
    let golden = readFile(file.changeFileExt("golden"))
    let (output, error) = runScenarioFile(file, root, planners)
    let name = extractFilename(file)
    if error.len > 0:
      echo "[FAIL] ", name, ": ", error
      inc failures
      continue
    if output == golden:
      echo "[OK]   ", name
      continue
    inc failures
    let want = golden.split('\n')
    let got = output.split('\n')
    var scenarioName = ""
    for i in 0 ..< max(want.len, got.len):
      let w = if i < want.len: want[i] else: ""
      let g = if i < got.len: got[i] else: ""
      if w.startsWith("scenario "): scenarioName = w
      if w != g:
        echo "[FAIL] ", name, " ", scenarioName, ": first difference at line ", i + 1
        for k in max(0, i - 8) ..< i: echo "    ", want[k]
        echo "  want: ", w
        echo "  got:  ", g
        break
  if failures > 0:
    echo failures, " scenario file(s) failed"
    return false
  echo "all ", files.len, " scenario files of ", directory, " match the oracle"
  true
