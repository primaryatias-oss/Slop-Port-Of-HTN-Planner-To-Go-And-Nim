## htn_demo is the terminal port of HTNDemo: the Domain Runner over the demo
## domains and the NPC simulation of Wanderer agents.
##
##   htn_demo list
##   htn_demo run <domain> [method] [--worldstate=PATH] [--backtracking=none|facts|branches|all] [--rt]
##   htn_demo simulate [--agents=8] [--speed=1] [--fps=30] [--seconds=N] [--ascii] [--rt]
##   htn_demo trace runner [--rt]
##   htn_demo trace simulate [--agents=8] [--steps=3600] [--snapshot-every=300] [--rt]
##
## --rt selects the planners generated with runtime backtracking support (the
## only ones on which the backtracking mode has an effect). "trace" prints the
## deterministic output of the original demo's headless driver
## (tools/oracle/demo_oracle.cpp) used by the tests.

import std/[monotimes, os, posix, sets, strformat, strutils, tables, terminal, times]
import htn/[integration, planner]
import agent, runner, world

const usage = """usage:
  htn_demo list
  htn_demo run <domain> [method] [--worldstate=PATH] [--backtracking=none|facts|branches|all] [--rt]
  htn_demo simulate [--agents=8] [--speed=1] [--fps=30] [--seconds=N] [--ascii] [--rt]
  htn_demo trace runner [--rt]
  htn_demo trace simulate [--agents=8] [--steps=3600] [--snapshot-every=300] [--rt]
"""

type Options = object
  positional: seq[string]
  values: Table[string, string]

proc fail(message: string) =
  stderr.writeLine "htn_demo: ", message
  quit 2

proc parseOptions(args: seq[string]): Options =
  for argument in args:
    if argument.startsWith("--"):
      let body = argument[2 .. ^1]
      let separator = body.find('=')
      if separator < 0: result.values[body] = ""
      else: result.values[body[0 ..< separator]] = body[separator + 1 .. ^1]
    else:
      result.positional.add argument

proc has(o: Options, name: string): bool = name in o.values

proc integer(o: Options, name: string, fallback: int): int =
  if name notin o.values: return fallback
  try: parseInt(o.values[name])
  except ValueError:
    fail(&"invalid --{name} value \"{o.values[name]}\"")
    0

proc number(o: Options, name: string, fallback: float): float =
  if name notin o.values: return fallback
  try: parseFloat(o.values[name])
  except ValueError:
    fail(&"invalid --{name} value \"{o.values[name]}\"")
    0.0

proc repositoryRoot(o: Options): string =
  ## The directory holding WorldStates, from the current directory upwards
  ## (or --root).
  if o.values.getOrDefault("root").len > 0: return o.values["root"]
  var probe = getCurrentDir()
  for _ in 0 ..< 8:
    if dirExists(probe / "WorldStates"): return probe
    let parent = probe.parentDir
    if parent == probe or parent.len == 0: break
    probe = parent
  fail("cannot find the repository's WorldStates directory; run inside the repository or pass --root=DIR")

proc writeStdout(text: string) = stdout.write(text)
proc writeStderr(text: string) = stderr.write(text)

proc list(o: Options) =
  let root = repositoryRoot(o)
  let worldStates = findWorldStates(root)
  echo "Domains (top-level methods; preferred world state):"
  for d in domains:
    let index = bestWorldState(d, worldStates)
    let best = if index >= 0: repositoryRelative(root, worldStates[index]) else: "-"
    echo "  ", d.name.alignLeft(25), " ", d.methods.join(", ")
    echo "  ", "".alignLeft(25), "   world state: ", best
  echo "\nWorld states:"
  for path in worldStates: echo "  ", repositoryRelative(root, path)

proc parseMode(text: string): BacktrackingMode =
  case text.toLowerAscii
  of "", "all": bmAll
  of "none": bmNone
  of "facts", "facts-and-axioms", "facts_and_axioms": bmFactsAndAxioms
  of "branches": bmBranches
  else:
    fail(&"unknown backtracking mode \"{text}\" (none, facts, branches or all)")
    bmAll

