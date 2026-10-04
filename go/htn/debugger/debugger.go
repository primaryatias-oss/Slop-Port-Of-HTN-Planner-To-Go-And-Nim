// Package debugger records generated planner execution as a tree of
// structured events (port of HTNGeneratedDebugger). It depends only on the
// generated debug metadata and on the events emitted by planners built with
// the "htndebug" tag:
//
//	d := debugger.New()
//	d.SetEnabled(true)
//	unit.SetGeneratedDebugger(d)
//	unit.Decompose()
//	for _, node := range d.Nodes() { ... }
package debugger

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/planner"
)

// NoIndex marks an absent index.
const NoIndex = planner.NoIndex

// NodeKind is the kind of a recorded node.
type NodeKind uint8

// Node kinds.
const (
	Plan NodeKind = iota
	Method
	Branch
	Fact
	Axiom
	And
	Or
	Alt
	Not
	Call
	CallBind
	BuiltinComparison
	BuiltinListSplit
	Task
	UnknownCondition
)

var nodeKindNames = [...]string{"Plan", "Method", "Branch", "Fact", "Axiom", "And", "Or", "Alt", "Not", "Call",
	"CallBind", "BuiltinComparison", "BuiltinListSplit", "Task", "UnknownCondition"}

// String returns the kind name.
func (k NodeKind) String() string {
	if int(k) < len(nodeKindNames) {
		return nodeKindNames[k]
	}
	return "?"
}

// TokenKind classifies a title token for display.
type TokenKind uint8

// Title token kinds.
const (
	TokenNormal TokenKind = iota
	TokenResult
	TokenVariable
	TokenConstant
	TokenStringLiteral
	TokenCallExpression
)

// SourceLocation is a node's domain source range.
type SourceLocation struct {
	DomainPath                       string
	Line, Column, EndLine, EndColumn uint32
}

// TitleToken is one display token of a node title.
type TitleToken struct {
	Kind        TokenKind
	Text        string
	SpaceBefore bool
}

// VariableValue is one bound variable captured at an event.
type VariableValue struct {
	Slot  uint32
	Name  string
	Value atom.Atom
}

// ConstantValue is one constant referenced by a node.
type ConstantValue struct {
	Name, Value string
}

// Node is one recorded method, branch, condition, axiom, task or plan.
type Node struct {
	EventNodeID       uint32
	MetadataIndex     uint32
	ParentEventNodeID uint32
	Kind              NodeKind
	Source            SourceLocation
	DisplayName       string
	Started           bool
	Completed         bool
	Succeeded         bool
	TitleTokens       []TitleToken
	Constants         []ConstantValue
	VariablesBefore   []VariableValue
	VariablesAfter    []VariableValue
	ScopeVariableMask []uint64
	Children          []uint32
}

type pendingTask struct {
	metadataIndex, parentEventNodeID uint32
}

// Debugger is the event recorder. It implements planner.Debugger.
type Debugger struct {
	enabled    bool
	domainPath string
	nodes      []Node
	openNodes  []uint32
	// Hidden conditions still emit balanced events. Keep their nesting
	// separate so their End event cannot close a visible parent.
	conditionVisibility []bool
	pendingTasks        []pendingTask
	revision            uint64
}

// New creates a disabled debugger.
func New() *Debugger { return &Debugger{} }

// SetEnabled enables capture; disabling also resets the capture.
func (d *Debugger) SetEnabled(enabled bool) {
	d.enabled = enabled
	if !enabled {
		d.Reset("")
	}
}

// IsEnabled reports whether events are captured.
func (d *Debugger) IsEnabled() bool { return d.enabled }

// Reset starts a new capture for the domain at domainPath.
func (d *Debugger) Reset(domainPath string) {
	d.domainPath = domainPath
	d.nodes = d.nodes[:0]
	d.openNodes = d.openNodes[:0]
	d.conditionVisibility = d.conditionVisibility[:0]
	d.pendingTasks = d.pendingTasks[:0]
	d.revision++
}

// DomainPath returns the domain of the current capture.
func (d *Debugger) DomainPath() string { return d.domainPath }

// Revision changes on every reset.
func (d *Debugger) Revision() uint64 { return d.revision }

// Nodes returns the recorded nodes; a node's EventNodeID is its index.
func (d *Debugger) Nodes() []Node { return d.nodes }

