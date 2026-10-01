## The demo world (DemoGridTerrain and DemoWanderer): a random grid terrain
## with interactables and the persistent gameplay state of one civilian. The
## generation reproduces the original's std::mt19937-based sequences.

import htn/atom
import stdrand

type
  Cell* = object
    ## A grid coordinate, marshalled to HTN as the list (X Y).
    x*, y*: int32

  CellType* = enum
    cellWalkable, cellBlocked, cellInteractable

  InteractableType* = enum
    viewpoint, bench, shopWindow

  Interactable* = object
    id*: string
    kind*: InteractableType
    location*: Cell
    contextAnimation*: string
    usageTimeSeconds*: float32

  Terrain* = ref object
    width, height: int
    cells: seq[CellType]
    interactables: seq[Interactable]

  WandererState* = enum
    walking, contextual

  Wanderer* = ref object
    terrain: Terrain
    waypoints: array[12, Cell]
    state: WandererState
    location: Cell
    destination: Cell
    speed: float32
    destinationIndex: int
    journeys: uint64

const
  DefaultWidth* = 64
  DefaultHeight* = 64
  DefaultSeed* = 0xA57A1234'u32
  DefaultInteractableCount* = 12
  WaypointCount* = 12

proc `$`*(c: Cell): string = "(" & $c.x & " " & $c.y & ")"

proc cellAtom*(c: Cell): Atom = newList(newInt(c.x), newInt(c.y))

proc parseCell*(a: Atom, c: var Cell): bool =
  ## Converts an HTN list of two integers into a cell.
  if not a.isKind(akList) or a.len != 2: return false
  let x = a.at(0)[0]
  let y = a.at(1)[0]
  if not x.isKind(akInt) or not y.isKind(akInt): return false
  c = Cell(x: x.intValue, y: y.intValue)
  true

# Typed callterm conversions (HTNTypeTraits/HTNTypeConverter<Cell>).
proc atomKindOf*(t: typedesc[Cell]): (bool, AtomKind) = (true, akList)
proc atomTypeName*(t: typedesc[Cell]): string = "Cell"
proc fromAtom*(ctx: RootRef, a: Atom, v: var Cell): bool = parseCell(a, v)
proc toAtom*(ctx: RootRef, v: Cell, a: var Atom): bool = (a = cellAtom(v); true)

proc name*(t: InteractableType): string =
  case t
  of viewpoint: "Viewpoint"
  of bench: "Bench"
  of shopWindow: "Shop window"

proc width*(t: Terrain): int = t.width
proc height*(t: Terrain): int = t.height
proc interactables*(t: Terrain): seq[Interactable] = t.interactables

proc isInside*(t: Terrain, c: Cell): bool =
  c.x >= 0 and int(c.x) < t.width and c.y >= 0 and int(c.y) < t.height

proc index(t: Terrain, c: Cell): int = int(c.y) * t.width + int(c.x)

proc cellType*(t: Terrain, c: Cell): CellType =
  ## The type of `c` (outside cells are blocked).
  if not t.isInside(c): cellBlocked else: t.cells[t.index(c)]

proc isBlocked*(t: Terrain, c: Cell): bool = t.cellType(c) == cellBlocked

proc interactableAt*(t: Terrain, c: Cell): int =
  ## The index of the interactable at `c`, or -1.
  for i, interactable in t.interactables:
    if interactable.location == c: return i
  -1

proc interactable*(t: Terrain, i: int): Interactable = t.interactables[i]

proc setCellType(t: Terrain, c: Cell, kind: CellType) =
  if t.isInside(c): t.cells[t.index(c)] = kind

proc buildRandomTerrain(t: Terrain, seed: uint32) =
  var generator = initMt19937(seed)
  for i in 0 ..< t.cells.len:
    t.cells[i] = if generator.bernoulli(0.22): cellBlocked else: cellWalkable
  # Keep the border traversable: a guaranteed corridor around the map.
  for x in 0 ..< t.width:
    t.setCellType(Cell(x: int32(x), y: 0), cellWalkable)
    t.setCellType(Cell(x: int32(x), y: int32(t.height - 1)), cellWalkable)
  for y in 0 ..< t.height:
    t.setCellType(Cell(x: 0, y: int32(y)), cellWalkable)
    t.setCellType(Cell(x: int32(t.width - 1), y: int32(y)), cellWalkable)

