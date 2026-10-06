package planner

import "github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"

// Generated execution debugging (HTN_DEBUG_DECOMPOSITION of the original).
//
// Generated planners always contain the debug metadata and event calls, but
// both are guarded by DebugEnabled, a constant that is true only in builds
// with the "htndebug" tag (go build -tags htndebug). Without the tag the
// compiler removes the instrumentation entirely.

// NoIndex marks an absent index in debug metadata (HTN_GENERATED_NO_INDEX).
const NoIndex = ^uint32(0)

// DebugSlotMaskWords is the number of 64-bit words of a variable slot mask.
const DebugSlotMaskWords = 4

// Condition kinds of debug metadata (HTNGeneratedConditionKind).
const (
	DebugConditionFact uint32 = iota
	DebugConditionAxiom
	DebugConditionAnd
	DebugConditionOr
	DebugConditionAlt
	DebugConditionNot
	DebugConditionCall
	DebugConditionCallBind
	DebugConditionBuiltinComparison
	DebugConditionBuiltinListSplit
	DebugConditionAssignment
)

// Task kinds of debug metadata (HTNGeneratedTaskKind).
const (
	DebugTaskCompound uint32 = iota
	DebugTaskPrimitive
	DebugTaskDeferred
)

// Value flags of debug metadata (HTNGeneratedDebugValueFlags).
const (
	DebugValueVariable uint32 = 1 << iota
	DebugValueStringLiteral
	DebugValueCallExpression
)

// DebugValue describes one compiled value.
type DebugValue struct {
	Flags        uint32
	Text         uint32 // original source expression
	ResolvedText uint32 // compile-time resolved value text
	SourceLine   uint32
	VariableSlot uint32
}

// DebugCondition describes one compiled condition node.
type DebugCondition struct {
	Kind, ID, FirstArgument, ArgumentCount, FirstChildRef, ChildCount uint32
	OutputValue, ResolvedIndex, SourceLine                            uint32
	Expression                                                        string // original domain syntax
	Internal                                                          bool   // compiler-generated step without a source row
}

// DebugTask describes one compiled task occurrence.
type DebugTask struct {
	Kind, ID, FirstArgument, ArgumentCount, SourceLine, PlanStepHead uint32
}

// DebugBranch describes one compiled branch.
type DebugBranch struct {
	ID, Condition, FirstTask, TaskCount, SourceLine uint32
}

// DebugMethod describes one compiled method.
type DebugMethod struct {
	ID, FirstParameter, ParameterCount, FirstBranch, BranchCount, SourceLine uint32
	VariableSlotMask                                                         [DebugSlotMaskWords]uint64
}

// DebugAxiom describes one compiled axiom.
type DebugAxiom struct {
	ID, FirstParameter, ParameterCount, Condition, SourceLine uint32
	VariableSlotMask                                          [DebugSlotMaskWords]uint64
}

// DebugConstant describes one constant.
type DebugConstant struct {
	GroupID, ID, Value, SourceLine uint32
}

// DebugSourceRange is a 1-based source range of a compiled element.
type DebugSourceRange struct {
	SourceFileIndex, BeginLine, BeginColumn, EndLine, EndColumn uint32
}

// DebugMetadata describes the compiled domain for debuggers
// (HTNGeneratedDebugMetadata).
type DebugMetadata struct {
	SourceFile         string
	Strings            []string
	Values             []DebugValue
	VariableStringIDs  []uint32 // NoIndex marks compiler-internal slots
	Conditions         []DebugCondition
	ConditionChildRefs []uint32
	Tasks              []DebugTask
	Branches           []DebugBranch
	Methods            []DebugMethod
	Axioms             []DebugAxiom
	Constants          []DebugConstant
	CallTermSlotCount  uint32
	FactSlotCount      uint32
	SourceFiles        []string
	ValueSources       []DebugSourceRange
	ConditionSources   []DebugSourceRange
	TaskSources        []DebugSourceRange
	BranchSources      []DebugSourceRange
	MethodSources      []DebugSourceRange
	AxiomSources       []DebugSourceRange
	ConstantSources    []DebugSourceRange
}