// FindNode returns the node with the given id or nil.
func (d *Debugger) FindNode(id uint32) *Node {
	if int(id) < len(d.nodes) {
		return &d.nodes[id]
	}
	return nil
}

func metadata(domain *planner.Definition) *planner.DebugMetadata {
	if domain == nil {
		return nil
	}
	return domain.DebugMetadata
}

// BeginPlan implements planner.Debugger.
func (d *Debugger) BeginPlan(domain *planner.Definition, method uint32, values []atom.Atom) {
	m := metadata(domain)
	if !d.enabled || m == nil || int(method) >= len(m.Methods) {
		return
	}
	debugMethod := &m.Methods[method]
	d.beginNode(Plan, method, debugMethod.SourceLine, resolveString(m, debugMethod.ID, "plan"), NoIndex)
	node := &d.nodes[len(d.nodes)-1]
	node.DisplayName = "(" + node.DisplayName + ")"
	applySourceLocation(node, m, m.MethodSources, method, uint32(len(m.Methods)))
	d.setCurrentNodeScopeMask(debugMethod.VariableSlotMask)
	d.captureCurrentVariables(m, values, true)
}

// EndPlan implements planner.Debugger.
func (d *Debugger) EndPlan(domain *planner.Definition, values []atom.Atom, result bool) {
	d.endNode(metadata(domain), values, result)
}

// BeginMethod implements planner.Debugger.
func (d *Debugger) BeginMethod(domain *planner.Definition, method uint32, values []atom.Atom) {
	m := metadata(domain)
	if !d.enabled || m == nil || int(method) >= len(m.Methods) {
		return
	}
	debugMethod := &m.Methods[method]
	d.beginNode(Method, method, debugMethod.SourceLine, resolveString(m, debugMethod.ID, "method"), NoIndex)
	node := &d.nodes[len(d.nodes)-1]
	buildDeclarationTitle(m, "method", debugMethod.FirstParameter, debugMethod.ParameterCount, node)
	applySourceLocation(node, m, m.MethodSources, method, uint32(len(m.Methods)))
	d.setCurrentNodeScopeMask(debugMethod.VariableSlotMask)
	d.captureCurrentVariables(m, values, true)
}

// EndMethod implements planner.Debugger.
func (d *Debugger) EndMethod(domain *planner.Definition, values []atom.Atom, result bool) {
	d.endNode(metadata(domain), values, result)
}

// BeginBranch implements planner.Debugger.
func (d *Debugger) BeginBranch(domain *planner.Definition, branch uint32, values []atom.Atom) {
	m := metadata(domain)
	if !d.enabled || m == nil || int(branch) >= len(m.Branches) {
		return
	}
	debugBranch := &m.Branches[branch]
	d.beginNode(Branch, branch, debugBranch.SourceLine, resolveString(m, debugBranch.ID, "branch"), NoIndex)
	node := &d.nodes[len(d.nodes)-1]
	node.DisplayName = "(" + node.DisplayName + " ...)"
	applySourceLocation(node, m, m.BranchSources, branch, uint32(len(m.Branches)))
	node.ScopeVariableMask = buildBranchScopeMask(m, debugBranch)
	d.addParentMethodParametersToScope(m, node)
	d.captureCurrentVariables(m, values, true)
}

// EndBranch implements planner.Debugger.
func (d *Debugger) EndBranch(domain *planner.Definition, values []atom.Atom, result bool) {
	d.endNode(metadata(domain), values, result)
}

// CapturePendingTask implements planner.Debugger.
func (d *Debugger) CapturePendingTask(task uint32) {
	if !d.enabled {
		return
	}
	parent := NoIndex
	if len(d.openNodes) > 0 {
		parent = d.openNodes[len(d.openNodes)-1]
	}
	d.pendingTasks = append(d.pendingTasks, pendingTask{metadataIndex: task, parentEventNodeID: parent})
}

