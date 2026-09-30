// Package planner is the runtime used by generated HTN planners.
//
// It ports the generated-planner ABI of the original framework
// (HTNGeneratedPlanner.h): a generated domain exports an immutable Definition,
// callers own execution storage (an *Exec), and DecomposeCall runs one
// decomposition. Generated code performs all domain-specific work; this
// package provides the generic machinery that the C generator emitted inline:
// variable slots, the pending-continuation stack with restore snapshots,
// explicit call frames with branch-retry state and the iterative dispatcher.
package planner

import (
	"math"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/callterm"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/worldstate"
)

// ABIVersion identifies the generated-planner contract of this port. Generated
// definitions must report the same value.
const ABIVersion uint32 = 0x47540001

// DecompositionStatus describes why a planning request finished. Numeric
// values match HTNDecompositionStatus.
type DecompositionStatus uint32

const (
	Succeeded DecompositionStatus = iota
	NoPlan
	BacktrackingCapacityExceeded
	OutOfMemory
	InvalidContext
	InvalidCall
	PreparationFailed
	NotRun
	CallFrameCapacityExceeded
)

var statusNames = [...]string{
	"SUCCEEDED", "NO_PLAN", "BACKTRACKING_CAPACITY_EXCEEDED", "OUT_OF_MEMORY", "INVALID_CONTEXT",
	"INVALID_CALL", "PREPARATION_FAILED", "NOT_RUN", "CALL_FRAME_CAPACITY_EXCEEDED",
}

// String returns the status name.
func (s DecompositionStatus) String() string {
	if int(s) < len(statusNames) {
		return statusNames[s]
	}
	return "UNKNOWN"
}

// BacktrackingMode selects which optional backtracking forms are allowed at
// runtime when a planner was generated with runtime backtracking support.
type BacktrackingMode uint32

const (
	BacktrackingNone           BacktrackingMode = 0
	BacktrackingFactsAndAxioms BacktrackingMode = 1 << 0
	BacktrackingBranches       BacktrackingMode = 1 << 1
	BacktrackingAll                             = BacktrackingFactsAndAxioms | BacktrackingBranches
)

// Features advertised by a generated definition.
type Features uint32

const (
	FeatureNone                Features = 0
	FeatureRuntimeBacktracking Features = 1 << 0
)

// Context is the per-invocation execution descriptor
// (HTNGeneratedPlannerContext). All referenced objects are borrowed.
type Context struct {
	WorldState            *worldstate.WorldState
	Bindings              *callterm.BindingContext
	BacktrackingMode      BacktrackingMode
	Execution             *Exec
	Prepared              any
	ClientContext         any
	CallTermErrorPolicy   callterm.ErrorPolicy
	CallTermErrorCallback callterm.ErrorCallback
	Debugger              Debugger
}

// ExecutionInfo exposes call-frame diagnostics of the last decomposition.
type ExecutionInfo struct {
	CallFrameCapacity uint32
	PeakCallFrames    uint32
	CallFrameSize     uintptr
	LastError         string
}

// Definition is the immutable descriptor exported by a generated domain.
type Definition struct {
	ABIVersion           uint32
	Features             Features
	DomainID             string
	SourceFile           string
	NewPreparedStorage   func() any
	NewExecutionStorage  func() *Exec
	DecomposeCall        func(ctx *Context, call atom.Atom, requireTopLevel bool) (atom.Atom, DecompositionStatus)
	FactNames            []string
	CallTermRequirements []callterm.Requirement
	Debug                *DebugMetadata
}

// ValidateDefinition reports whether a definition matches this runtime's ABI
// and provides a complete lifecycle contract.
func ValidateDefinition(d *Definition) bool {
	return d != nil && d.ABIVersion == ABIVersion && d.NewPreparedStorage != nil &&
		d.NewExecutionStorage != nil && d.DecomposeCall != nil
}

// ExecutionInfoOf returns the diagnostics of execution storage (nil for nil).
func ExecutionInfoOf(ex *Exec) *ExecutionInfo {
	if ex == nil {
		return nil
	}
	return &ex.Info
}

// TaskFn is a generated method or task function. It returns 0 (failure),
// 1 (success) or 2 (suspend: run ex.Next as a child, then resume).
type TaskFn func(ex *Exec) int

