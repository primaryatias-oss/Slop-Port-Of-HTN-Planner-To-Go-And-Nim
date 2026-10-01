## htn_benchmark measures generated Nim planners through the public hooks
## used by a host application (port of HTNBenchmark).
##
##   htn_benchmark [iterations] [max_threads]
##   htn_benchmark [iterations] [max_threads] --lifecycle
##   htn_benchmark [iterations] [max_threads] --lifecycle-heavy
##
## The regular suite reports throughput, latency, allocations, world-state
## gameplay scenarios and parallel scaling on the ComplexScenario domain.
## htn_benchmark.nims builds it with --mm:atomicArc so worker threads can
## share the immutable planner definition and callterm registry, and with
## -d:nimAllocStats for the allocation counts.

import std/[algorithm, atomics, cpuinfo, monotimes, os, strformat, strutils, times, typedthreads]
import htn/[atom, callterm, integration, planner, worldstate]
import ../tests/generated/[complexscenario, worldstatelookupscenarios]

type
  BenchmarkCase = object
    name, category, worldState: string

  Runner = ref object
    ## One planner instance: world state, prepared and execution storage and
    ## the reusable top-level call.
    database: DatabaseHook
    definition: Definition
    context: Context
    call: Atom

  AllocationCounts = object
    ## Mirrors system.AllocStats (whose fields are private).
    allocCount, deallocCount: int

const benchmarkCases = [
  BenchmarkCase(name: "Idle", category: "Small / branch-light",
    worldState: "WorldStates/Test/complex_scenario_idle.worldstate"),
  BenchmarkCase(name: "Combat", category: "Branching",
    worldState: "WorldStates/Test/complex_scenario_combat.worldstate"),
  BenchmarkCase(name: "Emergency", category: "Alternative branch",
    worldState: "WorldStates/Test/complex_scenario_emergency.worldstate"),
  BenchmarkCase(name: "Mobility", category: "Callterm / movement",
    worldState: "WorldStates/Test/complex_scenario_mobility.worldstate"),
  BenchmarkCase(name: "Recovery", category: "Conditions / recovery",
    worldState: "WorldStates/Test/complex_scenario_recovery.worldstate"),
  BenchmarkCase(name: "FactHeavy100", category: "Fact-heavy / recursive",
    worldState: "WorldStates/Test/complex_scenario_recursive_100.worldstate")]

const worldStateScenarioFacts = ["active_entity", "entity_state", "entity_target", "entity_squad", "entity_weapon",
  "entity_cover", "entity_route", "target_status", "squad_order", "weapon_status", "cover_quality", "route_status",
  "ammo_available"]

proc resolveRepositoryPath(relative: string): string =
  ## Looks for a repository-relative path from the current directory upwards.
  var probe = getCurrentDir()
  for _ in 0 ..< 6:
    let candidate = probe / relative
    if fileExists(candidate): return candidate
    let parent = probe.parentDir
    if parent == probe or parent.len == 0: break
    probe = parent
  ""

proc bindBenchmarkCallTerms(r: Registry) =
  r.bindRaw("binded_function_with_args", proc (a: var Arguments): Atom =
    newBool(a.len == 1 and a.values[0].isBound))
  r.bindRaw("get_health", proc (a: var Arguments): Atom =
    if a.len == 1 and a.values[0].isBound: newInt(50) else: newInt(0))
  r.bindRaw("get_max_speed", proc (a: var Arguments): Atom =
    if a.len == 1 and a.values[0].isBound: newFloat(1) else: newFloat(0))
  r.bindRaw("lt", proc (a: var Arguments): Atom =
    newBool(a.len == 2 and a.values[0].isKind(akInt) and a.values[1].isKind(akInt) and
      a.values[0].intValue < a.values[1].intValue))
  r.bindRaw("inc", proc (a: var Arguments): Atom =
    if a.len == 1 and a.values[0].isKind(akInt): newInt(a.values[0].intValue + 1) else: newInt(0))

proc execute(r: Runner): bool =
  r.definition.decomposeCall(r.context, r.call, true)[1] == dsSucceeded

proc newRunner(worldStatePath: string, definition: Definition, registry: Registry): Runner =
  ## A warmed-up runner, or nil when the world state or the warm-up fails.
  let r = Runner(database: newDatabaseHook(), definition: definition)
  if not r.database.parseWorldStateFile(worldStatePath): return nil
  r.context = Context(worldState: r.database.worldState, bindings: newBindingContext(registry),
    backtrackingMode: bmAll, execution: definition.newExecutionStorage(),
    prepared: definition.newPreparedStorage(), callTermErrorPolicy: epFailSilently)
  r.call = makeCall(intern("run_scenario"), [])[0]
  # Warm-up: resolve fact metadata and grow reusable scratch storage.
  if not r.execute(): return nil
  r

proc allocationCounts(): AllocationCounts = cast[AllocationCounts](getAllocStats())

proc elapsedMicroseconds(start, finish: MonoTime): float = float((finish - start).inNanoseconds) / 1000

proc percentile(sorted: seq[float], p: float): float =
  if sorted.len == 0: 0.0 else: sorted[int(p * float(sorted.len - 1))]