// BeginTask implements planner.Debugger.
func (d *Debugger) BeginTask(domain *planner.Definition, task uint32, values []atom.Atom) {
	m := metadata(domain)
	if !d.enabled || m == nil || int(task) >= len(m.Tasks) {
		return
	}
	debugTask := &m.Tasks[task]
	parentOverride := NoIndex
	for i := len(d.pendingTasks) - 1; i >= 0; i-- {
		if d.pendingTasks[i].metadataIndex == task {
			parentOverride = d.pendingTasks[i].parentEventNodeID
			d.pendingTasks = append(d.pendingTasks[:i], d.pendingTasks[i+1:]...)
			break
		}
	}
	fallback := "compound"
	if debugTask.Kind == planner.DebugTaskPrimitive {
		fallback = "primitive"
	}
	d.beginNode(Task, task, debugTask.SourceLine, resolveString(m, debugTask.ID, fallback), parentOverride)
	node := &d.nodes[len(d.nodes)-1]
	applySourceLocation(node, m, m.TaskSources, task, uint32(len(m.Tasks)))
	buildTaskTitleTokens(m, debugTask, node)
	d.captureCurrentVariables(m, values, true)
}

// EndTask implements planner.Debugger.
func (d *Debugger) EndTask(domain *planner.Definition, values []atom.Atom, result bool) {
	d.endNode(metadata(domain), values, result)
}

// BeginAxiom implements planner.Debugger.
func (d *Debugger) BeginAxiom(domain *planner.Definition, axiom uint32, values []atom.Atom) {
	m := metadata(domain)
	if !d.enabled || m == nil || int(axiom) >= len(m.Axioms) {
		return
	}
	debugAxiom := &m.Axioms[axiom]
	d.beginNode(Axiom, axiom, debugAxiom.SourceLine, resolveString(m, debugAxiom.ID, "axiom"), NoIndex)
	node := &d.nodes[len(d.nodes)-1]
	buildDeclarationTitle(m, "axiom", debugAxiom.FirstParameter, debugAxiom.ParameterCount, node)
	applySourceLocation(node, m, m.AxiomSources, axiom, uint32(len(m.Axioms)))
	d.setCurrentNodeScopeMask(debugAxiom.VariableSlotMask)
	d.captureCurrentVariables(m, values, true)
}

// EndAxiom implements planner.Debugger.
func (d *Debugger) EndAxiom(domain *planner.Definition, values []atom.Atom, result bool) {
	d.endNode(metadata(domain), values, result)
}

// BeginCondition implements planner.Debugger.
func (d *Debugger) BeginCondition(domain *planner.Definition, condition uint32, values []atom.Atom) {
	if !d.enabled {
		return
	}
	m := metadata(domain)
	visible := m != nil && int(condition) < len(m.Conditions) && !m.Conditions[condition].Internal
	d.conditionVisibility = append(d.conditionVisibility, visible)
	if !visible {
		return
	}
	// Composite conditions pre-create their metadata subtree so every
	// precondition shows, including terms skipped by short-circuiting. When
	// execution reaches one of them, reuse that pending node.
	parent := NoIndex
	if len(d.openNodes) > 0 {
		parent = d.openNodes[len(d.openNodes)-1]
	}
	if existing := d.findPendingConditionChild(parent, condition); existing != NoIndex {
		d.nodes[existing].Started = true
		d.openNodes = append(d.openNodes, existing)
	} else {
		debugCondition := &m.Conditions[condition]
		var displayName string
		if debugCondition.Kind == planner.DebugConditionBuiltinComparison {
			displayName = builtinComparisonName(debugCondition.ID)
		} else {
			displayName = resolveString(m, debugCondition.ID, conditionName(debugCondition.Kind))
		}
		d.beginNode(conditionKind(debugCondition.Kind), condition, debugCondition.SourceLine, displayName, NoIndex)
		node := &d.nodes[len(d.nodes)-1]
		applySourceLocation(node, m, m.ConditionSources, condition, uint32(len(m.Conditions)))
		node.Started = true
		buildConditionTitleTokens(m, debugCondition, node)
		if debugCondition.Kind == planner.DebugConditionNot {
			if len(node.ScopeVariableMask) == 0 {
				node.ScopeVariableMask = make([]uint64, planner.DebugSlotMaskWords)
			}
			collectConditionScopeSlots(m, condition, node.ScopeVariableMask)
		}
		d.createConditionMetadataChildren(m, node.EventNodeID, debugCondition)
	}
	d.captureCurrentVariables(m, values, true)
}

