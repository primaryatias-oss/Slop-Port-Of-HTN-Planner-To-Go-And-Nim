// Package integration is the optional engine integration layer of the HTN
// planner (HTNIntegration): a DatabaseHook owning a world state, a per-entity
// PlannerHook selecting a generated planner definition, and PlanningUnits
// that own execution storage, the active plan and deferred-step expansion.
package integration

import (
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/callterm"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/planner"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/worldstate"
)

// LogError receives integration errors (for example call-frame capacity
// diagnostics). It is nil by default.
var LogError func(message string)

// DatabaseHook owns a world state loaded from world-state files.
type DatabaseHook struct {
	world *worldstate.WorldState
}

// NewDatabaseHook creates a database with an empty world state.
func NewDatabaseHook() *DatabaseHook { return &DatabaseHook{world: worldstate.New()} }

// WorldState returns the owned world state.
func (d *DatabaseHook) WorldState() *worldstate.WorldState { return d.world }

// ParseWorldStateFile replaces the facts with the contents of a world-state
// file. The world state object and its fact tables are retained so cached
// planner storage stays valid.
func (d *DatabaseHook) ParseWorldStateFile(path string) error {
	d.world.RemoveAllFacts()
	return worldstate.ParseFile(d.world, path)
}

// ParseWorldStateText replaces the facts with parsed world-state text.
func (d *DatabaseHook) ParseWorldStateText(text string) bool {
	d.world.RemoveAllFacts()
	return worldstate.ParseText(d.world, text)
}

// ExecutionContext is the per-execution descriptor
// (HTNPlannerExecutionContext). Planning units fill the world state, bindings,
// call and storage fields for every execution.
type ExecutionContext struct {
	WorldState            *worldstate.WorldState
	Bindings              *callterm.BindingContext
	Call                  atom.Atom
	BacktrackingMode      planner.BacktrackingMode
	Execution             *planner.Exec
	Debugger              planner.Debugger
	ClientContext         any
	CallTermErrorPolicy   callterm.ErrorPolicy
	CallTermErrorCallback callterm.ErrorCallback
}

// PlannerHook is a per-entity planner facade. The referenced world state must
// outlive the hook.
type PlannerHook struct {
	world        *worldstate.WorldState
	definition   *planner.Definition
	prepared     any
	bindings     *callterm.BindingContext
	factRegistry *worldstate.FactRegistry
}

// NewPlannerHook creates a hook for world using the shared callterm registry
// (an empty registry when nil).
func NewPlannerHook(world *worldstate.WorldState, registry *callterm.Registry) *PlannerHook {
	return &PlannerHook{world: world, bindings: callterm.NewBindingContext(registry),
		factRegistry: worldstate.NewFactRegistry()}
}

// SetGeneratedPlannerDefinition selects the generated backend. An incompatible
// definition is rejected and the previous one is kept; nil clears it.
func (h *PlannerHook) SetGeneratedPlannerDefinition(definition *planner.Definition) bool {
	if definition == nil {
		h.definition = nil
		h.prepared = nil
		h.factRegistry.Reset()
		return true
	}
	if !planner.ValidateDefinition(definition) {
		return false
	}
	prepared := definition.NewPreparedStorage()
	if prepared == nil {
		return false
	}
	h.prepared = prepared
	h.definition = definition
	h.factRegistry.Reset()
	for _, name := range definition.FactNames {
		h.factRegistry.Register(atom.Intern(name))
	}
	return true
}

// GeneratedPlannerDefinition returns the selected definition.
func (h *PlannerHook) GeneratedPlannerDefinition() *planner.Definition { return h.definition }

// HasGeneratedPlannerDefinition reports whether a definition is selected.
func (h *PlannerHook) HasGeneratedPlannerDefinition() bool { return h.definition != nil }

// PreparedStorage returns the definition's prepared storage.
func (h *PlannerHook) PreparedStorage() any { return h.prepared }

// Bindings returns the entity's callterm binding context.
func (h *PlannerHook) Bindings() *callterm.BindingContext { return h.bindings }

// WorldState returns the entity's permanent world state.
func (h *PlannerHook) WorldState() *worldstate.WorldState { return h.world }

// FactRegistry returns the domain-specific fact registry.
func (h *PlannerHook) FactRegistry() *worldstate.FactRegistry { return h.factRegistry }

// FindFactSlot returns the compact slot of a fact symbol.
func (h *PlannerHook) FindFactSlot(symbol *atom.Symbol) worldstate.FactSlot {
	return h.factRegistry.FindSlot(symbol)
}

