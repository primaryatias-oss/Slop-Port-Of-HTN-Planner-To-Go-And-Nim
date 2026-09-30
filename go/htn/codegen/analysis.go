// Package codegen emits native Go planners from the compiler IR.
//
// The emitted code follows the execution model of the original C generator
// (HTNCCodeGenerator) operation by operation: conditions are compiled into
// continuation-passing control flow with explicit checkpoints, facts with
// unbound variables become choice points, axioms are inlined at their call
// sites between generated begin/end helpers, and methods/tasks suspend into
// an explicit call-frame dispatcher instead of recursing natively.
package codegen

import (
	"sort"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
)

// boundSet is a set of variable string ids statically known to be bound.
type boundSet map[uint32]struct{}

func (s boundSet) has(id uint32) bool { _, ok := s[id]; return ok }

func (s boundSet) clone() boundSet {
	c := make(boundSet, len(s))
	for k := range s {
		c[k] = struct{}{}
	}
	return c
}

type conditionAnalysis struct {
	boundAfter               boundSet
	mayProduceMultipleValues bool
}

func isOutputParameterName(name string) bool {
	return strings.HasPrefix(name, "out_") || strings.HasPrefix(name, "io_")
}

// analyzeCondition computes the statically guaranteed bindings after a
// condition and whether a fact may produce several solutions
// (AnalyzeCondition in the original generator).
func analyzeCondition(ir *compiler.IR, index uint32, bound boundSet) conditionAnalysis {
	result := conditionAnalysis{boundAfter: bound.clone()}
	if index == compiler.NoIndex || int(index) >= len(ir.Conditions) {
		return result
	}
	c := &ir.Conditions[index]
	bind := func(v *compiler.IRValue) {
		if v.Kind == compiler.ValueVariable && v.VariableSlot != compiler.NoIndex {
			result.boundAfter[v.Text] = struct{}{}
		}
	}
	switch c.Kind {
	case compiler.IRCondFact:
		for i := uint32(0); i < c.ArgumentCount; i++ {
			v := &ir.Values[c.FirstArgument+i]
			if v.Kind == compiler.ValueVariable && v.VariableSlot != compiler.NoIndex && !bound.has(v.Text) {
				result.mayProduceMultipleValues = true
			}
			bind(v)
		}
	case compiler.IRCondAxiom:
		if axiom := ir.FindAxiom(c.ID, c.ArgumentCount); axiom != nil {
			for i := uint32(0); i < axiom.ParameterCount; i++ {
				if isOutputParameterName(ir.Strings.Get(ir.Values[axiom.FirstParameter+i].Text)) {
					bind(&ir.Values[c.FirstArgument+i])
				}
			}
		}
	case compiler.IRCondAssignment, compiler.IRCondCallBind:
		if c.OutputValue != compiler.NoIndex {
			bind(&ir.Values[c.OutputValue])
		}
	case compiler.IRCondListSplit:
		for i := uint32(1); i < c.ArgumentCount; i++ {
			bind(&ir.Values[c.FirstArgument+i])
		}
	case compiler.IRCondAnd:
		for i := uint32(0); i < c.ChildCount; i++ {
			result.boundAfter = analyzeCondition(ir, ir.ConditionChildRefs[c.FirstChildRef+i], result.boundAfter).boundAfter
		}
	case compiler.IRCondOr, compiler.IRCondAlt:
		// Only bindings produced by every alternative are statically guaranteed.
		for i := uint32(0); i < c.ChildCount; i++ {
			child := analyzeCondition(ir, ir.ConditionChildRefs[c.FirstChildRef+i], bound)
			if i == 0 {
				result.boundAfter = child.boundAfter
			} else {
				for k := range result.boundAfter {
					if !child.boundAfter.has(k) {
						delete(result.boundAfter, k)
					}
				}
			}
		}
	}
	return result
}

func addWrite(v *compiler.IRValue, writes map[uint32]uint32) {
	if v.Kind == compiler.ValueVariable && v.VariableSlot != compiler.NoIndex {
		if _, exists := writes[v.Text]; !exists {
			writes[v.Text] = v.VariableSlot
		}
	}
}