// EndCondition implements planner.Debugger.
func (d *Debugger) EndCondition(domain *planner.Definition, values []atom.Atom, result bool) {
	if !d.enabled || len(d.conditionVisibility) == 0 {
		return
	}
	visible := d.conditionVisibility[len(d.conditionVisibility)-1]
	d.conditionVisibility = d.conditionVisibility[:len(d.conditionVisibility)-1]
	if visible {
		d.endNode(metadata(domain), values, result)
	}
}

func buildDeclarationTitle(m *planner.DebugMetadata, declaration string, firstParameter, parameterCount uint32, node *Node) {
	title := "(:" + declaration + " (" + node.DisplayName
	for i := uint32(0); i < parameterCount && int(firstParameter+i) < len(m.Values); i++ {
		title += " " + resolveString(m, m.Values[firstParameter+i].Text, "?")
	}
	node.DisplayName = title + ") ...)"
}

func applySourceLocation(node *Node, m *planner.DebugMetadata, ranges []planner.DebugSourceRange, index, count uint32) {
	if m == nil || ranges == nil || index >= count || int(index) >= len(ranges) {
		return
	}
	r := &ranges[index]
	if int(r.SourceFileIndex) < len(m.SourceFiles) {
		node.Source.DomainPath = m.SourceFiles[r.SourceFileIndex]
	}
	node.Source.Line = r.BeginLine
	node.Source.Column = r.BeginColumn
	node.Source.EndLine = r.EndLine
	node.Source.EndColumn = r.EndColumn
}

func resolveString(m *planner.DebugMetadata, id uint32, fallback string) string {
	if m != nil && int(id) < len(m.Strings) {
		return m.Strings[id]
	}
	return fallback
}

func conditionKind(kind uint32) NodeKind {
	switch kind {
	case planner.DebugConditionFact:
		return Fact
	case planner.DebugConditionAxiom:
		return Axiom
	case planner.DebugConditionAnd:
		return And
	case planner.DebugConditionOr:
		return Or
	case planner.DebugConditionAlt:
		return Alt
	case planner.DebugConditionNot:
		return Not
	case planner.DebugConditionCall:
		return Call
	case planner.DebugConditionAssignment, planner.DebugConditionCallBind:
		return CallBind
	case planner.DebugConditionBuiltinComparison:
		return BuiltinComparison
	case planner.DebugConditionBuiltinListSplit:
		return BuiltinListSplit
	}
	return UnknownCondition
}

func builtinComparisonName(operator uint32) string {
	switch operator {
	case 0:
		return "=="
	case 1:
		return "!="
	case 2:
		return "<"
	case 3:
		return "<="
	case 4:
		return ">"
	case 5:
		return ">="
	}
	return "comparison"
}

func conditionName(kind uint32) string {
	switch kind {
	case planner.DebugConditionAnd:
		return "and"
	case planner.DebugConditionOr:
		return "or"
	case planner.DebugConditionAlt:
		return "alt"
	case planner.DebugConditionNot:
		return "not"
	case planner.DebugConditionCall:
		return "call"
	case planner.DebugConditionAssignment:
		return "="
	case planner.DebugConditionCallBind:
		return "call-bind"
	case planner.DebugConditionBuiltinComparison:
		return "comparison"
	case planner.DebugConditionBuiltinListSplit:
		return "split_list"
	case planner.DebugConditionAxiom:
		return "axiom"
	case planner.DebugConditionFact:
		return "fact"
	}
	return "condition"
}

func addTitleToken(node *Node, kind TokenKind, text string) {
	if text == "" {
		return
	}
	node.TitleTokens = append(node.TitleTokens, TitleToken{Kind: kind, Text: text, SpaceBefore: true})
}

func addConstantValue(node *Node, name, value string) {
	for _, existing := range node.Constants {
		if existing.Name == name {
			return
		}
	}
	node.Constants = append(node.Constants, ConstantValue{Name: name, Value: value})
}

