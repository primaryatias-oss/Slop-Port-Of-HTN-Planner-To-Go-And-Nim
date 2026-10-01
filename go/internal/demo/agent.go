package demo

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/callterm"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/integration"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/planner"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/worldstate"
)

var (
	taskWalkSegment                = atom.Intern("!walk_segment")
	taskPlayContextualAnimation    = atom.Intern("!play_contextual_animation")
	taskWaitForPathfindingQuery    = atom.Intern("!wait_for_pathfinding_query")
	taskWandererIdle               = atom.Intern("!wanderer_idle")
	taskSay                        = atom.Intern("!say")
	reasonTexts                    = map[callterm.ErrorReason]string{}
	maxImmediatePlanningPasses     = 4
	waitTaskDurationSeconds        = float32(0.15)
	idleTaskDurationSeconds        = float32(0.4)
	sayTaskDurationSeconds         = float32(0.8)
	maxHistoryEntries              = 160
	contextualAnimationTaskMinimum = float32(0.01)
)

func init() {
	reasonTexts[callterm.ReasonNotRegistered] = "not registered"
	reasonTexts[callterm.ReasonMissingBinding] = "no callable bound"
	reasonTexts[callterm.ReasonMissingInstance] = "daemon instance missing"
	reasonTexts[callterm.ReasonArgumentCountMismatch] = "argument count mismatch"
	reasonTexts[callterm.ReasonArgumentTypeMismatch] = "incompatible argument type"
	reasonTexts[callterm.ReasonArgumentConversionFailed] = "argument conversion failed"
	reasonTexts[callterm.ReasonReturnConversionFailed] = "return conversion failed"
}

// CallTermErrorReporter returns a callterm error callback that prints the
// demo's report (ReportGeneratedDemoCallTermError) to w.
func CallTermErrorReporter(w io.Writer) callterm.ErrorCallback {
	orDefault := func(text, fallback string) string {
		if text == "" {
			return fallback
		}
		return text
	}
	return func(_ any, info *callterm.ErrorInfo) {
		reason, ok := reasonTexts[info.Reason]
		if !ok {
			reason = "unknown reason"
		}
		fmt.Fprintf(w, "[Generated] Callterm '%s' failed: %s (daemon: %s, domain: %s, source: %s:%d:%d)\n",
			orDefault(info.Name, "<unknown>"), reason, orDefault(info.DaemonID, "-"),
			orDefault(info.Source.Domain, "<unknown>"), orDefault(info.Source.File, "<unavailable>"),
			info.Source.Line, info.Source.Column)
		if info.ArgumentIndex != callterm.NoIndex {
			fmt.Fprintf(w, "  argument %d: expected atom type %d (%s), received %d\n", info.ArgumentIndex+1,
				info.ExpectedAtomType, orDefault(info.ExpectedTypeName, "see atom type"), info.ActualAtomType)
		}
	}
}

// BindCallTerms registers the demo's callterms (BindCalls of the original
// demo): list operations, the Wanderer/pathfinder callterms, the demo daemon
// and the plain test callterms.
func BindCallTerms(r *callterm.Registry) {
	callterm.BindListCallTerms(r)
	// AIHTNDemoWandererAgent::BindCallTerms
	r.MustBindFunc("inc", func(value int32) int32 { return value + 1 })
	r.MustBindFunc("both_coordinates_even", func(c Cell) bool { return c.X%2 == 0 && c.Y%2 == 0 })
	r.MustBindFunc("both_coordinates_odd", func(c Cell) bool { return c.X%2 != 0 && c.Y%2 != 0 })
	if err := r.BindMemberFunc("request_path_from_to", PathfinderDaemonID,
		func(p *Pathfinder, from, to Cell) int32 { return p.requestPath(from, to) }); err != nil {
		panic(err)
	}
	r.MustBindFunc("same_location", func(from, to Cell) bool { return from == to })
	// AIHtnDaemonDemoTest::BindCallTerms
	if err := r.BindMemberFunc("add_target_available", DaemonDemoTestID,
		func(d *DaemonDemoTest) bool { return d.addTargetAvailable() }); err != nil {
		panic(err)
	}
	intArgument := func(a *callterm.Arguments, i int) (int32, bool) {
		return a.At(i).Int(), a.At(i).Is(atom.KindInt)
	}
	r.Bind("binded_function_with_args", func(a *callterm.Arguments) atom.Atom { return atom.NewBool(a.Len() == 1) })
	r.Bind("get_health", func(*callterm.Arguments) atom.Atom { return atom.NewInt(50) })
	r.Bind("get_max_speed", func(*callterm.Arguments) atom.Atom { return atom.NewFloat(1) })
	r.Bind("lt", func(a *callterm.Arguments) atom.Atom {
		if a.Len() != 2 {
			return atom.NewBool(false)
		}
		left, leftOK := intArgument(a, 0)
		right, rightOK := intArgument(a, 1)
		return atom.NewBool(leftOK && rightOK && left < right)
	})
	r.Bind("inc", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 1 {
			if value, ok := intArgument(a, 0); ok {
				return atom.NewInt(value + 1)
			}
		}
		return atom.NewInt(0)
	})
	r.Bind("add", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 2 {
			left, leftOK := intArgument(a, 0)
			right, rightOK := intArgument(a, 1)
			if leftOK && rightOK {
				return atom.NewInt(left + right)
			}
		}
		return atom.NewInt(0)
	})
	r.Bind("mul", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 2 {
			left, leftOK := intArgument(a, 0)
			right, rightOK := intArgument(a, 1)
			if leftOK && rightOK {
				return atom.NewInt(left * right)
			}
		}
		return atom.NewInt(0)
	})
}