// PendingTask is one statically-known continuation of a branch.
type PendingTask struct {
	Fn TaskFn
	// Restore lists the slots the immediately preceding sibling may mutate.
	Restore []uint32
}

// BranchContinuations lists a branch's tasks in push order (last task first).
type BranchContinuations struct {
	Tasks        []PendingTask
	TotalRestore int
}

// CallFrame is one suspended generated method/task invocation.
type CallFrame struct {
	parent           int
	Function         TaskFn
	Resume           uint32
	ChildResult      int
	RetryPlanSize    int
	RetryPendingBase int
	RetryFrame       uint64
	RetryValues      []atom.Atom
}

type pendingEntry struct {
	fn        TaskFn
	snapStart int
	snapCount int
	frameID   uint64
}

// Config sizes execution storage. It is emitted by the code generator.
type Config struct {
	VariableCount        int
	FactCount            int
	CallTermCount        int
	CallFrameCapacity    uint32
	FixedCapacity        bool
	BacktrackingCapacity int
	SnapshotCapacity     int
	CapacityError        string
}

// Exec is the mutable execution storage of one planner instance. It must not
// be shared by concurrent decompositions.
type Exec struct {
	Ctx          *Context
	V            []atom.Atom
	Plan         []atom.Atom
	FailureState DecompositionStatus
	Info         ExecutionInfo
	Next         TaskFn

	CurrentFrameID uint64
	NextFrameID    uint64

	FactTables     []*worldstate.Tables
	factWorld      *worldstate.WorldState
	factGeneration uint64
	factsReady     bool
	CallTerms      []callterm.Slot
	callBindings   *callterm.BindingContext
	callsReady     bool

	pending    []pendingEntry
	snapSlots  []uint32
	snapValues []atom.Atom

	frames     []CallFrame
	current    int
	frameCount uint32

	config Config
	inv    callterm.Invocation
}

// NewExec creates execution storage for a generated domain configuration.
func NewExec(config Config) *Exec {
	ex := &Exec{config: config, current: -1, CurrentFrameID: 1, NextFrameID: 2}
	ex.V = make([]atom.Atom, config.VariableCount)
	ex.FactTables = make([]*worldstate.Tables, config.FactCount)
	ex.CallTerms = make([]callterm.Slot, config.CallTermCount)
	ex.Info.CallFrameCapacity = config.CallFrameCapacity
	ex.Info.CallFrameSize = callFrameSize
	ex.FailureState = NoPlan
	return ex
}

const callFrameSize = 80

// Frame returns the current call frame. The pointer stays valid until the
// generated function returns to the dispatcher.
func (ex *Exec) Frame() *CallFrame { return &ex.frames[ex.current] }

// PendingCount returns the number of pending continuations.
func (ex *Exec) PendingCount() int { return len(ex.pending) }

// Begin resets execution state for a new decomposition and refreshes cached
// fact tables and callterm slots when their sources changed. factSymbols and
// callNames are indexed by slot.
func (ex *Exec) Begin(ctx *Context, factSymbols []*atom.Symbol, callNames []string) {
	ex.Ctx = ctx
	ex.inv = callterm.Invocation{Bindings: ctx.Bindings, ClientContext: ctx.ClientContext,
		Policy: ctx.CallTermErrorPolicy, Callback: ctx.CallTermErrorCallback}
	ex.FailureState = NoPlan
	generation := ctx.WorldState.Generation()
	factsReady := ex.factsReady && ex.factWorld == ctx.WorldState && ex.factGeneration == generation
	callsReady := ex.callsReady && ex.callBindings == ctx.Bindings
	for i := range ex.snapValues {
		ex.snapValues[i] = atom.Atom{}
	}
	for i := range ex.V {
		ex.V[i] = atom.Atom{}
	}
	ex.pending = ex.pending[:0]
	ex.snapSlots = ex.snapSlots[:0]
	ex.snapValues = ex.snapValues[:0]
	ex.Plan = ex.Plan[:0]
	ex.CurrentFrameID = 1
	ex.NextFrameID = 2
	if !factsReady {
		for i, symbol := range factSymbols {
			ex.FactTables[i] = ctx.WorldState.ResolveGeneratedTables(symbol)
		}
		ex.factWorld = ctx.WorldState
		ex.factGeneration = generation
		ex.factsReady = true
	}
	if !callsReady {
		for i, name := range callNames {
			ex.CallTerms[i] = ctx.Bindings.ResolveSlot(name)
		}
		ex.callBindings = ctx.Bindings
		ex.callsReady = true
	}
}