proc generateInteractables(t: Terrain, seed: uint32, count: int) =
  const definitions = [
    (viewpoint, "viewpoint", "look_at_view", 2.5'f32),
    (bench, "bench", "sit_on_bench", 4.0'f32),
    (shopWindow, "shop_window", "check_shop_window", 3.0'f32)]
  t.interactables = @[]
  if count <= 0: return
  var available: seq[Cell]
  for y in 0 ..< t.height:
    for x in 0 ..< t.width:
      let candidate = Cell(x: int32(x), y: int32(y))
      if t.cellType(candidate) == cellWalkable: available.add candidate
  var generator = initMt19937(seed)
  shuffle(available, generator)
  var counters: array[3, int]
  for i in 0 ..< min(count, available.len):
    let index = int(generator.uniformBelow(uint64(definitions.len)))
    let (kind, prefix, animation, usage) = definitions[index]
    let typeIndex = counters[index]
    inc counters[index]
    t.interactables.add Interactable(id: prefix & "_" & $typeIndex, kind: kind, location: available[i],
      contextAnimation: animation, usageTimeSeconds: usage)
    t.setCellType(available[i], cellInteractable)

proc newTerrain*(width = DefaultWidth, height = DefaultHeight, seed = DefaultSeed,
    interactableCount = DefaultInteractableCount): Terrain =
  ## A random terrain like DemoGridTerrain's constructor.
  result = Terrain(width: max(1, width), height: max(1, height))
  result.cells = newSeq[CellType](result.width * result.height)
  result.buildRandomTerrain(seed)
  result.generateInteractables(seed xor 0x6C8E9CF5'u32, interactableCount)

proc buildWaypoints(w: Wanderer) =
  let t = w.terrain
  let width = t.width
  let height = t.height
  proc index(c: Cell): int = int(c.y) * width + int(c.x)
  # Only use cells connected to the first traversable cell, so every
  # consecutive waypoint can be reached.
  var start = Cell(x: 0, y: 0)
  if t.isBlocked(start):
    block search:
      for y in 0 ..< height:
        for x in 0 ..< width:
          if not t.isBlocked(Cell(x: int32(x), y: int32(y))):
            start = Cell(x: int32(x), y: int32(y))
            break search
  var visited = newSeq[bool](width * height)
  var reachable: seq[Cell]
  var pending = @[start]
  var head = 0
  visited[index(start)] = true
  const directions = [Cell(x: 1, y: 0), Cell(x: -1, y: 0), Cell(x: 0, y: 1), Cell(x: 0, y: -1)]
  while head < pending.len:
    let current = pending[head]
    inc head
    reachable.add current
    for direction in directions:
      let next = Cell(x: current.x + direction.x, y: current.y + direction.y)
      if not t.isInside(next) or t.isBlocked(next) or visited[index(next)]: continue
      visited[index(next)] = true
      pending.add next
  let seed = 0x91E10DA5'u32 xor (uint32(width) * 73856093'u32) xor (uint32(height) * 19349663'u32) xor
    uint32((uint64(reachable.len) * 83492791'u64) and 0xffffffff'u64)
  var generator = initMt19937(seed)
  shuffle(reachable, generator)
  var chosen: seq[Cell]
  proc addUnique(c: Cell) =
    if c notin chosen: chosen.add c
  var reachableInteractables: seq[Cell]
  for interactable in t.interactables:
    if t.isInside(interactable.location) and visited[index(interactable.location)]:
      reachableInteractables.add interactable.location
  shuffle(reachableInteractables, generator)
  for i in 0 ..< min(3, reachableInteractables.len): addUnique(reachableInteractables[i])
  for candidate in reachable:
    if chosen.len >= WaypointCount: break
    addUnique(candidate)
  if chosen.len == 0: chosen.add start
  for i in 0 ..< WaypointCount: w.waypoints[i] = chosen[i mod chosen.len]

proc newWanderer*(terrain: Terrain): Wanderer =
  ## A wanderer and its patrol loop on `terrain`.
  result = Wanderer(terrain: terrain, speed: 5.5, destinationIndex: 1)
  result.buildWaypoints()

proc reset*(w: Wanderer, index: int) =
  ## Places the wanderer at waypoint `index`, heading to the next one.
  let current = index mod WaypointCount
  w.location = w.waypoints[current]
  w.destinationIndex = (current + 1) mod WaypointCount
  w.destination = w.waypoints[w.destinationIndex]
  w.state = walking
  w.journeys = 0

proc selectNextDestination(w: Wanderer) =
  w.destinationIndex = (w.destinationIndex + 1) mod WaypointCount
  w.destination = w.waypoints[w.destinationIndex]
  w.state = walking

proc arrive(w: Wanderer) =
  inc w.journeys
  if w.terrain.interactableAt(w.location) >= 0:
    w.state = contextual
    return
  w.selectNextDestination()

proc notifyWalkSegmentCompleted*(w: Wanderer, location: Cell) =
  if w.state != walking: return
  w.location = location
  if w.location == w.destination: w.arrive()

proc notifyContextualAnimationCompleted*(w: Wanderer) =
  if w.state == contextual: w.selectNextDestination()

proc state*(w: Wanderer): WandererState = w.state
proc stateName*(w: Wanderer): string =
  if w.state == walking: "Walking" else: "Contextual animation"
proc location*(w: Wanderer): Cell = w.location
proc destination*(w: Wanderer): Cell = w.destination
proc speed*(w: Wanderer): float32 = w.speed
proc journeys*(w: Wanderer): uint64 = w.journeys
proc waypoint*(w: Wanderer, i: int): Cell = w.waypoints[i mod WaypointCount]