// HistoryEntry is one line of an agent's history.
type HistoryEntry struct {
	AgeSeconds float32
	Text       string
}

// Agent is one long-lived Wanderer NPC (AIHTNDemoWandererAgent): it owns its
// gameplay state, daemons, world state, planner hook and planning unit, and
// executes primitive tasks over simulated time.
type Agent struct {
	id              uint32
	definition      *planner.Definition
	initialWaypoint int
	registry        *callterm.Registry
	report          callterm.ErrorCallback

	database *integration.DatabaseHook
	hook     *integration.PlannerHook
	unit     *integration.PlanningUnit

	terrain        *Terrain
	wanderer       *Wanderer
	wandererDaemon *WandererDaemon
	pathfinder     *Pathfinder
	terrainDaemon  *TerrainDaemon

	initialized       bool
	age               float32
	remaining         float32
	taskStarted       bool
	lastPlanSucceeded bool
	planCount         uint64
	completedTasks    uint64
	lastPlanner       time.Duration
	totalPlanner      time.Duration
	maxPlanner        time.Duration
	history           []HistoryEntry
}

// NewAgent creates an NPC that starts at waypoint initialWaypoint. report
// receives callterm errors.
func NewAgent(id uint32, definition *planner.Definition, initialWaypoint int, terrain *Terrain,
	registry *callterm.Registry, report callterm.ErrorCallback) *Agent {
	wanderer := NewWanderer(terrain)
	return &Agent{id: id, definition: definition, initialWaypoint: initialWaypoint, registry: registry,
		report: report, terrain: terrain, wanderer: wanderer, wandererDaemon: &WandererDaemon{wanderer: wanderer},
		pathfinder: NewPathfinder(terrain), terrainDaemon: &TerrainDaemon{terrain: terrain}}
}

// Initialize creates the persistent planner and execution storage.
func (a *Agent) Initialize() bool {
	if a.initialized {
		return true
	}
	if a.definition == nil {
		a.addHistory("Initialization failed: missing generated Wanderer definition")
		return false
	}
	a.database = integration.NewDatabaseHook()
	a.hook = integration.NewPlannerHook(a.database.WorldState(), a.registry)
	a.hook.Bindings().SetDaemon(PathfinderDaemonID, a.pathfinder)
	a.wanderer.Reset(a.initialWaypoint)
	a.wandererDaemon.Initialize(a.hook)
	a.pathfinder.Initialize(a.hook)
	a.terrainDaemon.Initialize(a.hook)
	if !a.hook.SetGeneratedPlannerDefinition(a.definition) {
		a.addHistory("Initialization failed: incompatible generated planner ABI")
		return false
	}
	a.unit = integration.NewPlanningUnit(a.database, a.hook, "run")
	a.unit.ExecutionContext().CallTermErrorPolicy = callterm.PolicyReport
	a.unit.ExecutionContext().CallTermErrorCallback = a.report
	a.writeWorldState()
	a.initialized = true
	a.addHistory("NPC spawned; persistent planner/execution storage created")
	return true
}