// ResetDiagnostics clears the execution info of the previous decomposition.
func (ex *Exec) ResetDiagnostics() {
	ex.Info.PeakCallFrames = 0
	ex.Info.LastError = ""
}

// refreshFacts re-resolves cached fact tables after a callterm created a new
// fact entry (the world-state generation changed).
func (ex *Exec) refreshFacts(factSymbols []*atom.Symbol) {
	generation := ex.Ctx.WorldState.Generation()
	if generation == ex.factGeneration {
		return
	}
	for i, symbol := range factSymbols {
		ex.FactTables[i] = ex.Ctx.WorldState.ResolveGeneratedTables(symbol)
	}
	ex.factGeneration = generation
}

// Invoke runs the callterm cached in slot with the given arguments and refreshes
// the fact cache afterwards. It returns the result and whether it is bound.
func (ex *Exec) Invoke(slot int, args []atom.Atom, source *callterm.Source, factSymbols []*atom.Symbol) (atom.Atom, bool) {
	call := &ex.CallTerms[slot]
	result := callterm.InvokeEntry(call.Entry, call.Name, args, source, &ex.inv)
	ex.refreshFacts(factSymbols)
	return result, result.IsBound()
}

// PushBranch schedules a branch's tasks. It fails when a fixed-capacity
// planner cannot store them.
func (ex *Exec) PushBranch(b *BranchContinuations) bool {
	if ex.config.FixedCapacity {
		capacity := ex.config.BacktrackingCapacity
		snapshots := ex.config.SnapshotCapacity
		if len(b.Tasks) > capacity || b.TotalRestore > snapshots ||
			len(ex.pending) > capacity-len(b.Tasks) || len(ex.snapSlots) > snapshots-b.TotalRestore {
			ex.FailureState = BacktrackingCapacityExceeded
			return false
		}
	}
	frameID := ex.CurrentFrameID
	for i := range b.Tasks {
		task := &b.Tasks[i]
		start := len(ex.snapSlots)
		for _, slot := range task.Restore {
			ex.snapSlots = append(ex.snapSlots, slot)
			ex.snapValues = append(ex.snapValues, ex.V[slot])
		}
		ex.pending = append(ex.pending, pendingEntry{fn: task.Fn, snapStart: start, snapCount: len(task.Restore), frameID: frameID})
	}
	return true
}

// PopPending removes the most recent continuation and restores its snapshot.
func (ex *Exec) PopPending() TaskFn {
	n := len(ex.pending)
	if n == 0 {
		return nil
	}
	entry := ex.pending[n-1]
	ex.pending = ex.pending[:n-1]
	for i := entry.snapStart; i < entry.snapStart+entry.snapCount; i++ {
		ex.V[ex.snapSlots[i]] = ex.snapValues[i]
		ex.snapValues[i] = atom.Atom{}
	}
	ex.snapSlots = ex.snapSlots[:entry.snapStart]
	ex.snapValues = ex.snapValues[:entry.snapStart]
	ex.CurrentFrameID = entry.frameID
	return entry.fn
}

// SaveRetry captures a method's retry state before a branch that can fall
// back to a later branch.
func (ex *Exec) SaveRetry(frame *CallFrame, slots []uint32) {
	frame.RetryPlanSize = len(ex.Plan)
	frame.RetryPendingBase = len(ex.pending)
	frame.RetryFrame = ex.CurrentFrameID
	if cap(frame.RetryValues) < len(slots) {
		frame.RetryValues = make([]atom.Atom, len(slots))
	}
	frame.RetryValues = frame.RetryValues[:len(slots)]
	for i, slot := range slots {
		frame.RetryValues[i] = ex.V[slot]
	}
}

// ReleaseRetry drops the captured retry values.
func (ex *Exec) ReleaseRetry(frame *CallFrame) {
	for i := range frame.RetryValues {
		frame.RetryValues[i] = atom.Atom{}
	}
}