proc runCommand(o: Options) =
  if o.positional.len < 1: fail("run needs a domain name (see htn_demo list)")
  let root = repositoryRoot(o)
  var d: Domain
  if not findDomain(o.positional[0], d): fail(&"unknown domain \"{o.positional[0]}\" (see htn_demo list)")
  let methodName = if o.positional.len > 1: o.positional[1] else: d.methods[0]
  var worldState = o.values.getOrDefault("worldstate")
  if worldState.len == 0:
    let worldStates = findWorldStates(root)
    let index = bestWorldState(d, worldStates)
    if index < 0: fail("no world states found under " & root)
    worldState = worldStates[index]
  let mode = parseMode(o.values.getOrDefault("backtracking"))
  let runtimeBacktracking = o.has("rt")
  let demoRunner = newRunner(callTermErrorReporter(writeStderr))
  if not demoRunner.select(d.definition(runtimeBacktracking), methodName):
    fail("could not load the generated planner of " & d.name)
  if not demoRunner.database.parseWorldStateFile(worldState):
    fail("world state [" & worldState & "] could not be read or parsed")
  let variant = if runtimeBacktracking: "generated with runtime backtracking support" else: "generated"
  echo &"Domain:        {d.name} ({variant})\nMethod:        {methodName}\n" &
    &"World state:   {repositoryRelative(root, worldState)}\nBacktracking:  {modeName(mode)}"
  let start = getMonoTime()
  let (status, steps) = demoRunner.run(methodName, mode)
  let elapsed = float((getMonoTime() - start).inNanoseconds) / 1e6
  if status != dsSucceeded:
    echo &"Result:        Failure ({status}) in {elapsed:.3f} ms"
    if status == dsCallFrameCapacityExceeded:
      echo "Generated call-frame capacity exceeded. Regenerate this domain with a larger " &
        "--call-frame-capacity and rebuild it."
    quit 1
  echo &"Result:        Success in {elapsed:.3f} ms\nPlan ({steps.len} step(s)):"
  for i, step in steps: echo &"  {i + 1:>2}. {step}"

proc trace(o: Options) =
  if o.positional.len < 1: fail("trace needs runner or simulate")
  case o.positional[0]
  of "runner": runnerTrace(writeStdout, repositoryRoot(o), o.has("rt"))
  of "simulate":
    simulationTrace(writeStdout, o.integer("agents", 8), o.integer("steps", 3600), o.integer("snapshot-every", 300),
      o.has("rt"))
  else: fail(&"unknown trace \"{o.positional[0]}\"")

const npcColors = [196, 46, 51, 201, 226, 208, 39, 129, 118, 214, 45, 207]

proc npcSymbol(id: uint32): char =
  const symbols = "123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
  symbols[int(id - 1) mod symbols.len]

