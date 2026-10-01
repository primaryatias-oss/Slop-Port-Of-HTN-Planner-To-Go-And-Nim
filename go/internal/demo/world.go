// Package demo ports HTNDemo for terminals: the Domain Runner over the demo
// domains and the NPC simulation of Wanderer agents on a shared grid terrain
// (DemoGridTerrain, DemoWanderer, AIHTNDemoPathfinder, AIHTNDemoWandererAgent).
// The world generation reproduces the original's std::mt19937-based sequences,
// so runs match the original demo step for step.
package demo

import (
	"strconv"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/callterm"
)

// Cell is a grid coordinate, marshalled to HTN as the list (X Y).
type Cell struct{ X, Y int32 }

// String formats a cell like the demo's (X Y).
func (c Cell) String() string {
	return "(" + strconv.Itoa(int(c.X)) + " " + strconv.Itoa(int(c.Y)) + ")"
}

// cellAtom converts a cell into its HTN list.
func cellAtom(c Cell) atom.Atom { return atom.NewList(atom.NewInt(c.X), atom.NewInt(c.Y)) }

// parseCell converts an HTN list of two integers into a cell.
func parseCell(a atom.Atom) (Cell, bool) {
	if !a.Is(atom.KindList) || a.Len() != 2 {
		return Cell{}, false
	}
	x, _ := a.At(0)
	y, _ := a.At(1)
	if !x.Is(atom.KindInt) || !y.Is(atom.KindInt) {
		return Cell{}, false
	}
	return Cell{x.Int(), y.Int()}, true
}

func init() {
	list := atom.KindList
	callterm.RegisterConverter[Cell]("Cell", &list,
		func(_ any, a atom.Atom) (Cell, bool) { return parseCell(a) },
		func(_ any, c Cell) (atom.Atom, bool) { return cellAtom(c), true })
}

// CellType classifies a terrain cell.
type CellType uint8

// Terrain cell types.
const (
	CellWalkable CellType = iota
	CellBlocked
	CellInteractable
)

// InteractableType is the kind of an interactable object.
type InteractableType uint8

// Interactable types.
const (
	Viewpoint InteractableType = iota
	Bench
	ShopWindow
)

// Name returns the display name of an interactable type.
func (t InteractableType) Name() string {
	switch t {
	case Viewpoint:
		return "Viewpoint"
	case Bench:
		return "Bench"
	case ShopWindow:
		return "Shop window"
	}
	return "Interactable"
}

// Interactable is an object NPCs play a contextual animation at.
type Interactable struct {
	ID               string
	Type             InteractableType
	Location         Cell
	ContextAnimation string
	UsageTimeSeconds float32
}

// Default terrain parameters (DemoGridTerrain).
const (
	DefaultWidth             = 64
	DefaultHeight            = 64
	DefaultSeed              = uint32(0xA57A1234)
	DefaultInteractableCount = 12
)

// Terrain is the shared world grid with its interactables.
type Terrain struct {
	width, height int
	cells         []CellType
	interactables []Interactable
}

// NewTerrain builds a random terrain like DemoGridTerrain's constructor.
func NewTerrain(width, height int, seed uint32, interactableCount int) *Terrain {
	t := &Terrain{width: max(1, width), height: max(1, height)}
	t.cells = make([]CellType, t.width*t.height)
	t.buildRandomTerrain(seed)
	t.generateInteractables(seed^0x6C8E9CF5, interactableCount)
	return t
}

// NewDefaultTerrain builds the demo's 64x64 terrain.
func NewDefaultTerrain() *Terrain {
	return NewTerrain(DefaultWidth, DefaultHeight, DefaultSeed, DefaultInteractableCount)
}

// Width returns the terrain width in cells.
func (t *Terrain) Width() int { return t.width }

// Height returns the terrain height in cells.
func (t *Terrain) Height() int { return t.height }

// IsInside reports whether c lies on the grid.
func (t *Terrain) IsInside(c Cell) bool {
	return c.X >= 0 && int(c.X) < t.width && c.Y >= 0 && int(c.Y) < t.height
}