// RestoreRetry rolls back a failed branch subtree: discards its pending
// continuations, truncates the partial plan and restores the method slots.
func (ex *Exec) RestoreRetry(frame *CallFrame, slots []uint32) {
	for len(ex.pending) > frame.RetryPendingBase {
		ex.PopPending()
	}
	for i := len(ex.Plan) - 1; i >= frame.RetryPlanSize; i-- {
		ex.Plan[i] = atom.Atom{}
	}
	if len(ex.Plan) > frame.RetryPlanSize {
		ex.Plan = ex.Plan[:frame.RetryPlanSize]
	}
	for i, slot := range slots {
		ex.V[slot] = frame.RetryValues[i]
		frame.RetryValues[i] = atom.Atom{}
	}
	ex.CurrentFrameID = frame.RetryFrame
}

// EnterFrame starts a fresh logical variable frame for a compound call.
func (ex *Exec) EnterFrame() {
	ex.CurrentFrameID = ex.NextFrameID
	ex.NextFrameID++
}

// Run drives fn and every function it suspends into with explicit call frames
// (the generated _RUN dispatcher). It never recurses natively.
func (ex *Exec) Run(fn TaskFn) int {
	if len(ex.frames) == 0 {
		ex.frames = append(ex.frames, CallFrame{})
	}
	ex.frameCount = 1
	if ex.Info.PeakCallFrames == 0 {
		ex.Info.PeakCallFrames = 1
	}
	ex.current = 0
	root := &ex.frames[0]
	root.parent = -1
	root.Resume = 0
	root.Function = fn
	result := 0
	for ex.current >= 0 {
		frame := &ex.frames[ex.current]
		result = frame.Function(ex)
		frame = &ex.frames[ex.current]
		if result == 2 {
			if ex.frameCount == ex.config.CallFrameCapacity {
				ex.FailureState = CallFrameCapacityExceeded
				ex.Info.LastError = ex.config.CapacityError
				frame.ChildResult = 0
				continue
			}
			index := int(ex.frameCount)
			ex.frameCount++
			if ex.frameCount > ex.Info.PeakCallFrames {
				ex.Info.PeakCallFrames = ex.frameCount
			}
			if index >= len(ex.frames) {
				ex.frames = append(ex.frames, CallFrame{})
			}
			child := &ex.frames[index]
			child.Resume = 0
			child.parent = ex.current
			child.Function = ex.Next
			ex.current = index
		} else {
			parent := frame.parent
			frame.Function = nil
			ex.frameCount--
			ex.current = parent
			if parent >= 0 {
				ex.frames[parent].ChildResult = result
			}
		}
	}
	return result
}

// AppendPlanStep appends a plan step built from head and args. It fails when
// an argument is unbound (the original reports OUT_OF_MEMORY in that case).
func (ex *Exec) AppendPlanStep(head *atom.Symbol, args []atom.Atom) bool {
	elems := make([]atom.Atom, 0, len(args)+1)
	elems = append(elems, atom.NewSymbol(head))
	for _, arg := range args {
		if !arg.IsBound() {
			ex.FailureState = OutOfMemory
			return false
		}
		elems = append(elems, arg)
	}
	ex.Plan = append(ex.Plan, atom.NewListOwned(elems))
	return true
}

// PlanAtom returns the completed plan as a list atom.
func (ex *Exec) PlanAtom() atom.Atom {
	steps := make([]atom.Atom, len(ex.Plan))
	copy(steps, ex.Plan)
	return atom.NewListOwned(steps)
}

// SetIfChanged binds slot to value when value is bound and differs from the
// current binding (EmitGeneratedSetCopyIfChanged).
func (ex *Exec) SetIfChanged(slot uint32, value atom.Atom) {
	if value.IsBound() {
		existing := ex.V[slot]
		if !existing.IsBound() || !atom.Equal(existing, value) {
			ex.V[slot] = value
		}
	}
}

