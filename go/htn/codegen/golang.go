package codegen

import (
	"errors"
	"fmt"
	"go/format"
	"math"
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
)

// BacktrackingPolicy selects how generated planners store pending
// continuations.
type BacktrackingPolicy uint8

const (
	// FixedWithOverflow grows storage beyond the inline capacity.
	FixedWithOverflow BacktrackingPolicy = iota
	// FixedCapacity fails with BACKTRACKING_CAPACITY_EXCEEDED when full.
	FixedCapacity
)

// DefaultModulePath is the import path of this Go module.
const DefaultModulePath = "github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go"

// Options configures Go code generation.
type Options struct {
	// PackageName of the generated file.
	PackageName string
	// EntryPointName names the exported accessor <EntryPointName>_GetDefinition.
	EntryPointName string
	// SourceFilePath is the display path of the root domain.
	SourceFilePath string
	// LinkedSourceFiles are the display paths of every linked source file.
	LinkedSourceFiles          []string
	BacktrackingPolicy         BacktrackingPolicy
	RuntimeBacktrackingSupport bool
	BacktrackingCapacity       uint32
	CallFrameCapacity          uint32
	// ModulePath overrides the import path of the htn packages.
	ModulePath string
	// ToolName is used in the capacity diagnostic ("htn-translator").
	ToolName string
}

// DefaultOptions returns the translator defaults.
func DefaultOptions() Options {
	return Options{BacktrackingCapacity: 32, CallFrameCapacity: 8192, ModulePath: DefaultModulePath, ToolName: "htn-translator"}
}

func isIdentifierStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentifierChar(c byte) bool { return isIdentifierStart(c) || (c >= '0' && c <= '9') }

// Generate emits the Go source of a planner for a linked domain.
func Generate(domain *compiler.Domain, options Options) (string, error) {
	if options.EntryPointName == "" {
		return "", errors.New("Entry point name must not be empty")
	}
	if options.CallFrameCapacity == 0 {
		return "", errors.New("Call frame capacity must be greater than zero")
	}
	if options.BacktrackingCapacity == 0 {
		return "", errors.New("Backtracking capacity must be greater than zero")
	}
	if options.BacktrackingPolicy != FixedWithOverflow && options.BacktrackingPolicy != FixedCapacity {
		return "", errors.New("Unknown backtracking policy")
	}
	name := options.EntryPointName
	if name[0] < 'A' || name[0] > 'Z' {
		return "", errors.New("Entry point must be an exported Go identifier: " + name)
	}
	for i := 0; i < len(name); i++ {
		if !isIdentifierChar(name[i]) {
			return "", errors.New("Entry point must be an exported Go identifier: " + name)
		}
	}
	if options.PackageName == "" {
		options.PackageName = "generated"
	}
	if options.ModulePath == "" {
		options.ModulePath = DefaultModulePath
	}
	if options.ToolName == "" {
		options.ToolName = "htn-translator"
	}
	sourceFiles := options.LinkedSourceFiles
	if len(sourceFiles) == 0 {
		file := options.SourceFilePath
		if file == "" {
			file = "<domain>"
		}
		sourceFiles = []string{file}
	}
	ir, message := compiler.BuildIR(domain, sourceFiles, options.RuntimeBacktrackingSupport)
	if ir == nil {
		return "", errors.New(message)
	}
	g := &generator{ir: ir, options: options, symbols: map[string]string{}}
	source := g.makeSource()
	if ir.HasError() {
		return "", errors.New(ir.Error)
	}
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return source, fmt.Errorf("internal error: generated Go does not parse: %w", err)
	}
	return string(formatted), nil
}

type generator struct {
	ir        *compiler.IR
	options   Options
	nextLabel int
	symbols   map[string]string
	symOrder  []string
	callSites []string
	top       strings.Builder
	funcs     strings.Builder
	initBody  strings.Builder
}

func (g *generator) newLabel() int {
	g.nextLabel++
	return g.nextLabel
}

func (g *generator) symbol(text string) string {
	if name, ok := g.symbols[text]; ok {
		return name
	}
	name := "sym" + strconv.Itoa(len(g.symOrder))
	g.symbols[text] = name
	g.symOrder = append(g.symOrder, text)
	return name
}

func (g *generator) atomLiteral(a atom.Atom) string {
	switch a.Kind() {
	case atom.KindBool:
		if a.Bool() {
			return "atom.NewBool(true)"
		}
		return "atom.NewBool(false)"
	case atom.KindInt:
		return fmt.Sprintf("atom.NewInt(%d)", a.Int())
	case atom.KindFloat:
		return fmt.Sprintf("atom.NewFloat(math.Float32frombits(0x%08x))", math.Float32bits(a.Float()))
	case atom.KindString:
		return "atom.NewString(" + strconv.Quote(a.Str()) + ")"
	case atom.KindSymbol:
		return "atom.NewSymbol(" + g.symbol(a.Symbol().Text()) + ")"
	case atom.KindList:
		parts := make([]string, 0, a.Len())
		for _, e := range a.Elements() {
			parts = append(parts, g.atomLiteral(e))
		}
		return "atom.NewList(" + strings.Join(parts, ", ") + ")"
	}
	return "atom.Atom{}"
}

func staticName(index uint32) string { return "sv" + strconv.FormatUint(uint64(index), 10) }

// Debugger events (HTN_GENERATED_EVENT_DEBUG_*). The planner's event helpers
// do nothing unless planner.DebugEnabled (the "htndebug" build tag), so
// release builds inline them away like the original's empty macros.

func debugEvent(event string, argument any) string {
	return fmt.Sprintf("ex.Debug%s(&definition, %v)", event, argument)
}

// debugStatements returns event calls as statements indented by indent.
func debugStatements(indent string, calls ...string) string {
	var b strings.Builder
	for _, call := range calls {
		b.WriteString(indent + call + "\n")
	}
	return b.String()
}

func goComment(expression string) string {
	expression = strings.ReplaceAll(expression, "\n", "\\n")
	return strings.ReplaceAll(expression, "\r", "\\r")
}

func (g *generator) sourceFile(index uint32) string {
	if int(index) < len(g.ir.SourceFiles) {
		return g.ir.SourceFiles[index]
	}
	return ""
}

func (g *generator) callSource(location compiler.SourceLocation) string {
	name := "cs" + strconv.Itoa(len(g.callSites))
	g.callSites = append(g.callSites, fmt.Sprintf("var %s = callterm.Source{Domain: %s, File: %s, Line: %d, Column: %d}",
		name, strconv.Quote(g.ir.DomainID), strconv.Quote(g.sourceFile(location.FileIndex)),
		location.Range.Begin.Line, location.Range.Begin.Column))
	return name
}

func (g *generator) arithmeticError(v *compiler.IRValue, context string) string {
	file := "<unknown domain>"
	if int(v.Source.FileIndex) < len(g.ir.SourceFiles) {
		file = g.ir.SourceFiles[v.Source.FileIndex]
	}
	line := v.Source.Range.Begin.Line
	if line < 1 {
		line = 1
	}
	column := v.Source.Range.Begin.Column
	if column < 1 {
		column = 1
	}
	expression := "<unknown expression>"
	if int(v.DebugText) < len(g.ir.Strings.Values) {
		expression = g.ir.Strings.Values[v.DebugText]
	}
	return file + "(" + strconv.Itoa(line) + "," + strconv.Itoa(column) + "): error: Arithmetic expression '" +
		expression + "' cannot be used " + context
}

const defaultValueContext = "in this generated value context"

// valueRef returns a Go expression of type atom.Atom for a value reference
// (BuildGeneratedValueAtomReference). An unbound atom stands for NULL.
func (g *generator) valueRef(index uint32, context string) string {
	if int(index) >= len(g.ir.Values) {
		g.ir.SetError("Generated value reference is out of range")
		return "atom.Atom{}"
	}
	return g.recordRef(&g.ir.Values[index], context)
}

func (g *generator) recordRef(v *compiler.IRValue, context string) string {
	if v.Kind == compiler.ValueArithmetic {
		g.ir.SetError(g.arithmeticError(v, context))
		return "atom.Atom{}"
	}
	if v.Kind == compiler.ValueVariable {
		if v.VariableSlot == compiler.NoIndex {
			return "atom.Atom{}"
		}
		return fmt.Sprintf("ex.V[%d]", v.VariableSlot)
	}
	if v.StaticValueIndex == compiler.NoIndex {
		g.ir.SetError("Generated static value has no prepared-value index")
		return "atom.Atom{}"
	}
	return staticName(v.StaticValueIndex)
}

// arithmeticValue returns a Go expression evaluating an arithmetic operand
// tree (EmitGeneratedArithmeticValue).
func (g *generator) arithmeticValue(v *compiler.IRValue, context string) string {
	switch v.Kind {
	case compiler.ValueVariable:
		if v.VariableSlot == compiler.NoIndex {
			return "atom.Atom{}"
		}
		return fmt.Sprintf("ex.V[%d]", v.VariableSlot)
	case compiler.ValueConstant:
		for i := range g.ir.Constants {
			if g.ir.Constants[i].ID == v.Text {
				return g.valueRef(g.ir.Constants[i].Value, defaultValueContext)
			}
		}
		return "atom.Atom{}"
	case compiler.ValueArithmetic:
	default:
		switch v.AtomType {
		case atom.KindInt:
			return fmt.Sprintf("atom.NewInt(%d)", v.Literal.Int())
		case atom.KindFloat:
			return fmt.Sprintf("atom.NewFloat(math.Float32frombits(0x%08x))", math.Float32bits(v.Literal.Float()))
		}
		return "atom.Atom{}"
	}
	if int(v.ArithmeticExpression) >= len(g.ir.ArithmeticExpressions) {
		g.ir.SetError(g.arithmeticError(v, context))
		return "atom.Atom{}"
	}
	expression := &g.ir.ArithmeticExpressions[v.ArithmeticExpression]
	operands := make([]string, 0, len(expression.Operands))
	for i := range expression.Operands {
		operands = append(operands, g.arithmeticValue(&expression.Operands[i], context))
	}
	return fmt.Sprintf("planner.Arith(%d, []atom.Atom{%s})", expression.Operator, strings.Join(operands, ", "))
}

