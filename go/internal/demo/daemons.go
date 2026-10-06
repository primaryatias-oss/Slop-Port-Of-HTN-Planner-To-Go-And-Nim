package demo

import (
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/integration"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/worldstate"
)

var (
	symPathfindState         = atom.Intern("pathfind_state")
	symPathfinder            = atom.Intern("pathfinder")
	symPathfindingSegment    = atom.Intern("pathfinding_segment")
	symInProgress            = atom.Intern("in_progress")
	symSucceeded             = atom.Intern("succeeded")
	symFailed                = atom.Intern("failed")
	symWandererState         = atom.Intern("wanderer_state")
	symWandererLocation      = atom.Intern("wanderer_location")
	symWandererDestination   = atom.Intern("wanderer_destination")
	symWalking               = atom.Intern("walking")
	symContextual            = atom.Intern("contextual")
	symContextualAnimationAt = atom.Intern("contextual_animation_at")
)

type requestStatus uint8

const (
	requestInProgress requestStatus = iota
	requestSucceeded
	requestFailed
)

type pathRequest struct {
	id             int32
	from, to       Cell
	status         requestStatus
	ticksRemaining int
	path           []Cell
}

// Pathfinder is the asynchronous single-request pathfinding daemon
// (AIHTNDemoPathfinder). A request completes after one tick per cell of
// Manhattan distance and publishes string-pulled A* segments.
type Pathfinder struct {
	integration.DaemonBase
	terrain       *Terrain
	request       *pathRequest
	nextRequestID int32
}

// PathfinderDaemonID is the daemon type of the pathfinder's member callterms.
const PathfinderDaemonID = "AIHTNDemoPathfinder"

// NewPathfinder creates a pathfinder over terrain.
func NewPathfinder(terrain *Terrain) *Pathfinder {
	return &Pathfinder{terrain: terrain, nextRequestID: 1}
}

// OnWriteWorldState rebuilds the pathfinding facts from the request state.
func (p *Pathfinder) OnWriteWorldState(world *worldstate.WorldState) {
	world.ClearFact(symPathfindState, 4)
	world.ClearFact(symPathfinder, 4)
	world.ClearFact(symPathfindingSegment, 3)
	if p.request != nil {
		p.writeRequestFacts(world, p.request)
	}
}

// Update advances an in-progress request by one tick.
func (p *Pathfinder) Update(float32) {
	if p.request == nil || p.request.status != requestInProgress {
		return
	}
	p.request.ticksRemaining--
	if p.request.ticksRemaining > 0 {
		return
	}
	if path, ok := p.findPath(p.request.from, p.request.to); ok {
		p.request.status = requestSucceeded
		p.request.path = path
	} else {
		p.request.status = requestFailed
		p.request.path = nil
	}
}

// RemainingPath returns the rest of the resolved path from current to
// destination (for display).
func (p *Pathfinder) RemainingPath(current, destination Cell) ([]Cell, bool) {
	r := p.request
	if r == nil || r.status != requestSucceeded || r.to != destination {
		return nil, false
	}
	path := []Cell{current}
	if current == destination {
		return path, true
	}
	if current == r.from {
		return append(path, r.path...), true
	}
	for i, cell := range r.path {
		if cell == current {
			return append(path, r.path[i+1:]...), true
		}
	}
	return nil, false
}