// Arith evaluates a generated arithmetic expression with the original
// semantics: int32 arithmetic with overflow checks unless an operand is a
// float, float arithmetic in double precision stored as float32. It returns
// an unbound atom when the expression is invalid.
func Arith(op uint32, operands []atom.Atom) atom.Atom {
	count := len(operands)
	if count == 0 {
		return atom.Atom{}
	}
	useFloat := false
	for i := range operands {
		k := operands[i].Kind()
		if k != atom.KindInt && k != atom.KindFloat {
			return atom.Atom{}
		}
		if k == atom.KindFloat {
			useFloat = true
		}
	}
	if op == 4 && (count != 2 || useFloat) {
		return atom.Atom{}
	}
	if op == 5 || op == 6 {
		if count != 1 {
			return atom.Atom{}
		}
		delta := int64(1)
		if op == 6 {
			delta = -1
		}
		if !useFloat {
			incremented := int64(operands[0].Int()) + delta
			if incremented < math.MinInt32 || incremented > math.MaxInt32 {
				return atom.Atom{}
			}
			return atom.NewInt(int32(incremented))
		}
		value := float64(operands[0].Float()) + float64(delta)
		if value != value || value < -math.MaxFloat32 || value > math.MaxFloat32 {
			return atom.Atom{}
		}
		return atom.NewFloat(float32(value))
	}
	if !useFloat {
		var result int64
		i := 1
		if op == 2 {
			result = 1
			i = 0
		} else {
			result = int64(operands[0].Int())
		}
		if op == 1 && count == 1 {
			result = -int64(operands[0].Int())
			i = count
		}
		for ; i < count; i++ {
			value := int64(operands[i].Int())
			switch op {
			case 0:
				result += value
			case 1:
				result -= value
			case 2:
				result *= value
			case 3:
				if value == 0 {
					return atom.Atom{}
				}
				result /= value
			case 4:
				if value == 0 {
					return atom.Atom{}
				}
				result %= value
			default:
				return atom.Atom{}
			}
			if result < math.MinInt32 || result > math.MaxInt32 {
				return atom.Atom{}
			}
		}
		return atom.NewInt(int32(result))
	}
	number := func(a atom.Atom) float64 {
		if a.Kind() == atom.KindInt {
			return float64(a.Int())
		}
		return float64(a.Float())
	}
	var result float64
	i := 1
	if op == 2 {
		result = 1
		i = 0
	} else {
		result = number(operands[0])
	}
	if op == 1 && count == 1 {
		result = -result
		i = count
	}
	for ; i < count; i++ {
		value := number(operands[i])
		switch op {
		case 0:
			result += value
		case 1:
			result -= value
		case 2:
			result *= value
		case 3:
			if value == 0 {
				return atom.Atom{}
			}
			result /= value
		default:
			return atom.Atom{}
		}
		if result != result || result < -math.MaxFloat32 || result > math.MaxFloat32 {
			return atom.Atom{}
		}
	}
	return atom.NewFloat(float32(result))
}

// Compare implements the generated built-in comparison: unbound operands
// fail, == and != compare numbers numerically and other atoms structurally,
// ordering requires two numbers.
func Compare(left, right atom.Atom, op uint32) bool {
	if !left.IsBound() || !right.IsBound() {
		return false
	}
	lk, rk := left.Kind(), right.Kind()
	leftNumeric := lk == atom.KindInt || lk == atom.KindFloat
	rightNumeric := rk == atom.KindInt || rk == atom.KindFloat
	var l, r float64
	switch lk {
	case atom.KindInt:
		l = float64(left.Int())
	case atom.KindFloat:
		l = float64(left.Float())
	}
	switch rk {
	case atom.KindInt:
		r = float64(right.Int())
	case atom.KindFloat:
		r = float64(right.Float())
	}
	if op == 0 || op == 1 {
		var equal bool
		if leftNumeric && rightNumeric {
			equal = l == r
		} else {
			equal = atom.Equal(left, right)
		}
		if op == 0 {
			return equal
		}
		return !equal
	}
	if !leftNumeric || !rightNumeric {
		return false
	}
	switch op {
	case 2:
		return l < r
	case 3:
		return l <= r
	case 4:
		return l > r
	case 5:
		return l >= r
	}
	return false
}

// AxiomScope stores the caller state around one axiom invocation.
type AxiomScope struct {
	Saved       []atom.Atom
	Args        []atom.Atom
	CallerFrame uint64
}

// Clone returns an independent copy of the scope (used when a successful
// axiom solution is suspended so that its caller can continue).
func (s *AxiomScope) Clone() AxiomScope {
	return AxiomScope{Saved: append([]atom.Atom(nil), s.Saved...), Args: append([]atom.Atom(nil), s.Args...),
		CallerFrame: s.CallerFrame}
}