// Debugger receives generated execution events. values are the execution's
// variable slots (a slot is bound when its atom is bound); they are only
// valid during the call.
type Debugger interface {
	// Reset starts a new capture for the domain at domainPath.
	Reset(domainPath string)
	BeginPlan(d *Definition, method uint32, values []atom.Atom)
	EndPlan(d *Definition, values []atom.Atom, result bool)
	BeginMethod(d *Definition, method uint32, values []atom.Atom)
	EndMethod(d *Definition, values []atom.Atom, result bool)
	BeginBranch(d *Definition, branch uint32, values []atom.Atom)
	EndBranch(d *Definition, values []atom.Atom, result bool)
	CapturePendingTask(task uint32)
	BeginTask(d *Definition, task uint32, values []atom.Atom)
	EndTask(d *Definition, values []atom.Atom, result bool)
	BeginCondition(d *Definition, condition uint32, values []atom.Atom)
	EndCondition(d *Definition, values []atom.Atom, result bool)
	BeginAxiom(d *Definition, axiom uint32, values []atom.Atom)
	EndAxiom(d *Definition, values []atom.Atom, result bool)
}

// Event helpers called by generated code. They do nothing unless DebugEnabled.

func (ex *Exec) debugger() Debugger {
	if !DebugEnabled || ex.Ctx == nil {
		return nil
	}
	return ex.Ctx.Debugger
}

// DebugBeginPlan reports the start of a decomposition.
func (ex *Exec) DebugBeginPlan(d *Definition, method uint32) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.BeginPlan(d, method, ex.V)
	}
}

// DebugEndPlan reports the end of a decomposition.
func (ex *Exec) DebugEndPlan(d *Definition, result bool) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.EndPlan(d, ex.V, result)
	}
}

// DebugBeginMethod reports a method invocation.
func (ex *Exec) DebugBeginMethod(d *Definition, method uint32) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.BeginMethod(d, method, ex.V)
	}
}

// DebugEndMethod reports the result of a method invocation.
func (ex *Exec) DebugEndMethod(d *Definition, result bool) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.EndMethod(d, ex.V, result)
	}
}

// DebugBeginBranch reports a branch attempt.
func (ex *Exec) DebugBeginBranch(d *Definition, branch uint32) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.BeginBranch(d, branch, ex.V)
	}
}

// DebugEndBranch reports the result of a branch attempt.
func (ex *Exec) DebugEndBranch(d *Definition, result bool) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.EndBranch(d, ex.V, result)
	}
}

// DebugCapturePendingTask records the parent of a scheduled task.
func (ex *Exec) DebugCapturePendingTask(task uint32) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.CapturePendingTask(task)
	}
}

// DebugBeginTask reports a task execution.
func (ex *Exec) DebugBeginTask(d *Definition, task uint32) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.BeginTask(d, task, ex.V)
	}
}

// DebugEndTask reports the result of a task execution.
func (ex *Exec) DebugEndTask(d *Definition, result bool) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.EndTask(d, ex.V, result)
	}
}

// DebugBeginCondition reports a condition evaluation.
func (ex *Exec) DebugBeginCondition(d *Definition, condition uint32) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.BeginCondition(d, condition, ex.V)
	}
}

// DebugEndCondition reports the result of a condition evaluation.
func (ex *Exec) DebugEndCondition(d *Definition, result bool) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.EndCondition(d, ex.V, result)
	}
}

// DebugBeginAxiom reports an axiom evaluation.
func (ex *Exec) DebugBeginAxiom(d *Definition, axiom uint32) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.BeginAxiom(d, axiom, ex.V)
	}
}

// DebugEndAxiom reports the result of an axiom evaluation.
func (ex *Exec) DebugEndAxiom(d *Definition, result bool) {
	if !DebugEnabled {
		return
	}
	if debugger := ex.debugger(); debugger != nil {
		debugger.EndAxiom(d, ex.V, result)
	}
}

// DebugTables is the compact form of DebugMetadata emitted by generated
// planners: records are flattened into fixed-width integer rows, in the field
// order of the corresponding Debug* struct.
type DebugTables struct {
	SourceFile        string
	Strings           []string
	Values            []uint32 // Flags, Text, ResolvedText, SourceLine, VariableSlot
	VariableStringIDs []uint32
	// Kind, ID, FirstArgument, ArgumentCount, FirstChildRef, ChildCount,
	// OutputValue, ResolvedIndex, SourceLine, Internal
	Conditions           []uint32
	ConditionExpressions []string
	ConditionChildRefs   []uint32
	Tasks                []uint32 // Kind, ID, FirstArgument, ArgumentCount, SourceLine, PlanStepHead
	Branches             []uint32 // ID, Condition, FirstTask, TaskCount, SourceLine
	// ID, FirstParameter, ParameterCount, FirstBranch, BranchCount, SourceLine,
	// VariableSlotMask words
	Methods []uint64
	// ID, FirstParameter, ParameterCount, Condition, SourceLine,
	// VariableSlotMask words
	Axioms            []uint64
	Constants         []uint32 // GroupID, ID, Value, SourceLine
	CallTermSlotCount uint32
	FactSlotCount     uint32
	SourceFiles       []string
	// Source ranges: SourceFileIndex, BeginLine, BeginColumn, EndLine, EndColumn
	ValueSources, ConditionSources, TaskSources, BranchSources []uint32
	MethodSources, AxiomSources, ConstantSources               []uint32
}

