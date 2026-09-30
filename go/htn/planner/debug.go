package planner

// Debugger receives generated execution events from planners translated with
// instrumentation enabled. A nil Debugger disables event capture.
type Debugger interface {
	BeginPlan(d *Definition, method int)
	EndPlan(d *Definition, succeeded bool)
	BeginMethod(d *Definition, method int)
	EndMethod(d *Definition, succeeded bool)
	BeginBranch(d *Definition, branch int)
	EndBranch(d *Definition, succeeded bool)
	BeginCondition(d *Definition, condition int, choice bool)
	EndCondition(d *Definition, condition int, succeeded bool, choice bool)
	BeginAxiom(d *Definition, axiom int)
	EndAxiom(d *Definition, succeeded bool)
	BeginTask(d *Definition, task int)
	EndTask(d *Definition, succeeded bool)
	CapturePendingTask(task int)
}

// DebugMetadata describes the compiled domain for debuggers. It is emitted by
// instrumented translations only.
type DebugMetadata struct {
	SourceFile  string
	SourceFiles []string
	Strings     []string
	Methods     []DebugMethod
	Branches    []DebugBranch
	Tasks       []DebugTask
	Conditions  []DebugCondition
	Axioms      []DebugAxiom
	Values      []DebugValue
	Variables   []string
}

// DebugSourceRange is a 1-based source range of a compiled element.
type DebugSourceRange struct {
	File                                       uint32
	BeginLine, BeginColumn, EndLine, EndColumn uint32
}

// DebugMethod describes one compiled method.
type DebugMethod struct {
	Name                           string
	FirstParameter, ParameterCount uint32
	FirstBranch, BranchCount       uint32
	Source                         DebugSourceRange
}

// DebugBranch describes one compiled branch.
type DebugBranch struct {
	Name                 string
	Condition            int
	FirstTask, TaskCount uint32
	Source               DebugSourceRange
}

// DebugTask describes one compiled task occurrence.
type DebugTask struct {
	Kind                         uint8
	Name                         string
	Expression                   string
	FirstArgument, ArgumentCount uint32
	Source                       DebugSourceRange
}

// DebugCondition describes one compiled condition node.
type DebugCondition struct {
	Kind                         uint8
	Expression                   string
	FirstArgument, ArgumentCount uint32
	Children                     []int
	Internal                     bool
	Source                       DebugSourceRange
}

// DebugAxiom describes one compiled axiom.
type DebugAxiom struct {
	Name                           string
	FirstParameter, ParameterCount uint32
	Source                         DebugSourceRange
}

// DebugValue describes one compiled value.
type DebugValue struct {
	Text         string
	Resolved     string
	VariableSlot int
	IsVariable   bool
}
