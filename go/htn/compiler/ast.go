package compiler

import (
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

// Node carries the source location shared by every AST element.
type Node struct {
	Range     lexer.Range
	FileIndex uint32
}

// ValueKind classifies AST values.
type ValueKind uint8

const (
	ValueIdentifier ValueKind = iota
	ValueLiteral
	ValueVariable
	ValueConstant
	ValueCall
	ValueArithmetic
)

// ArithmeticOperator enumerates arithmetic expression operators.
type ArithmeticOperator uint8

const (
	OpAdd ArithmeticOperator = iota
	OpSubtract
	OpMultiply
	OpDivide
	OpModulo
	OpIncrement
	OpDecrement
)

// Value is an AST value: identifier, literal, variable, constant reference,
// nested call or arithmetic expression. Variable and constant values store
// their name (without prefix) as a string atom.
type Value struct {
	Node
	Kind               ValueKind
	Atom               atom.Atom
	CallID             *Value
	CallArguments      []*Value
	ArithmeticOp       ArithmeticOperator
	ArithmeticOperands []*Value
}

// ConditionKind classifies AST conditions.
type ConditionKind uint8

const (
	CondFact ConditionKind = iota
	CondAxiom
	CondCall
	CondAssignment
	CondComparison
	CondSplit
	CondAnd
	CondOr
	CondAlt
	CondNot
)

// Condition is an AST condition. Comparison conditions store the operator
// (== != < <= > >= as 0..5) in Operator; split conditions store the split
// operation (split_list, split_list_front, split_list_back as 0..2).
type Condition struct {
	Node
	Kind      ConditionKind
	ID        *Value
	Arguments []*Value
	Output    *Value
	Operator  uint32
	Children  []*Condition
}

// TaskKind classifies branch tasks.
type TaskKind uint8

const (
	TaskPrimitive TaskKind = iota
	TaskCompound
	TaskDeferred
)

// Task is one task of a branch task list.
type Task struct {
	Node
	Kind      TaskKind
	ID        *Value
	Arguments []*Value
}

// Branch is one method branch: an optional precondition and its tasks.
type Branch struct {
	Node
	ID           string
	Precondition *Condition
	Tasks        []*Task
}

// Method is a method declaration.
type Method struct {
	Node
	ID              string
	Parameters      []*Value
	Branches        []*Branch
	TopLevel        bool
	IsBase          bool
	OverridesDomain string
}

// Axiom is an axiom declaration.
type Axiom struct {
	Node
	ID              string
	IsBase          bool
	OverridesDomain string
	Parameters      []*Value
	Body            *Condition
}

// Constant is one constant declaration.
type Constant struct {
	Node
	ID    string
	Value *Value
}

// ConstantGroup is one (:constants ...) block.
type ConstantGroup struct {
	Node
	ID              string
	IsBase          bool
	OverridesDomain string
	Constants       []*Constant
}

// Domain is the AST of one domain file or of a linked domain.
type Domain struct {
	ID             string
	Range          lexer.Range
	FileIndex      uint32
	IsTopLevel     bool
	IsBase         bool
	ConstantGroups []*ConstantGroup
	Axioms         []*Axiom
	Methods        []*Method
}

// ValueText returns the unquoted text of a value's atom ("" for nil).
func ValueText(v *Value) string {
	if v == nil {
		return ""
	}
	return atom.ToString(v.Atom, false)
}