// requestPath is the request_path_from_to callterm.
func (p *Pathfinder) requestPath(from, to Cell) int32 {
	if p.World == nil || !p.terrain.IsInside(from) || !p.terrain.IsInside(to) {
		return 0
	}
	world := p.World
	// Re-requesting the running query must not restart its countdown.
	if p.request != nil && p.request.from == from && p.request.to == to {
		p.writeRequestFacts(world, p.request)
		return p.request.id
	}
	request := &pathRequest{id: p.nextRequestID, from: from, to: to,
		ticksRemaining: abs(int(to.X-from.X)) + abs(int(to.Y-from.Y))}
	p.nextRequestID++
	if request.ticksRemaining == 0 {
		if path, ok := p.findPath(from, to); ok {
			request.status = requestSucceeded
			request.path = path
		} else {
			request.status = requestFailed
		}
	}
	p.request = request
	world.ClearFact(symPathfindState, 4)
	world.ClearFact(symPathfinder, 4)
	world.ClearFact(symPathfindingSegment, 3)
	// Publish immediately: the callterm runs during a decomposition.
	p.writeRequestFacts(world, request)
	return request.id
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

type openNode struct{ index, f int }

func (p *Pathfinder) findPath(start, goal Cell) ([]Cell, bool) {
	t := p.terrain
	width, height := t.width, t.height
	toIndex := func(x, y int) int { return y*width + x }
	startIndex := toIndex(int(start.X), int(start.Y))
	goalIndex := toIndex(int(goal.X), int(goal.Y))
	if t.IsBlocked(start) || t.IsBlocked(goal) {
		return nil, false
	}
	if startIndex == goalIndex {
		return nil, true
	}
	const infinity = int(^uint32(0) >> 1)
	gScore := make([]int, width*height)
	parent := make([]int, width*height)
	for i := range gScore {
		gScore[i] = infinity
		parent[i] = -1
	}
	heuristic := func(x, y int) int { return abs(x-int(goal.X)) + abs(y-int(goal.Y)) }
	open := minHeap[openNode]{greater: func(a, b openNode) bool { return a.f > b.f }}
	gScore[startIndex] = 0
	open.push(openNode{startIndex, heuristic(int(start.X), int(start.Y))})
	directions := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for !open.empty() {
		current := open.top()
		open.pop()
		if current.index == goalIndex {
			break
		}
		x, y := current.index%width, current.index/width
		for _, direction := range directions {
			nextX, nextY := x+direction[0], y+direction[1]
			if nextX < 0 || nextX >= width || nextY < 0 || nextY >= height {
				continue
			}
			next := toIndex(nextX, nextY)
			if t.IsBlocked(Cell{int32(nextX), int32(nextY)}) {
				continue
			}
			tentative := gScore[current.index] + 1
			if tentative >= gScore[next] {
				continue
			}
			gScore[next] = tentative
			parent[next] = current.index
			open.push(openNode{next, tentative + heuristic(nextX, nextY)})
		}
	}
	if gScore[goalIndex] == infinity {
		return nil, false
	}
	var raw []Cell
	for index := goalIndex; index != -1; index = parent[index] {
		raw = append(raw, Cell{int32(index % width), int32(index / width)})
	}
	for i, j := 0, len(raw)-1; i < j; i, j = i+1, j-1 {
		raw[i], raw[j] = raw[j], raw[i]
	}
	return p.stringPull(raw), true
}

// hasLineOfSight walks the supercover line between cell centres; passing
// exactly through a corner requires both side cells to be free.
func (p *Pathfinder) hasLineOfSight(from, to Cell) bool {
	t := p.terrain
	if !t.IsInside(from) || !t.IsInside(to) || t.IsBlocked(from) || t.IsBlocked(to) {
		return false
	}
	x, y := int(from.X), int(from.Y)
	deltaX, deltaY := abs(int(to.X-from.X)), abs(int(to.Y-from.Y))
	sign := func(v int32) int {
		switch {
		case v > 0:
			return 1
		case v < 0:
			return -1
		}
		return 0
	}
	stepX, stepY := sign(to.X-from.X), sign(to.Y-from.Y)
	crossedX, crossedY := 0, 0
	for crossedX < deltaX || crossedY < deltaY {
		decision := (1+2*crossedX)*deltaY - (1+2*crossedY)*deltaX
		switch {
		case decision == 0:
			if (stepX != 0 && t.IsBlocked(Cell{int32(x + stepX), int32(y)})) ||
				(stepY != 0 && t.IsBlocked(Cell{int32(x), int32(y + stepY)})) {
				return false
			}
			x += stepX
			y += stepY
			crossedX++
			crossedY++
		case decision < 0:
			x += stepX
			crossedX++
		default:
			y += stepY
			crossedY++
		}
		crossed := Cell{int32(x), int32(y)}
		if !t.IsInside(crossed) || t.IsBlocked(crossed) {
			return false
		}
	}
	return true
}

// stringPull keeps only the furthest visible node from each anchor.
func (p *Pathfinder) stringPull(raw []Cell) []Cell {
	var path []Cell
	if len(raw) <= 1 {
		return path
	}
	anchor := 0
	for anchor+1 < len(raw) {
		furthest := anchor + 1
		for candidate := len(raw) - 1; candidate > anchor+1; candidate-- {
			if p.hasLineOfSight(raw[anchor], raw[candidate]) {
				furthest = candidate
				break
			}
		}
		path = append(path, raw[furthest])
		anchor = furthest
	}
	return path
}

func (p *Pathfinder) writeRequestFacts(world *worldstate.WorldState, r *pathRequest) {
	from, to := cellAtom(r.from), cellAtom(r.to)
	state := symInProgress
	switch r.status {
	case requestSucceeded:
		state = symSucceeded
	case requestFailed:
		state = symFailed
	}
	world.WriteFact(symPathfindState, atom.NewSymbol(state), from, to, atom.NewInt(r.id))
	if r.status != requestSucceeded {
		return
	}
	world.WriteFact(symPathfinder, atom.NewInt(r.id), atom.NewInt(int32(len(r.path))), from, to)
	for i, cell := range r.path {
		world.WriteFact(symPathfindingSegment, atom.NewInt(r.id), atom.NewInt(int32(i)), cellAtom(cell))
	}
}

// WandererDaemon publishes one wanderer's gameplay state (AIHTNDemoWanderer).
type WandererDaemon struct {
	integration.DaemonBase
	wanderer *Wanderer
}

// Update does nothing: the wanderer advances through task completion.
func (d *WandererDaemon) Update(float32) {}

// OnWriteWorldState writes wanderer_state, wanderer_location and (while
// walking) wanderer_destination.
func (d *WandererDaemon) OnWriteWorldState(world *worldstate.WorldState) {
	world.ClearFact(symWandererState, 1)
	world.ClearFact(symWandererLocation, 1)
	world.ClearFact(symWandererDestination, 1)
	state := symContextual
	if d.wanderer.State() == Walking {
		state = symWalking
	}
	world.WriteFact(symWandererState, atom.NewSymbol(state))
	world.WriteFact(symWandererLocation, cellAtom(d.wanderer.Location()))
	if d.wanderer.State() == Walking {
		world.WriteFact(symWandererDestination, cellAtom(d.wanderer.Destination()))
	}
}

// TerrainDaemon publishes the terrain's contextual animations
// (AIHTNDemoGridTerrainDaemon).
type TerrainDaemon struct {
	integration.DaemonBase
	terrain *Terrain
}

// Update does nothing: the terrain is static.
func (d *TerrainDaemon) Update(float32) {}

// OnWriteWorldState writes contextual_animation_at for every interactable.
func (d *TerrainDaemon) OnWriteWorldState(world *worldstate.WorldState) {
	world.ClearFact(symContextualAnimationAt, 2)
	for _, interactable := range d.terrain.Interactables() {
		world.WriteFact(symContextualAnimationAt, cellAtom(interactable.Location),
			atom.NewSymbol(atom.Intern(interactable.ContextAnimation)))
	}
}

// DaemonDemoTest is the demo daemon behind the add_target_available member
// callterm (AIHtnDaemonDemoTest). The Domain Runner binds the callterm but no
// instance, so calls report a missing daemon instance like the original.
type DaemonDemoTest struct {
	integration.DaemonBase
}

// DaemonDemoTestID is the daemon type of add_target_available.
const DaemonDemoTestID = "AIHtnDaemonDemoTest"

func (d *DaemonDemoTest) addTargetAvailable() bool {
	if d.World == nil {
		return false
	}
	d.World.AddFact("target_available", atom.NewString("enemy0"))
	return true
}