// argumentValue evaluates a condition/task argument: arithmetic expressions
// are computed, other values are referenced.
func (g *generator) argumentValue(index uint32, context string) string {
	v := &g.ir.Values[index]
	if v.Kind == compiler.ValueArithmetic {
		return g.arithmeticValue(v, context)
	}
	return g.valueRef(index, defaultValueContext)
}

func (g *generator) makeSource() string {
	ir := g.ir
	var out strings.Builder
	fmt.Fprintf(&out, "// Code generated by %s. DO NOT EDIT.\n", g.options.ToolName)
	fmt.Fprintf(&out, "// Source domain: %s\n", goComment(g.displaySourceFile()))
	if len(g.options.LinkedSourceFiles) > 1 {
		out.WriteString("// Linked domain sources:\n")
		for _, f := range g.options.LinkedSourceFiles {
			fmt.Fprintf(&out, "//   %s\n", goComment(f))
		}
	}
	fmt.Fprintf(&out, "\npackage %s\n\n", g.options.PackageName)
	fmt.Fprintf(&out, "import (\n\t\"math\"\n\n\t\"%s/htn/atom\"\n\t\"%s/htn/callterm\"\n\t\"%s/htn/planner\"\n)\n\n",
		g.options.ModulePath, g.options.ModulePath, g.options.ModulePath)
	out.WriteString("var _ = math.Float32frombits\n\n")

	// Static values (prepared storage in the original).
	var statics strings.Builder
	for i := range ir.StaticValues {
		sv := &ir.StaticValues[i]
		var literal string
		if sv.Literal.IsBound() {
			literal = g.atomLiteral(sv.Literal)
		} else {
			literal = "atom.NewString(" + strconv.Quote(ir.Strings.Get(sv.Text)) + ")"
		}
		fmt.Fprintf(&statics, "var %s = %s\n", staticName(uint32(i)), literal)
	}

	factSymbols := make([]string, 0, len(ir.FactStringIDs))
	factNames := make([]string, 0, len(ir.FactStringIDs))
	for _, id := range ir.FactStringIDs {
		factSymbols = append(factSymbols, g.symbol(ir.Strings.Get(id)))
		factNames = append(factNames, strconv.Quote(ir.Strings.Get(id)))
	}
	callNames := make([]string, 0, len(ir.CallTermStringIDs))
	for _, id := range ir.CallTermStringIDs {
		callNames = append(callNames, strconv.Quote(ir.Strings.Get(id)))
	}

	restoreSlots, maxRestore := g.taskRestoreSlots()
	snapshotCapacity := int(g.options.BacktrackingCapacity) * maxRestore
	if g.options.BacktrackingPolicy != FixedCapacity && snapshotCapacity > compiler.MaxVariableSlots {
		snapshotCapacity = compiler.MaxVariableSlots
	}

	requirements := g.callTermRequirements()

	// Emission order mirrors the original generator so that the first
	// translation error is the same.
	for i := range ir.Conditions {
		c := &ir.Conditions[i]
		if c.Kind != compiler.IRCondAxiom || c.ResolvedIndex == compiler.NoIndex || int(c.ResolvedIndex) >= len(ir.Axioms) {
			continue
		}
		g.emitAxiomHelpers(uint32(i))
	}
	for i := range ir.Conditions {
		c := &ir.Conditions[i]
		if c.Kind != compiler.IRCondFact {
			continue
		}
		hasVariable := false
		for a := uint32(0); a < c.ArgumentCount; a++ {
			if int(c.FirstArgument+a) < len(ir.Values) && ir.Values[c.FirstArgument+a].Kind == compiler.ValueVariable {
				hasVariable = true
				break
			}
		}
		if hasVariable {
			g.emitFactChoiceHelper(uint32(i))
		}
	}
	g.emitBranchContinuations(restoreSlots)
	for t := range ir.Tasks {
		g.emitTask(uint32(t))
	}
	for m := range ir.Methods {
		g.emitMethod(uint32(m))
	}
	g.emitEntryPoint()

	// Assemble: symbols, statics and call sites are emitted after the helpers
	// registered them.
	for i, text := range g.symOrder {
		fmt.Fprintf(&out, "var sym%d = atom.Intern(%s)\n", i, strconv.Quote(text))
	}
	out.WriteString("\n")
	out.WriteString(statics.String())
	out.WriteString("\n")
	fmt.Fprintf(&out, "var factSymbols = []*atom.Symbol{%s}\n\n", strings.Join(factSymbols, ", "))
	fmt.Fprintf(&out, "var callNames = []string{%s}\n\n", strings.Join(callNames, ", "))
	for _, site := range g.callSites {
		out.WriteString(site + "\n")
	}
	out.WriteString("\n")
	out.WriteString(g.top.String())

	capacityError := g.displaySourceFile() + ": Domain '" + ir.DomainID + "' exceeded its call-frame capacity (" +
		strconv.FormatUint(uint64(g.options.CallFrameCapacity), 10) + "). Regenerate the domain with " +
		g.options.ToolName + " --call-frame-capacity=<larger value> and recompile the generated code."
	fmt.Fprintf(&out, "func newExecution() *planner.Exec {\n\treturn planner.NewExec(planner.Config{VariableCount: %d, FactCount: %d, CallTermCount: %d, CallFrameCapacity: %d, FixedCapacity: %t, BacktrackingCapacity: %d, SnapshotCapacity: %d, CapacityError: %s})\n}\n\n",
		len(ir.VariableStringIDs), len(ir.FactStringIDs), len(ir.CallTermStringIDs), g.options.CallFrameCapacity,
		g.options.BacktrackingPolicy == FixedCapacity, g.options.BacktrackingCapacity, snapshotCapacity,
		strconv.Quote(capacityError))

	out.WriteString("type preparedData struct{ domain string }\n\n")
	fmt.Fprintf(&out, "var prepared = &preparedData{domain: %s}\n\n", strconv.Quote(ir.DomainID))
	out.WriteString("var definition planner.Definition\n\n")
	fmt.Fprintf(&out, "// %s_GetDefinition returns the generated planner definition of domain %s.\n", g.options.EntryPointName, goComment(ir.DomainID))
	fmt.Fprintf(&out, "func %s_GetDefinition() *planner.Definition { return &definition }\n\n", g.options.EntryPointName)
	out.WriteString("func init() {\n")
	out.WriteString(g.initBody.String())
	features := "planner.FeatureNone"
	if ir.RuntimeBacktrackingSupport {
		features = "planner.FeatureRuntimeBacktracking"
	}
	fmt.Fprintf(&out, "\tdefinition = planner.Definition{\n\t\tABIVersion: planner.ABIVersion,\n\t\tFeatures: %s,\n\t\tDomainID: %s,\n\t\tSourceFile: %s,\n",
		features, strconv.Quote(ir.DomainID), strconv.Quote(g.displaySourceFile()))
	out.WriteString("\t\tNewPreparedStorage: func() any { return prepared },\n\t\tNewExecutionStorage: newExecution,\n\t\tDecomposeCall: decomposeCall,\n")
	fmt.Fprintf(&out, "\t\tFactNames: []string{%s},\n", strings.Join(factNames, ", "))
	out.WriteString("\t\tCallTermRequirements: []callterm.Requirement{\n")
	for _, r := range requirements {
		out.WriteString("\t\t\t" + r + ",\n")
	}
	out.WriteString("\t\t},\n\t}\n")
	out.WriteString("\tif planner.DebugEnabled {\n\t\tdefinition.DebugMetadata = newDebugMetadata()\n\t}\n}\n\n")
	g.emitDebugMetadata(&out)
	out.WriteString(g.funcs.String())
	return out.String()
}