// Decompose runs one generated decomposition for ctx.Call. The plan is
// returned on success; failures return an empty list.
func (h *PlannerHook) Decompose(ctx *ExecutionContext, requireTopLevel bool) (atom.Atom, planner.DecompositionStatus) {
	if h.definition == nil || h.definition.DecomposeCall == nil || !ctx.Call.IsBound() {
		return atom.EmptyList(), planner.InvalidContext
	}
	generated := planner.Context{
		WorldState:            ctx.WorldState,
		Bindings:              ctx.Bindings,
		BacktrackingMode:      ctx.BacktrackingMode,
		Execution:             ctx.Execution,
		Prepared:              h.prepared,
		ClientContext:         ctx.ClientContext,
		CallTermErrorPolicy:   ctx.CallTermErrorPolicy,
		CallTermErrorCallback: ctx.CallTermErrorCallback,
		Debugger:              ctx.Debugger,
	}
	if ctx.Debugger != nil {
		if resetter, ok := ctx.Debugger.(interface{ Reset(sourceFile string) }); ok {
			resetter.Reset(h.definition.SourceFile)
		}
	}
	plan, status := h.definition.DecomposeCall(&generated, ctx.Call, requireTopLevel)
	if status == planner.CallFrameCapacityExceeded && LogError != nil && ctx.Execution != nil &&
		ctx.Execution.Info.LastError != "" {
		LogError(ctx.Execution.Info.LastError)
	}
	if status != planner.Succeeded {
		return atom.EmptyList(), status
	}
	return plan, status
}

// PrimitiveTaskResolution is the outcome of ResolveCurrentPrimitiveTask.
type PrimitiveTaskResolution uint8

const (
	TaskReady PrimitiveTaskResolution = iota
	PlanCompleted
	Failed
)

// PlanningUnit owns reusable execution storage, the last decomposition and the
// active plan of one agent/job. Planning units are not safe for concurrent use.
type PlanningUnit struct {
	options               ExecutionContext
	database              *DatabaseHook
	hook                  *PlannerHook
	defaultTopLevelMethod *atom.Symbol
	lastDecomposition     atom.Atom
	currentPlan           []atom.Atom
	currentIndex          int
	execution             *planner.Exec
	executionDefinition   *planner.Definition
}

// NewPlanningUnit creates a planning unit with a default top-level method.
func NewPlanningUnit(database *DatabaseHook, hook *PlannerHook, defaultTopLevelMethod string) *PlanningUnit {
	return &PlanningUnit{
		options:               ExecutionContext{BacktrackingMode: planner.BacktrackingAll},
		database:              database,
		hook:                  hook,
		defaultTopLevelMethod: atom.Intern(defaultTopLevelMethod),
		lastDecomposition:     atom.EmptyList(),
	}
}

// ExecutionContext exposes the runtime options (backtracking mode, client
// context, callterm error policy/callback, debugger). Configure them while idle.
func (u *PlanningUnit) ExecutionContext() *ExecutionContext { return &u.options }

// SetBacktrackingMode selects the runtime backtracking mode.
func (u *PlanningUnit) SetBacktrackingMode(mode planner.BacktrackingMode) {
	u.options.BacktrackingMode = mode
}

// BacktrackingMode returns the runtime backtracking mode.
func (u *PlanningUnit) BacktrackingMode() planner.BacktrackingMode { return u.options.BacktrackingMode }

// SetClientContext sets the borrowed client services passed to converters and
// callbacks.
func (u *PlanningUnit) SetClientContext(clientContext any) { u.options.ClientContext = clientContext }

// DatabaseHook returns the database.
func (u *PlanningUnit) DatabaseHook() *DatabaseHook { return u.database }

// PlannerHook returns the planner hook.
func (u *PlanningUnit) PlannerHook() *PlannerHook { return u.hook }

// DefaultTopLevelMethod returns the default top-level method symbol.
func (u *PlanningUnit) DefaultTopLevelMethod() *atom.Symbol { return u.defaultTopLevelMethod }

// LastDecomposition returns the plan of the last top-level decomposition
// (an empty list after failures).
func (u *PlanningUnit) LastDecomposition() atom.Atom { return u.lastDecomposition }

// ExecutionStorage returns the planning unit's execution storage (nil before
// the first decomposition).
func (u *PlanningUnit) ExecutionStorage() *planner.Exec { return u.execution }

func (u *PlanningUnit) ensureExecutionStorage() bool {
	definition := u.hook.GeneratedPlannerDefinition()
	if definition == nil {
		return true
	}
	if u.execution != nil && u.executionDefinition == definition {
		return true
	}
	u.execution = definition.NewExecutionStorage()
	u.executionDefinition = definition
	if u.execution == nil {
		u.executionDefinition = nil
		return false
	}
	return true
}

func (u *PlanningUnit) executeCall(call atom.Atom, requireTopLevel bool) (atom.Atom, planner.DecompositionStatus) {
	if !u.ensureExecutionStorage() {
		return atom.EmptyList(), planner.OutOfMemory
	}
	ctx := u.options
	ctx.WorldState = u.hook.WorldState()
	ctx.Bindings = u.hook.Bindings()
	ctx.Call = call
	ctx.Execution = u.execution
	return u.hook.Decompose(&ctx, requireTopLevel)
}