proc renderFrame(s: Simulation, ascii: bool, elapsed: float): string =
  ## One frame with ANSI escape sequences: two grid rows per text row with
  ## half blocks, or one character per cell in ASCII mode.
  let t = s.terrain
  var occupant = initTable[Cell, Agent]()
  var path = initHashSet[Cell]()
  for npc in s.agents:
    occupant[npc.wanderer.location] = npc
    var cells: seq[Cell]
    if npc.navigationPath(cells):
      for cell in cells: path.incl cell
  proc color(c: Cell): int =
    if c in occupant: return npcColors[int(occupant[c].id - 1) mod npcColors.len]
    case t.cellType(c)
    of cellBlocked: 94
    of cellInteractable: 220
    of cellWalkable: (if c in path: 244 else: 236)
  result = "\e[H"
  if ascii:
    for y in countdown(t.height - 1, 0):
      for x in 0 ..< t.width:
        let c = Cell(x: int32(x), y: int32(y))
        if c in occupant: result.add npcSymbol(occupant[c].id)
        else:
          result.add(case t.cellType(c)
            of cellBlocked: '#'
            of cellInteractable: 'o'
            of cellWalkable: (if c in path: '+' else: '.'))
      result.add "\e[K\n"
  else:
    var y = t.height - 1
    while y >= 0:
      var lastTop, lastBottom = -1
      for x in 0 ..< t.width:
        let top = color(Cell(x: int32(x), y: int32(y)))
        let bottom = if y > 0: color(Cell(x: int32(x), y: int32(y - 1))) else: 0
        # Only emit the colors that change.
        if top != lastTop:
          result.add &"\e[38;5;{top}m"
          lastTop = top
        if bottom != lastBottom:
          result.add &"\e[48;5;{bottom}m"
          lastBottom = bottom
        result.add "▀"
      result.add "\e[0m\e[K\n"
      y -= 2
  result.add &"\nSimulated {s.age:.1f} s (wall {elapsed:.1f} s)  NPCs: {s.agents.len}  terrain {t.width}x{t.height}  " &
    "Ctrl-C to quit\e[K\n"
  result.add &"""{"NPC":<4} {"State":<21} {"Location":<9} {"Destination":<11} {"Plans":<6} {"Journeys":<8} Current task""" &
    "\e[K\n"
  for npc in s.agents:
    var task = npc.currentTaskName
    let plan = npc.currentPlan
    if npc.currentTaskIndex < plan.len: task = npc.formatTask(plan[npc.currentTaskIndex])
    let label = if ascii: alignLeft($npcSymbol(npc.id), 4)
                else: &"\e[38;5;{npcColors[int(npc.id - 1) mod npcColors.len]}m{alignLeft($npc.id, 4)}\e[0m"
    let w = npc.wanderer
    result.add &"{label} {w.stateName:<21} {$w.location:<9} {$w.destination:<11} {npc.planCount:<6} " &
      &"{w.journeys:<8} {task}\e[K\n"
  result.add "\e[J"

var interrupted = false

proc onInterrupt() {.noconv.} = interrupted = true

proc simulate(o: Options) =
  let agents = o.integer("agents", 8)
  let speed = o.number("speed", 1)
  let fps = o.integer("fps", 30)
  let seconds = o.number("seconds", 0)
  if agents < 0 or fps <= 0 or speed <= 0: fail("--agents must be >= 0, --fps and --speed > 0")
  let s = newSimulation(agents, o.has("rt"), callTermErrorReporter(writeStderr))
  let interactive = isatty(stdout)
  let ascii = o.has("ascii") or not interactive
  let deltaTime = float32(speed / float(fps))
  if not interactive:
    # Not a terminal: simulate without delays and print one ASCII frame per
    # simulated second.
    let limit = if seconds > 0: seconds else: 10.0
    var frame = 0
    while float(s.age) < limit:
      inc frame
      s.update(deltaTime)
      if frame mod fps == 0:
        echo renderFrame(s, true, 0).multiReplace(("\e[H", ""), ("\e[K", ""), ("\e[J", ""))
    return
  setControlCHook(onInterrupt)
  stdout.write "\e[?25l\e[2J"
  let start = getMonoTime()
  let frameTime = initDuration(nanoseconds = 1_000_000_000 div fps)
  var next = getMonoTime()
  while not interrupted:
    next = next + frameTime
    let wait = next - getMonoTime()
    if wait > DurationZero: sleep(int(wait.inMilliseconds))
    s.update(deltaTime)
    stdout.write renderFrame(s, ascii, float((getMonoTime() - start).inMilliseconds) / 1000)
    stdout.flushFile()
    if seconds > 0 and float(s.age) >= seconds: break
  stdout.write "\e[0m\e[?25h\n"
  stdout.flushFile()

proc main() =
  let params = commandLineParams()
  if params.len < 1:
    stderr.write usage
    quit 2
  let o = parseOptions(params[1 .. ^1])
  case params[0]
  of "list": list(o)
  of "run": runCommand(o)
  of "simulate": simulate(o)
  of "trace": trace(o)
  of "help", "-h", "--help": stdout.write usage
  else:
    stderr.write usage
    quit 2

main()