// emitDebugMetadata emits newDebugMetadata, the compiled-domain description
// consumed by debuggers (HTN_DEBUG_DECOMPOSITION metadata). Records are
// flattened into the rows of planner.DebugTables.
func (g *generator) emitDebugMetadata(out *strings.Builder) {
	ir := g.ir
	index := func(v uint32) string {
		if v == compiler.NoIndex {
			return "planner.NoIndex"
		}
		return strconv.FormatUint(uint64(v), 10)
	}
	wideIndex := func(v uint32) string {
		if v == compiler.NoIndex {
			return "uint64(planner.NoIndex)"
		}
		return strconv.FormatUint(uint64(v), 10)
	}
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	table := func(name, typ string, rows []string) {
		fmt.Fprintf(out, "\t\t%s: []%s{", name, typ)
		if len(rows) == 0 {
			out.WriteString("},\n")
			return
		}
		out.WriteString("\n")
		for _, row := range rows {
			out.WriteString("\t\t\t" + row + ",\n")
		}
		out.WriteString("\t\t},\n")
	}
	quoted := func(values []string) []string {
		rows := make([]string, len(values))
		for i, v := range values {
			rows[i] = strconv.Quote(v)
		}
		return rows
	}
	source := func(s compiler.SourceLocation) string {
		return fmt.Sprintf("%d, %d, %d, %d, %d", s.FileIndex, s.Range.Begin.Line, s.Range.Begin.Column,
			s.Range.End.Line, s.Range.End.Column)
	}
	join := func(parts ...string) string { return strings.Join(parts, ", ") }
	number := func(v uint32) string { return strconv.FormatUint(uint64(v), 10) }
	maskWords := func(mask compiler.SlotMask) string {
		words := make([]string, len(mask))
		for i, w := range mask {
			words[i] = "0x" + strconv.FormatUint(w, 16)
		}
		return strings.Join(words, ", ")
	}

	out.WriteString("func newDebugMetadata() *planner.DebugMetadata {\n\treturn planner.NewDebugMetadata(&planner.DebugTables{\n")
	fmt.Fprintf(out, "\t\tSourceFile: %s,\n", strconv.Quote(g.displaySourceFile()))
	table("Strings", "string", quoted(ir.Strings.Values))

	var rows []string
	for i := range ir.Values {
		v := &ir.Values[i]
		var flags uint32
		if v.Kind == compiler.ValueVariable && v.DebugAsVariable {
			flags |= 1
		}
		if v.Kind == compiler.ValueLiteral && v.AtomType == atom.KindString {
			flags |= 2
		}
		if v.Kind == compiler.ValueVariable && !v.DebugAsVariable && v.DebugText != v.Text {
			flags |= 4
		}
		rows = append(rows, join(number(flags), index(v.DebugText), index(v.Text), number(v.SourceLine), index(v.VariableSlot)))
	}
	table("Values", "uint32", rows)

	rows = nil
	for _, id := range ir.VariableStringIDs {
		if ir.DebugInternalVariableStringIDs[id] {
			rows = append(rows, "planner.NoIndex")
		} else {
			rows = append(rows, index(id))
		}
	}
	table("VariableStringIDs", "uint32", rows)

	rows = nil
	var expressions []string
	for i := range ir.Conditions {
		v := &ir.Conditions[i]
		d := v
		if v.DebugCondition != compiler.NoIndex {
			d = &ir.Conditions[v.DebugCondition]
		}
		rows = append(rows, join(number(uint32(d.Kind)), index(d.ID), index(d.FirstArgument), index(d.ArgumentCount),
			index(v.FirstChildRef), index(v.ChildCount), index(d.OutputValue), index(d.ResolvedIndex),
			number(uint32(v.DebugSource.Range.Begin.Line)), flag(v.DebugInternal)))
		expressions = append(expressions, v.DebugExpression)
	}
	table("Conditions", "uint32", rows)
	table("ConditionExpressions", "string", quoted(expressions))

	rows = nil
	for _, ref := range ir.ConditionChildRefs {
		rows = append(rows, index(ref))
	}
	table("ConditionChildRefs", "uint32", rows)

	rows = nil
	for i := range ir.Tasks {
		t := &ir.Tasks[i]
		rows = append(rows, join(number(uint32(t.Kind)), index(t.ID), index(t.FirstArgument), index(t.ArgumentCount),
			number(t.SourceLine), index(t.PlanStepHead)))
	}
	table("Tasks", "uint32", rows)

	rows = nil
	for i := range ir.Branches {
		b := &ir.Branches[i]
		rows = append(rows, join(index(b.ID), index(b.Condition), index(b.FirstTask), index(b.TaskCount), number(b.SourceLine)))
	}
	table("Branches", "uint32", rows)

	rows = nil
	for i := range ir.Methods {
		m := &ir.Methods[i]
		rows = append(rows, join(wideIndex(m.ID), wideIndex(m.FirstParameter), wideIndex(m.ParameterCount),
			wideIndex(m.FirstBranch), wideIndex(m.BranchCount), number(m.SourceLine), maskWords(m.VariableSlotMask)))
	}
	table("Methods", "uint64", rows)

	rows = nil
	for i := range ir.Axioms {
		a := &ir.Axioms[i]
		rows = append(rows, join(wideIndex(a.ID), wideIndex(a.FirstParameter), wideIndex(a.ParameterCount),
			wideIndex(a.Condition), number(a.SourceLine), maskWords(a.VariableSlotMask)))
	}
	table("Axioms", "uint64", rows)

	rows = nil
	for i := range ir.Constants {
		c := &ir.Constants[i]
		rows = append(rows, join(index(c.GroupID), index(c.ID), index(c.Value), number(c.SourceLine)))
	}
	table("Constants", "uint32", rows)

	fmt.Fprintf(out, "\t\tCallTermSlotCount: %d,\n\t\tFactSlotCount: %d,\n", len(ir.CallTermStringIDs), len(ir.FactStringIDs))
	sourceFiles := ir.SourceFiles
	if len(sourceFiles) == 0 {
		sourceFiles = []string{g.displaySourceFile()}
	}
	table("SourceFiles", "string", quoted(sourceFiles))

	sources := func(name string, count int, location func(int) compiler.SourceLocation) {
		rows := make([]string, count)
		for i := range rows {
			rows[i] = source(location(i))
		}
		table(name, "uint32", rows)
	}
	sources("ValueSources", len(ir.Values), func(i int) compiler.SourceLocation { return ir.Values[i].Source })
	sources("ConditionSources", len(ir.Conditions), func(i int) compiler.SourceLocation { return ir.Conditions[i].DebugSource })
	sources("TaskSources", len(ir.Tasks), func(i int) compiler.SourceLocation { return ir.Tasks[i].Source })
	sources("BranchSources", len(ir.Branches), func(i int) compiler.SourceLocation { return ir.Branches[i].Source })
	sources("MethodSources", len(ir.Methods), func(i int) compiler.SourceLocation { return ir.Methods[i].Source })
	sources("AxiomSources", len(ir.Axioms), func(i int) compiler.SourceLocation { return ir.Axioms[i].Source })
	sources("ConstantSources", len(ir.Constants), func(i int) compiler.SourceLocation { return ir.Constants[i].Source })
	out.WriteString("\t})\n}\n\n")
}

func (g *generator) displaySourceFile() string {
	if g.options.SourceFilePath == "" {
		return "<domain>"
	}
	return g.options.SourceFilePath
}

func (g *generator) callTermRequirements() []string {
	ir := g.ir
	type site struct {
		id, file uint32
		line     int
		column   int
	}
	seen := map[site]bool{}
	var result []string
	emit := func(id uint32, location compiler.SourceLocation) {
		key := site{id, location.FileIndex, location.Range.Begin.Line, location.Range.Begin.Column}
		if seen[key] {
			return
		}
		seen[key] = true
		result = append(result, fmt.Sprintf("{Name: %s, Source: callterm.Source{Domain: %s, File: %s, Line: %d, Column: %d}}",
			strconv.Quote(ir.Strings.Get(id)), strconv.Quote(ir.DomainID), strconv.Quote(g.sourceFile(location.FileIndex)),
			location.Range.Begin.Line, location.Range.Begin.Column))
	}
	for i := range ir.Conditions {
		c := &ir.Conditions[i]
		if c.Kind == compiler.IRCondCall || c.Kind == compiler.IRCondCallBind {
			emit(c.ID, c.Source)
		}
	}
	for _, calls := range ir.TaskCallExpressions {
		for i := range calls {
			emit(calls[i].ID, calls[i].Source)
		}
	}
	return result
}

// taskRestoreSlots computes, for every task after the first of its branch, the
// slots its immediately preceding sibling may mutate.
func (g *generator) taskRestoreSlots() ([][]uint32, int) {
	ir := g.ir
	restore := make([][]uint32, len(ir.Tasks))
	mutation := func(task uint32) compiler.SlotMask {
		var mask compiler.SlotMask
		if int(task) >= len(ir.Tasks) {
			return mask
		}
		if int(task) < len(ir.TaskCallExpressions) {
			for _, call := range ir.TaskCallExpressions[task] {
				mask.Add(call.OutputSlot)
			}
		}
		t := &ir.Tasks[task]
		if t.Kind == compiler.IRTaskCompound {
			if target := ir.FindMethod(t.ID, t.ArgumentCount); target >= 0 {
				for w := range mask {
					mask[w] |= ir.Methods[target].VariableSlotMask[w]
				}
			}
		}
		return mask
	}
	maxRestore := 0
	for bi := range ir.Branches {
		b := &ir.Branches[bi]
		for local := uint32(1); local < b.TaskCount; local++ {
			task := b.FirstTask + local
			mask := mutation(task - 1)
			restore[task] = mask.Slots()
			if len(restore[task]) > maxRestore {
				maxRestore = len(restore[task])
			}
		}
	}
	return restore, maxRestore
}

func slotList(slots []uint32) string {
	parts := make([]string, len(slots))
	for i, s := range slots {
		parts[i] = strconv.FormatUint(uint64(s), 10)
	}
	return "[]uint32{" + strings.Join(parts, ", ") + "}"
}

func (g *generator) emitBranchContinuations(restore [][]uint32) {
	ir := g.ir
	for bi := range ir.Branches {
		b := &ir.Branches[bi]
		if b.TaskCount == 0 {
			continue
		}
		fmt.Fprintf(&g.top, "var bc%d planner.BranchContinuations\n", bi)
		var tasks []string
		total := 0
		for i := uint32(0); i < b.TaskCount; i++ {
			task := b.FirstTask + (b.TaskCount - 1 - i)
			slots := restore[task]
			total += len(slots)
			if len(slots) == 0 {
				tasks = append(tasks, fmt.Sprintf("{Fn: task%d}", task))
			} else {
				tasks = append(tasks, fmt.Sprintf("{Fn: task%d, Restore: %s}", task, slotList(slots)))
			}
		}
		fmt.Fprintf(&g.initBody, "\tbc%d = planner.BranchContinuations{Tasks: []planner.PendingTask{%s}, TotalRestore: %d}\n",
			bi, strings.Join(tasks, ", "), total)
	}
	g.top.WriteString("\n")
}

// ---------------------------------------------------------------------------
// Function body writer

type fnLine struct {
	label  int
	text   string
	indent int
}

type fnWriter struct {
	lines    []fnLine
	decls    []string
	declared map[string]bool
	used     map[int]bool
	indent   int
}

func newFnWriter() *fnWriter {
	return &fnWriter{declared: map[string]bool{}, used: map[int]bool{}, indent: 1}
}

func (w *fnWriter) code(format string, args ...any) {
	w.lines = append(w.lines, fnLine{label: -1, text: fmt.Sprintf(format, args...), indent: w.indent})
}