proc runSuite(definition: Definition, registry: Registry, iterations: int): (int, string) =
  let latencySamples = min(iterations, 5000)
  let allocationIterations = min(iterations, 1000)
  echo "scenario      category                    plans/s      us/plan   p50 us   p95 us   p99 us   max us  allocs/plan  failures"
  var failures = 0
  var factHeavy = ""
  for c in benchmarkCases:
    let path = resolveRepositoryPath(c.worldState)
    if path.len == 0:
      stderr.writeLine "Could not locate ", c.worldState, "."
      quit 2
    let r = newRunner(path, definition, registry)
    if r == nil:
      stderr.writeLine "Failed to initialize benchmark scenario '", c.name, "'."
      quit 3
    var caseFailures = 0
    let start = getMonoTime()
    for _ in 0 ..< iterations:
      if not r.execute(): inc caseFailures
    let elapsed = elapsedMicroseconds(start, getMonoTime())
    var samples = newSeq[float](latencySamples)
    for i in 0 ..< latencySamples:
      let sampleStart = getMonoTime()
      if not r.execute(): inc caseFailures
      samples[i] = elapsedMicroseconds(sampleStart, getMonoTime())
    samples.sort()
    let before = allocationCounts()
    for _ in 0 ..< allocationIterations:
      if not r.execute(): inc caseFailures
    let after = allocationCounts()
    let allocations = float(after.allocCount - before.allocCount) / float(allocationIterations)
    let seconds = elapsed / 1e6
    echo &"{c.name:<13} {c.category:<24} {int(float(iterations) / seconds + 0.5):>11} {elapsed / float(iterations):>12.3f} " &
      &"{percentile(samples, 0.50):>8.2f} {percentile(samples, 0.95):>8.2f} {percentile(samples, 0.99):>8.2f} " &
      &"{percentile(samples, 1.0):>8.2f} {allocations:>12.1f} {caseFailures:>9}"
    failures += caseFailures
    if c.name == "FactHeavy100": factHeavy = path
  (failures, factHeavy)

proc rebuildWorldStateScenario(w: WorldState, rows: int): bool =
  ## A full snapshot: the selected entity is the last row so linear lookups
  ## must traverse the tables.
  w.removeAllFacts()
  template s(name: string): Symbol = intern(name)
  template i(v: int): Atom = newInt(int32(v))
  if not w.writeFact(s"active_entity", i(rows - 1)): return false
  for row in 0 ..< rows:
    let id = i(row)
    if not (w.writeFact(s"entity_state", id, i(row mod 4)) and w.writeFact(s"entity_target", id, id) and
        w.writeFact(s"entity_squad", id, id) and w.writeFact(s"entity_weapon", id, id) and
        w.writeFact(s"entity_cover", id, id) and w.writeFact(s"entity_route", id, id) and
        w.writeFact(s"target_status", id, i(1)) and w.writeFact(s"squad_order", id, i(2)) and
        w.writeFact(s"weapon_status", id, i(1)) and w.writeFact(s"cover_quality", id, i(3)) and
        w.writeFact(s"route_status", id, i(1)) and w.writeFact(s"ammo_available", id)):
      return false
  true

proc runWorldStateScenarios(registry: Registry): int =
  let path = resolveRepositoryPath("WorldStates/Test/worldstate_lookup_scenarios.worldstate")
  if path.len == 0:
    echo "\nGenerated WorldState scenarios: SKIPPED (world state not found)"
    return 0
  echo "\nGenerated WorldState gameplay scenarios (real generated decomposition)"
  echo "  each frame performs RemoveAllFacts -> full WriteFact snapshot -> generated Decompose"
  echo "  scenario              rows/fact  fact types    iterations      us/frame      frames/s   failures"
  let definition = CreateWorldstateLookupScenariosHTN_GetDefinition()
  for (name, rows, iterations) in [("TypicalGameAI", 5, 10000), ("CrowdAI", 50, 5000), ("LargeFactDatabase", 500, 500)]:
    let r = newRunner(path, definition, registry)
    if r == nil:
      echo &"  {name:<20} initialization failed"
      continue
    let factRegistry = newFactRegistry()
    for fact in worldStateScenarioFacts: discard factRegistry.register(intern(fact))
    let world = r.database.worldState
    world.setFactRegistry(factRegistry)
    if not rebuildWorldStateScenario(world, rows) or not r.execute():
      echo &"  {name:<20} warm-up failed"
      continue
    var scenarioFailures = 0
    let start = getMonoTime()
    for _ in 0 ..< iterations:
      if not rebuildWorldStateScenario(world, rows) or not r.execute(): inc scenarioFailures
    let seconds = elapsedMicroseconds(start, getMonoTime()) / 1e6
    echo &"  {name:<20} {rows:>10} {worldStateScenarioFacts.len:>11} {iterations:>13} " &
      &"{seconds * 1e6 / float(iterations):>13.3f} {float(iterations) / seconds:>13.2f} {scenarioFailures:>10}"
    result += scenarioFailures

