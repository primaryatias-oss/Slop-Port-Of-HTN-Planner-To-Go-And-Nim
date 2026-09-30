package atom

// Domain syntax prefixes (HTNDomainSyntax.h).
const (
	VariablePrefix      = '?'
	ConstantPrefix      = '@'
	AxiomCallPrefix     = '#'
	DeferredCallPrefix  = '&'
	PrimitiveTaskPrefix = '!'
	DeclarationPrefix   = ':'
)

// PlanStepKind classifies a plan step by the prefix of its head symbol.
type PlanStepKind uint8

const (
	PlanStepInvalid PlanStepKind = iota
	PlanStepPrimitiveTask
	PlanStepDeferredCall
)

// A runtime HTN call has one representation everywhere: a list whose first
// element is the call head symbol followed by the arguments: (head arg0 ... argN).
// Plan steps reuse that representation; the head prefix carries the step kind.

// MakeCall builds the call atom (head args...). It fails when head is nil or
// any argument is unbound (HTNAtom_CreateCall).
func MakeCall(head *Symbol, args ...Atom) (Atom, bool) {
	if head == nil {
		return Atom{}, false
	}
	elems := make([]Atom, 0, len(args)+1)
	elems = append(elems, NewSymbol(head))
	for _, arg := range args {
		if !arg.IsBound() {
			return Atom{}, false
		}
		elems = append(elems, arg)
	}
	return NewListOwned(elems), true
}

// MakeCallFromPointers is MakeCall over optional values: a nil or unbound
// argument makes the construction fail (HTNAtom_CreateCallFromPointers).
func MakeCallFromPointers(head *Symbol, args []*Atom) (Atom, bool) {
	if head == nil {
		return Atom{}, false
	}
	elems := make([]Atom, 0, len(args)+1)
	elems = append(elems, NewSymbol(head))
	for _, arg := range args {
		if arg == nil || !arg.IsBound() {
			return Atom{}, false
		}
		elems = append(elems, *arg)
	}
	return NewListOwned(elems), true
}

// IsValidCall reports whether call is a non-empty list headed by a symbol.
func IsValidCall(call Atom) bool {
	if call.kind != KindList || len(call.list.elems) < 1 {
		return false
	}
	head := call.list.elems[0]
	return head.kind == KindSymbol && head.sym != nil
}

// CallHead returns the head symbol of a valid call, or nil.
func CallHead(call Atom) *Symbol {
	if !IsValidCall(call) {
		return nil
	}
	return call.list.elems[0].sym
}

// CallArgumentCount returns the number of arguments of a valid call.
func CallArgumentCount(call Atom) int {
	if !IsValidCall(call) {
		return 0
	}
	return len(call.list.elems) - 1
}

// CallArgument returns argument i of a valid call.
func CallArgument(call Atom, i int) (Atom, bool) {
	if !IsValidCall(call) || i < 0 || i >= len(call.list.elems)-1 {
		return Atom{}, false
	}
	return call.list.elems[i+1], true
}

// CallArguments returns the arguments of a valid call (read-only slice).
func CallArguments(call Atom) []Atom {
	if !IsValidCall(call) {
		return nil
	}
	return call.list.elems[1:]
}

// IsPrimitiveTaskHead reports whether the symbol text starts with '!'.
func IsPrimitiveTaskHead(head *Symbol) bool {
	return head != nil && len(head.text) > 0 && head.text[0] == PrimitiveTaskPrefix
}

// IsDeferredCallHead reports whether the symbol text starts with '&'.
func IsDeferredCallHead(head *Symbol) bool {
	return head != nil && len(head.text) > 0 && head.text[0] == DeferredCallPrefix
}

// MakePrimitiveTaskHead returns the '!'-prefixed symbol for name.
func MakePrimitiveTaskHead(name string) *Symbol {
	if len(name) > 0 && name[0] == PrimitiveTaskPrefix {
		return Intern(name)
	}
	return Intern(string(PrimitiveTaskPrefix) + name)
}

// MakeDeferredCallHead returns the '&'-prefixed symbol for name.
func MakeDeferredCallHead(name string) *Symbol {
	if len(name) > 0 && name[0] == DeferredCallPrefix {
		return Intern(name)
	}
	return Intern(string(DeferredCallPrefix) + name)
}

// GetPlanStepKind classifies a plan step atom.
func GetPlanStepKind(step Atom) PlanStepKind {
	head := CallHead(step)
	if IsPrimitiveTaskHead(head) {
		return PlanStepPrimitiveTask
	}
	if IsDeferredCallHead(head) {
		return PlanStepDeferredCall
	}
	return PlanStepInvalid
}

// MakeCallFromDeferredPlanStep converts (&name args...) into (name args...).
func MakeCallFromDeferredPlanStep(step Atom) (Atom, bool) {
	if GetPlanStepKind(step) != PlanStepDeferredCall {
		return Atom{}, false
	}
	head := CallHead(step)
	elems := make([]Atom, 0, len(step.list.elems))
	elems = append(elems, NewSymbol(Intern(head.text[1:])))
	elems = append(elems, step.list.elems[1:]...)
	return NewListOwned(elems), true
}

// FormatPlanStep renders a plan step as "head arg1 arg2" with quoted strings,
// the format used throughout the original test-suite.
func FormatPlanStep(step Atom) string {
	head := CallHead(step)
	text := "<invalid task>"
	if head != nil {
		text = head.text
	}
	for _, arg := range CallArguments(step) {
		text += " " + ToString(arg, true)
	}
	return text
}

// FormatPlan renders every step of a plan list.
func FormatPlan(plan Atom) []string {
	elems := plan.Elements()
	result := make([]string, 0, len(elems))
	for _, step := range elems {
		result = append(result, FormatPlanStep(step))
	}
	return result
}
