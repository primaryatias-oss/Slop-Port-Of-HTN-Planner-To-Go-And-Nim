package compiler

import (
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

// CallableSignature is the internal overload key "name/arity".
func CallableSignature(name string, argumentCount int) string {
	return name + "/" + strconv.Itoa(argumentCount)
}

type stringSet map[string]struct{}

func (s stringSet) has(v string) bool { _, ok := s[v]; return ok }

func (s stringSet) add(v string) bool {
	if _, ok := s[v]; ok {
		return false
	}
	s[v] = struct{}{}
	return true
}

func (s stringSet) clone() stringSet {
	c := make(stringSet, len(s))
	for k := range s {
		c[k] = struct{}{}
	}
	return c
}

type assignmentScopeKind uint8

const (
	scopeLeaf assignmentScopeKind = iota
	scopeSequence
	scopeAlternatives
	scopeNegation
)

type assignmentScopeNode struct {
	kind        assignmentScopeKind
	destination string
	rng         lexer.Range
	uses        []string
	children    []assignmentScopeNode
}

// assignmentParameterIsInitiallyUsed: axiom output parameters declare slots
// but their first use may initialize them.
func assignmentParameterIsInitiallyUsed(name string, isAxiom bool) bool {
	return !isAxiom || (!strings.HasPrefix(name, "out_") && !strings.HasPrefix(name, "io_"))
}

func validateAssignmentScope(node *assignmentScopeNode, seen stringSet, report func(lexer.Range, string)) bool {
	valid := true
	for _, use := range node.uses {
		seen.add(use)
	}
	if node.destination != "" {
		if !seen.add(node.destination) {
			report(node.rng, "Assignment destination '?"+node.destination+
				"' has already been declared or used; assignment must declare a fresh variable")
			valid = false
		}
	}
	entry := seen.clone()
	for i := range node.children {
		if node.kind == scopeAlternatives || node.kind == scopeNegation {
			branch := entry.clone()
			valid = validateAssignmentScope(&node.children[i], branch, report) && valid
			// A later declaration cannot reuse a name mentioned in any alternative.
			for k := range branch {
				seen[k] = struct{}{}
			}
		} else {
			valid = validateAssignmentScope(&node.children[i], seen, report) && valid
		}
	}
	return valid
}

func collectAssignmentUses(v *Value, uses *[]string) {
	if v == nil {
		return
	}
	if v.Kind == ValueVariable {
		*uses = append(*uses, ValueText(v))
	}
	for _, a := range v.CallArguments {
		collectAssignmentUses(a, uses)
	}
	for _, o := range v.ArithmeticOperands {
		collectAssignmentUses(o, uses)
	}
}

func buildAssignmentScope(c *Condition) assignmentScopeNode {
	var node assignmentScopeNode
	if c == nil {
		return node
	}
	node.rng = c.Range
	if c.Kind == CondAssignment {
		node.destination = ValueText(c.Output)
	} else {
		collectAssignmentUses(c.Output, &node.uses)
	}
	for _, a := range c.Arguments {
		collectAssignmentUses(a, &node.uses)
	}
	switch c.Kind {
	case CondAnd:
		node.kind = scopeSequence
	case CondNot:
		node.kind = scopeNegation
	case CondOr, CondAlt:
		node.kind = scopeAlternatives
	default:
		node.kind = scopeLeaf
	}
	for _, child := range c.Children {
		node.children = append(node.children, buildAssignmentScope(child))
	}
	return node
}

type validator struct {
	files       []string
	diagnostics *DiagnosticSink
}

func (v *validator) file(index uint32) string {
	if int(index) < len(v.files) {
		return v.files[index]
	}
	return ""
}

func (v *validator) errorAt(n *Node, message string) {
	v.diagnostics.Error(v.file(n.FileIndex), message, RecoveryRecoverable, n.Range)
}

func (v *validator) validateAssignments(c *Condition, parameters []*Value, isAxiom bool) bool {
	seen := stringSet{}
	for _, p := range parameters {
		if assignmentParameterIsInitiallyUsed(ValueText(p), isAxiom) {
			seen.add(ValueText(p))
		}
	}
	scope := buildAssignmentScope(c)
	return validateAssignmentScope(&scope, seen, func(r lexer.Range, message string) {
		file := ""
		if c != nil {
			file = v.file(c.FileIndex)
		}
		v.diagnostics.Error(file, message, RecoveryRecoverable, r)
	})
}

func declareVariables(c *Condition, variables stringSet) {
	if c == nil {
		return
	}
	declare := func(value *Value) {
		if value != nil && value.Kind == ValueVariable {
			variables.add(ValueText(value))
		}
	}
	switch c.Kind {
	case CondFact, CondAxiom:
		for _, a := range c.Arguments {
			declare(a)
		}
	case CondAssignment, CondCall:
		declare(c.Output)
	case CondSplit:
		if len(c.Arguments) == 3 {
			declare(c.Arguments[1])
			declare(c.Arguments[2])
		}
	default:
		for _, child := range c.Children {
			declareVariables(child, variables)
		}
	}
}

func (v *validator) validateValueUse(value *Value, variables stringSet, usage string) bool {
	if value == nil {
		return true
	}
	if value.Kind == ValueVariable && !variables.has(ValueText(value)) {
		v.errorAt(&value.Node, "Unknown variable '"+ValueText(value)+"' used by "+usage+".")
		return false
	}
	valid := true
	if value.Kind == ValueCall {
		for _, a := range value.CallArguments {
			valid = v.validateValueUse(a, variables, "call expression '"+ValueText(value.CallID)+"'") && valid
		}
	}
	if value.Kind == ValueArithmetic {
		for _, o := range value.ArithmeticOperands {
			valid = v.validateValueUse(o, variables, usage) && valid
		}
	}
	return valid
}

func (v *validator) validateConditionVariables(c *Condition, variables stringSet, singletons stringSet) bool {
	if c == nil {
		return true
	}
	valid := true
	checkSingletonUse := func(value *Value, usage string) {
		if value != nil && value.Kind == ValueVariable && strings.HasPrefix(ValueText(value), "any_") {
			v.errorAt(&value.Node, "Singleton variable '?"+ValueText(value)+
				"' may only be used as a fact argument; it is never bound and cannot be consumed by "+usage+".")
			valid = false
		}
	}
	switch c.Kind {
	case CondFact:
		for _, a := range c.Arguments {
			if a.Kind == ValueVariable && strings.HasPrefix(ValueText(a), "any_") && !singletons.add(ValueText(a)) {
				v.errorAt(&a.Node, "Singleton variable '?"+ValueText(a)+"' may only appear once in the same scope.")
				valid = false
			}
		}
	case CondAssignment, CondCall:
		isAssignment := c.Kind == CondAssignment
		usage := "an assignment"
		if !isAssignment {
			usage = "callterm '" + ValueText(c.ID) + "'"
		}
		for _, a := range c.Arguments {
			checkSingletonUse(a, usage)
			valid = v.validateValueUse(a, variables, usage) && valid
		}
		if isAssignment {
			checkSingletonUse(c.Output, "an assignment destination")
		} else {
			checkSingletonUse(c.Output, "a callterm output")
		}
	case CondComparison, CondSplit:
		for i, a := range c.Arguments {
			usage := "a built-in comparison"
			if c.Kind != CondComparison {
				if i == 0 {
					usage = "split_list input"
				} else {
					usage = "split_list output"
				}
			}
			checkSingletonUse(a, usage)
			valid = v.validateValueUse(a, variables, usage) && valid
		}
	default:
		for _, child := range c.Children {
			valid = v.validateConditionVariables(child, variables, singletons) && valid
		}
	}
	return valid
}

func (v *validator) validateMethodVariables(m *Method) bool {
	parameters := stringSet{}
	for _, p := range m.Parameters {
		parameters.add(ValueText(p))
	}
	valid := true
	for _, branch := range m.Branches {
		variables := parameters.clone()
		singletons := stringSet{}
		valid = v.validateAssignments(branch.Precondition, m.Parameters, false) && valid
		declareVariables(branch.Precondition, variables)
		valid = v.validateConditionVariables(branch.Precondition, variables, singletons) && valid
		for _, task := range branch.Tasks {
			var usage string
			switch task.Kind {
			case TaskPrimitive:
				usage = "primitive task '"
			case TaskDeferred:
				usage = "deferred call '"
			default:
				usage = "compound task '"
			}
			usage += ValueText(task.ID) + "'"
			for _, a := range task.Arguments {
				if a.Kind == ValueVariable && strings.HasPrefix(ValueText(a), "any_") {
					v.errorAt(&a.Node, "Singleton variable '?"+ValueText(a)+
						"' may only be used as a fact argument; it is never bound and cannot be consumed by "+usage+".")
					valid = false
				}
				valid = v.validateValueUse(a, variables, usage) && valid
			}
		}
	}
	return valid
}

func collectAxiomCalls(c *Condition, calls *[]string) {
	if c == nil {
		return
	}
	if c.Kind == CondAxiom {
		*calls = append(*calls, CallableSignature(ValueText(c.ID), len(c.Arguments)))
	}
	for _, child := range c.Children {
		collectAxiomCalls(child, calls)
	}
}

func (v *validator) validateAxiomCycles(axioms []*Axiom) bool {
	indexByID := map[string]int{}
	for i, a := range axioms {
		key := CallableSignature(a.ID, len(a.Parameters))
		if _, exists := indexByID[key]; !exists {
			indexByID[key] = i
		}
	}
	states := make([]uint8, len(axioms))
	var stack []int
	var visit func(int) bool
	visit = func(index int) bool {
		states[index] = 1
		stack = append(stack, index)
		var calls []string
		collectAxiomCalls(axioms[index].Body, &calls)
		for _, call := range calls {
			target, ok := indexByID[call]
			if !ok {
				continue
			}
			if states[target] == 0 {
				if !visit(target) {
					return false
				}
			} else if states[target] == 1 {
				begin := 0
				for i, s := range stack {
					if s == target {
						begin = i
						break
					}
				}
				var cycle []string
				for _, item := range stack[begin:] {
					cycle = append(cycle, CallableSignature(axioms[item].ID, len(axioms[item].Parameters)))
				}
				smallest := 0
				for i := range cycle {
					if cycle[i] < cycle[smallest] {
						smallest = i
					}
				}
				rotated := append(append([]string{}, cycle[smallest:]...), cycle[:smallest]...)
				message := "Cyclic axiom dependency: "
				for _, id := range rotated {
					message += id + " -> "
				}
				message += rotated[0]
				v.errorAt(&axioms[index].Node, message)
				return false
			}
		}
		stack = stack[:len(stack)-1]
		states[index] = 2
		return true
	}
	for i := range axioms {
		if states[i] == 0 && !visit(i) {
			return false
		}
	}
	return true
}

type overridable interface {
	declarationKey() string
	isBase() bool
	overridesDomain() string
	parameterCount() int
	node() *Node
	id() string
}

func (m *Method) declarationKey() string  { return CallableSignature(m.ID, len(m.Parameters)) }
func (m *Method) isBase() bool            { return m.IsBase }
func (m *Method) overridesDomain() string { return m.OverridesDomain }
func (m *Method) parameterCount() int     { return len(m.Parameters) }
func (m *Method) node() *Node             { return &m.Node }
func (m *Method) id() string              { return m.ID }
func (a *Axiom) declarationKey() string   { return CallableSignature(a.ID, len(a.Parameters)) }
func (a *Axiom) isBase() bool             { return a.IsBase }
func (a *Axiom) overridesDomain() string  { return a.OverridesDomain }
func (a *Axiom) parameterCount() int      { return len(a.Parameters) }
func (a *Axiom) node() *Node              { return &a.Node }
func (a *Axiom) id() string               { return a.ID }

type overrideState struct {
	qualified      map[string]overridable
	effectiveOwner map[string]string
	specialization map[string]bool
}

func newOverrideState() *overrideState {
	return &overrideState{qualified: map[string]overridable{}, effectiveOwner: map[string]string{},
		specialization: map[string]bool{}}
}

func (v *validator) validateOverride(module *Domain, declaration overridable, kind string,
	baseDomains map[string]bool, state *overrideState) bool {
	key := declaration.declarationKey()
	qualifiedID := module.ID + "::" + key
	if declaration.isBase() && !module.IsBase {
		v.errorAt(declaration.node(), kind+" '"+qualifiedID+"' is declared base but domain '"+
			module.ID+"' is not a base domain")
		return false
	}
	if _, exists := state.qualified[qualifiedID]; exists {
		v.errorAt(declaration.node(), "Duplicate "+kind+" declaration '"+qualifiedID+"'")
		return false
	}
	if declaration.overridesDomain() != "" {
		baseQualifiedID := declaration.overridesDomain() + "::" + key
		base, baseExists := state.qualified[baseQualifiedID]
		baseDomain, baseDomainExists := baseDomains[declaration.overridesDomain()]
		if !baseExists || !baseDomainExists || !baseDomain || !state.specialization[baseQualifiedID] ||
			base.parameterCount() != declaration.parameterCount() ||
			state.effectiveOwner[key] != declaration.overridesDomain() {
			v.errorAt(declaration.node(), "Invalid "+kind+" override '"+qualifiedID+"' of '"+baseQualifiedID+"'")
			return false
		}
		state.specialization[qualifiedID] = module.IsBase
	} else {
		if owner, exists := state.effectiveOwner[key]; exists {
			v.errorAt(declaration.node(), kind+" '"+declaration.id()+"' already exists in domain '"+owner+"'")
			return false
		}
		state.specialization[qualifiedID] = declaration.isBase()
	}
	state.qualified[qualifiedID] = declaration
	state.effectiveOwner[key] = module.ID
	return true
}

func (v *validator) validateOverrideChains(modules []*Domain) bool {
	baseDomains := map[string]bool{}
	for _, m := range modules {
		if _, exists := baseDomains[m.ID]; !exists {
			baseDomains[m.ID] = m.IsBase
		}
	}
	qualifiedConstants := map[string]*Constant{}
	effectiveConstantOwner := map[string]string{}
	constantSpecialization := map[string]bool{}
	axiomState := newOverrideState()
	methodState := newOverrideState()
	valid := true
	for _, module := range modules {
		for _, group := range module.ConstantGroups {
			if group.IsBase && !module.IsBase {
				v.errorAt(&group.Node, "Constants block '"+module.ID+"::"+group.ID+
					"' is declared base but domain '"+module.ID+"' is not a base domain")
				valid = false
				continue
			}
			if group.OverridesDomain != "" {
				if isBase, exists := baseDomains[group.OverridesDomain]; !exists || !isBase {
					v.errorAt(&group.Node, "Constants block '"+module.ID+"::"+group.ID+
						"' overrides missing or non-base domain '"+group.OverridesDomain+"'")
					valid = false
					continue
				}
			}
			for _, constant := range group.Constants {
				qualifiedID := module.ID + "::" + constant.ID
				if group.OverridesDomain == "" {
					if owner, exists := effectiveConstantOwner[constant.ID]; exists {
						v.errorAt(&constant.Node, "Constant '"+constant.ID+"' already exists in domain '"+owner+"'")
						valid = false
						continue
					}
					constantSpecialization[qualifiedID] = group.IsBase
				} else {
					baseQualifiedID := group.OverridesDomain + "::" + constant.ID
					if _, exists := qualifiedConstants[baseQualifiedID]; !exists ||
						!constantSpecialization[baseQualifiedID] ||
						effectiveConstantOwner[constant.ID] != group.OverridesDomain {
						v.errorAt(&constant.Node, "Invalid constant override '"+qualifiedID+"' of '"+baseQualifiedID+"'")
						valid = false
						continue
					}
					constantSpecialization[qualifiedID] = module.IsBase
				}
				if _, exists := qualifiedConstants[qualifiedID]; !exists {
					qualifiedConstants[qualifiedID] = constant
				}
				effectiveConstantOwner[constant.ID] = module.ID
			}
		}
		for _, axiom := range module.Axioms {
			valid = v.validateOverride(module, axiom, "Axiom", baseDomains, axiomState) && valid
		}
		for _, method := range module.Methods {
			valid = v.validateOverride(module, method, "Method", baseDomains, methodState) && valid
		}
	}
	return valid
}

// ValidateDomainModules validates linked modules given in dependency-first
// order with the root module last.
func ValidateDomainModules(modules []*Domain, sourceFiles []string, requireTopLevelRoot bool,
	diagnostics *DiagnosticSink) bool {
	if len(modules) == 0 {
		return false
	}
	v := &validator{files: sourceFiles, diagnostics: diagnostics}
	valid := true
	root := modules[len(modules)-1]
	if requireTopLevelRoot && !root.IsTopLevel {
		diagnostics.Error(v.file(root.FileIndex), "Root domain must be top_level_domain", RecoveryRecoverable, root.Range)
		valid = false
	}
	domainIDs := stringSet{}
	topLevelMethods := 0
	for _, module := range modules {
		if !domainIDs.add(module.ID) {
			diagnostics.Error(v.file(module.FileIndex), "Duplicate domain id '"+module.ID+"'", RecoveryDependent, module.Range)
			valid = false
		}
		if module != root && module.IsTopLevel {
			diagnostics.Error(v.file(module.FileIndex), "Included domain cannot be top_level_domain",
				RecoveryRecoverable, module.Range)
			valid = false
		}
		for _, method := range module.Methods {
			if method.TopLevel {
				topLevelMethods++
			}
			if module != root && method.TopLevel {
				v.errorAt(&method.Node, "Included domain '"+module.ID+"' cannot declare top_level_method '"+method.ID+"'")
				valid = false
			}
			for _, parameter := range method.Parameters {
				if !strings.HasPrefix(ValueText(parameter), "inp_") {
					v.errorAt(&parameter.Node, "Method '"+method.ID+"' parameter '?"+ValueText(parameter)+
						"' must use the inp_ prefix")
					valid = false
				}
			}
			valid = v.validateMethodVariables(method) && valid
		}
		for _, axiom := range module.Axioms {
			valid = v.validateAssignments(axiom.Body, axiom.Parameters, true) && valid
			variables := stringSet{}
			singletons := stringSet{}
			for _, parameter := range axiom.Parameters {
				variables.add(ValueText(parameter))
			}
			declareVariables(axiom.Body, variables)
			valid = v.validateConditionVariables(axiom.Body, variables, singletons) && valid
		}
		for _, axiom := range module.Axioms {
			for _, parameter := range axiom.Parameters {
				name := ValueText(parameter)
				if !strings.HasPrefix(name, "inp_") && !strings.HasPrefix(name, "out_") && !strings.HasPrefix(name, "io_") {
					v.errorAt(&parameter.Node, "Axiom '"+axiom.ID+"' parameter '?"+name+
						"' must use an inp_, out_ or io_ prefix")
					valid = false
				}
			}
		}
	}
	if requireTopLevelRoot && root.IsTopLevel && topLevelMethods == 0 {
		diagnostics.Error(v.file(root.FileIndex), "Root top_level_domain has no top_level_method",
			RecoveryRecoverable, root.Range)
		valid = false
	}
	valid = v.validateOverrideChains(modules) && valid

	methods := stringSet{}
	for _, module := range modules {
		for _, method := range module.Methods {
			methods.add(CallableSignature(module.ID+"::"+method.ID, len(method.Parameters)))
			methods.add(CallableSignature(method.ID, len(method.Parameters)))
		}
	}
	for _, module := range modules {
		for _, method := range module.Methods {
			for _, branch := range method.Branches {
				for _, task := range branch.Tasks {
					if task.Kind == TaskPrimitive {
						continue
					}
					id := ValueText(task.ID)
					if !methods.has(CallableSignature(id, len(task.Arguments))) {
						prefix := "Compound method call '"
						if task.Kind == TaskDeferred {
							prefix = "Deferred method call '"
						}
						v.errorAt(&task.Node, prefix+id+"' cannot be resolved at link time: no overload accepts "+
							strconv.Itoa(len(task.Arguments))+" argument(s)")
						valid = false
					}
				}
			}
		}
	}

	var linked []*Axiom
	for _, module := range modules {
		for _, axiom := range module.Axioms {
			qualified := *axiom
			qualified.ID = module.ID + "::" + axiom.ID
			linked = append(linked, &qualified)
		}
	}
	effective := map[string]*Axiom{}
	for _, module := range modules {
		for _, axiom := range module.Axioms {
			effective[CallableSignature(axiom.ID, len(axiom.Parameters))] = axiom
		}
	}
	for _, module := range modules {
		for _, axiom := range module.Axioms {
			if effective[CallableSignature(axiom.ID, len(axiom.Parameters))] == axiom {
				linked = append(linked, axiom)
			}
		}
	}
	axiomSignatures := stringSet{}
	for _, axiom := range linked {
		axiomSignatures.add(CallableSignature(axiom.ID, len(axiom.Parameters)))
	}
	var validateAxiomCall func(*Condition)
	validateAxiomCall = func(c *Condition) {
		if c == nil {
			return
		}
		if c.Kind == CondAxiom && !axiomSignatures.has(CallableSignature(ValueText(c.ID), len(c.Arguments))) {
			v.errorAt(&c.Node, "Axiom call '"+ValueText(c.ID)+"' cannot be resolved at link time: no overload accepts "+
				strconv.Itoa(len(c.Arguments))+" argument(s)")
			valid = false
		}
		for _, child := range c.Children {
			validateAxiomCall(child)
		}
	}
	for _, module := range modules {
		for _, method := range module.Methods {
			for _, branch := range method.Branches {
				validateAxiomCall(branch.Precondition)
			}
		}
		for _, axiom := range module.Axioms {
			validateAxiomCall(axiom.Body)
		}
	}
	valid = v.validateAxiomCycles(linked) && valid
	return valid && !diagnostics.HasErrors()
}