func addValueTitleToken(m *planner.DebugMetadata, valueIndex uint32, node *Node) {
	if m == nil || int(valueIndex) >= len(m.Values) {
		addTitleToken(node, TokenNormal, "<invalid>")
		return
	}
	value := &m.Values[valueIndex]
	// The metadata keeps the original domain expression (constant names,
	// '?' prefixes and string quotes) rather than the resolved value.
	text := resolveString(m, value.Text, "?")
	switch {
	case value.Flags&planner.DebugValueVariable != 0:
		addTitleToken(node, TokenVariable, text)
	case text != "" && text[0] == '@':
		addTitleToken(node, TokenConstant, text)
		addConstantValue(node, text, resolveString(m, value.ResolvedText, "<unresolved>"))
	case value.Flags&planner.DebugValueStringLiteral != 0:
		addTitleToken(node, TokenStringLiteral, text)
	case value.Flags&planner.DebugValueCallExpression != 0:
		addTitleToken(node, TokenCallExpression, text)
	default:
		addTitleToken(node, TokenNormal, text)
	}
}

func buildTaskTitleTokens(m *planner.DebugMetadata, task *planner.DebugTask, node *Node) {
	node.TitleTokens = node.TitleTokens[:0]
	var head string
	if (task.Kind == planner.DebugTaskPrimitive || task.Kind == planner.DebugTaskDeferred) && task.PlanStepHead != NoIndex {
		fallback := "&deferred"
		if task.Kind == planner.DebugTaskPrimitive {
			fallback = "!primitive"
		}
		head = resolveString(m, task.PlanStepHead, fallback)
	} else {
		fallback := "compound"
		if task.Kind == planner.DebugTaskPrimitive {
			fallback = "!primitive"
		}
		head = resolveString(m, task.ID, fallback)
		if task.Kind == planner.DebugTaskPrimitive && (head == "" || head[0] != '!') {
			head = "!" + head
		}
	}
	addTitleToken(node, TokenNormal, "("+head)
	for i := uint32(0); i < task.ArgumentCount; i++ {
		addValueTitleToken(m, task.FirstArgument+i, node)
	}
	addTitleToken(node, TokenNormal, ")")
	node.TitleTokens[len(node.TitleTokens)-1].SpaceBefore = false
	var name strings.Builder
	for _, token := range node.TitleTokens {
		if name.Len() > 0 && token.SpaceBefore {
			name.WriteByte(' ')
		}
		name.WriteString(token.Text)
	}
	node.DisplayName = name.String()
}

func buildConditionTitleTokens(m *planner.DebugMetadata, condition *planner.DebugCondition, node *Node) {
	node.TitleTokens = node.TitleTokens[:0]
	node.DisplayName = condition.Expression
	expression := node.DisplayName
	head, callName := false, false
	// Split the compiler-formatted source for display only.
	for i := 0; i < len(expression); {
		if expression[i] == ' ' {
			i++
			continue
		}
		begin := i
		first := expression[i]
		i++
		if first == '"' {
			for i < len(expression) {
				c := expression[i]
				i++
				if c == '\\' && i < len(expression) {
					i++
				} else if c == '"' {
					break
				}
			}
		} else if first != '(' && first != ')' {
			for i < len(expression) && expression[i] != ' ' && expression[i] != '(' && expression[i] != ')' {
				i++
			}
		}
		text := expression[begin:i]
		kind := TokenNormal
		switch {
		case first == atom.VariablePrefix:
			kind = TokenVariable
		case first == atom.ConstantPrefix:
			kind = TokenConstant
		case first == '"':
			kind = TokenStringLiteral
		case text == "call" || callName:
			kind = TokenCallExpression
		case head && first != ')':
			kind = TokenResult
		}
		addTitleToken(node, kind, text)
		node.TitleTokens[len(node.TitleTokens)-1].SpaceBefore = begin == 0 || expression[begin-1] == ' '
		head = first == '('
		callName = text == "call"
		if kind == TokenConstant && m != nil {
			for v := range m.Values {
				if text == resolveString(m, m.Values[v].Text, "") {
					addConstantValue(node, text, resolveString(m, m.Values[v].ResolvedText, "<unresolved>"))
					break
				}
			}
		}
	}
}

func isConditionNodeKind(kind NodeKind) bool {
	switch kind {
	case Fact, Axiom, And, Or, Alt, Not, Call, CallBind, BuiltinComparison, BuiltinListSplit, UnknownCondition:
		return true
	}
	return false
}