func (w *fnWriter) comment(text string) {
	if text != "" {
		w.code("// %s", goComment(text))
	}
}

func (w *fnWriter) label(l int) { w.lines = append(w.lines, fnLine{label: l}) }

func (w *fnWriter) jump(l int) string {
	w.used[l] = true
	return "goto L" + strconv.Itoa(l)
}

func (w *fnWriter) declare(name, typ string) {
	if !w.declared[name] {
		w.declared[name] = true
		w.decls = append(w.decls, name+" "+typ)
	}
}

func (w *fnWriter) open(format string, args ...any) {
	w.code(format, args...)
	w.indent++
}

func (w *fnWriter) close() {
	w.indent--
	w.code("}")
}

// gotoTargets extracts the labels referenced by "goto L<n>" in a line.
func gotoTargets(text string, visit func(int)) {
	for {
		index := strings.Index(text, "goto L")
		if index < 0 {
			return
		}
		text = text[index+len("goto L"):]
		end := 0
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
		}
		if label, err := strconv.Atoi(text[:end]); err == nil {
			visit(label)
		}
		text = text[end:]
	}
}

// eliminateDeadCode drops top-level statements that follow an unconditional
// goto/return until the next referenced label, repeating until stable so that
// no unused label or unreachable statement remains.
func (w *fnWriter) eliminateDeadCode(pinned map[int]bool) []fnLine {
	lines := w.lines
	for {
		used := map[int]bool{}
		for l := range pinned {
			used[l] = true
		}
		for _, line := range lines {
			if line.label < 0 {
				gotoTargets(line.text, func(l int) { used[l] = true })
			}
		}
		kept := make([]fnLine, 0, len(lines))
		reachable := true
		for _, line := range lines {
			if line.label >= 0 {
				if used[line.label] {
					reachable = true
					kept = append(kept, line)
				}
				continue
			}
			if !reachable {
				continue
			}
			kept = append(kept, line)
			if line.indent == 1 && (strings.HasPrefix(line.text, "goto ") || strings.HasPrefix(line.text, "return")) {
				reachable = false
			}
		}
		if len(kept) == len(lines) {
			return kept
		}
		lines = kept
	}
}

func (w *fnWriter) render(out *strings.Builder, pinned map[int]bool) {
	for _, d := range w.decls {
		out.WriteString("\tvar " + d + "\n")
	}
	for _, d := range w.decls {
		name := d[:strings.IndexByte(d, ' ')]
		out.WriteString("\t_ = " + name + "\n")
	}
	for _, line := range w.eliminateDeadCode(pinned) {
		if line.label >= 0 {
			fmt.Fprintf(out, "L%d:\n", line.label)
			continue
		}
		out.WriteString(strings.Repeat("\t", line.indent))
		out.WriteString(line.text)
		out.WriteString("\n")
	}
}

// ---------------------------------------------------------------------------
// Axioms

func axiomDirection(ir *compiler.IR, parameter *compiler.IRValue) (input, output bool) {
	if parameter.Kind != compiler.ValueVariable || parameter.VariableSlot == compiler.NoIndex ||
		int(parameter.Text) >= len(ir.Strings.Values) {
		ir.SetError("Generated axiom parameter has no compile-time variable slot")
		return true, false
	}
	name := ir.Strings.Values[parameter.Text]
	if strings.HasPrefix(name, "out_") {
		return false, true
	}
	if strings.HasPrefix(name, "io_") {
		return true, true
	}
	return true, false
}

func (g *generator) emitAxiomHelpers(condition uint32) {
	ir := g.ir
	c := &ir.Conditions[condition]
	axiom := &ir.Axioms[c.ResolvedIndex]
	if axiom.ParameterCount != c.ArgumentCount {
		ir.SetError("Axiom call/parameter arity mismatch while emitting direct axiom helpers")
		return
	}
	axiomName := ir.Strings.Get(c.ID)
	if int(c.ID) >= len(ir.Strings.Values) {
		axiomName = "<unknown>"
	}
	f := &g.funcs
	fmt.Fprintf(f, "func axiomBegin%d(ex *planner.Exec, scope *planner.AxiomScope) {\n", condition)
	f.WriteString("\tfor i := range scope.Args {\n\t\tscope.Args[i] = atom.Atom{}\n\t}\n")
	for i := uint32(0); i < c.ArgumentCount; i++ {
		parameter := &ir.Values[axiom.FirstParameter+i]
		_, output := axiomDirection(ir, parameter)
		context := "as argument " + strconv.FormatUint(uint64(i+1), 10) + " of axiom '" + axiomName + "'"
		caller := &ir.Values[c.FirstArgument+i]
		var reference string
		if caller.Kind == compiler.ValueArithmetic {
			reference = g.arithmeticValue(caller, context)
		} else {
			reference = g.valueRef(c.FirstArgument+i, context)
		}
		fmt.Fprintf(f, "\tin%d := %s\n", i, reference)
		if output {
			fmt.Fprintf(f, "\tif in%d.IsBound() {\n\t\tscope.Args[%d] = in%d\n\t}\n", i, i, i)
		}
	}
	for k, slot := range axiom.VariableSlotMask.Slots() {
		fmt.Fprintf(f, "\tscope.Saved[%d] = ex.V[%d]\n\tex.V[%d] = atom.Atom{}\n", k, slot, slot)
	}
	f.WriteString("\tscope.CallerFrame = ex.CurrentFrameID\n\tex.EnterFrame()\n")
	for i := uint32(0); i < c.ArgumentCount; i++ {
		parameter := &ir.Values[axiom.FirstParameter+i]
		input, _ := axiomDirection(ir, parameter)
		if !input {
			continue
		}
		if parameter.VariableSlot == compiler.NoIndex {
			ir.SetError("Generated axiom input parameter has no variable slot")
			return
		}
		fmt.Fprintf(f, "\tif in%d.IsBound() {\n\t\tex.SetIfChanged(%d, in%d)\n\t}\n", i, parameter.VariableSlot, i)
	}
	f.WriteString(debugStatements("\t", debugEvent("BeginAxiom", c.ResolvedIndex)))
	f.WriteString("}\n\n")

	fmt.Fprintf(f, "func axiomEnd%d(ex *planner.Exec, succeeded bool, scope *planner.AxiomScope) bool {\n", condition)
	f.WriteString("\tvalid := succeeded\n")
	for i := uint32(0); i < c.ArgumentCount; i++ {
		parameter := &ir.Values[axiom.FirstParameter+i]
		if _, output := axiomDirection(ir, parameter); !output {
			continue
		}
		caller := &ir.Values[c.FirstArgument+i]
		if parameter.VariableSlot == compiler.NoIndex {
			ir.SetError("Generated axiom output parameter has no variable slot")
			return
		}
		fmt.Fprintf(f, "\tif valid {\n\t\tif output := ex.V[%d]; !output.IsBound() {\n\t\t\tvalid = false\n\t\t} else if scope.Args[%d].IsBound() && !atom.Equal(output, scope.Args[%d]) {\n\t\t\tvalid = false\n\t\t}",
			parameter.VariableSlot, i, i)
		if caller.Kind != compiler.ValueVariable {
			fmt.Fprintf(f, " else if !scope.Args[%d].IsBound() {\n\t\t\tvalid = false\n\t\t}", i)
		}
		f.WriteString("\n\t}\n")
	}
	for i := uint32(0); i < c.ArgumentCount; i++ {
		caller := &ir.Values[c.FirstArgument+i]
		parameter := &ir.Values[axiom.FirstParameter+i]
		if _, output := axiomDirection(ir, parameter); caller.Kind != compiler.ValueVariable || !output {
			continue
		}
		for j := uint32(0); j < i; j++ {
			other := &ir.Values[c.FirstArgument+j]
			otherParameter := &ir.Values[axiom.FirstParameter+j]
			if _, otherOutput := axiomDirection(ir, otherParameter); other.Kind == compiler.ValueVariable &&
				other.VariableSlot == caller.VariableSlot && otherOutput {
				fmt.Fprintf(f, "\tif valid && !atom.Equal(ex.V[%d], ex.V[%d]) {\n\t\tvalid = false\n\t}\n",
					parameter.VariableSlot, otherParameter.VariableSlot)
			}
		}
	}
	var outputs []uint32
	for i := uint32(0); i < c.ArgumentCount; i++ {
		parameter := &ir.Values[axiom.FirstParameter+i]
		caller := &ir.Values[c.FirstArgument+i]
		if _, output := axiomDirection(ir, parameter); !output || caller.Kind != compiler.ValueVariable {
			continue
		}
		if caller.VariableSlot == compiler.NoIndex || parameter.VariableSlot == compiler.NoIndex {
			ir.SetError("Generated axiom variable output has no caller/parameter slot")
			return
		}
		outputs = append(outputs, i)
		fmt.Fprintf(f, "\tvar out%d atom.Atom\n", i)
	}
	if len(outputs) > 0 {
		f.WriteString("\tif valid {\n")
		for _, i := range outputs {
			fmt.Fprintf(f, "\t\tout%d = ex.V[%d]\n", i, ir.Values[axiom.FirstParameter+i].VariableSlot)
		}
		f.WriteString("\t}\n")
	}
	f.WriteString(debugStatements("\t", debugEvent("EndAxiom", "valid")))
	for k, slot := range axiom.VariableSlotMask.Slots() {
		fmt.Fprintf(f, "\tex.V[%d] = scope.Saved[%d]\n", slot, k)
	}
	f.WriteString("\tex.CurrentFrameID = scope.CallerFrame\n")
	if len(outputs) > 0 {
		f.WriteString("\tif valid {\n")
		for _, i := range outputs {
			fmt.Fprintf(f, "\t\tex.SetIfChanged(%d, out%d)\n", ir.Values[c.FirstArgument+i].VariableSlot, i)
		}
		f.WriteString("\t}\n")
	}
	f.WriteString("\treturn valid\n}\n\n")
}