// CellType returns the type of c (outside cells are blocked).
func (t *Terrain) CellType(c Cell) CellType {
	if !t.IsInside(c) {
		return CellBlocked
	}
	return t.cells[t.index(c)]
}

// IsBlocked reports whether c cannot be walked.
func (t *Terrain) IsBlocked(c Cell) bool { return t.CellType(c) == CellBlocked }

// Interactables returns every interactable.
func (t *Terrain) Interactables() []Interactable { return t.interactables }

// InteractableAt returns the interactable at c or nil.
func (t *Terrain) InteractableAt(c Cell) *Interactable {
	for i := range t.interactables {
		if t.interactables[i].Location == c {
			return &t.interactables[i]
		}
	}
	return nil
}

func (t *Terrain) index(c Cell) int { return int(c.Y)*t.width + int(c.X) }

func (t *Terrain) setCellType(c Cell, cellType CellType) {
	if t.IsInside(c) {
		t.cells[t.index(c)] = cellType
	}
}

func (t *Terrain) buildRandomTerrain(seed uint32) {
	generator := newMT19937(seed)
	for i := range t.cells {
		if generator.bernoulli(0.22) {
			t.cells[i] = CellBlocked
		} else {
			t.cells[i] = CellWalkable
		}
	}
	// Keep the border traversable: a guaranteed corridor around the map.
	for x := 0; x < t.width; x++ {
		t.setCellType(Cell{int32(x), 0}, CellWalkable)
		t.setCellType(Cell{int32(x), int32(t.height - 1)}, CellWalkable)
	}
	for y := 0; y < t.height; y++ {
		t.setCellType(Cell{0, int32(y)}, CellWalkable)
		t.setCellType(Cell{int32(t.width - 1), int32(y)}, CellWalkable)
	}
}

func (t *Terrain) generateInteractables(seed uint32, count int) {
	definitions := []struct {
		kind      InteractableType
		prefix    string
		animation string
		usage     float32
	}{
		{Viewpoint, "viewpoint", "look_at_view", 2.5},
		{Bench, "bench", "sit_on_bench", 4.0},
		{ShopWindow, "shop_window", "check_shop_window", 3.0},
	}
	t.interactables = nil
	if count <= 0 {
		return
	}
	var available []Cell
	for y := 0; y < t.height; y++ {
		for x := 0; x < t.width; x++ {
			candidate := Cell{int32(x), int32(y)}
			if t.CellType(candidate) == CellWalkable {
				available = append(available, candidate)
			}
		}
	}
	generator := newMT19937(seed)
	shuffle(available, generator)
	count = min(count, len(available))
	counters := make([]int, len(definitions))
	for i := 0; i < count; i++ {
		index := generator.uniformBelow(uint64(len(definitions)))
		definition := definitions[index]
		typeIndex := counters[index]
		counters[index]++
		t.interactables = append(t.interactables, Interactable{
			ID:               definition.prefix + "_" + strconv.Itoa(typeIndex),
			Type:             definition.kind,
			Location:         available[i],
			ContextAnimation: definition.animation,
			UsageTimeSeconds: definition.usage,
		})
		t.setCellType(available[i], CellInteractable)
	}
}

// WandererState is the gameplay state of a wanderer.
type WandererState uint8

// Wanderer states.
const (
	Walking WandererState = iota
	Contextual
)

// WaypointCount is the length of every wanderer's patrol loop.
const WaypointCount = 12

// Wanderer is the persistent gameplay state of one civilian (DemoWanderer).
type Wanderer struct {
	terrain          *Terrain
	waypoints        [WaypointCount]Cell
	state            WandererState
	location         Cell
	destination      Cell
	speed            float32
	destinationIndex int
	journeys         uint64
}

// NewWanderer creates a wanderer and its patrol loop on terrain.
func NewWanderer(terrain *Terrain) *Wanderer {
	w := &Wanderer{terrain: terrain, speed: 5.5, destinationIndex: 1}
	w.buildWaypoints()
	return w
}