// Update advances the NPC by deltaTime seconds.
func (a *Agent) Update(deltaTime float32) {
	if !a.initialized {
		return
	}
	deltaTime = max(0, deltaTime)
	a.age += deltaTime
	a.wandererDaemon.Update(deltaTime)
	a.terrainDaemon.Update(deltaTime)
	a.pathfinder.Update(deltaTime)
	a.writeWorldState()
	if a.unit == nil || len(a.unit.CurrentPlan()) == 0 {
		// Planning is synchronous: ask for the next piece of work at once.
		a.tryPlan()
		if a.unit == nil || len(a.unit.CurrentPlan()) == 0 {
			return
		}
	}
	if !a.taskStarted {
		a.startCurrentTask()
	}
	if !a.taskStarted {
		return
	}
	a.remaining -= deltaTime
	if a.remaining <= 0 {
		a.completeCurrentTask()
	}
}

func (a *Agent) writeWorldState() {
	if a.database == nil {
		return
	}
	world := a.database.WorldState()
	a.terrainDaemon.WriteWorldState(a.terrainDaemon, world)
	a.wandererDaemon.WriteWorldState(a.wandererDaemon, world)
	a.pathfinder.WriteWorldState(a.pathfinder, world)
}

func (a *Agent) tryPlan() {
	if a.unit == nil {
		return
	}
	// A successful decomposition may contain no primitive task (a callterm
	// started a pathfinding request): replan at once, within a small budget.
	for pass := 0; pass < maxImmediatePlanningPasses; pass++ {
		start := time.Now()
		a.lastPlanSucceeded = a.unit.Decompose() == planner.Succeeded
		a.lastPlanner = time.Since(start)
		a.totalPlanner += a.lastPlanner
		a.maxPlanner = max(a.maxPlanner, a.lastPlanner)
		a.planCount++
		if !a.lastPlanSucceeded {
			a.addHistory("Planning failed; retrying next update")
			return
		}
		a.taskStarted = false
		if len(a.unit.CurrentPlan()) == 0 {
			a.addHistory("Plan completed immediately (side effect); replanning now")
			a.writeWorldState()
			continue
		}
		a.addHistory("New plan: " + strconv.Itoa(len(a.unit.CurrentPlan())) + " plan step(s)")
		a.startCurrentTask()
		return
	}
	a.addHistory("Warning: immediate replanning budget exhausted with no executable task")
}

func taskArgument(task atom.Atom, index int) (Cell, bool) {
	if atom.CallArgumentCount(task) <= index {
		return Cell{}, false
	}
	argument, _ := atom.CallArgument(task, index)
	return parseCell(argument)
}

func (a *Agent) startCurrentTask() {
	if a.unit == nil {
		return
	}
	switch a.unit.ResolveCurrentPrimitiveTask() {
	case integration.PlanCompleted:
		a.finishPlan()
		return
	case integration.Failed:
		a.lastPlanSucceeded = false
		a.addHistory("Plan resolution failed")
		a.unit.ClearCurrentPlan()
		a.taskStarted = false
		return
	}
	task, ok := a.unit.CurrentPrimitiveTask()
	if !ok {
		a.lastPlanSucceeded = false
		a.addHistory("Plan contains no executable primitive task")
		a.unit.ClearCurrentPlan()
		a.taskStarted = false
		return
	}
	switch atom.CallHead(task) {
	case taskWalkSegment:
		distance := float32(1)
		if target, ok := taskArgument(task, 0); ok {
			current := a.wanderer.Location()
			deltaX := float32(target.X - current.X)
			deltaY := float32(target.Y - current.Y)
			distance = float32(math.Sqrt(float64(float32(deltaX*deltaX) + float32(deltaY*deltaY))))
		}
		speed := max(float32(0.01), a.wanderer.Speed())
		a.remaining = max(float32(0.01), distance/speed)
	case taskPlayContextualAnimation:
		var interactable *Interactable
		if location, ok := taskArgument(task, 1); ok && atom.CallArgumentCount(task) >= 2 {
			interactable = a.terrain.InteractableAt(location)
		}
		if interactable != nil {
			a.remaining = max(contextualAnimationTaskMinimum, interactable.UsageTimeSeconds)
		} else {
			a.remaining = 0.05
			a.addHistory("Warning: contextual animation has no matching interactable")
		}
	case taskWaitForPathfindingQuery:
		a.remaining = waitTaskDurationSeconds
	case taskWandererIdle:
		a.remaining = idleTaskDurationSeconds
	case taskSay:
		a.remaining = sayTaskDurationSeconds
	default:
		a.remaining = 0.05
	}
	a.taskStarted = true
	a.addHistory("Start: " + a.FormatTask(task))
}