// ---------------------------------------------------------------------------
// Facts

func (g *generator) staticFactMatch(index uint32, argument uint32) string {
	v := resolveStaticValue(g.ir, index)
	if v == nil || v.Kind == compiler.ValueVariable {
		g.ir.SetError("Static fact argument could not be resolved at translation time")
		return "false"
	}
	return fmt.Sprintf("atom.Equal(args[%d], %s)", argument, g.valueRef(index, defaultValueContext))
}

func (g *generator) emitFactChoiceHelper(condition uint32) {
	ir := g.ir
	c := &ir.Conditions[condition]
	f := &g.funcs
	if c.ResolvedIndex == compiler.NoIndex {
		ir.SetError("Generated fact choice has no compile-time fact slot")
		return
	}
	var checks []string
	first := map[uint32]uint32{}
	var firstOrder []uint32
	for i := uint32(0); i < c.ArgumentCount; i++ {
		index := c.FirstArgument + i
		if int(index) >= len(ir.Values) {
			continue
		}
		v := &ir.Values[index]
		if v.Kind == compiler.ValueVariable && v.VariableSlot == compiler.NoIndex {
			continue
		}
		if v.Kind == compiler.ValueVariable {
			checks = append(checks, fmt.Sprintf("if v := ex.V[%d]; v.IsBound() && !atom.Equal(args[%d], v) {\n\t\t\tcontinue\n\t\t}", v.VariableSlot, i))
		} else {
			checks = append(checks, fmt.Sprintf("if !(%s) {\n\t\t\tcontinue\n\t\t}", g.staticFactMatch(index, i)))
		}
		if v.Kind == compiler.ValueVariable && v.VariableSlot != compiler.NoIndex {
			if earlier, exists := first[v.Text]; exists {
				checks = append(checks, fmt.Sprintf("if !atom.Equal(args[%d], args[%d]) {\n\t\t\tcontinue\n\t\t}", earlier, i))
			} else {
				first[v.Text] = i
				firstOrder = append(firstOrder, v.Text)
			}
		}
	}
	fmt.Fprintf(f, "func factChoice%d(ex *planner.Exec, target uint32) bool {\n", condition)
	fmt.Fprintf(f, "\ttable := &ex.FactTables[%d][%d]\n\tvar solution uint32\n", c.ResolvedIndex, c.ArgumentCount)
	f.WriteString("\tfor row, rows := 0, table.RowCount(); row < rows; row++ {\n")
	needsArgs := len(checks) > 0 || len(firstOrder) > 0
	if needsArgs {
		f.WriteString("\t\targs := table.Row(row)\n")
	}
	for _, check := range checks {
		f.WriteString("\t\t" + check + "\n")
	}
	f.WriteString("\t\tif solution != target {\n\t\t\tsolution++\n\t\t\tcontinue\n\t\t}\n")
	for _, text := range firstOrder {
		argument := first[text]
		slot := ir.Values[c.FirstArgument+argument].VariableSlot
		fmt.Fprintf(f, "\t\tif !ex.V[%d].IsBound() && args[%d].IsBound() {\n\t\t\tex.V[%d] = args[%d]\n\t\t}\n", slot, argument, slot, argument)
	}
	f.WriteString("\t\treturn true\n\t}\n\treturn false\n}\n\n")
}

// ---------------------------------------------------------------------------
// Tasks

func (g *generator) emitTask(taskIndex uint32) {
	ir := g.ir
	task := &ir.Tasks[taskIndex]
	f := &g.funcs
	taskName := ir.Strings.Get(task.ID)
	fmt.Fprintf(f, "func task%d(ex *planner.Exec) int {\n", taskIndex)
	endFailed := debugStatements("\t\t", debugEvent("EndTask", false))
	f.WriteString("\tif frame := ex.Frame(); frame.Resume != 0 {\n" +
		debugStatements("\t\t", debugEvent("EndTask", "frame.ChildResult != 0")) + "\t\treturn frame.ChildResult\n\t}\n")
	f.WriteString(debugStatements("\t", debugEvent("BeginTask", taskIndex)))
	if int(taskIndex) < len(ir.TaskCallExpressions) {
		for ci := range ir.TaskCallExpressions[taskIndex] {
			call := &ir.TaskCallExpressions[taskIndex][ci]
			callName := ir.Strings.Get(call.ID)
			if int(call.ID) >= len(ir.Strings.Values) {
				callName = "<unknown>"
			}
			var arguments []string
			for ai := range call.Arguments {
				argument := &call.Arguments[ai]
				switch argument.Kind {
				case compiler.ValueArithmetic:
					arguments = append(arguments, g.arithmeticValue(argument,
						"as argument "+strconv.Itoa(ai+1)+" of callterm '"+callName+"'"))
				case compiler.ValueVariable:
					if argument.VariableSlot == compiler.NoIndex {
						arguments = append(arguments, "atom.Atom{}")
					} else {
						arguments = append(arguments, fmt.Sprintf("ex.V[%d]", argument.VariableSlot))
					}
				default:
					if argument.StaticValueIndex == compiler.NoIndex {
						ir.SetError("Generated task call argument has no prepared-value index")
						return
					}
					arguments = append(arguments, staticName(argument.StaticValueIndex))
				}
			}
			if int(call.CallTermSlot) >= len(ir.CallTermStringIDs) {
				ir.SetError("Generated task call expression has invalid callterm slot")
				return
			}
			source := g.callSource(call.Source)
			fmt.Fprintf(f, "\t// %s\n", goComment(call.DomainExpression))
			argumentList := "nil"
			if len(arguments) > 0 {
				argumentList = "[]atom.Atom{" + strings.Join(arguments, ", ") + "}"
			}
			fmt.Fprintf(f, "\tif result, ok := ex.Invoke(%d, %s, &%s, factSymbols); !ok {\n%s\t\treturn 0\n\t} else {\n\t\tex.SetIfChanged(%d, result)\n\t}\n",
				call.CallTermSlot, argumentList, source, endFailed, call.OutputSlot)
		}
	}
	switch task.Kind {
	case compiler.IRTaskPrimitive, compiler.IRTaskDeferred:
		var arguments []string
		for ai := uint32(0); ai < task.ArgumentCount; ai++ {
			index := task.FirstArgument + ai
			if int(index) >= len(ir.Values) {
				ir.SetError("Generated primitive task argument is out of range")
				return
			}
			argument := &ir.Values[index]
			if argument.Kind == compiler.ValueArithmetic {
				arguments = append(arguments, g.arithmeticValue(argument,
					"as argument "+strconv.FormatUint(uint64(ai+1), 10)+" of primitive task '"+taskName+"'"))
			} else {
				arguments = append(arguments, g.valueRef(index, defaultValueContext))
			}
		}
		fmt.Fprintf(f, "\t// %s\n", goComment(task.DomainExpression))
		argumentList := "nil"
		if len(arguments) > 0 {
			argumentList = "[]atom.Atom{" + strings.Join(arguments, ", ") + "}"
		}
		fmt.Fprintf(f, "\tif !ex.AppendPlanStep(%s, %s) {\n%s\t\treturn 0\n\t}\n%s\treturn 1\n}\n\n",
			g.symbol(ir.Strings.Get(task.PlanStepHead)), argumentList, endFailed,
			debugStatements("\t", debugEvent("EndTask", true)))
	default:
		method := ir.FindMethod(task.ID, task.ArgumentCount)
		if method < 0 {
			f.WriteString(debugStatements("\t", debugEvent("EndTask", false)) + "\treturn 0 // unresolved compound task\n}\n\n")
			return
		}
		fmt.Fprintf(f, "\t// %s\n", goComment(task.DomainExpression))
		target := &ir.Methods[method]
		if task.ArgumentCount != target.ParameterCount {
			ir.SetError("Generated compound task parameter count mismatch")
			return
		}
		for ai := uint32(0); ai < task.ArgumentCount; ai++ {
			index := task.FirstArgument + ai
			if int(index) >= len(ir.Values) {
				ir.SetError("Generated compound task argument is out of range")
				return
			}
			argument := &ir.Values[index]
			var reference string
			if argument.Kind == compiler.ValueArithmetic {
				reference = g.arithmeticValue(argument,
					"as argument "+strconv.FormatUint(uint64(ai+1), 10)+" of compound task '"+taskName+"'")
			} else {
				reference = g.valueRef(index, defaultValueContext)
			}
			fmt.Fprintf(f, "\targ%d := %s\n", ai, reference)
		}
		if task.ArgumentCount > 0 {
			var checks []string
			for ai := uint32(0); ai < task.ArgumentCount; ai++ {
				checks = append(checks, fmt.Sprintf("!arg%d.IsBound()", ai))
			}
			fmt.Fprintf(f, "\tif %s {\n%s\t\treturn 0\n\t}\n", strings.Join(checks, " || "), endFailed)
		}
		for _, slot := range target.VariableSlotMask.Slots() {
			fmt.Fprintf(f, "\tex.V[%d] = atom.Atom{}\n", slot)
		}
		f.WriteString("\tex.EnterFrame()\n")
		for ai := uint32(0); ai < task.ArgumentCount; ai++ {
			parameter := &ir.Values[target.FirstParameter+ai]
			if parameter.Kind != compiler.ValueVariable || parameter.VariableSlot == compiler.NoIndex {
				ir.SetError("Generated compound target parameter has no variable slot")
				return
			}
			fmt.Fprintf(f, "\tex.SetIfChanged(%d, arg%d)\n", parameter.VariableSlot, ai)
		}
		fmt.Fprintf(f, "\tex.Frame().Resume = 1\n\tex.Next = method%d\n\treturn 2\n}\n\n", method)
	}
}

// ---------------------------------------------------------------------------
// Methods and conditions

type commitFn func()
type successFn func(retry int, commit commitFn)