func (d *Debugger) findPendingConditionChild(parent, condition uint32) uint32 {
	if parent == NoIndex || int(parent) >= len(d.nodes) {
		return NoIndex
	}
	for _, child := range d.nodes[parent].Children {
		if int(child) >= len(d.nodes) {
			continue
		}
		node := &d.nodes[child]
		if !node.Started && node.MetadataIndex == condition && isConditionNodeKind(node.Kind) {
			return child
		}
	}
	return NoIndex
}

func (d *Debugger) createConditionMetadataNode(m *planner.DebugMetadata, condition, parent uint32) uint32 {
	if m == nil || int(condition) >= len(m.Conditions) {
		return NoIndex
	}
	debugCondition := &m.Conditions[condition]
	if debugCondition.Internal {
		return NoIndex
	}
	node := Node{
		EventNodeID:       uint32(len(d.nodes)),
		MetadataIndex:     condition,
		ParentEventNodeID: parent,
		Kind:              conditionKind(debugCondition.Kind),
		Source:            SourceLocation{DomainPath: d.domainPath, Line: debugCondition.SourceLine, EndLine: debugCondition.SourceLine},
	}
	applySourceLocation(&node, m, m.ConditionSources, condition, uint32(len(m.Conditions)))
	if debugCondition.Kind == planner.DebugConditionBuiltinComparison {
		node.DisplayName = builtinComparisonName(debugCondition.ID)
	} else {
		node.DisplayName = resolveString(m, debugCondition.ID, conditionName(debugCondition.Kind))
	}
	buildConditionTitleTokens(m, debugCondition, &node)
	if parent != NoIndex && int(parent) < len(d.nodes) {
		node.ScopeVariableMask = append([]uint64(nil), d.nodes[parent].ScopeVariableMask...)
		d.nodes[parent].Children = append(d.nodes[parent].Children, node.EventNodeID)
	}
	if debugCondition.Kind == planner.DebugConditionNot {
		if len(node.ScopeVariableMask) == 0 {
			node.ScopeVariableMask = make([]uint64, planner.DebugSlotMaskWords)
		}
		collectConditionScopeSlots(m, condition, node.ScopeVariableMask)
	}
	d.nodes = append(d.nodes, node)
	id := uint32(len(d.nodes) - 1)
	d.createConditionMetadataChildren(m, id, debugCondition)
	return id
}

func (d *Debugger) createConditionMetadataChildren(m *planner.DebugMetadata, parent uint32, condition *planner.DebugCondition) {
	if m == nil || len(m.ConditionChildRefs) == 0 || condition.ChildCount == 0 {
		return
	}
	for i := uint32(0); i < condition.ChildCount; i++ {
		ref := condition.FirstChildRef + i
		if int(ref) >= len(m.ConditionChildRefs) {
			continue
		}
		d.createConditionMetadataNode(m, m.ConditionChildRefs[ref], parent)
	}
}

func (d *Debugger) beginNode(kind NodeKind, metadataIndex, line uint32, name string, parentOverride uint32) {
	node := Node{
		EventNodeID:   uint32(len(d.nodes)),
		MetadataIndex: metadataIndex,
		Kind:          kind,
		Started:       true,
		Source:        SourceLocation{DomainPath: d.domainPath, Line: line, EndLine: line},
		DisplayName:   name,
	}
	switch {
	case parentOverride != NoIndex:
		node.ParentEventNodeID = parentOverride
	case len(d.openNodes) > 0:
		node.ParentEventNodeID = d.openNodes[len(d.openNodes)-1]
	default:
		node.ParentEventNodeID = NoIndex
	}
	if node.ParentEventNodeID != NoIndex && int(node.ParentEventNodeID) < len(d.nodes) {
		parent := &d.nodes[node.ParentEventNodeID]
		node.ScopeVariableMask = append([]uint64(nil), parent.ScopeVariableMask...)
		parent.Children = append(parent.Children, node.EventNodeID)
	}
	d.nodes = append(d.nodes, node)
	d.openNodes = append(d.openNodes, uint32(len(d.nodes)-1))
}

func (d *Debugger) setCurrentNodeScopeMask(mask [planner.DebugSlotMaskWords]uint64) {
	if len(d.openNodes) == 0 {
		return
	}
	d.nodes[d.openNodes[len(d.openNodes)-1]].ScopeVariableMask = append([]uint64(nil), mask[:]...)
}

