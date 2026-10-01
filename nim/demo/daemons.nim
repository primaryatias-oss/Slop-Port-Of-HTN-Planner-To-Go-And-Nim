## The demo daemons: asynchronous pathfinding (AIHTNDemoPathfinder), the
## wanderer and terrain publishers (AIHTNDemoWanderer,
## AIHTNDemoGridTerrainDaemon) and the add_target_available demo daemon
## (AIHtnDaemonDemoTest).

import std/algorithm
import htn/[atom, integration, worldstate]
import stdrand, world

let
  symPathfindState = intern("pathfind_state")
  symPathfinder = intern("pathfinder")
  symPathfindingSegment = intern("pathfinding_segment")
  symInProgress = intern("in_progress")
  symSucceeded = intern("succeeded")
  symFailed = intern("failed")
  symWandererState = intern("wanderer_state")
  symWandererLocation = intern("wanderer_location")
  symWandererDestination = intern("wanderer_destination")
  symWalking = intern("walking")
  symContextual = intern("contextual")
  symContextualAnimationAt = intern("contextual_animation_at")

const
  PathfinderDaemonID* = "AIHTNDemoPathfinder"
    ## Daemon type of the pathfinder's member callterms.
  DaemonDemoTestID* = "AIHtnDaemonDemoTest"
    ## Daemon type of add_target_available.

type
  RequestStatus = enum
    requestInProgress, requestSucceeded, requestFailed

  PathRequest = ref object
    id: int32
    source, target: Cell
    status: RequestStatus
    ticksRemaining: int
    path: seq[Cell]

  Pathfinder* = ref object of Daemon
    ## Single-request asynchronous pathfinding: a request completes after one
    ## tick per cell of Manhattan distance and publishes string-pulled A*
    ## segments.
    terrain: Terrain
    request: PathRequest
    nextRequestID: int32

  WandererDaemon* = ref object of Daemon
    ## Publishes one wanderer's gameplay state.
    wanderer*: Wanderer

  TerrainDaemon* = ref object of Daemon
    ## Publishes the terrain's contextual animations.
    terrain*: Terrain

  DaemonDemoTest* = ref object of Daemon
    ## The daemon behind add_target_available. The Domain Runner binds the
    ## callterm but no instance, so calls report a missing daemon instance.

  OpenNode = object
    index, f: int

proc newPathfinder*(terrain: Terrain): Pathfinder = Pathfinder(terrain: terrain, nextRequestID: 1)

proc writeRequestFacts(p: Pathfinder, w: WorldState, r: PathRequest) =
  let source = cellAtom(r.source)
  let target = cellAtom(r.target)
  let state = case r.status
    of requestInProgress: symInProgress
    of requestSucceeded: symSucceeded
    of requestFailed: symFailed
  discard w.writeFact(symPathfindState, newSymbol(state), source, target, newInt(r.id))
  if r.status != requestSucceeded: return
  discard w.writeFact(symPathfinder, newInt(r.id), newInt(int32(r.path.len)), source, target)
  for i, cell in r.path:
    discard w.writeFact(symPathfindingSegment, newInt(r.id), newInt(int32(i)), cellAtom(cell))

method onWriteWorldState*(p: Pathfinder, w: WorldState) =
  ## Rebuilds the pathfinding facts from the request state.
  discard w.clearFact(symPathfindState, 4)
  discard w.clearFact(symPathfinder, 4)
  discard w.clearFact(symPathfindingSegment, 3)
  if p.request != nil: p.writeRequestFacts(w, p.request)

proc hasLineOfSight(p: Pathfinder, source, target: Cell): bool =
  ## Supercover traversal between cell centres; passing exactly through a
  ## corner requires both side cells to be free.
  let t = p.terrain
  if not t.isInside(source) or not t.isInside(target) or t.isBlocked(source) or t.isBlocked(target):
    return false
  var x = int(source.x)
  var y = int(source.y)
  let deltaX = abs(int(target.x - source.x))
  let deltaY = abs(int(target.y - source.y))
  let stepX = cmp(target.x, source.x)
  let stepY = cmp(target.y, source.y)
  var crossedX = 0
  var crossedY = 0
  while crossedX < deltaX or crossedY < deltaY:
    let decision = (1 + 2 * crossedX) * deltaY - (1 + 2 * crossedY) * deltaX
    if decision == 0:
      if (stepX != 0 and t.isBlocked(Cell(x: int32(x + stepX), y: int32(y)))) or
          (stepY != 0 and t.isBlocked(Cell(x: int32(x), y: int32(y + stepY)))):
        return false
      x += stepX
      y += stepY
      inc crossedX
      inc crossedY
    elif decision < 0:
      x += stepX
      inc crossedX
    else:
      y += stepY
      inc crossedY
    let crossed = Cell(x: int32(x), y: int32(y))
    if not t.isInside(crossed) or t.isBlocked(crossed): return false
  true

proc stringPull(p: Pathfinder, raw: seq[Cell]): seq[Cell] =
  ## Keeps only the furthest visible node from each anchor.
  if raw.len <= 1: return @[]
  var anchor = 0
  while anchor + 1 < raw.len:
    var furthest = anchor + 1
    var candidate = raw.len - 1
    while candidate > anchor + 1:
      if p.hasLineOfSight(raw[anchor], raw[candidate]):
        furthest = candidate
        break
      dec candidate
    result.add raw[furthest]
    anchor = furthest

