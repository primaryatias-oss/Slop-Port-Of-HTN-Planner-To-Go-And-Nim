package compiler

import (
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

// NoIndex marks an absent IR reference.
const NoIndex uint32 = 0xFFFFFFFF

// MaxVariableSlots is the maximum number of distinct variables of a domain
// (HTN_GENERATED_MAX_VARIABLE_SLOTS).
const MaxVariableSlots = 256

// VariableSlotMaskWords is the number of 64-bit words of a slot mask.
const VariableSlotMaskWords = (MaxVariableSlots + 63) / 64

// SlotMask is a set of variable slots.
type SlotMask [VariableSlotMaskWords]uint64

// Has reports whether slot is in the mask.
func (m *SlotMask) Has(slot uint32) bool {
	if slot >= MaxVariableSlots {
		return false
	}
	return m[slot>>6]&(uint64(1)<<(slot&63)) != 0
}

// Add inserts slot into the mask (slots beyond the maximum are ignored; the
// builder reports that overflow separately).
func (m *SlotMask) Add(slot uint32) {
	if slot == NoIndex || slot >= MaxVariableSlots {
		return
	}
	m[slot>>6] |= uint64(1) << (slot & 63)
}

// Slots returns the slots in ascending order.
func (m *SlotMask) Slots() []uint32 {
	var slots []uint32
	for word := uint32(0); word < VariableSlotMaskWords; word++ {
		bits := m[word]
		for bit := uint32(0); bits != 0; bit++ {
			if bits&1 != 0 {
				slots = append(slots, word*64+bit)
			}
			bits >>= 1
		}
	}
	return slots
}

// Count returns the number of slots in the mask.
func (m *SlotMask) Count() int { return len(m.Slots()) }

// IRConditionKind uses the numeric values of HTNGeneratedConditionKind.
type IRConditionKind uint8

const (
	IRCondFact       IRConditionKind = 0
	IRCondAxiom      IRConditionKind = 1
	IRCondAnd        IRConditionKind = 2
	IRCondOr         IRConditionKind = 3
	IRCondAlt        IRConditionKind = 4
	IRCondNot        IRConditionKind = 5
	IRCondCall       IRConditionKind = 6
	IRCondCallBind   IRConditionKind = 7
	IRCondComparison IRConditionKind = 8
	IRCondListSplit  IRConditionKind = 9
	IRCondAssignment IRConditionKind = 10
)

// Built-in comparison operators (HTNGeneratedBuiltinComparisonOperator).
const (
	CompareEqual uint32 = iota
	CompareNotEqual
	CompareLess
	CompareLessEqual
	CompareGreater
	CompareGreaterEqual
)

// Built-in list split operations.
const (
	ListSplit      uint32 = 0
	ListSplitFront uint32 = 1
	ListSplitBack  uint32 = 2
)

// IRTaskKind uses the numeric values of HTNGeneratedTaskKind.
type IRTaskKind uint8

const (
	IRTaskCompound  IRTaskKind = 0
	IRTaskPrimitive IRTaskKind = 1
	IRTaskDeferred  IRTaskKind = 2
)

// SourceLocation is a file index plus range.
type SourceLocation struct {
	FileIndex uint32
	Range     lexer.Range
}

// StringTable interns compiler strings.
type StringTable struct {
	Values  []string
	indices map[string]uint32
}

// Add interns value and returns its index.
func (t *StringTable) Add(value string) uint32 {
	if t.indices == nil {
		t.indices = make(map[string]uint32)
	}
	if index, ok := t.indices[value]; ok {
		return index
	}
	index := uint32(len(t.Values))
	t.Values = append(t.Values, value)
	t.indices[value] = index
	return index
}

// Find returns the index of value or NoIndex.
func (t *StringTable) Find(value string) uint32 {
	if index, ok := t.indices[value]; ok {
		return index
	}
	return NoIndex
}

// Get returns the string at index or "".
func (t *StringTable) Get(index uint32) string {
	if int(index) < len(t.Values) {
		return t.Values[index]
	}
	return ""
}

// IRValue is one lowered value. Literal holds the static value of literals.
type IRValue struct {
	Kind                 ValueKind
	Text                 uint32
	DebugText            uint32
	SourceLine           uint32
	VariableSlot         uint32
	StaticValueIndex     uint32
	ArithmeticExpression uint32
	Source               SourceLocation
	AtomType             atom.Kind
	Literal              atom.Atom
	DebugAsVariable      bool
}

func newIRValue() IRValue {
	return IRValue{VariableSlot: NoIndex, StaticValueIndex: NoIndex, ArithmeticExpression: NoIndex, DebugAsVariable: true}
}

// IRArithmeticExpression is an operator applied to operand values.
type IRArithmeticExpression struct {
	Operator ArithmeticOperator
	Operands []IRValue
}

// IRStaticValue is a compile-time constant atom.
type IRStaticValue struct {
	Text     uint32
	AtomType atom.Kind
	Literal  atom.Atom
}

// IRCondition is one lowered condition node.
type IRCondition struct {
	// AssignmentGuardValue checks an assignment destination before any lowered
	// initializer call runs.
	AssignmentGuardValue uint32
	Kind                 IRConditionKind
	ID                   uint32
	FirstArgument        uint32
	ArgumentCount        uint32
	FirstChildRef        uint32
	ChildCount           uint32
	OutputValue          uint32
	ResolvedIndex        uint32
	SourceLine           uint32
	DomainExpression     string
	Source               SourceLocation
	DebugExpression      string
	DebugSource          SourceLocation
	DebugCondition       uint32
	DebugInternal        bool
}

// IRTask is one task occurrence of a branch.
type IRTask struct {
	Kind             IRTaskKind
	ID               uint32
	PlanStepHead     uint32 // string id of the prefixed plan-step head, NoIndex for compound tasks
	FirstArgument    uint32
	ArgumentCount    uint32
	SourceLine       uint32
	DomainExpression string
	Source           SourceLocation
}

// IRTaskCallExpression is a nested (call ...) evaluated before its task.
type IRTaskCallExpression struct {
	ID               uint32
	CallTermSlot     uint32
	OutputSlot       uint32
	SourceLine       uint32
	DomainExpression string
	Arguments        []IRValue
	Source           SourceLocation
}

// IRBranch is one method branch.
type IRBranch struct {
	ID, Condition, FirstTask, TaskCount, SourceLine uint32
	Source                                          SourceLocation
}

// IRMethod is one linked method.
type IRMethod struct {
	ID, FirstParameter, ParameterCount, FirstBranch, BranchCount uint32
	IsTopLevel, IsExternallyDecomposable                         bool
	SourceLine                                                   uint32
	VariableSlotMask                                             SlotMask
	Source                                                       SourceLocation
}

// IRAxiom is one linked axiom.
type IRAxiom struct {
	ID, FirstParameter, ParameterCount, Condition, SourceLine uint32
	VariableSlotMask                                          SlotMask
	Source                                                    SourceLocation
}

// IRConstant is one constant.
type IRConstant struct {
	GroupID, ID, Value, SourceLine uint32
	Source                         SourceLocation
}

// IR is the complete lowered representation of a linked domain.
type IR struct {
	DomainID                       string
	SourceFiles                    []string
	RuntimeBacktrackingSupport     bool
	Strings                        StringTable
	Values                         []IRValue
	ArithmeticExpressions          []IRArithmeticExpression
	StaticValues                   []IRStaticValue
	VariableStringIDs              []uint32
	DebugInternalVariableStringIDs map[uint32]bool
	VariableSlotByStringID         map[uint32]uint32
	PreparedSymbols                []uint32
	preparedSymbolSlot             map[uint32]uint32
	Conditions                     []IRCondition
	FactStringIDs                  []uint32
	FactSlotByStringID             map[uint32]uint32
	CallTermStringIDs              []uint32
	CallTermSlotByStringID         map[uint32]uint32
	ConditionChildRefs             []uint32
	Tasks                          []IRTask
	TaskCallExpressions            [][]IRTaskCallExpression
	SyntheticTaskCallCount         uint32
	Branches                       []IRBranch
	Methods                        []IRMethod
	Axioms                         []IRAxiom
	Constants                      []IRConstant
	Error                          string
}

// SetError records the first error.
func (ir *IR) SetError(message string) {
	if ir.Error == "" {
		ir.Error = message
	}
}

// HasError reports whether an error was recorded.
func (ir *IR) HasError() bool { return ir.Error != "" }

// FindMethod returns the first method with the given name string id and arity.
func (ir *IR) FindMethod(stringID uint32, argumentCount uint32) int {
	for i := range ir.Methods {
		if ir.Methods[i].ID == stringID && ir.Methods[i].ParameterCount == argumentCount {
			return i
		}
	}
	return -1
}

// FindAxiom returns the first axiom with the given name string id and arity.
func (ir *IR) FindAxiom(stringID uint32, argumentCount uint32) *IRAxiom {
	for i := range ir.Axioms {
		if ir.Axioms[i].ID == stringID && ir.Axioms[i].ParameterCount == argumentCount {
			return &ir.Axioms[i]
		}
	}
	return nil
}

// ChildConditions returns the child condition indices of a condition.
func (ir *IR) ChildConditions(c *IRCondition) []uint32 {
	return ir.ConditionChildRefs[c.FirstChildRef : c.FirstChildRef+c.ChildCount]
}

// MarkValueSlots adds the variable slots used by value (including arithmetic
// operands) to mask.
func (ir *IR) MarkValueSlots(mask *SlotMask, value *IRValue) {
	if value.Kind == ValueVariable && value.VariableSlot != NoIndex {
		mask.Add(value.VariableSlot)
	}
	if value.Kind == ValueArithmetic && int(value.ArithmeticExpression) < len(ir.ArithmeticExpressions) {
		for i := range ir.ArithmeticExpressions[value.ArithmeticExpression].Operands {
			ir.MarkValueSlots(mask, &ir.ArithmeticExpressions[value.ArithmeticExpression].Operands[i])
		}
	}
}