func markValueSlot(m *planner.DebugMetadata, valueIndex uint32, mask []uint64) {
	if m == nil || int(valueIndex) >= len(m.Values) {
		return
	}
	value := &m.Values[valueIndex]
	if value.Flags&planner.DebugValueVariable == 0 || value.VariableSlot == NoIndex {
		return
	}
	word := value.VariableSlot >> 6
	if int(word) >= len(mask) {
		return
	}
	mask[word] |= uint64(1) << (value.VariableSlot & 63)
}

func collectConditionScopeSlots(m *planner.DebugMetadata, condition uint32, mask []uint64) {
	if m == nil || int(condition) >= len(m.Conditions) {
		return
	}
	c := &m.Conditions[condition]
	for i := uint32(0); i < c.ArgumentCount; i++ {
		markValueSlot(m, c.FirstArgument+i, mask)
	}
	if c.OutputValue != NoIndex {
		markValueSlot(m, c.OutputValue, mask)
	}
	if len(m.ConditionChildRefs) == 0 {
		return
	}
	for i := uint32(0); i < c.ChildCount; i++ {
		if ref := c.FirstChildRef + i; int(ref) < len(m.ConditionChildRefs) {
			collectConditionScopeSlots(m, m.ConditionChildRefs[ref], mask)
		}
	}
}

// collectExportedConditionScopeSlots skips NOT subtrees: their bindings are
// local and never part of the enclosing branch scope.
func collectExportedConditionScopeSlots(m *planner.DebugMetadata, condition uint32, mask []uint64) {
	if m == nil || int(condition) >= len(m.Conditions) {
		return
	}
	c := &m.Conditions[condition]
	for i := uint32(0); i < c.ArgumentCount; i++ {
		markValueSlot(m, c.FirstArgument+i, mask)
	}
	if c.OutputValue != NoIndex {
		markValueSlot(m, c.OutputValue, mask)
	}
	if c.Kind == planner.DebugConditionNot || len(m.ConditionChildRefs) == 0 {
		return
	}
	for i := uint32(0); i < c.ChildCount; i++ {
		if ref := c.FirstChildRef + i; int(ref) < len(m.ConditionChildRefs) {
			collectExportedConditionScopeSlots(m, m.ConditionChildRefs[ref], mask)
		}
	}
}

func buildBranchScopeMask(m *planner.DebugMetadata, branch *planner.DebugBranch) []uint64 {
	mask := make([]uint64, planner.DebugSlotMaskWords)
	collectExportedConditionScopeSlots(m, branch.Condition, mask)
	if len(m.Tasks) == 0 || len(m.Values) == 0 {
		return mask
	}
	for i := uint32(0); i < branch.TaskCount; i++ {
		taskIndex := branch.FirstTask + i
		if int(taskIndex) >= len(m.Tasks) {
			continue
		}
		task := &m.Tasks[taskIndex]
		for a := uint32(0); a < task.ArgumentCount; a++ {
			markValueSlot(m, task.FirstArgument+a, mask)
		}
	}
	return mask
}

func (d *Debugger) addParentMethodParametersToScope(m *planner.DebugMetadata, node *Node) {
	if m == nil || len(m.Methods) == 0 || node.ParentEventNodeID == NoIndex || int(node.ParentEventNodeID) >= len(d.nodes) {
		return
	}
	parent := &d.nodes[node.ParentEventNodeID]
	if (parent.Kind != Method && parent.Kind != Plan) || int(parent.MetadataIndex) >= len(m.Methods) {
		return
	}
	method := &m.Methods[parent.MetadataIndex]
	for i := uint32(0); i < method.ParameterCount; i++ {
		markValueSlot(m, method.FirstParameter+i, node.ScopeVariableMask)
	}
}