type methodGen struct {
	*generator
	w *fnWriter
}

// debug emits debugger event calls.
func (m *methodGen) debug(calls ...string) {
	for _, call := range calls {
		m.w.code("%s", call)
	}
}

func (m *methodGen) beginCondition(condition uint32) {
	m.debug(debugEvent("BeginCondition", condition))
}

func (m *methodGen) endCondition(result any) {
	m.debug(debugEvent("EndCondition", result))
}

func (m *methodGen) declareCheckpoint(plan checkpointPlan) {
	for _, slot := range plan.slots {
		m.w.declare(fmt.Sprintf("cp%d_%d", plan.id, slot), "atom.Atom")
	}
}

func (m *methodGen) pushCheckpoint(plan checkpointPlan) {
	for _, slot := range plan.slots {
		m.w.code("cp%d_%d = ex.V[%d]", plan.id, slot, slot)
	}
}

func (m *methodGen) rollbackCheckpoint(plan checkpointPlan) {
	for _, slot := range plan.slots {
		m.w.code("ex.V[%d] = cp%d_%d", slot, plan.id, slot)
	}
}

func (m *methodGen) unboundGuard(v *compiler.IRValue, failure int) {
	if v.Kind != compiler.ValueVariable || v.VariableSlot == compiler.NoIndex {
		m.w.code("%s", m.w.jump(failure))
		return
	}
	m.w.code("if ex.V[%d].IsBound() {", v.VariableSlot)
	m.w.code("\t%s", m.w.jump(failure))
	m.w.code("}")
}

func (m *methodGen) emitDeterministicFact(condition uint32, success, failure int) {
	ir := m.ir
	c := &ir.Conditions[condition]
	if c.ResolvedIndex == compiler.NoIndex {
		ir.SetError("Generated fact has no compile-time fact slot")
		return
	}
	var checks []string
	for i := uint32(0); i < c.ArgumentCount; i++ {
		index := c.FirstArgument + i
		if int(index) >= len(ir.Values) {
			continue
		}
		v := &ir.Values[index]
		if v.Kind == compiler.ValueVariable && v.VariableSlot == compiler.NoIndex {
			continue
		}
		if v.Kind == compiler.ValueVariable {
			// Statically bound variable (unbound variables make the fact a
			// choice point instead).
			checks = append(checks, fmt.Sprintf("if !atom.Equal(args[%d], ex.V[%d]) {\n\t\t\tcontinue\n\t\t}", i, v.VariableSlot))
			continue
		}
		checks = append(checks, fmt.Sprintf("if !(%s) {\n\t\t\tcontinue\n\t\t}", m.staticFactMatch(index, i)))
	}
	w := m.w
	w.open("{")
	m.beginCondition(condition)
	w.code("table := &ex.FactTables[%d][%d]", c.ResolvedIndex, c.ArgumentCount)
	w.code("matched := false")
	w.open("for row, rows := 0, table.RowCount(); row < rows; row++ {")
	if len(checks) > 0 {
		w.code("args := table.Row(row)")
		for _, check := range checks {
			for _, line := range strings.Split(check, "\n") {
				w.code("%s", strings.TrimPrefix(line, "\t\t"))
			}
		}
	}
	w.code("matched = true")
	w.code("break")
	w.close()
	m.endCondition("matched")
	w.code("if matched {")
	w.code("\t%s", w.jump(success))
	w.code("}")
	w.code("%s", w.jump(failure))
	w.close()
}

func (m *methodGen) emitLeaf(condition uint32, bound boundSet, success, failure int) {
	ir := m.ir
	c := &ir.Conditions[condition]
	w := m.w
	w.comment(c.DomainExpression)
	switch c.Kind {
	case compiler.IRCondAssignment:
		output := &ir.Values[c.OutputValue]
		input := &ir.Values[c.FirstArgument]
		var reference string
		if input.Kind == compiler.ValueArithmetic {
			reference = m.arithmeticValue(input, defaultValueContext)
		} else {
			reference = m.valueRef(c.FirstArgument, defaultValueContext)
		}
		m.beginCondition(condition)
		w.open("{")
		w.code("value := %s", reference)
		w.open("if value.IsBound() && !ex.V[%d].IsBound() {", output.VariableSlot)
		w.code("ex.V[%d] = value", output.VariableSlot)
		m.endCondition(true)
		w.code("%s", w.jump(success))
		w.close()
		m.endCondition(false)
		w.code("%s", w.jump(failure))
		w.close()
		return
	case compiler.IRCondComparison:
		if c.ArgumentCount != 2 {
			ir.SetError("Built-in comparison must contain exactly two operands")
			return
		}
		if result, ok := staticComparison(ir, c); ok {
			m.beginCondition(condition)
			m.endCondition(result)
			if result {
				w.code("%s", w.jump(success))
			} else {
				w.code("%s", w.jump(failure))
			}
			return
		}
		m.beginCondition(condition)
		left := m.argumentValue(c.FirstArgument, defaultValueContext)
		right := m.argumentValue(c.FirstArgument+1, defaultValueContext)
		w.open("if planner.Compare(%s, %s, %d) {", left, right, c.ID)
		m.endCondition(true)
		w.code("%s", w.jump(success))
		w.close()
		m.endCondition(false)
		w.code("%s", w.jump(failure))
		return
	case compiler.IRCondListSplit:
		if c.ArgumentCount != 3 {
			ir.SetError("Built-in list split must contain exactly three arguments")
			return
		}
		elementOutput := &ir.Values[c.FirstArgument+1]
		remainderOutput := &ir.Values[c.FirstArgument+2]
		listReference := m.valueRef(c.FirstArgument, defaultValueContext)
		elementReference := m.valueRef(c.FirstArgument+1, defaultValueContext)
		remainderReference := m.valueRef(c.FirstArgument+2, defaultValueContext)
		direction := "atom.SplitFront"
		if c.ID == compiler.ListSplitBack {
			direction = "atom.SplitBack"
		}
		elementVariable := elementOutput.Kind == compiler.ValueVariable && elementOutput.VariableSlot != compiler.NoIndex
		remainderVariable := remainderOutput.Kind == compiler.ValueVariable && remainderOutput.VariableSlot != compiler.NoIndex
		m.beginCondition(condition)
		w.open("{")
		w.code("valid := false")
		w.open("if element, remainder, ok := %s.SplitList(%s); ok {", listReference, direction)
		w.code("valid = true")
		if elementVariable {
			w.code("if existing := ex.V[%d]; existing.IsBound() && !atom.Equal(existing, element) {", elementOutput.VariableSlot)
		} else {
			w.code("if reference := %s; !reference.IsBound() || !atom.Equal(reference, element) {", elementReference)
		}
		w.code("\tvalid = false")
		w.code("}")
		if remainderVariable {
			w.code("if existing := ex.V[%d]; existing.IsBound() && !atom.Equal(existing, remainder) {", remainderOutput.VariableSlot)
		} else {
			w.code("if reference := %s; !reference.IsBound() || !atom.Equal(reference, remainder) {", remainderReference)
		}
		w.code("\tvalid = false")
		w.code("}")
		if elementVariable && remainderVariable && elementOutput.VariableSlot == remainderOutput.VariableSlot {
			w.code("if !ex.V[%d].IsBound() && !atom.Equal(element, remainder) {", elementOutput.VariableSlot)
			w.code("\tvalid = false")
			w.code("}")
		}
		w.open("if valid {")
		if elementVariable {
			w.code("if !ex.V[%d].IsBound() {", elementOutput.VariableSlot)
			w.code("\tex.V[%d] = element", elementOutput.VariableSlot)
			w.code("}")
		}
		if remainderVariable {
			w.code("if !ex.V[%d].IsBound() {", remainderOutput.VariableSlot)
			w.code("\tex.V[%d] = remainder", remainderOutput.VariableSlot)
			w.code("}")
		}
		w.close()
		w.close()
		m.endCondition("valid")
		w.code("if valid {")
		w.code("\t%s", w.jump(success))
		w.code("}")
		w.code("%s", w.jump(failure))
		w.close()
		return
	case compiler.IRCondFact:
		m.emitDeterministicFact(condition, success, failure)
		return
	}
	var arguments []string
	for i := uint32(0); i < c.ArgumentCount; i++ {
		arguments = append(arguments, m.argumentValue(c.FirstArgument+i, defaultValueContext))
	}
	argumentList := "nil"
	if len(arguments) > 0 {
		argumentList = "[]atom.Atom{" + strings.Join(arguments, ", ") + "}"
	}
	switch c.Kind {
	case compiler.IRCondCall:
		if int(c.ResolvedIndex) >= len(ir.CallTermStringIDs) {
			ir.SetError("Generated callterm condition has invalid callterm slot")
			return
		}
		source := m.callSource(c.Source)
		m.beginCondition(condition)
		w.open("if result, ok := ex.Invoke(%d, %s, &%s, factSymbols); ok && result.Is(atom.KindBool) && result.Bool() {",
			c.ResolvedIndex, argumentList, source)
		m.endCondition(true)
		w.code("%s", w.jump(success))
		w.close()
		m.endCondition(false)
		w.code("%s", w.jump(failure))
	case compiler.IRCondCallBind:
		if int(c.ResolvedIndex) >= len(ir.CallTermStringIDs) {
			ir.SetError("Generated call-bind condition has invalid callterm slot")
			return
		}
		if c.OutputValue == compiler.NoIndex || int(c.OutputValue) >= len(ir.Values) || ir.Values[c.OutputValue].Kind != compiler.ValueVariable {
			ir.SetError("Generated call-bind output is not a variable")
			return
		}
		output := &ir.Values[c.OutputValue]
		source := m.callSource(c.Source)
		m.beginCondition(condition)
		w.open("if !ex.V[%d].IsBound() {", output.VariableSlot)
		w.open("if result, ok := ex.Invoke(%d, %s, &%s, factSymbols); ok {", c.ResolvedIndex, argumentList, source)
		w.code("ex.SetIfChanged(%d, result)", output.VariableSlot)
		m.endCondition(true)
		w.code("%s", w.jump(success))
		w.close()
		w.close()
		m.endCondition(false)
		w.code("%s", w.jump(failure))
	default:
		ir.SetError("HTNTranslator attempted to emit an unsupported generated-condition fallback for condition " +
			strconv.FormatUint(uint64(condition), 10))
	}
}