func (w *Wanderer) buildWaypoints() {
	t := w.terrain
	width, height := t.width, t.height
	index := func(c Cell) int { return int(c.Y)*width + int(c.X) }
	// Only use cells connected to the first traversable cell, so every
	// consecutive waypoint can be reached.
	start := Cell{0, 0}
	if t.IsBlocked(start) {
	search:
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				if !t.IsBlocked(Cell{int32(x), int32(y)}) {
					start = Cell{int32(x), int32(y)}
					break search
				}
			}
		}
	}
	visited := make([]bool, width*height)
	var reachable []Cell
	pending := []Cell{start}
	visited[index(start)] = true
	directions := []Cell{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		reachable = append(reachable, current)
		for _, direction := range directions {
			next := Cell{current.X + direction.X, current.Y + direction.Y}
			if !t.IsInside(next) || t.IsBlocked(next) || visited[index(next)] {
				continue
			}
			visited[index(next)] = true
			pending = append(pending, next)
		}
	}
	seed := uint32(0x91E10DA5) ^ uint32(width)*73856093 ^ uint32(height)*19349663 ^
		uint32(uint64(len(reachable))*83492791)
	generator := newMT19937(seed)
	shuffle(reachable, generator)
	var chosen []Cell
	addUnique := func(c Cell) {
		for _, existing := range chosen {
			if existing == c {
				return
			}
		}
		chosen = append(chosen, c)
	}
	var reachableInteractables []Cell
	for _, interactable := range t.interactables {
		if t.IsInside(interactable.Location) && visited[index(interactable.Location)] {
			reachableInteractables = append(reachableInteractables, interactable.Location)
		}
	}
	shuffle(reachableInteractables, generator)
	for i := 0; i < min(3, len(reachableInteractables)); i++ {
		addUnique(reachableInteractables[i])
	}
	for _, candidate := range reachable {
		if len(chosen) >= WaypointCount {
			break
		}
		addUnique(candidate)
	}
	if len(chosen) == 0 {
		chosen = append(chosen, start)
	}
	for i := range w.waypoints {
		w.waypoints[i] = chosen[i%len(chosen)]
	}
}

// Reset places the wanderer at waypoint index, heading to the next one.
func (w *Wanderer) Reset(index int) {
	current := index % WaypointCount
	w.location = w.waypoints[current]
	w.destinationIndex = (current + 1) % WaypointCount
	w.destination = w.waypoints[w.destinationIndex]
	w.state = Walking
	w.journeys = 0
}

// NotifyWalkSegmentCompleted moves the wanderer to location.
func (w *Wanderer) NotifyWalkSegmentCompleted(location Cell) {
	if w.state != Walking {
		return
	}
	w.location = location
	if w.location == w.destination {
		w.arrive()
	}
}

// NotifyContextualAnimationCompleted resumes walking after an animation.
func (w *Wanderer) NotifyContextualAnimationCompleted() {
	if w.state == Contextual {
		w.selectNextDestination()
	}
}

func (w *Wanderer) arrive() {
	w.journeys++
	if w.terrain.InteractableAt(w.location) != nil {
		w.state = Contextual
		return
	}
	w.selectNextDestination()
}

func (w *Wanderer) selectNextDestination() {
	w.destinationIndex = (w.destinationIndex + 1) % WaypointCount
	w.destination = w.waypoints[w.destinationIndex]
	w.state = Walking
}

// State returns the gameplay state.
func (w *Wanderer) State() WandererState { return w.state }

// StateName returns the display name of the state.
func (w *Wanderer) StateName() string {
	if w.state == Walking {
		return "Walking"
	}
	return "Contextual animation"
}

// Location returns the current cell.
func (w *Wanderer) Location() Cell { return w.location }

// Destination returns the current destination.
func (w *Wanderer) Destination() Cell { return w.destination }

// Speed returns the walking speed in cells per second.
func (w *Wanderer) Speed() float32 { return w.speed }

// Journeys returns the number of reached destinations.
func (w *Wanderer) Journeys() uint64 { return w.journeys }

// Waypoint returns the location of waypoint i.
func (w *Wanderer) Waypoint(i int) Cell { return w.waypoints[i%WaypointCount] }