func debugSourceRanges(rows []uint32) []DebugSourceRange {
	ranges := make([]DebugSourceRange, len(rows)/5)
	for i := range ranges {
		r := rows[i*5:]
		ranges[i] = DebugSourceRange{SourceFileIndex: r[0], BeginLine: r[1], BeginColumn: r[2], EndLine: r[3], EndColumn: r[4]}
	}
	return ranges
}

func debugSlotMask(words []uint64) (mask [DebugSlotMaskWords]uint64) {
	copy(mask[:], words)
	return mask
}

// NewDebugMetadata expands the compact tables of a generated planner.
func NewDebugMetadata(t *DebugTables) *DebugMetadata {
	m := &DebugMetadata{
		SourceFile:         t.SourceFile,
		Strings:            t.Strings,
		VariableStringIDs:  t.VariableStringIDs,
		ConditionChildRefs: t.ConditionChildRefs,
		CallTermSlotCount:  t.CallTermSlotCount,
		FactSlotCount:      t.FactSlotCount,
		SourceFiles:        t.SourceFiles,
		ValueSources:       debugSourceRanges(t.ValueSources),
		ConditionSources:   debugSourceRanges(t.ConditionSources),
		TaskSources:        debugSourceRanges(t.TaskSources),
		BranchSources:      debugSourceRanges(t.BranchSources),
		MethodSources:      debugSourceRanges(t.MethodSources),
		AxiomSources:       debugSourceRanges(t.AxiomSources),
		ConstantSources:    debugSourceRanges(t.ConstantSources),
	}
	m.Values = make([]DebugValue, len(t.Values)/5)
	for i := range m.Values {
		r := t.Values[i*5:]
		m.Values[i] = DebugValue{Flags: r[0], Text: r[1], ResolvedText: r[2], SourceLine: r[3], VariableSlot: r[4]}
	}
	m.Conditions = make([]DebugCondition, len(t.Conditions)/10)
	for i := range m.Conditions {
		r := t.Conditions[i*10:]
		m.Conditions[i] = DebugCondition{Kind: r[0], ID: r[1], FirstArgument: r[2], ArgumentCount: r[3],
			FirstChildRef: r[4], ChildCount: r[5], OutputValue: r[6], ResolvedIndex: r[7], SourceLine: r[8],
			Expression: t.ConditionExpressions[i], Internal: r[9] != 0}
	}
	m.Tasks = make([]DebugTask, len(t.Tasks)/6)
	for i := range m.Tasks {
		r := t.Tasks[i*6:]
		m.Tasks[i] = DebugTask{Kind: r[0], ID: r[1], FirstArgument: r[2], ArgumentCount: r[3], SourceLine: r[4], PlanStepHead: r[5]}
	}
	m.Branches = make([]DebugBranch, len(t.Branches)/5)
	for i := range m.Branches {
		r := t.Branches[i*5:]
		m.Branches[i] = DebugBranch{ID: r[0], Condition: r[1], FirstTask: r[2], TaskCount: r[3], SourceLine: r[4]}
	}
	const methodWidth = 6 + DebugSlotMaskWords
	m.Methods = make([]DebugMethod, len(t.Methods)/methodWidth)
	for i := range m.Methods {
		r := t.Methods[i*methodWidth:]
		m.Methods[i] = DebugMethod{ID: uint32(r[0]), FirstParameter: uint32(r[1]), ParameterCount: uint32(r[2]),
			FirstBranch: uint32(r[3]), BranchCount: uint32(r[4]), SourceLine: uint32(r[5]),
			VariableSlotMask: debugSlotMask(r[6:methodWidth])}
	}
	const axiomWidth = 5 + DebugSlotMaskWords
	m.Axioms = make([]DebugAxiom, len(t.Axioms)/axiomWidth)
	for i := range m.Axioms {
		r := t.Axioms[i*axiomWidth:]
		m.Axioms[i] = DebugAxiom{ID: uint32(r[0]), FirstParameter: uint32(r[1]), ParameterCount: uint32(r[2]),
			Condition: uint32(r[3]), SourceLine: uint32(r[4]), VariableSlotMask: debugSlotMask(r[5:axiomWidth])}
	}
	m.Constants = make([]DebugConstant, len(t.Constants)/4)
	for i := range m.Constants {
		r := t.Constants[i*4:]
		m.Constants[i] = DebugConstant{GroupID: r[0], ID: r[1], Value: r[2], SourceLine: r[3]}
	}
	return m
}