func (a *Agent) completeCurrentTask() {
	var task atom.Atom
	ok := false
	if a.unit != nil {
		task, ok = a.unit.CurrentPrimitiveTask()
	}
	if !ok {
		a.finishPlan()
		return
	}
	switch atom.CallHead(task) {
	case taskWalkSegment:
		if location, ok := taskArgument(task, 0); ok {
			a.wanderer.NotifyWalkSegmentCompleted(location)
		}
	case taskPlayContextualAnimation:
		a.wanderer.NotifyContextualAnimationCompleted()
	}
	a.completedTasks++
	a.addHistory("Complete: " + a.FormatTask(task))
	a.unit.CompleteCurrentPrimitiveTask()
	a.taskStarted = false
	a.remaining = 0
	a.writeWorldState()
	a.startCurrentTask()
}

func (a *Agent) finishPlan() {
	if a.unit != nil {
		a.unit.ClearCurrentPlan()
	}
	a.taskStarted = false
	a.remaining = 0
	// The planner is synchronous: request the next plan immediately.
	a.tryPlan()
}

func (a *Agent) addHistory(text string) {
	a.history = append(a.history, HistoryEntry{AgeSeconds: a.age, Text: text})
	if len(a.history) > maxHistoryEntries {
		a.history = append(a.history[:0], a.history[len(a.history)-maxHistoryEntries:]...)
	}
}

// FormatTask renders a plan step as "head arg..." with quoted strings.
func (a *Agent) FormatTask(task atom.Atom) string {
	head := atom.CallHead(task)
	text := "<invalid task>"
	if head != nil {
		text = head.Text()
	}
	for _, argument := range atom.CallArguments(task) {
		text += " " + atom.ToString(argument, true)
	}
	return text
}

// ID returns the NPC id.
func (a *Agent) ID() uint32 { return a.id }

// Wanderer returns the NPC's gameplay state.
func (a *Agent) Wanderer() *Wanderer { return a.wanderer }

// IsInitialized reports whether the planner was created.
func (a *Agent) IsInitialized() bool { return a.initialized }

// LastPlanSucceeded reports whether the last decomposition succeeded.
func (a *Agent) LastPlanSucceeded() bool { return a.lastPlanSucceeded }

// PlanCount returns the number of decompositions.
func (a *Agent) PlanCount() uint64 { return a.planCount }

// CompletedTaskCount returns the number of executed primitive tasks.
func (a *Agent) CompletedTaskCount() uint64 { return a.completedTasks }

// RemainingSeconds returns the remaining time of the current task.
func (a *Agent) RemainingSeconds() float32 { return a.remaining }

// PlannerTimes returns the last, total and maximum decomposition time.
func (a *Agent) PlannerTimes() (last, total, maximum time.Duration) {
	return a.lastPlanner, a.totalPlanner, a.maxPlanner
}

// History returns the recent history (at most 160 entries).
func (a *Agent) History() []HistoryEntry { return a.history }

// CurrentPlan returns the active plan.
func (a *Agent) CurrentPlan() []atom.Atom {
	if a.unit == nil {
		return nil
	}
	return a.unit.CurrentPlan()
}

// CurrentTaskIndex returns the position of the current task in the plan.
func (a *Agent) CurrentTaskIndex() int {
	if a.unit == nil {
		return 0
	}
	return a.unit.CurrentPrimitiveTaskIndex()
}

// CurrentTaskName returns the head of the current task.
func (a *Agent) CurrentTaskName() string {
	var task atom.Atom
	ok := false
	if a.unit != nil {
		task, ok = a.unit.CurrentPrimitiveTask()
	}
	if !ok {
		if a.lastPlanSucceeded {
			return "<no task>"
		}
		return "<plan failed>"
	}
	head := atom.CallHead(task)
	if head == nil {
		return "<invalid task>"
	}
	return head.Text()
}

// NavigationPath returns the remaining resolved path (for display).
func (a *Agent) NavigationPath() ([]Cell, bool) {
	return a.pathfinder.RemainingPath(a.wanderer.Location(), a.wanderer.Destination())
}

// WorldState returns the NPC's world state.
func (a *Agent) WorldState() *worldstate.WorldState {
	if a.database == nil {
		return nil
	}
	return a.database.WorldState()
}