func (m *methodGen) emitSequence(and *compiler.IRCondition, offset uint32, bound boundSet, failure int, success successFn) {
	if offset == and.ChildCount {
		success(failure, func() {})
		return
	}
	child := m.ir.ConditionChildRefs[and.FirstChildRef+offset]
	m.emitConditionContinuation(child, bound, failure, func(retry int, commit commitFn) {
		m.emitSequence(and, offset+1, analyzeCondition(m.ir, child, bound).boundAfter, retry,
			func(suffixRetry int, suffixCommit commitFn) {
				success(suffixRetry, func() { suffixCommit(); commit() })
			})
	})
}

func (m *methodGen) emitConditionContinuation(condition uint32, bound boundSet, failure int, success successFn) {
	if condition == compiler.NoIndex {
		success(failure, func() {})
		return
	}
	ir := m.ir
	w := m.w
	c := &ir.Conditions[condition]
	w.comment(c.DomainExpression)
	analysis := analyzeCondition(ir, condition, bound)
	fail := m.newLabel()
	checkpoint := buildCheckpointPlan(ir, m.newLabel(), condition, bound)
	m.declareCheckpoint(checkpoint)
	m.pushCheckpoint(checkpoint)
	// Composite conditions report their own events; leaves report theirs
	// where they are evaluated.
	composite := c.Kind == compiler.IRCondAnd || c.Kind == compiler.IRCondOr || c.Kind == compiler.IRCondAlt ||
		c.Kind == compiler.IRCondNot || c.Kind == compiler.IRCondAxiom
	if composite {
		m.beginCondition(condition)
	}
	if c.AssignmentGuardValue != compiler.NoIndex {
		m.unboundGuard(&ir.Values[c.AssignmentGuardValue], fail)
	}
	succeed := func(retry int, commit commitFn) {
		if !composite {
			success(retry, func() { commit() })
			return
		}
		// A retry re-enters the composite condition.
		resume := m.newLabel()
		m.endCondition(true)
		success(resume, func() { commit() })
		w.label(resume)
		m.beginCondition(condition)
		w.code("%s", w.jump(retry))
	}
	switch {
	case c.Kind == compiler.IRCondAnd:
		m.emitSequence(c, 0, bound, fail, succeed)
	case c.Kind == compiler.IRCondOr || c.Kind == compiler.IRCondAlt:
		runtimeAlt := c.Kind == compiler.IRCondAlt && ir.RuntimeBacktrackingSupport
		altVar := fmt.Sprintf("alt%d", fail)
		if runtimeAlt {
			w.declare(altVar, "bool")
			w.code("%s = false", altVar)
		}
		for i := uint32(0); i < c.ChildCount; i++ {
			next := m.newLabel()
			m.emitConditionContinuation(ir.ConditionChildRefs[c.FirstChildRef+i], bound, next, func(retry int, commit commitFn) {
				if c.Kind == compiler.IRCondOr {
					// OR commits to its first successful child.
					commit()
					succeed(fail, func() {})
				} else {
					if runtimeAlt {
						w.code("%s = true", altVar)
					}
					succeed(retry, commit)
				}
			})
			w.label(next)
			if runtimeAlt {
				w.code("if %s && ex.Ctx.BacktrackingMode&planner.BacktrackingFactsAndAxioms == 0 {", altVar)
				w.code("\t%s", w.jump(fail))
				w.code("}")
			}
		}
		w.code("%s", w.jump(fail))
	case c.Kind == compiler.IRCondNot:
		absent := m.newLabel()
		if c.ChildCount > 0 {
			m.emitConditionContinuation(ir.ConditionChildRefs[c.FirstChildRef], bound, absent, func(_ int, commit commitFn) {
				commit()
				w.code("%s", w.jump(fail))
			})
		} else {
			w.code("%s", w.jump(absent))
		}
		w.label(absent)
		succeed(fail, func() {})
	case c.Kind == compiler.IRCondAxiom:
		m.emitAxiomCall(condition, fail, checkpoint, succeed)
	case c.Kind == compiler.IRCondFact && analysis.mayProduceMultipleValues:
		retry := m.newLabel()
		next := m.newLabel()
		cursor := fmt.Sprintf("fc%d", retry)
		w.declare(cursor, "uint32")
		w.code("%s = 0", cursor)
		w.label(next)
		m.beginCondition(condition)
		w.code("%s++", cursor)
		w.open("if !factChoice%d(ex, %s-1) {", condition, cursor)
		m.endCondition(false)
		w.code("%s", w.jump(fail))
		w.close()
		m.endCondition(true)
		succeed(retry, func() {})
		w.label(retry)
		if ir.RuntimeBacktrackingSupport {
			w.code("if ex.Ctx.BacktrackingMode&planner.BacktrackingFactsAndAxioms == 0 {")
			w.code("\t%s", w.jump(fail))
			w.code("}")
		}
		m.rollbackCheckpoint(checkpoint)
		m.pushCheckpoint(checkpoint)
		w.code("%s", w.jump(next))
	default:
		matched := m.newLabel()
		m.emitLeaf(condition, bound, matched, fail)
		w.label(matched)
		succeed(fail, func() {})
	}
	w.label(fail)
	m.rollbackCheckpoint(checkpoint)
	if composite {
		m.endCondition(false)
	}
	w.code("%s", w.jump(failure))
}

func (m *methodGen) emitAxiomCall(condition uint32, fail int, checkpoint checkpointPlan, succeed successFn) {
	ir := m.ir
	w := m.w
	c := &ir.Conditions[condition]
	if c.ResolvedIndex == compiler.NoIndex || int(c.ResolvedIndex) >= len(ir.Axioms) {
		ir.SetError("Generated axiom scope has no resolved axiom")
		return
	}
	axiom := &ir.Axioms[c.ResolvedIndex]
	scope := fmt.Sprintf("as%d_%d", condition, fail)
	bodyFailure := m.newLabel()
	bodyBound := boundSet{}
	for i := uint32(0); i < axiom.ParameterCount; i++ {
		parameter := &ir.Values[axiom.FirstParameter+i]
		name := ir.Strings.Get(parameter.Text)
		if strings.HasPrefix(name, "inp_") {
			bodyBound[parameter.Text] = struct{}{}
		}
		if strings.HasPrefix(name, "out_") {
			m.unboundGuard(&ir.Values[c.FirstArgument+i], fail)
		}
	}
	maskSlots := axiom.VariableSlotMask.Slots()
	w.declare(scope+"Saved", fmt.Sprintf("[%d]atom.Atom", len(maskSlots)))
	w.declare(scope+"Args", fmt.Sprintf("[%d]atom.Atom", c.ArgumentCount))
	w.declare(scope, "planner.AxiomScope")
	w.declare(scope+"Frame", "uint64")
	w.code("%s = planner.AxiomScope{Saved: %sSaved[:], Args: %sArgs[:]}", scope, scope, scope)
	w.code("axiomBegin%d(ex, &%s)", condition, scope)
	w.code("%sFrame = ex.CurrentFrameID", scope)
	m.emitConditionContinuation(axiom.Condition, bodyBound, bodyFailure, func(retry int, commit commitFn) {
		// Suspend the axiom's local frame while the caller checks its suffix.
		locals := checkpointPlan{id: m.newLabel(), slots: maskSlots}
		m.declareCheckpoint(locals)
		m.pushCheckpoint(locals)
		w.declare(scope+"CopySaved", fmt.Sprintf("[%d]atom.Atom", len(maskSlots)))
		w.declare(scope+"CopyArgs", fmt.Sprintf("[%d]atom.Atom", c.ArgumentCount))
		w.declare(scope+"Copy", "planner.AxiomScope")
		w.code("%sCopySaved = %sSaved", scope, scope)
		w.code("%sCopyArgs = %sArgs", scope, scope)
		w.code("%sCopy = planner.AxiomScope{Saved: %sCopySaved[:], Args: %sCopyArgs[:], CallerFrame: %s.CallerFrame}",
			scope, scope, scope, scope)
		resume := m.newLabel()
		w.code("if !axiomEnd%d(ex, true, &%sCopy) {", condition, scope)
		w.code("\t%s", w.jump(resume))
		w.code("}")
		succeed(resume, func() { commit() })
		w.label(resume)
		m.rollbackCheckpoint(checkpoint)
		m.pushCheckpoint(checkpoint)
		m.rollbackCheckpoint(locals)
		w.code("ex.CurrentFrameID = %sFrame", scope)
		m.debug(debugEvent("BeginAxiom", c.ResolvedIndex))
		w.code("%s", w.jump(retry))
	})
	w.label(bodyFailure)
	w.code("axiomEnd%d(ex, false, &%s)", condition, scope)
	w.code("%s", w.jump(fail))
}

