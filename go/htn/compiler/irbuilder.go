package compiler

import (
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

// FormatValueExpression renders a value as domain syntax.
func FormatValueExpression(v *Value) string {
	switch v.Kind {
	case ValueIdentifier:
		return atom.ToString(v.Atom, false)
	case ValueLiteral:
		return atom.ToString(v.Atom, true)
	case ValueVariable:
		return "?" + atom.ToString(v.Atom, false)
	case ValueConstant:
		return "@" + atom.ToString(v.Atom, false)
	case ValueCall:
		result := "(call " + FormatValueExpression(v.CallID)
		for _, a := range v.CallArguments {
			result += " " + FormatValueExpression(a)
		}
		return result + ")"
	case ValueArithmetic:
		operators := [...]string{"+", "-", "*", "/", "%", "++", "--"}
		op := "?"
		if int(v.ArithmeticOp) < len(operators) {
			op = operators[v.ArithmeticOp]
		}
		result := "(" + op
		for _, o := range v.ArithmeticOperands {
			result += " " + FormatValueExpression(o)
		}
		return result + ")"
	}
	return atom.ToString(v.Atom, true)
}

func formatArguments(arguments []*Value) string {
	result := ""
	for _, a := range arguments {
		result += " " + FormatValueExpression(a)
	}
	return result
}

func listSplitOperationName(operation uint32) string {
	switch operation {
	case 1:
		return "split_list_front"
	case 2:
		return "split_list_back"
	}
	return "split_list"
}

// FormatCondition renders a condition as domain syntax.
func FormatCondition(c *Condition) string {
	switch c.Kind {
	case CondFact:
		return "(" + FormatValueExpression(c.ID) + formatArguments(c.Arguments) + ")"
	case CondAxiom:
		return "(#" + FormatValueExpression(c.ID) + formatArguments(c.Arguments) + ")"
	case CondCall:
		invocation := "(call " + FormatValueExpression(c.ID) + formatArguments(c.Arguments) + ")"
		if c.Output == nil {
			return invocation
		}
		return "(" + FormatValueExpression(c.Output) + " " + invocation + ")"
	case CondAssignment:
		return "(= " + FormatValueExpression(c.Output) + " " + FormatValueExpression(c.Arguments[0]) + ")"
	case CondComparison:
		operators := [...]string{"==", "!=", "<", "<=", ">", ">="}
		op := "?"
		if int(c.Operator) < len(operators) {
			op = operators[c.Operator]
		}
		return "(" + op + " " + FormatValueExpression(c.Arguments[0]) + " " + FormatValueExpression(c.Arguments[1]) + ")"
	case CondSplit:
		list := FormatValueExpression(c.Arguments[0])
		element := FormatValueExpression(c.Arguments[1])
		remainder := FormatValueExpression(c.Arguments[2])
		if c.Operator == 2 {
			return "(" + listSplitOperationName(c.Operator) + " " + list + " " + remainder + " " + element + ")"
		}
		return "(" + listSplitOperationName(c.Operator) + " " + list + " " + element + " " + remainder + ")"
	}
	composite := func(name string) string {
		result := "(" + name
		for _, child := range c.Children {
			result += " " + FormatCondition(child)
		}
		return result + ")"
	}
	switch c.Kind {
	case CondAnd:
		return composite("and")
	case CondOr:
		return composite("or")
	case CondAlt:
		return composite("alt")
	case CondNot:
		return "(not " + FormatCondition(c.Children[0]) + ")"
	}
	return "<condition>"
}

// FormatTask renders a task as domain syntax.
func FormatTask(t *Task) string {
	prefix := ""
	switch t.Kind {
	case TaskPrimitive:
		prefix = "!"
	case TaskDeferred:
		prefix = "&"
	}
	return "(" + prefix + FormatValueExpression(t.ID) + formatArguments(t.Arguments) + ")"
}

type irBuilder struct {
	*IR
	domain *Domain
}

func sourceLine(r SourceLocation) uint32 {
	if r.Range.Begin.Line < 1 {
		return 1
	}
	return uint32(r.Range.Begin.Line)
}

func (b *irBuilder) allocateVariableSlot(stringID uint32) uint32 {
	if slot, ok := b.VariableSlotByStringID[stringID]; ok {
		return slot
	}
	slot := uint32(len(b.VariableStringIDs))
	b.VariableStringIDs = append(b.VariableStringIDs, stringID)
	b.VariableSlotByStringID[stringID] = slot
	return slot
}

func (b *irBuilder) allocatePreparedSymbol(stringID uint32) uint32 {
	if slot, ok := b.preparedSymbolSlot[stringID]; ok {
		return slot
	}
	slot := uint32(len(b.PreparedSymbols))
	b.PreparedSymbols = append(b.PreparedSymbols, stringID)
	b.preparedSymbolSlot[stringID] = slot
	return slot
}

func (b *irBuilder) allocateCallTermSlot(stringID uint32) uint32 {
	if slot, ok := b.CallTermSlotByStringID[stringID]; ok {
		return slot
	}
	slot := uint32(len(b.CallTermStringIDs))
	b.CallTermStringIDs = append(b.CallTermStringIDs, stringID)
	b.CallTermSlotByStringID[stringID] = slot
	return slot
}

func (b *irBuilder) allocateFactSlot(stringID uint32) uint32 {
	if slot, ok := b.FactSlotByStringID[stringID]; ok {
		return slot
	}
	slot := uint32(len(b.FactStringIDs))
	b.FactStringIDs = append(b.FactStringIDs, stringID)
	b.FactSlotByStringID[stringID] = slot
	b.allocatePreparedSymbol(stringID)
	return slot
}

func (b *irBuilder) allocateListSymbols(a atom.Atom) {
	switch a.Kind() {
	case atom.KindList:
		for _, element := range a.Elements() {
			b.allocateListSymbols(element)
		}
	case atom.KindSymbol:
		b.allocatePreparedSymbol(b.Strings.Add(atom.ToString(a, false)))
	}
}

func location(n *Node) SourceLocation { return SourceLocation{FileIndex: n.FileIndex, Range: n.Range} }

func (b *irBuilder) makeValueRecord(node *Value) IRValue {
	record := newIRValue()
	if node.Kind == ValueCall {
		file := "<domain>"
		if int(node.FileIndex) < len(b.SourceFiles) {
			file = b.SourceFiles[node.FileIndex]
		}
		b.SetError(file + "(" + strconv.Itoa(node.Range.Begin.Line) + "," + strconv.Itoa(node.Range.Begin.Column) +
			"): error: Call expression '" + FormatValueExpression(node) + "' was not lowered to a runtime invocation")
		return record
	}
	record.Kind = node.Kind
	record.Text = b.Strings.Add(atom.ToString(node.Atom, false))
	record.DebugText = b.Strings.Add(FormatValueExpression(node))
	record.AtomType = node.Atom.Kind()
	if node.Kind == ValueLiteral {
		record.Literal = node.Atom
	}
	switch record.AtomType {
	case atom.KindList:
		b.allocateListSymbols(node.Atom)
	case atom.KindSymbol:
		b.allocatePreparedSymbol(record.Text)
	}
	record.Source = location(&node.Node)
	record.SourceLine = sourceLine(record.Source)
	if record.Kind == ValueVariable && !strings.HasPrefix(b.Strings.Get(record.Text), "any_") {
		record.VariableSlot = b.allocateVariableSlot(record.Text)
	}
	if record.Kind == ValueArithmetic {
		record.ArithmeticExpression = uint32(len(b.ArithmeticExpressions))
		b.ArithmeticExpressions = append(b.ArithmeticExpressions, IRArithmeticExpression{Operator: node.ArithmeticOp})
		for _, operand := range node.ArithmeticOperands {
			operandRecord := b.makeValueRecord(operand)
			b.ArithmeticExpressions[record.ArithmeticExpression].Operands =
				append(b.ArithmeticExpressions[record.ArithmeticExpression].Operands, operandRecord)
		}
	}
	return record
}

func (b *irBuilder) addValue(node *Value) uint32 {
	index := uint32(len(b.Values))
	record := b.makeValueRecord(node)
	b.Values = append(b.Values, record)
	return index
}

func (b *irBuilder) buildTaskArgument(node *Value, calls *[]IRTaskCallExpression) IRValue {
	if node.Kind == ValueArithmetic {
		shell := *node
		shell.ArithmeticOperands = nil
		record := b.makeValueRecord(&shell)
		record.DebugText = b.Strings.Add(FormatValueExpression(node))
		for _, operand := range node.ArithmeticOperands {
			prepared := b.buildTaskArgument(operand, calls)
			b.ArithmeticExpressions[record.ArithmeticExpression].Operands =
				append(b.ArithmeticExpressions[record.ArithmeticExpression].Operands, prepared)
		}
		return record
	}
	if node.Kind == ValueCall {
		call := IRTaskCallExpression{ID: NoIndex, CallTermSlot: NoIndex, OutputSlot: NoIndex}
		call.DomainExpression = FormatValueExpression(node)
		call.ID = b.Strings.Add(atom.ToString(node.CallID.Atom, false))
		call.CallTermSlot = b.allocateCallTermSlot(call.ID)
		call.Source = location(&node.Node)
		call.SourceLine = sourceLine(call.Source)
		for _, argument := range node.CallArguments {
			call.Arguments = append(call.Arguments, b.buildTaskArgument(argument, calls))
		}
		debugExpression := b.Strings.Add(call.DomainExpression)
		hiddenName := "__task_call_result_" + strconv.FormatUint(uint64(b.SyntheticTaskCallCount), 10)
		b.SyntheticTaskCallCount++
		hidden := b.Strings.Add(hiddenName)
		b.DebugInternalVariableStringIDs[hidden] = true
		call.OutputSlot = b.allocateVariableSlot(hidden)
		*calls = append(*calls, call)
		result := newIRValue()
		result.Kind = ValueVariable
		result.Text = hidden
		result.DebugText = debugExpression
		result.VariableSlot = call.OutputSlot
		result.Source = location(&node.Node)
		result.SourceLine = sourceLine(result.Source)
		result.DebugAsVariable = false
		return result
	}
	return b.makeValueRecord(node)
}

func hasNestedCall(v *Value, root bool) bool {
	if !root && v.Kind == ValueCall {
		return true
	}
	for _, a := range v.CallArguments {
		if hasNestedCall(a, false) {
			return true
		}
	}
	for _, o := range v.ArithmeticOperands {
		if hasNestedCall(o, false) {
			return true
		}
	}
	return false
}

func (b *irBuilder) captureValue(v *Value, prefix *[]*Condition) *Value {
	hiddenName := "$assignment_call_" + strconv.FormatUint(uint64(b.SyntheticTaskCallCount), 10)
	b.SyntheticTaskCallCount++
	variable := &Value{Kind: ValueVariable, Atom: atom.NewString(hiddenName)}
	b.DebugInternalVariableStringIDs[b.Strings.Add(hiddenName)] = true
	variable.Range = v.Range
	variable.FileIndex = v.FileIndex
	binding := &Condition{Kind: CondAssignment, Output: variable, Arguments: []*Value{v}}
	binding.Range = v.Range
	binding.FileIndex = v.FileIndex
	*prefix = append(*prefix, binding)
	return variable
}

func (b *irBuilder) prepareExpression(v *Value, prefix *[]*Condition) *Value {
	value := *v
	value.CallArguments = append([]*Value(nil), v.CallArguments...)
	value.ArithmeticOperands = append([]*Value(nil), v.ArithmeticOperands...)
	for i, argument := range value.CallArguments {
		value.CallArguments[i] = b.captureValue(b.prepareExpression(argument, prefix), prefix)
	}
	for i, operand := range value.ArithmeticOperands {
		// Validate each numeric operand before evaluating subsequent calls.
		// Multiplication by one preserves its numeric type and value.
		checked := &Value{Kind: ValueArithmetic, ArithmeticOp: OpMultiply}
		checked.Range = operand.Range
		checked.FileIndex = operand.FileIndex
		one := &Value{Kind: ValueLiteral, Atom: atom.NewInt(1)}
		one.Range = lexer.DefaultRange
		prepared := b.prepareExpression(operand, prefix)
		if prepared.Kind == ValueCall {
			prepared = b.captureValue(prepared, prefix)
		}
		checked.ArithmeticOperands = []*Value{prepared, one}
		value.ArithmeticOperands[i] = b.captureValue(checked, prefix)
	}
	return &value
}

func (b *irBuilder) preserveLoweredSource(sequence uint32, original *Condition) {
	c := &b.Conditions[sequence]
	c.DebugExpression = FormatCondition(original)
	c.DebugSource = location(&original.Node)
	c.DebugCondition = b.ConditionChildRefs[c.FirstChildRef+c.ChildCount-1]
	for i := int(sequence) + 1; i < len(b.Conditions); i++ {
		b.Conditions[i].DebugInternal = true
	}
}

func (b *irBuilder) addCondition(node *Condition) uint32 {
	if node == nil {
		return NoIndex
	}
	if node.Kind != CondAssignment {
		hasCalls := false
		for _, argument := range node.Arguments {
			hasCalls = hasCalls || hasNestedCall(argument, false)
		}
		if hasCalls {
			var prefix []*Condition
			condition := *node
			condition.Arguments = append([]*Value(nil), node.Arguments...)
			for i, argument := range condition.Arguments {
				// Comparisons and callterms read all operands: capture them in
				// source order so failure stops before later invocations.
				if node.Kind == CondComparison || node.Kind == CondCall || hasNestedCall(argument, false) {
					condition.Arguments[i] = b.captureValue(b.prepareExpression(argument, &prefix), &prefix)
				}
			}
			prefix = append(prefix, &condition)
			sequence := &Condition{Kind: CondAnd, Children: prefix}
			sequence.Range = node.Range
			sequence.FileIndex = node.FileIndex
			index := b.addCondition(sequence)
			b.preserveLoweredSource(index, node)
			return index
		}
	}
	if node.Kind == CondAssignment && hasNestedCall(node.Arguments[0], true) {
		var prefix []*Condition
		expression := b.prepareExpression(node.Arguments[0], &prefix)
		if len(prefix) > 0 {
			binding := *node
			binding.Arguments = []*Value{expression}
			prefix = append(prefix, &binding)
			sequence := &Condition{Kind: CondAnd, Children: prefix}
			sequence.Range = node.Range
			sequence.FileIndex = node.FileIndex
			index := b.addCondition(sequence)
			guard := b.addValue(node.Output)
			b.Conditions[index].AssignmentGuardValue = guard
			b.preserveLoweredSource(index, node)
			return index
		}
	}

	record := IRCondition{AssignmentGuardValue: NoIndex, ID: NoIndex, OutputValue: NoIndex,
		ResolvedIndex: NoIndex, DebugCondition: NoIndex}
	record.DomainExpression = FormatCondition(node)
	record.Source = location(&node.Node)
	record.SourceLine = sourceLine(record.Source)
	record.DebugSource = record.Source
	record.DebugExpression = record.DomainExpression
	switch node.Kind {
	case CondAnd:
		record.DebugExpression = "(and ...)"
	case CondOr:
		record.DebugExpression = "(or ...)"
	case CondAlt:
		record.DebugExpression = "(alt ...)"
	case CondNot:
		record.DebugExpression = "(not ...)"
	}
	index := uint32(len(b.Conditions))
	b.Conditions = append(b.Conditions, record)

	switch node.Kind {
	case CondFact:
		record.Kind = IRCondFact
		record.ID = b.Strings.Add(atom.ToString(node.ID.Atom, false))
		record.ResolvedIndex = b.allocateFactSlot(record.ID)
		record.FirstArgument = uint32(len(b.Values))
		for _, argument := range node.Arguments {
			b.addValue(argument)
		}
		record.ArgumentCount = uint32(len(b.Values)) - record.FirstArgument
	case CondAxiom:
		record.Kind = IRCondAxiom
		record.ID = b.Strings.Add(atom.ToString(node.ID.Atom, false))
		record.FirstArgument = uint32(len(b.Values))
		for _, argument := range node.Arguments {
			b.addValue(argument)
		}
		record.ArgumentCount = uint32(len(b.Values)) - record.FirstArgument
	case CondCall:
		if node.Output != nil {
			record.Kind = IRCondCallBind
		} else {
			record.Kind = IRCondCall
		}
		record.ID = b.Strings.Add(atom.ToString(node.ID.Atom, false))
		record.ResolvedIndex = b.allocateCallTermSlot(record.ID)
		if node.Output != nil {
			record.OutputValue = b.addValue(node.Output)
		}
		record.FirstArgument = uint32(len(b.Values))
		for _, argument := range node.Arguments {
			b.addValue(argument)
		}
		record.ArgumentCount = uint32(len(b.Values)) - record.FirstArgument
	case CondAssignment:
		record.OutputValue = b.addValue(node.Output)
		expression := node.Arguments[0]
		if expression.Kind == ValueCall {
			record.Kind = IRCondCallBind
			record.Source = location(&expression.Node)
			record.SourceLine = sourceLine(record.Source)
			record.ID = b.Strings.Add(atom.ToString(expression.CallID.Atom, false))
			record.ResolvedIndex = b.allocateCallTermSlot(record.ID)
			record.FirstArgument = uint32(len(b.Values))
			for _, argument := range expression.CallArguments {
				b.addValue(argument)
			}
			record.ArgumentCount = uint32(len(b.Values)) - record.FirstArgument
		} else {
			record.Kind = IRCondAssignment
			record.FirstArgument = b.addValue(expression)
			record.ArgumentCount = 1
		}
	case CondComparison:
		record.Kind = IRCondComparison
		record.ID = node.Operator
		record.FirstArgument = uint32(len(b.Values))
		b.addValue(node.Arguments[0])
		b.addValue(node.Arguments[1])
		record.ArgumentCount = 2
	case CondSplit:
		record.Kind = IRCondListSplit
		record.ID = node.Operator
		record.FirstArgument = uint32(len(b.Values))
		b.addValue(node.Arguments[0])
		b.addValue(node.Arguments[1])
		b.addValue(node.Arguments[2])
		record.ArgumentCount = 3
	case CondAnd:
		record.Kind = IRCondAnd
	case CondOr:
		record.Kind = IRCondOr
	case CondAlt:
		record.Kind = IRCondAlt
	case CondNot:
		record.Kind = IRCondNot
	}
	children := make([]uint32, 0, len(node.Children))
	for _, child := range node.Children {
		children = append(children, b.addCondition(child))
	}
	record.FirstChildRef = uint32(len(b.ConditionChildRefs))
	b.ConditionChildRefs = append(b.ConditionChildRefs, children...)
	record.ChildCount = uint32(len(children))
	b.Conditions[index] = record
	return index
}

func (b *irBuilder) collectConditionSlots(conditionIndex uint32, mask *SlotMask) {
	if conditionIndex == NoIndex || int(conditionIndex) >= len(b.Conditions) {
		return
	}
	c := &b.Conditions[conditionIndex]
	for i := uint32(0); i < c.ArgumentCount; i++ {
		b.MarkValueSlots(mask, &b.Values[c.FirstArgument+i])
	}
	if c.OutputValue != NoIndex {
		b.MarkValueSlots(mask, &b.Values[c.OutputValue])
	}
	for i := uint32(0); i < c.ChildCount; i++ {
		b.collectConditionSlots(b.ConditionChildRefs[c.FirstChildRef+i], mask)
	}
}

func (b *irBuilder) collectTaskSlots(taskIndex uint32, mask *SlotMask) {
	if int(taskIndex) >= len(b.Tasks) {
		return
	}
	t := &b.Tasks[taskIndex]
	for i := uint32(0); i < t.ArgumentCount; i++ {
		b.MarkValueSlots(mask, &b.Values[t.FirstArgument+i])
	}
	if int(taskIndex) >= len(b.TaskCallExpressions) {
		return
	}
	for ci := range b.TaskCallExpressions[taskIndex] {
		call := &b.TaskCallExpressions[taskIndex][ci]
		mask.Add(call.OutputSlot)
		for ai := range call.Arguments {
			b.MarkValueSlots(mask, &call.Arguments[ai])
		}
	}
}

func (b *irBuilder) addTask(node *Task) {
	record := IRTask{PlanStepHead: NoIndex}
	record.DomainExpression = FormatTask(node)
	switch node.Kind {
	case TaskPrimitive:
		record.Kind = IRTaskPrimitive
	case TaskDeferred:
		record.Kind = IRTaskDeferred
	default:
		record.Kind = IRTaskCompound
	}
	id := atom.ToString(node.ID.Atom, false)
	record.ID = b.Strings.Add(id)
	if record.Kind == IRTaskPrimitive {
		record.PlanStepHead = b.Strings.Add("!" + id)
		b.allocatePreparedSymbol(record.PlanStepHead)
	} else if record.Kind == IRTaskDeferred {
		record.PlanStepHead = b.Strings.Add("&" + id)
		b.allocatePreparedSymbol(record.PlanStepHead)
	}
	record.Source = location(&node.Node)
	record.SourceLine = sourceLine(record.Source)
	var calls []IRTaskCallExpression
	arguments := make([]IRValue, 0, len(node.Arguments))
	for _, argument := range node.Arguments {
		arguments = append(arguments, b.buildTaskArgument(argument, &calls))
	}
	record.FirstArgument = uint32(len(b.Values))
	b.Values = append(b.Values, arguments...)
	record.ArgumentCount = uint32(len(arguments))
	b.Tasks = append(b.Tasks, record)
	b.TaskCallExpressions = append(b.TaskCallExpressions, calls)
}

func (b *irBuilder) build() {
	for _, group := range b.domain.ConstantGroups {
		groupID := b.Strings.Add(group.ID)
		for _, constant := range group.Constants {
			record := IRConstant{GroupID: groupID, ID: b.Strings.Add(constant.ID)}
			record.Source = location(&constant.Node)
			record.SourceLine = sourceLine(record.Source)
			record.Value = b.addValue(constant.Value)
			b.Constants = append(b.Constants, record)
		}
	}
	for _, axiom := range b.domain.Axioms {
		record := IRAxiom{ID: b.Strings.Add(axiom.ID), Condition: NoIndex}
		record.Source = location(&axiom.Node)
		record.SourceLine = sourceLine(record.Source)
		record.FirstParameter = uint32(len(b.Values))
		for _, parameter := range axiom.Parameters {
			b.addValue(parameter)
		}
		record.ParameterCount = uint32(len(b.Values)) - record.FirstParameter
		record.Condition = b.addCondition(axiom.Body)
		for i := uint32(0); i < record.ParameterCount; i++ {
			b.MarkValueSlots(&record.VariableSlotMask, &b.Values[record.FirstParameter+i])
		}
		b.collectConditionSlots(record.Condition, &record.VariableSlotMask)
		b.Axioms = append(b.Axioms, record)
	}
	for _, method := range b.domain.Methods {
		record := IRMethod{ID: b.Strings.Add(method.ID)}
		record.Source = location(&method.Node)
		record.SourceLine = sourceLine(record.Source)
		record.FirstParameter = uint32(len(b.Values))
		for _, parameter := range method.Parameters {
			b.addValue(parameter)
		}
		record.ParameterCount = uint32(len(b.Values)) - record.FirstParameter
		record.FirstBranch = uint32(len(b.Branches))
		record.IsTopLevel = method.TopLevel
		record.IsExternallyDecomposable = record.IsTopLevel
		if record.IsExternallyDecomposable {
			b.allocatePreparedSymbol(record.ID)
		}
		for _, branch := range method.Branches {
			br := IRBranch{ID: b.Strings.Add(branch.ID)}
			br.Source = location(&branch.Node)
			br.SourceLine = sourceLine(br.Source)
			br.Condition = b.addCondition(branch.Precondition)
			br.FirstTask = uint32(len(b.Tasks))
			for _, task := range branch.Tasks {
				b.addTask(task)
			}
			br.TaskCount = uint32(len(b.Tasks)) - br.FirstTask
			b.Branches = append(b.Branches, br)
		}
		record.BranchCount = uint32(len(b.Branches)) - record.FirstBranch
		// Compound calls get fresh logical frames but slots are domain-global:
		// the mask identifies every slot this method may observe or mutate.
		for i := uint32(0); i < record.ParameterCount; i++ {
			b.MarkValueSlots(&record.VariableSlotMask, &b.Values[record.FirstParameter+i])
		}
		for bi := uint32(0); bi < record.BranchCount; bi++ {
			br := &b.Branches[record.FirstBranch+bi]
			b.collectConditionSlots(br.Condition, &record.VariableSlotMask)
			for ti := uint32(0); ti < br.TaskCount; ti++ {
				b.collectTaskSlots(br.FirstTask+ti, &record.VariableSlotMask)
			}
		}
		b.Methods = append(b.Methods, record)
	}
	// Methods referenced by &deferred calls must be reachable through the
	// generated dispatch without becoming public top-level methods.
	for _, task := range b.Tasks {
		if task.Kind != IRTaskDeferred {
			continue
		}
		for mi := range b.Methods {
			if b.Methods[mi].ID != task.ID || b.Methods[mi].ParameterCount != task.ArgumentCount {
				continue
			}
			b.Methods[mi].IsExternallyDecomposable = true
			b.allocatePreparedSymbol(b.Methods[mi].ID)
			break
		}
	}
}

func (b *irBuilder) resolveCompileTimeReferences() bool {
	original := append([]IRValue(nil), b.Values...)
	findConstant := func(text uint32) *IRConstant {
		for i := range b.Constants {
			if b.Constants[i].ID == text {
				return &b.Constants[i]
			}
		}
		return nil
	}
	resolveFromOriginal := func(index uint32) *IRValue {
		for depth := 0; depth < 64; depth++ {
			if int(index) >= len(original) {
				return nil
			}
			value := &original[index]
			if value.Kind != ValueConstant {
				return value
			}
			constant := findConstant(value.Text)
			if constant == nil {
				return nil
			}
			index = constant.Value
		}
		return nil
	}
	for i := range b.Values {
		value := &b.Values[i]
		if value.Kind != ValueConstant {
			continue
		}
		resolved := resolveFromOriginal(uint32(i))
		if resolved == nil || resolved.Kind == ValueVariable || resolved.Kind == ValueConstant {
			b.SetError("Generated constant could not be resolved to a static value at translation time")
			return false
		}
		line, source, debugText := value.SourceLine, value.Source, value.DebugText
		*value = *resolved
		value.DebugText = debugText
		value.SourceLine = line
		value.Source = source
		value.VariableSlot = NoIndex
	}
	for ti := range b.TaskCallExpressions {
		for ci := range b.TaskCallExpressions[ti] {
			call := &b.TaskCallExpressions[ti][ci]
			for ai := range call.Arguments {
				value := &call.Arguments[ai]
				if value.Kind != ValueConstant {
					continue
				}
				constant := findConstant(value.Text)
				if constant == nil {
					b.SetError("Generated task call constant could not be resolved at translation time")
					return false
				}
				resolved := resolveFromOriginal(constant.Value)
				if resolved == nil || resolved.Kind == ValueVariable || resolved.Kind == ValueConstant {
					b.SetError("Generated task call constant could not be resolved to a static value at translation time")
					return false
				}
				line, source, debugText := value.SourceLine, value.Source, value.DebugText
				*value = *resolved
				value.DebugText = debugText
				value.SourceLine = line
				value.Source = source
				value.VariableSlot = NoIndex
			}
		}
	}
	b.StaticValues = nil
	allocateStatic := func(value *IRValue) {
		value.StaticValueIndex = NoIndex
		if value.Kind == ValueVariable || value.Kind == ValueArithmetic {
			return
		}
		value.StaticValueIndex = uint32(len(b.StaticValues))
		b.StaticValues = append(b.StaticValues, IRStaticValue{Text: value.Text, AtomType: value.AtomType, Literal: value.Literal})
	}
	for i := range b.Values {
		allocateStatic(&b.Values[i])
	}
	for ti := range b.TaskCallExpressions {
		for ci := range b.TaskCallExpressions[ti] {
			for ai := range b.TaskCallExpressions[ti][ci].Arguments {
				allocateStatic(&b.TaskCallExpressions[ti][ci].Arguments[ai])
			}
		}
	}
	for ci := range b.Conditions {
		c := &b.Conditions[ci]
		if c.Kind != IRCondAxiom {
			continue
		}
		found := false
		for ai := range b.Axioms {
			if b.Axioms[ai].ID == c.ID && b.Axioms[ai].ParameterCount == c.ArgumentCount {
				c.ResolvedIndex = uint32(ai)
				found = true
				break
			}
		}
		if !found {
			b.SetError("Generated axiom reference could not be resolved at translation time")
			return false
		}
	}
	return true
}

func (b *irBuilder) validateCallTermCondition(index uint32, mayBeBound map[uint32]bool) (string, bool) {
	if index == NoIndex || int(index) >= len(b.Conditions) {
		return "", true
	}
	c := &b.Conditions[index]
	switch c.Kind {
	case IRCondFact:
		for i := uint32(0); i < c.ArgumentCount; i++ {
			v := &b.Values[c.FirstArgument+i]
			if v.Kind == ValueVariable {
				mayBeBound[v.Text] = true
			}
		}
		return "", true
	case IRCondAxiom:
		axiom := b.FindAxiom(c.ID, c.ArgumentCount)
		if axiom == nil {
			return "", true
		}
		count := axiom.ParameterCount
		if c.ArgumentCount < count {
			count = c.ArgumentCount
		}
		for i := uint32(0); i < count; i++ {
			name := b.Strings.Get(b.Values[axiom.FirstParameter+i].Text)
			if !strings.HasPrefix(name, "out_") && !strings.HasPrefix(name, "io_") {
				continue
			}
			caller := &b.Values[c.FirstArgument+i]
			if caller.Kind == ValueVariable {
				mayBeBound[caller.Text] = true
			}
		}
		return "", true
	case IRCondListSplit:
		for i := uint32(1); i < c.ArgumentCount; i++ {
			v := &b.Values[c.FirstArgument+i]
			if v.Kind == ValueVariable {
				mayBeBound[v.Text] = true
			}
		}
		return "", true
	case IRCondCallBind, IRCondAssignment:
		if c.OutputValue == NoIndex || int(c.OutputValue) >= len(b.Values) {
			return "", true
		}
		output := &b.Values[c.OutputValue]
		if output.Kind != ValueVariable {
			return "", true
		}
		if _, exists := mayBeBound[output.Text]; exists {
			callTerm := "<unknown>"
			if int(c.ID) < len(b.Strings.Values) {
				callTerm = b.Strings.Values[c.ID]
			}
			message := "Callterm output variable '?" + b.Strings.Get(output.Text) + "' may already be bound before call '" + callTerm + "'"
			if c.SourceLine != 0 {
				message += " at domain line " + strconv.FormatUint(uint64(c.SourceLine), 10)
			}
			return message, false
		}
		mayBeBound[output.Text] = true
		return "", true
	case IRCondAnd:
		for i := uint32(0); i < c.ChildCount; i++ {
			if message, ok := b.validateCallTermCondition(b.ConditionChildRefs[c.FirstChildRef+i], mayBeBound); !ok {
				return message, false
			}
		}
		return "", true
	case IRCondOr, IRCondAlt:
		union := make(map[uint32]bool, len(mayBeBound))
		for k, v := range mayBeBound {
			union[k] = v
		}
		for i := uint32(0); i < c.ChildCount; i++ {
			branch := make(map[uint32]bool, len(mayBeBound))
			for k, v := range mayBeBound {
				branch[k] = v
			}
			if message, ok := b.validateCallTermCondition(b.ConditionChildRefs[c.FirstChildRef+i], branch); !ok {
				return message, false
			}
			for k, v := range branch {
				if v {
					union[k] = true
				}
			}
		}
		for k := range mayBeBound {
			delete(mayBeBound, k)
		}
		for k, v := range union {
			mayBeBound[k] = v
		}
		return "", true
	case IRCondNot:
		for i := uint32(0); i < c.ChildCount; i++ {
			local := make(map[uint32]bool, len(mayBeBound))
			for k, v := range mayBeBound {
				local[k] = v
			}
			if message, ok := b.validateCallTermCondition(b.ConditionChildRefs[c.FirstChildRef+i], local); !ok {
				return message, false
			}
		}
	}
	return "", true
}

func (b *irBuilder) validateCallTermBindings() (string, bool) {
	for mi := range b.Methods {
		m := &b.Methods[mi]
		initial := map[uint32]bool{}
		for i := uint32(0); i < m.ParameterCount; i++ {
			p := &b.Values[m.FirstParameter+i]
			if p.Kind == ValueVariable {
				initial[p.Text] = true
			}
		}
		for bi := uint32(0); bi < m.BranchCount; bi++ {
			bindings := make(map[uint32]bool, len(initial))
			for k, v := range initial {
				bindings[k] = v
			}
			if message, ok := b.validateCallTermCondition(b.Branches[m.FirstBranch+bi].Condition, bindings); !ok {
				return message, false
			}
		}
	}
	for ai := range b.Axioms {
		a := &b.Axioms[ai]
		initial := map[uint32]bool{}
		for i := uint32(0); i < a.ParameterCount; i++ {
			p := &b.Values[a.FirstParameter+i]
			if p.Kind != ValueVariable {
				continue
			}
			if strings.HasPrefix(b.Strings.Get(p.Text), "inp_") {
				initial[p.Text] = true
			}
		}
		if message, ok := b.validateCallTermCondition(a.Condition, initial); !ok {
			return message, false
		}
	}
	return "", true
}

// BuildIR lowers a linked domain into the compiler IR. sourceFiles holds the
// display paths of the linked source files (used in diagnostics and callterm
// provenance).
func BuildIR(domain *Domain, sourceFiles []string, runtimeBacktrackingSupport bool) (*IR, string) {
	for _, method := range domain.Methods {
		for _, parameter := range method.Parameters {
			name := parameter.Atom.Str()
			if !strings.HasPrefix(name, "inp_") {
				return nil, "Method '" + method.ID + "' parameter '?" + name + "' must use the inp_ prefix"
			}
		}
	}
	for _, axiom := range domain.Axioms {
		for _, parameter := range axiom.Parameters {
			name := parameter.Atom.Str()
			if !strings.HasPrefix(name, "inp_") && !strings.HasPrefix(name, "out_") && !strings.HasPrefix(name, "io_") {
				return nil, "Axiom '" + axiom.ID + "' parameter '?" + name + "' must use an inp_, out_ or io_ prefix"
			}
		}
	}
	ir := &IR{
		DomainID:                       domain.ID,
		SourceFiles:                    sourceFiles,
		RuntimeBacktrackingSupport:     runtimeBacktrackingSupport,
		DebugInternalVariableStringIDs: map[uint32]bool{},
		VariableSlotByStringID:         map[uint32]uint32{},
		preparedSymbolSlot:             map[uint32]uint32{},
		FactSlotByStringID:             map[uint32]uint32{},
		CallTermSlotByStringID:         map[uint32]uint32{},
	}
	b := &irBuilder{IR: ir, domain: domain}
	b.build()
	if ir.HasError() {
		return nil, ir.Error
	}
	if !b.resolveCompileTimeReferences() {
		return nil, ir.Error
	}
	if message, ok := b.validateCallTermBindings(); !ok {
		return nil, message
	}
	if len(ir.VariableStringIDs) > MaxVariableSlots {
		return nil, "Generated domain requires " + strconv.Itoa(len(ir.VariableStringIDs)) +
			" variable slots, but HTN_GENERATED_MAX_VARIABLE_SLOTS is " + strconv.Itoa(MaxVariableSlots)
	}
	return ir, ""
}