// collectConditionWrites gathers the slots a condition may bind
// (CollectGeneratedConditionWrites).
func collectConditionWrites(ir *compiler.IR, index uint32, bound boundSet, writes map[uint32]uint32) {
	if index == compiler.NoIndex || int(index) >= len(ir.Conditions) {
		return
	}
	c := &ir.Conditions[index]
	switch c.Kind {
	case compiler.IRCondFact:
		for i := uint32(0); i < c.ArgumentCount; i++ {
			v := &ir.Values[c.FirstArgument+i]
			if v.Kind == compiler.ValueVariable && v.VariableSlot != compiler.NoIndex && !bound.has(v.Text) {
				addWrite(v, writes)
			}
		}
	case compiler.IRCondListSplit:
		for i := uint32(1); i < c.ArgumentCount; i++ {
			v := &ir.Values[c.FirstArgument+i]
			if v.Kind == compiler.ValueVariable && v.VariableSlot != compiler.NoIndex && !bound.has(v.Text) {
				addWrite(v, writes)
			}
		}
	case compiler.IRCondAssignment, compiler.IRCondCallBind:
		if c.OutputValue != compiler.NoIndex && int(c.OutputValue) < len(ir.Values) {
			v := &ir.Values[c.OutputValue]
			if v.Kind == compiler.ValueVariable && !bound.has(v.Text) {
				addWrite(v, writes)
			}
		}
	case compiler.IRCondAxiom:
		axiom := ir.FindAxiom(c.ID, c.ArgumentCount)
		if axiom == nil {
			break
		}
		count := axiom.ParameterCount
		if c.ArgumentCount < count {
			count = c.ArgumentCount
		}
		for i := uint32(0); i < count; i++ {
			parameter := &ir.Values[axiom.FirstParameter+i]
			if int(parameter.Text) >= len(ir.Strings.Values) || !isOutputParameterName(ir.Strings.Values[parameter.Text]) {
				continue
			}
			addWrite(&ir.Values[c.FirstArgument+i], writes)
		}
	case compiler.IRCondAnd:
		current := bound
		for i := uint32(0); i < c.ChildCount; i++ {
			ref := c.FirstChildRef + i
			if int(ref) >= len(ir.ConditionChildRefs) {
				break
			}
			child := ir.ConditionChildRefs[ref]
			collectConditionWrites(ir, child, current, writes)
			current = analyzeCondition(ir, child, current).boundAfter
		}
	case compiler.IRCondOr, compiler.IRCondAlt:
		for i := uint32(0); i < c.ChildCount; i++ {
			ref := c.FirstChildRef + i
			if int(ref) < len(ir.ConditionChildRefs) {
				collectConditionWrites(ir, ir.ConditionChildRefs[ref], bound, writes)
			}
		}
	case compiler.IRCondNot:
		if c.ChildCount != 0 && int(c.FirstChildRef) < len(ir.ConditionChildRefs) {
			collectConditionWrites(ir, ir.ConditionChildRefs[c.FirstChildRef], bound, writes)
		}
	}
}

type checkpointPlan struct {
	id    int
	slots []uint32
}

func buildCheckpointPlan(ir *compiler.IR, id int, condition uint32, bound boundSet) checkpointPlan {
	writes := map[uint32]uint32{}
	collectConditionWrites(ir, condition, bound, writes)
	plan := checkpointPlan{id: id}
	for _, slot := range writes {
		plan.slots = append(plan.slots, slot)
	}
	sort.Slice(plan.slots, func(i, j int) bool { return plan.slots[i] < plan.slots[j] })
	return plan
}

// staticComparison evaluates a comparison of two compile-time constants
// (TryEvaluateStaticBuiltinComparison).
func staticComparison(ir *compiler.IR, c *compiler.IRCondition) (result bool, ok bool) {
	if c.Kind != compiler.IRCondComparison || c.ArgumentCount != 2 {
		return false, false
	}
	left := resolveStaticValue(ir, c.FirstArgument)
	right := resolveStaticValue(ir, c.FirstArgument+1)
	if left == nil || right == nil || left.Kind == compiler.ValueVariable || right.Kind == compiler.ValueVariable ||
		left.Kind == compiler.ValueArithmetic || right.Kind == compiler.ValueArithmetic {
		return false, false
	}
	numeric := func(v *compiler.IRValue) (float64, bool) {
		switch v.AtomType {
		case atom.KindInt:
			return float64(v.Literal.Int()), true
		case atom.KindFloat:
			return float64(v.Literal.Float()), true
		}
		return 0, false
	}
	l, leftNumeric := numeric(left)
	r, rightNumeric := numeric(right)
	if c.ID == compiler.CompareEqual || c.ID == compiler.CompareNotEqual {
		var equal bool
		if leftNumeric && rightNumeric {
			equal = l == r
		} else if left.AtomType == right.AtomType {
			switch left.AtomType {
			case atom.KindBool:
				equal = left.Literal.Bool() == right.Literal.Bool()
			case atom.KindString, atom.KindSymbol:
				equal = left.Text == right.Text
			default:
				return false, false
			}
		} else {
			equal = false
		}
		if c.ID == compiler.CompareEqual {
			return equal, true
		}
		return !equal, true
	}
	if !leftNumeric || !rightNumeric {
		return false, false
	}
	switch c.ID {
	case compiler.CompareLess:
		return l < r, true
	case compiler.CompareLessEqual:
		return l <= r, true
	case compiler.CompareGreater:
		return l > r, true
	case compiler.CompareGreaterEqual:
		return l >= r, true
	}
	return false, false
}

// resolveStaticValue follows constant aliases (ResolveGeneratedStaticValue).
func resolveStaticValue(ir *compiler.IR, index uint32) *compiler.IRValue {
	for depth := 0; depth < 16; depth++ {
		if int(index) >= len(ir.Values) {
			return nil
		}
		v := &ir.Values[index]
		if v.Kind != compiler.ValueConstant {
			return v
		}
		found := false
		for i := range ir.Constants {
			if ir.Constants[i].ID == v.Text {
				index = ir.Constants[i].Value
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return nil
}