func (g *generator) emitMethod(methodIndex uint32) {
	ir := g.ir
	method := &ir.Methods[methodIndex]
	f := &g.funcs
	fmt.Fprintf(f, "// method%d: %s/%d\n", methodIndex, goComment(ir.Strings.Get(method.ID)), method.ParameterCount)
	fmt.Fprintf(f, "func method%d(ex *planner.Exec) int {\n", methodIndex)
	beginMethod := debugEvent("BeginMethod", methodIndex)
	endMethodFailed := debugEvent("EndMethod", false)
	endBranchFailed := debugEvent("EndBranch", false)
	if method.BranchCount == 0 {
		f.WriteString(debugStatements("\t", beginMethod, endMethodFailed) + "\treturn 0\n}\n\n")
		return
	}
	bound := boundSet{}
	for i := uint32(0); i < method.ParameterCount; i++ {
		parameter := &ir.Values[method.FirstParameter+i]
		if parameter.Kind == compiler.ValueVariable {
			bound[parameter.Text] = struct{}{}
		}
	}
	slots := method.VariableSlotMask.Slots()
	methodSlots := fmt.Sprintf("ms%d", methodIndex)
	needsSlots := false
	w := newFnWriter()
	m := &methodGen{generator: g, w: w}
	methodFailure := g.newLabel()
	branchLabels := make([]int, method.BranchCount)
	for i := range branchLabels {
		branchLabels[i] = g.newLabel()
	}
	resumeLabels := make([]int, method.BranchCount)
	var resumeCases []string
	pinned := map[int]bool{}
	for bi := uint32(0); bi < method.BranchCount; bi++ {
		if ir.Branches[method.FirstBranch+bi].TaskCount == 0 {
			continue
		}
		resumeLabels[bi] = g.newLabel()
		resumeCases = append(resumeCases, fmt.Sprintf("case %d:\n\t\tgoto L%d", bi+1, resumeLabels[bi]))
		pinned[resumeLabels[bi]] = true
	}
	m.debug(beginMethod)
	w.code("%s", w.jump(branchLabels[0]))
	for bi := uint32(0); bi < method.BranchCount; bi++ {
		branchIndex := method.FirstBranch + bi
		branch := &ir.Branches[branchIndex]
		branchSuccess := g.newLabel()
		branchFailed := g.newLabel()
		branchFailure := methodFailure
		if bi+1 < method.BranchCount {
			branchFailure = branchLabels[bi+1]
		}
		canRetry := bi+1 < method.BranchCount && branch.TaskCount != 0
		w.label(branchLabels[bi])
		w.comment("branch " + ir.Strings.Get(branch.ID))
		if canRetry {
			needsSlots = true
			w.code("ex.SaveRetry(frame, %s)", methodSlots)
		}
		m.debug(debugEvent("BeginBranch", branchIndex))
		if branch.Condition == compiler.NoIndex {
			w.code("%s", w.jump(branchSuccess))
		} else {
			m.emitConditionContinuation(branch.Condition, bound, branchFailed, func(_ int, commit commitFn) {
				commit()
				w.code("%s", w.jump(branchSuccess))
			})
		}
		w.label(branchFailed)
		if canRetry {
			w.code("ex.ReleaseRetry(frame)")
		}
		m.debug(endBranchFailed)
		w.code("%s", w.jump(branchFailure))
		w.label(branchSuccess)
		if branch.TaskCount != 0 {
			w.open("if !ex.PushBranch(&bc%d) {", branchIndex)
			if canRetry {
				w.code("ex.ReleaseRetry(frame)")
			}
			m.debug(endBranchFailed, endMethodFailed)
			w.code("return 0")
			w.close()
			captures := make([]string, 0, branch.TaskCount)
			for ti := uint32(0); ti < branch.TaskCount; ti++ {
				captures = append(captures, fmt.Sprintf("ex.DebugCapturePendingTask(%d)", branch.FirstTask+(branch.TaskCount-1-ti)))
			}
			m.debug(captures...)
			base := "0"
			if canRetry {
				base = "frame.RetryPendingBase"
			}
			loop := g.newLabel()
			commitLabel := g.newLabel()
			w.label(loop)
			w.open("if ex.PendingCount() > %s {", base)
			w.open("if next := ex.PopPending(); next != nil {")
			w.code("frame.Resume = %d", bi+1)
			w.code("ex.Next = next")
			w.code("return 2")
			w.close()
			w.code("frame.ChildResult = 0")
			w.code("%s", w.jump(resumeLabels[bi]))
			w.close()
			w.code("%s", w.jump(commitLabel))
			w.label(resumeLabels[bi])
			w.open("if frame.ChildResult == 0 {")
			if canRetry {
				w.open("if ex.FailureState != planner.NoPlan {")
				w.code("ex.ReleaseRetry(frame)")
				m.debug(endBranchFailed, endMethodFailed)
				w.code("return 0")
				w.close()
				if ir.RuntimeBacktrackingSupport {
					w.open("if ex.Ctx.BacktrackingMode&planner.BacktrackingBranches == 0 {")
					w.code("ex.ReleaseRetry(frame)")
					m.debug(endBranchFailed, endMethodFailed)
					w.code("return 0")
					w.close()
				}
				w.code("ex.RestoreRetry(frame, %s)", methodSlots)
				m.debug(endBranchFailed)
				w.code("%s", w.jump(branchFailure))
			} else {
				m.debug(endBranchFailed, endMethodFailed)
				w.code("return 0")
			}
			w.close()
			w.code("%s", w.jump(loop))
			w.label(commitLabel)
		}
		if canRetry {
			w.code("ex.ReleaseRetry(frame)")
		}
		m.debug(debugEvent("EndBranch", true), debugEvent("EndMethod", true))
		w.code("return 1")
	}
	w.label(methodFailure)
	m.debug(endMethodFailed)
	w.code("return 0")

	if needsSlots {
		fmt.Fprintf(&g.top, "var %s = %s\n", methodSlots, slotList(slots))
	}
	f.WriteString("\tframe := ex.Frame()\n\t_ = frame\n")
	var body strings.Builder
	w.render(&body, pinned)
	if len(resumeCases) > 0 {
		// The resume switch must follow the hoisted declarations.
		decls := len(w.decls) * 2
		lines := strings.SplitAfter(body.String(), "\n")
		var head, tail strings.Builder
		for i, line := range lines {
			if i < decls {
				head.WriteString(line)
			} else {
				tail.WriteString(line)
			}
		}
		f.WriteString(head.String())
		f.WriteString("\tswitch frame.Resume {\n\t" + strings.Join(resumeCases, "\n\t") + "\n\t}\n")
		f.WriteString(tail.String())
	} else {
		f.WriteString(body.String())
	}
	f.WriteString("}\n\n")
}

func (g *generator) emitEntryPoint() {
	ir := g.ir
	f := &g.funcs
	f.WriteString("func decomposeCall(ctx *planner.Context, call atom.Atom, requireTopLevel bool) (atom.Atom, planner.DecompositionStatus) {\n")
	f.WriteString("\tempty := atom.EmptyList()\n")
	f.WriteString("\tif ctx == nil || ctx.Execution == nil || ctx.Prepared == nil {\n\t\treturn empty, planner.InvalidContext\n\t}\n")
	f.WriteString("\tex := ctx.Execution\n\tex.ResetDiagnostics()\n")
	f.WriteString("\tif ctx.WorldState == nil || ctx.Bindings == nil {\n\t\treturn empty, planner.InvalidContext\n\t}\n")
	f.WriteString("\tif !call.Is(atom.KindList) || call.Len() < 1 {\n\t\treturn empty, planner.InvalidCall\n\t}\n")
	f.WriteString("\thead, _ := call.At(0)\n\tif !head.Is(atom.KindSymbol) {\n\t\treturn empty, planner.InvalidCall\n\t}\n")
	f.WriteString("\targumentCount := call.Len() - 1\n\t_ = argumentCount\n")
	f.WriteString("\tex.Begin(ctx, factSymbols, callNames)\n\tentry := -1\n")
	var cases strings.Builder
	for mi := range ir.Methods {
		method := &ir.Methods[mi]
		if !method.IsExternallyDecomposable {
			continue
		}
		fmt.Fprintf(f, "\tif entry < 0 && head.Symbol() == %s && argumentCount == %d {\n", g.symbol(ir.Strings.Get(method.ID)), method.ParameterCount)
		if !method.IsTopLevel {
			f.WriteString("\t\tif requireTopLevel {\n\t\t\treturn empty, planner.InvalidCall\n\t\t}\n")
		}
		fmt.Fprintf(f, "\t\tentry = %d\n\t}\n", mi)
		fmt.Fprintf(&cases, "\tcase %d:\n", mi)
		for pi := uint32(0); pi < method.ParameterCount; pi++ {
			parameter := &ir.Values[method.FirstParameter+pi]
			if parameter.Kind != compiler.ValueVariable || parameter.VariableSlot == compiler.NoIndex {
				ir.SetError("Generated top-level method parameter has no variable slot")
				return
			}
			fmt.Fprintf(&cases, "\t\tif argument, _ := call.At(%d); !argument.IsBound() {\n\t\t\treturn empty, planner.InvalidCall\n\t\t} else {\n\t\t\tex.V[%d] = argument\n\t\t}\n",
				pi+1, parameter.VariableSlot)
		}
		fmt.Fprintf(&cases, "\t\tresult = ex.Run(method%d)\n", mi)
	}
	f.WriteString("\tif entry < 0 {\n\t\treturn empty, planner.InvalidCall\n\t}\n")
	f.WriteString(debugStatements("\t", debugEvent("BeginPlan", "uint32(entry)")))
	f.WriteString("\tresult := 0\n\tswitch entry {\n")
	f.WriteString(cases.String())
	f.WriteString("\t}\n")
	f.WriteString("\tif result == 0 {\n" + debugStatements("\t\t", debugEvent("EndPlan", false)) + "\t\treturn empty, ex.FailureState\n\t}\n")
	f.WriteString("\tfor ex.PendingCount() != 0 {\n\t\tnext := ex.PopPending()\n\t\tif next == nil {\n\t\t\tbreak\n\t\t}\n\t\tif ex.Run(next) == 0 {\n" +
		debugStatements("\t\t\t", debugEvent("EndPlan", false)) + "\t\t\treturn empty, ex.FailureState\n\t\t}\n\t}\n")
	f.WriteString(debugStatements("\t", debugEvent("EndPlan", true)))
	f.WriteString("\treturn ex.PlanAtom(), planner.Succeeded\n}\n")
}