func (d *Debugger) captureCurrentVariables(m *planner.DebugMetadata, values []atom.Atom, before bool) {
	if !d.enabled || len(d.openNodes) == 0 || m == nil || values == nil || len(m.VariableStringIDs) == 0 {
		return
	}
	current := &d.nodes[d.openNodes[len(d.openNodes)-1]]
	out := current.VariablesAfter[:0]
	if before {
		out = current.VariablesBefore[:0]
	}
	slotCount := min(len(values), len(m.VariableStringIDs))
	for slot := 0; slot < slotCount; slot++ {
		if m.VariableStringIDs[slot] == NoIndex || !values[slot].IsBound() {
			continue
		}
		bit := uint64(1) << (uint(slot) & 63)
		if len(current.ScopeVariableMask) > 0 {
			word := slot >> 6
			if word >= len(current.ScopeVariableMask) || current.ScopeVariableMask[word]&bit == 0 {
				continue
			}
		}
		out = append(out, VariableValue{Slot: uint32(slot), Name: resolveString(m, m.VariableStringIDs[slot], "?"),
			Value: values[slot]})
	}
	if before {
		current.VariablesBefore = out
	} else {
		current.VariablesAfter = out
	}
}

func (d *Debugger) endNode(m *planner.DebugMetadata, values []atom.Atom, result bool) {
	if !d.enabled || len(d.openNodes) == 0 {
		return
	}
	d.captureCurrentVariables(m, values, false)
	id := d.openNodes[len(d.openNodes)-1]
	d.openNodes = d.openNodes[:len(d.openNodes)-1]
	d.nodes[id].Completed = true
	d.nodes[id].Succeeded = result
}

var tokenKindLetters = [...]string{"n", "r", "v", "c", "s", "x"}

func formatIndex(index uint32) string {
	if index == NoIndex {
		return "-"
	}
	return strconv.FormatUint(uint64(index), 10)
}

// Dump renders every recorded node in the text format of the C++ reference
// oracle (tools/oracle/oracle.cpp, DumpDebugger).
func (d *Debugger) Dump() []string {
	domainPath := d.domainPath
	if domainPath == "" {
		domainPath = "-"
	}
	lines := []string{fmt.Sprintf("debugger domain=%s nodes=%d", domainPath, len(d.nodes))}
	flag := func(value bool, set string) string {
		if value {
			return set
		}
		return "-"
	}
	for i := range d.nodes {
		n := &d.nodes[i]
		path := n.Source.DomainPath
		if path == "" {
			path = "-"
		}
		lines = append(lines, fmt.Sprintf("node %d parent=%s kind=%s meta=%s state=%s%s%s source=%s:%d:%d-%d:%d",
			n.EventNodeID, formatIndex(n.ParentEventNodeID), n.Kind, formatIndex(n.MetadataIndex),
			flag(n.Started, "S"), flag(n.Completed, "C"), flag(n.Succeeded, "+"), path, n.Source.Line,
			n.Source.Column, n.Source.EndLine, n.Source.EndColumn))
		lines = append(lines, "  title "+n.DisplayName)
		if len(n.TitleTokens) > 0 {
			var b strings.Builder
			b.WriteString("  tokens")
			for _, token := range n.TitleTokens {
				if token.SpaceBefore {
					b.WriteByte(' ')
				}
				b.WriteString("[" + tokenKindLetters[token.Kind] + "]" + token.Text)
			}
			lines = append(lines, b.String())
		}
		if len(n.Constants) > 0 {
			var b strings.Builder
			b.WriteString("  constants")
			for _, constant := range n.Constants {
				b.WriteString(" " + constant.Name + "=" + constant.Value + ";")
			}
			lines = append(lines, b.String())
		}
		for _, group := range []struct {
			label     string
			variables []VariableValue
		}{{"before", n.VariablesBefore}, {"after", n.VariablesAfter}} {
			if len(group.variables) == 0 {
				continue
			}
			var b strings.Builder
			b.WriteString("  " + group.label)
			for _, v := range group.variables {
				fmt.Fprintf(&b, " %d:%s=%s;", v.Slot, v.Name, atom.ToString(v.Value, true))
			}
			lines = append(lines, b.String())
		}
		if len(n.ScopeVariableMask) > 0 {
			var b strings.Builder
			b.WriteString("  scope")
			for _, word := range n.ScopeVariableMask {
				fmt.Fprintf(&b, " %016x", word)
			}
			lines = append(lines, b.String())
		}
		if len(n.Children) > 0 {
			var b strings.Builder
			b.WriteString("  children")
			for _, child := range n.Children {
				b.WriteString(" " + strconv.FormatUint(uint64(child), 10))
			}
			lines = append(lines, b.String())
		}
	}
	return lines
}

var _ planner.Debugger = (*Debugger)(nil)