// DecomposeCall decomposes a caller-owned top-level call (head arg...) and
// installs the resulting plan.
func (u *PlanningUnit) DecomposeCall(call atom.Atom) planner.DecompositionStatus {
	plan, status := u.executeCall(call, true)
	u.lastDecomposition = plan
	if status == planner.Succeeded {
		u.currentPlan = append(u.currentPlan[:0], plan.Elements()...)
		u.currentIndex = 0
	} else {
		u.ClearCurrentPlan()
	}
	return status
}

// DecomposeTopLevelMethod decomposes method with Go arguments converted
// through the registered type converters (using the unit's client context).
func (u *PlanningUnit) DecomposeTopLevelMethod(method *atom.Symbol, args ...any) planner.DecompositionStatus {
	if method == nil {
		return planner.InvalidCall
	}
	atoms := make([]atom.Atom, 0, len(args))
	for _, arg := range args {
		value, ok := callterm.ToAtom(u.options.ClientContext, arg)
		if !ok || !value.IsBound() {
			return planner.OutOfMemory
		}
		atoms = append(atoms, value)
	}
	call, ok := atom.MakeCall(method, atoms...)
	if !ok {
		return planner.OutOfMemory
	}
	return u.DecomposeCall(call)
}

// Decompose decomposes the default top-level method without arguments.
func (u *PlanningUnit) Decompose() planner.DecompositionStatus {
	return u.DecomposeTopLevelMethod(u.defaultTopLevelMethod)
}

// ResolveCurrentPrimitiveTask expands deferred calls at the current position
// until a primitive task is ready, the plan completes or an expansion fails.
func (u *PlanningUnit) ResolveCurrentPrimitiveTask() PrimitiveTaskResolution {
	for u.currentIndex < len(u.currentPlan) {
		step := u.currentPlan[u.currentIndex]
		kind := atom.GetPlanStepKind(step)
		if kind == atom.PlanStepPrimitiveTask {
			return TaskReady
		}
		if kind != atom.PlanStepDeferredCall {
			u.ClearCurrentPlan()
			return Failed
		}
		call, ok := atom.MakeCallFromDeferredPlanStep(step)
		if !ok || !atom.IsValidCall(call) {
			u.ClearCurrentPlan()
			return Failed
		}
		plan, status := u.executeCall(call, false)
		if status != planner.Succeeded {
			u.ClearCurrentPlan()
			return Failed
		}
		replacement := plan.Elements()
		rest := append([]atom.Atom(nil), u.currentPlan[u.currentIndex+1:]...)
		u.currentPlan = append(append(u.currentPlan[:u.currentIndex], replacement...), rest...)
	}
	return PlanCompleted
}

// CurrentPrimitiveTask returns the primitive task at the current position.
func (u *PlanningUnit) CurrentPrimitiveTask() (atom.Atom, bool) {
	if u.currentIndex >= len(u.currentPlan) {
		return atom.Atom{}, false
	}
	step := u.currentPlan[u.currentIndex]
	if atom.GetPlanStepKind(step) != atom.PlanStepPrimitiveTask {
		return atom.Atom{}, false
	}
	return step, true
}

// CompleteCurrentPrimitiveTask advances past the current primitive task.
func (u *PlanningUnit) CompleteCurrentPrimitiveTask() {
	if _, ok := u.CurrentPrimitiveTask(); ok {
		u.currentIndex++
	}
}

// ClearCurrentPlan discards the active plan.
func (u *PlanningUnit) ClearCurrentPlan() {
	u.currentPlan = u.currentPlan[:0]
	u.currentIndex = 0
}

// CurrentPlan returns the active plan (read-only).
func (u *PlanningUnit) CurrentPlan() []atom.Atom { return u.currentPlan }

// CurrentPrimitiveTaskIndex returns the active plan position.
func (u *PlanningUnit) CurrentPrimitiveTaskIndex() int { return u.currentIndex }

// Daemon feeds a planner's world state (AIHtnDaemonBase).
type Daemon interface {
	Update(deltaTime float32)
	OnWriteWorldState(world *worldstate.WorldState)
}

// DaemonBase provides the hook plumbing shared by daemons.
type DaemonBase struct {
	FactRegistry *worldstate.FactRegistry
	World        *worldstate.WorldState
}

// Initialize binds the daemon to a planner hook.
func (d *DaemonBase) Initialize(hook *PlannerHook) {
	if hook == nil {
		d.FactRegistry, d.World = nil, nil
		return
	}
	d.FactRegistry = hook.FactRegistry()
	d.World = hook.WorldState()
}

// WriteWorldState associates world with the daemon's fact registry and lets
// daemon write its knowledge.
func (d *DaemonBase) WriteWorldState(daemon Daemon, world *worldstate.WorldState) {
	world.SetFactRegistry(d.FactRegistry)
	daemon.OnWriteWorldState(world)
}