type WorkerArguments = object
  runner: Runner
  iterations: int
  start: ptr Atomic[bool]
  failures: ptr Atomic[int]

proc worker(arguments: WorkerArguments) {.thread.} =
  {.cast(gcsafe).}:
    while not arguments.start[].load(moAcquire): cpuRelax()
    var local = 0
    for _ in 0 ..< arguments.iterations:
      if not arguments.runner.execute(): inc local
    discard arguments.failures[].fetchAdd(local)

proc runThreadScaling(path: string, definition: Definition, registry: Registry,
    iterationsPerWorker, maxThreads: int): int =
  echo "\nParallel scaling: FactHeavy100 (independent WorldState + execution storage per worker)"
  var baseline = 0.0
  var failures: Atomic[int]
  for workers in [1, 2, 4, 8]:
    if workers > maxThreads: break
    var runners: seq[Runner]
    for i in 0 ..< workers:
      let r = newRunner(path, definition, registry)
      if r == nil:
        stderr.writeLine "Failed to initialize benchmark worker ", i, "."
        return failures.load + 1
      runners.add r
    var start: Atomic[bool]
    var threads = newSeq[Thread[WorkerArguments]](workers)
    for i in 0 ..< workers:
      createThread(threads[i], worker, WorkerArguments(runner: runners[i], iterations: iterationsPerWorker,
        start: addr start, failures: addr failures))
    let begin = getMonoTime()
    start.store(true, moRelease)
    joinThreads(threads)
    let seconds = elapsedMicroseconds(begin, getMonoTime()) / 1e6
    let throughput = float(iterationsPerWorker * workers) / seconds
    if workers == 1: baseline = throughput
    echo &"  {workers} thread(s): {throughput:.2f} plans/s  speedup={throughput / baseline:.2f}x"
  failures.load

proc runLifecycle(definition: Definition, registry: Registry, iterations: int, heavy: bool): int =
  echo "\nGenerated public API lifecycle: entity-cold setup / first plan / warmed plans"
  echo "scenario,backend,phase,ops,us/op,allocs/op,failures"
  for c in benchmarkCases:
    let factHeavy = c.name == "FactHeavy100"
    if factHeavy and not heavy:
      stderr.writeLine "FactHeavy100 skipped: use --lifecycle-heavy."
      continue
    let count = if factHeavy: min(iterations, 5) else: iterations
    let path = resolveRepositoryPath(c.worldState)
    var caseFailures = 0
    var phases: seq[(string, int, float, float)]
    template measure(phase: string, ops: int, body: untyped) =
      let before = allocationCounts()
      let start = getMonoTime()
      body
      let elapsed = elapsedMicroseconds(start, getMonoTime())
      let after = allocationCounts()
      phases.add (phase, ops, elapsed, float(after.allocCount - before.allocCount) / float(ops))
    let database = newDatabaseHook()
    measure("worldstate", 1):
      if not database.parseWorldStateFile(path): inc caseFailures
    var unit: PlanningUnit
    measure("setup", 1):
      let hook = newPlannerHook(database.worldState, registry)
      if not hook.setGeneratedPlannerDefinition(definition): inc caseFailures
      unit = newPlanningUnit(database, hook, "run_scenario")
    measure("first", 1):
      if unit.decompose() != dsSucceeded: inc caseFailures
    measure("steady", count):
      for _ in 0 ..< count:
        if unit.decompose() != dsSucceeded: inc caseFailures
    for (phase, ops, elapsed, allocations) in phases:
      echo &"{c.name},generated,{phase},{ops},{elapsed / float(ops):.3f},{allocations:.1f},{caseFailures}"
    result += caseFailures

proc main() =
  var iterations = 10000
  var maxThreads = min(countProcessors(), 8)
  if maxThreads <= 0: maxThreads = 1
  let params = commandLineParams()
  if params.len > 0:
    try:
      let value = parseInt(params[0])
      if value > 0: iterations = value
    except ValueError: discard
  if params.len > 1:
    try:
      let value = parseInt(params[1])
      if value > 0: maxThreads = value
    except ValueError: discard
  let definition = CreateComplexScenarioHTN_GetDefinition()
  let registry = newRegistry()
  bindBenchmarkCallTerms(registry)
  if params.len > 2 and params[2] in ["--lifecycle", "--lifecycle-heavy"]:
    if runLifecycle(definition, registry, iterations, params[2] == "--lifecycle-heavy") != 0: quit 4
    return
  echo &"HTN generated planner baseline benchmark (Nim port)\n  domain:            ComplexScenario\n" &
    &"  iterations/case:   {iterations}\n  max threads:       {maxThreads}\n" &
    &"  nim:               {NimVersion} {hostOS}/{hostCPU}\n"
  var (failures, factHeavy) = runSuite(definition, registry, iterations)
  failures += runWorldStateScenarios(registry)
  if factHeavy.len > 0:
    failures += runThreadScaling(factHeavy, definition, registry, max(iterations div 10, 100), maxThreads)
  echo &"\nBaseline result: {(if failures == 0: \"PASS\" else: \"FAIL\")}  total_failures={failures}"
  if failures != 0: quit 4

main()