proc greaterNode(a, b: OpenNode): bool = a.f > b.f

proc findPath(p: Pathfinder, start, goal: Cell, path: var seq[Cell]): bool =
  let t = p.terrain
  let width = t.width
  let height = t.height
  let startIndex = int(start.y) * width + int(start.x)
  let goalIndex = int(goal.y) * width + int(goal.x)
  if t.isBlocked(start) or t.isBlocked(goal): return false
  if startIndex == goalIndex:
    path = @[]
    return true
  const infinity = int(high(int32))
  var gScore = newSeq[int](width * height)
  var parent = newSeq[int](width * height)
  for i in 0 ..< gScore.len:
    gScore[i] = infinity
    parent[i] = -1
  proc heuristic(x, y: int): int = abs(x - int(goal.x)) + abs(y - int(goal.y))
  var open = initMinHeap[OpenNode](greaterNode)
  gScore[startIndex] = 0
  open.push OpenNode(index: startIndex, f: heuristic(int(start.x), int(start.y)))
  const directions = [(1, 0), (-1, 0), (0, 1), (0, -1)]
  while not open.empty:
    let current = open.top
    open.pop()
    if current.index == goalIndex: break
    let x = current.index mod width
    let y = current.index div width
    for (dx, dy) in directions:
      let nextX = x + dx
      let nextY = y + dy
      if nextX < 0 or nextX >= width or nextY < 0 or nextY >= height: continue
      let next = nextY * width + nextX
      if t.isBlocked(Cell(x: int32(nextX), y: int32(nextY))): continue
      let tentative = gScore[current.index] + 1
      if tentative >= gScore[next]: continue
      gScore[next] = tentative
      parent[next] = current.index
      open.push OpenNode(index: next, f: tentative + heuristic(nextX, nextY))
  if gScore[goalIndex] == infinity: return false
  var raw: seq[Cell]
  var index = goalIndex
  while index != -1:
    raw.add Cell(x: int32(index mod width), y: int32(index div width))
    index = parent[index]
  raw.reverse()
  path = p.stringPull(raw)
  true

method update*(p: Pathfinder, deltaTime: float32) =
  ## Advances an in-progress request by one tick.
  if p.request == nil or p.request.status != requestInProgress: return
  dec p.request.ticksRemaining
  if p.request.ticksRemaining > 0: return
  var path: seq[Cell]
  if p.findPath(p.request.source, p.request.target, path):
    p.request.status = requestSucceeded
    p.request.path = path
  else:
    p.request.status = requestFailed
    p.request.path = @[]

proc requestPath*(p: Pathfinder, source, target: Cell): int32 =
  ## The request_path_from_to callterm.
  if p.world == nil or not p.terrain.isInside(source) or not p.terrain.isInside(target): return 0
  let w = p.world
  # Re-requesting the running query must not restart its countdown.
  if p.request != nil and p.request.source == source and p.request.target == target:
    p.writeRequestFacts(w, p.request)
    return p.request.id
  let request = PathRequest(id: p.nextRequestID, source: source, target: target,
    ticksRemaining: abs(int(target.x - source.x)) + abs(int(target.y - source.y)))
  inc p.nextRequestID
  if request.ticksRemaining == 0:
    var path: seq[Cell]
    if p.findPath(source, target, path):
      request.status = requestSucceeded
      request.path = path
    else:
      request.status = requestFailed
  p.request = request
  discard w.clearFact(symPathfindState, 4)
  discard w.clearFact(symPathfinder, 4)
  discard w.clearFact(symPathfindingSegment, 3)
  # Publish immediately: the callterm runs during a decomposition.
  p.writeRequestFacts(w, request)
  request.id

proc remainingPath*(p: Pathfinder, current, destination: Cell, path: var seq[Cell]): bool =
  ## The rest of the resolved path from `current` to `destination`.
  path = @[]
  let r = p.request
  if r == nil or r.status != requestSucceeded or r.target != destination: return false
  path.add current
  if current == destination: return true
  if current == r.source:
    path.add r.path
    return true
  let index = r.path.find(current)
  if index < 0:
    path = @[]
    return false
  path.add r.path[index + 1 .. ^1]
  true

method onWriteWorldState*(d: WandererDaemon, w: WorldState) =
  ## wanderer_state, wanderer_location and (while walking) wanderer_destination.
  discard w.clearFact(symWandererState, 1)
  discard w.clearFact(symWandererLocation, 1)
  discard w.clearFact(symWandererDestination, 1)
  let state = if d.wanderer.state == walking: symWalking else: symContextual
  discard w.writeFact(symWandererState, newSymbol(state))
  discard w.writeFact(symWandererLocation, cellAtom(d.wanderer.location))
  if d.wanderer.state == walking:
    discard w.writeFact(symWandererDestination, cellAtom(d.wanderer.destination))

method onWriteWorldState*(d: TerrainDaemon, w: WorldState) =
  ## contextual_animation_at for every interactable.
  discard w.clearFact(symContextualAnimationAt, 2)
  for interactable in d.terrain.interactables:
    discard w.writeFact(symContextualAnimationAt, cellAtom(interactable.location),
      newSymbol(intern(interactable.contextAnimation)))

proc addTargetAvailable*(d: DaemonDemoTest): bool =
  if d.world == nil: return false
  d.world.addFact("target_available", newString("enemy0"))
  true
