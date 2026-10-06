// Package scenario runs the shared HTN scenario files (testdata/scenarios) on
// the Go port. The scenario language and its canonical output are defined by
// the C++ oracle (tools/oracle/oracle.cpp); golden files produced by the
// oracle are compared with this interpreter's output byte for byte.
package scenario

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/callterm"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/debugger"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/integration"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/planner"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/worldstate"
)

// Planners maps manifest variant names to generated definitions.
type Planners map[string]func() *planner.Definition

type runner struct {
	out      strings.Builder
	root     string
	planners Planners
}

func (r *runner) emit(line string) {
	r.out.WriteString(line)
	r.out.WriteByte('\n')
}

// FormatAtom formats an atom like the oracle: HTNAtomToString with quoted
// strings, "?" for unbound atoms.
func FormatAtom(a atom.Atom) string {
	if !a.IsBound() {
		return "?"
	}
	return atom.ToString(a, true)
}

// FormatStep formats one call-shaped plan step: "head arg...".
func FormatStep(step atom.Atom) string {
	head := atom.CallHead(step)
	text := "<invalid>"
	if head != nil {
		text = head.Text()
	}
	count := atom.CallArgumentCount(step)
	for i := 0; i < count; i++ {
		argument, ok := atom.CallArgument(step, i)
		text += " "
		if ok {
			text += FormatAtom(argument)
		} else {
			text += "?"
		}
	}
	return text
}

func (r *runner) emitPlan(plan atom.Atom) {
	if !plan.Is(atom.KindList) {
		r.emit("plan -")
		return
	}
	elements := plan.Elements()
	r.emit("plan " + strconv.Itoa(len(elements)))
	for _, step := range elements {
		r.emit("step " + FormatStep(step))
	}
}

// StatusName returns the oracle name of a decomposition status.
func StatusName(status planner.DecompositionStatus) string {
	switch status {
	case planner.Succeeded:
		return "SUCCEEDED"
	case planner.NoPlan:
		return "NO_PLAN"
	case planner.BacktrackingCapacityExceeded:
		return "BACKTRACKING_CAPACITY_EXCEEDED"
	case planner.OutOfMemory:
		return "OUT_OF_MEMORY"
	case planner.InvalidContext:
		return "INVALID_CONTEXT"
	case planner.InvalidCall:
		return "INVALID_CALL"
	case planner.PreparationFailed:
		return "PREPARATION_FAILED"
	case planner.NotRun:
		return "NOT_RUN"
	case planner.CallFrameCapacityExceeded:
		return "CALL_FRAME_CAPACITY_EXCEEDED"
	}
	return "UNKNOWN"
}

func index(value uint32) string {
	if value == callterm.NoIndex {
		return "-"
	}
	return strconv.FormatUint(uint64(value), 10)
}

func text(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func (r *runner) onError(_ any, info *callterm.ErrorInfo) {
	r.emit("error " + info.Reason.String() + " name=" + text(info.Name) + " daemon=" + text(info.DaemonID) +
		" source=" + text(info.Source.Domain) + "|" + text(info.Source.File) + ":" +
		strconv.FormatUint(uint64(info.Source.Line), 10) + ":" + strconv.FormatUint(uint64(info.Source.Column), 10) +
		" arg=" + index(info.ArgumentIndex) + " expected_count=" + index(info.ExpectedArgumentCount) +
		" actual_count=" + index(info.ActualArgumentCount) + " expected_type=" + index(info.ExpectedAtomType) +
		" actual_type=" + index(info.ActualAtomType) + " type_name=" + text(info.ExpectedTypeName))
}

func (r *runner) trace(name string, args *callterm.Arguments, result atom.Atom) {
	var b strings.Builder
	b.WriteString("trace ")
	b.WriteString(name)
	b.WriteByte('(')
	for i := 0; i < args.Len(); i++ {
		if i != 0 {
			b.WriteString(", ")
		}
		b.WriteString(FormatAtom(args.At(i)))
	}
	b.WriteString(") -> ")
	b.WriteString(FormatAtom(result))
	r.emit(b.String())
}

func isInt(args *callterm.Arguments, i int) bool { return args.At(i).Is(atom.KindInt) }

func intAt(args *callterm.Arguments, i int) int32 { return args.At(i).Int() }

func readCell(a atom.Atom) (int32, int32, bool) {
	if !a.Is(atom.KindList) || a.Len() != 2 {
		return 0, 0, false
	}
	x, _ := a.At(0)
	y, _ := a.At(1)
	if !x.Is(atom.KindInt) || !y.Is(atom.KindInt) {
		return 0, 0, false
	}
	return x.Int(), y.Int(), true
}

type standardCallTerm struct {
	name string
	fn   func(args *callterm.Arguments) atom.Atom
}

// standardCallTerms mirrors kStandardCallTerms of the oracle.
var standardCallTerms = []standardCallTerm{
	{"binded_function_with_args", func(a *callterm.Arguments) atom.Atom {
		return atom.NewBool(a.Len() == 1 && a.At(0).Is(atom.KindString))
	}},
	{"get_health", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 1 && isInt(a, 0) {
			return atom.NewInt(50)
		}
		return atom.NewInt(0)
	}},
	{"get_max_speed", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 1 && isInt(a, 0) {
			return atom.NewFloat(1.0)
		}
		return atom.NewFloat(0.0)
	}},
	{"lt", func(a *callterm.Arguments) atom.Atom {
		return atom.NewBool(a.Len() == 2 && isInt(a, 0) && isInt(a, 1) && intAt(a, 0) < intAt(a, 1))
	}},
	{"inc", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 1 && isInt(a, 0) {
			return atom.NewInt(intAt(a, 0) + 1)
		}
		return atom.NewInt(0)
	}},
	{"add", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 2 && isInt(a, 0) && isInt(a, 1) {
			return atom.NewInt(intAt(a, 0) + intAt(a, 1))
		}
		return atom.NewInt(0)
	}},
	{"mul", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 2 && isInt(a, 0) && isInt(a, 1) {
			return atom.NewInt(intAt(a, 0) * intAt(a, 1))
		}
		return atom.NewInt(0)
	}},
	{"assignment_probe", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 1 && isInt(a, 0) {
			return atom.NewInt(intAt(a, 0) + 1)
		}
		return atom.NewInt(0)
	}},
	{"get_entity_position", func(a *callterm.Arguments) atom.Atom {
		if a.Len() != 1 || !isInt(a, 0) {
			return atom.NewInt(0)
		}
		switch entity := intAt(a, 0); entity {
		case 1:
			return atom.NewInt(7)
		case 42:
			return atom.NewInt(20)
		default:
			return atom.NewInt(entity + 100)
		}
	}},
	{"get_distance_from_to", func(a *callterm.Arguments) atom.Atom {
		if a.Len() != 2 || !isInt(a, 0) || !isInt(a, 1) {
			return atom.NewFloat(0)
		}
		difference := intAt(a, 1) - intAt(a, 0)
		if difference < 0 {
			difference = -difference
		}
		return atom.NewFloat(float32(difference) / 100.0)
	}},
	{"distance", func(*callterm.Arguments) atom.Atom { return atom.NewFloat(0.1) }},
	{"identity", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 1 {
			return a.At(0)
		}
		return atom.Atom{}
	}},
	{"probe", func(*callterm.Arguments) atom.Atom { return atom.NewBool(true) }},
	{"axiom_trace", func(*callterm.Arguments) atom.Atom { return atom.NewBool(true) }},
	{"axiom_value", func(*callterm.Arguments) atom.Atom { return atom.NewInt(42) }},
	{"visit", func(*callterm.Arguments) atom.Atom { return atom.NewBool(true) }},
	{"same_location", func(a *callterm.Arguments) atom.Atom {
		if a.Len() != 2 {
			return atom.NewBool(false)
		}
		fromX, fromY, fromOK := readCell(a.At(0))
		if !fromOK {
			return atom.NewBool(false)
		}
		toX, toY, toOK := readCell(a.At(1))
		return atom.NewBool(toOK && fromX == toX && fromY == toY)
	}},
	{"both_coordinates_even", func(a *callterm.Arguments) atom.Atom {
		if a.Len() != 1 {
			return atom.NewBool(false)
		}
		x, y, ok := readCell(a.At(0))
		return atom.NewBool(ok && x%2 == 0 && y%2 == 0)
	}},
	{"both_coordinates_odd", func(a *callterm.Arguments) atom.Atom {
		if a.Len() != 1 {
			return atom.NewBool(false)
		}
		x, y, ok := readCell(a.At(0))
		return atom.NewBool(ok && x%2 != 0 && y%2 != 0)
	}},
}

func findStandard(name string) func(*callterm.Arguments) atom.Atom {
	for _, c := range standardCallTerms {
		if c.name == name {
			return c.fn
		}
	}
	return nil
}

func parseSignatureType(name string) (callterm.SignatureType, bool) {
	switch name {
	case "any":
		return callterm.AnyType(), true
	case "bool":
		return callterm.KindType(atom.KindBool), true
	case "int":
		return callterm.KindType(atom.KindInt), true
	case "float":
		return callterm.KindType(atom.KindFloat), true
	case "string":
		return callterm.KindType(atom.KindString), true
	case "symbol":
		return callterm.KindType(atom.KindSymbol), true
	case "list":
		return callterm.KindType(atom.KindList), true
	}
	return callterm.SignatureType{}, false
}

type worldStateDaemon struct{ world *worldstate.WorldState }

type agentDaemon struct{ value int32 }

type scenario struct {
	spec        []string
	definition  *planner.Definition
	database    *integration.DatabaseHook
	registry    *callterm.Registry
	hook        *integration.PlannerHook
	unit        *integration.PlanningUnit
	worldDaemon worldStateDaemon
	agent       agentDaemon
	mode        planner.BacktrackingMode
	policy      callterm.ErrorPolicy
	rawPrepared any
	rawExec     *planner.Exec
	// The generated event debugger (htndebug builds only).
	debuggerEnabled bool
	debugger        *debugger.Debugger
	dumpedRevision  uint64
}

type failure struct{ message string }

func (r *runner) bindCallTerms(s *scenario) {
	registry := s.registry
	excluded := map[string]bool{}
	standard := false
	for _, item := range s.spec {
		switch {
		case item == "standard":
			standard = true
		case item == "none":
			standard = false
		case strings.HasPrefix(item, "-"):
			excluded[item[1:]] = true
		}
	}
	if standard {
		if !excluded["list"] {
			callterm.BindListCallTerms(registry)
		}
		for _, c := range standardCallTerms {
			if excluded[c.name] {
				continue
			}
			name, fn := c.name, c.fn
			registry.Bind(name, func(args *callterm.Arguments) atom.Atom {
				result := fn(args)
				r.trace(name, args, result)
				return result
			})
		}
		if !excluded["add_target_available"] {
			registry.BindMember("add_target_available", "WorldStateDaemon", func(daemon any, args *callterm.Arguments) atom.Atom {
				daemon.(*worldStateDaemon).world.AddFact("target_available", atom.NewString("enemy0"))
				result := atom.NewBool(true)
				r.trace("add_target_available", args, result)
				return result
			}, callterm.Signature{})
		}
	}
	for _, item := range s.spec {
		parts := strings.Split(item, ":")
		if len(parts) < 2 {
			continue
		}
		kind, name := parts[0], parts[1]
		switch kind {
		case "typed":
			fn := findStandard(name)
			if fn == nil || len(parts) != 3 {
				panic(failure{"typed callterm needs a standard name and a signature: " + item})
			}
			signature := callterm.Signature{}
			if parts[2] != "none" {
				for _, typeName := range strings.Split(parts[2], ",") {
					t, ok := parseSignatureType(typeName)
					if !ok {
						panic(failure{"unknown signature type: " + typeName})
					}
					signature = append(signature, t)
				}
			}
			registry.BindWithSignature(name, func(args *callterm.Arguments) atom.Atom {
				result := fn(args)
				r.trace(name, args, result)
				return result
			}, signature)
		case "member":
			registry.BindMember(name, "agent", func(daemon any, args *callterm.Arguments) atom.Atom {
				result := atom.NewInt(daemon.(*agentDaemon).value)
				r.trace(name, args, result)
				return result
			}, callterm.Signature{})
		case "empty":
			registry.BindMember(name, "agent", nil, callterm.Signature{})
		default:
			panic(failure{"unknown callterm modifier: " + item})
		}
	}
}

func (r *runner) createPlanner(s *scenario, variant string) {
	get := r.planners[variant]
	if get == nil {
		panic(failure{"unknown planner variant " + variant})
	}
	s.definition = get()
	s.database = integration.NewDatabaseHook()
	s.registry = callterm.NewRegistry()
	r.bindCallTerms(s)
	s.hook = integration.NewPlannerHook(s.database.WorldState(), s.registry)
	s.worldDaemon.world = s.database.WorldState()
	s.hook.Bindings().SetDaemon("WorldStateDaemon", &s.worldDaemon)
	if !s.hook.SetGeneratedPlannerDefinition(s.definition) {
		panic(failure{"definition rejected: " + variant})
	}
	s.unit = integration.NewPlanningUnit(s.database, s.hook, "run")
	s.unit.SetBacktrackingMode(s.mode)
	s.unit.ExecutionContext().CallTermErrorPolicy = s.policy
	s.unit.ExecutionContext().CallTermErrorCallback = r.onError
	if s.debuggerEnabled {
		s.unit.SetGeneratedDebugger(s.debugger)
	}
}

// dumpDebugger writes every recorded node of the generated event debugger
// after a new capture (the debugger is reset by every decomposition).
func (r *runner) dumpDebugger(s *scenario) {
	if !s.debuggerEnabled || s.debugger.Revision() == s.dumpedRevision {
		return
	}
	s.dumpedRevision = s.debugger.Revision()
	for _, line := range s.debugger.Dump() {
		r.emit(line)
	}
}

// MakeCall parses "name arg..." with the world-state syntax and builds the
// call (name arg...).
func MakeCall(textValue string) (atom.Atom, bool) {
	temporary := worldstate.New()
	if !worldstate.ParseText(temporary, textValue) {
		return atom.Atom{}, false
	}
	facts := temporary.Facts()
	if len(facts) != 1 {
		return atom.Atom{}, false
	}
	tables := temporary.FindTables(facts[0])
	for arity := range tables {
		if tables[arity].RowCount() == 0 {
			continue
		}
		return atom.MakeCall(facts[0], tables[arity].Row(0)...)
	}
	return atom.Atom{}, false
}

func (r *runner) runRaw(s *scenario, callText string, requireTopLevel bool) {
	d := s.definition
	if s.rawExec == nil {
		s.rawPrepared = d.NewPreparedStorage()
		s.rawExec = d.NewExecutionStorage()
		if s.rawPrepared == nil || s.rawExec == nil {
			panic(failure{"storage initialization failed"})
		}
	}
	call, ok := MakeCall(callText)
	if !ok {
		panic(failure{"invalid call: " + callText})
	}
	ctx := planner.Context{
		WorldState:            s.database.WorldState(),
		Bindings:              s.hook.Bindings(),
		BacktrackingMode:      s.mode,
		Execution:             s.rawExec,
		Prepared:              s.rawPrepared,
		CallTermErrorPolicy:   s.policy,
		CallTermErrorCallback: r.onError,
	}
	if s.debuggerEnabled {
		ctx.Debugger = s.debugger
		domainPath := ""
		if d.DebugMetadata != nil {
			domainPath = d.DebugMetadata.SourceFile
		}
		s.debugger.Reset(domainPath)
	}
	plan, status := d.DecomposeCall(&ctx, call, requireTopLevel)
	r.emit("status " + StatusName(status))
	info := s.rawExec.Info
	lastError := info.LastError
	if lastError == "" {
		lastError = "-"
	}
	r.emit(fmt.Sprintf("info peak=%d capacity=%d error=%s", info.PeakCallFrames, info.CallFrameCapacity, lastError))
	r.emitPlan(plan)
	r.dumpDebugger(s)
}

func (r *runner) runFile(path string) (err error) {
	file, openErr := os.Open(path)
	if openErr != nil {
		return openErr
	}
	defer file.Close()
	lineNumber := 0
	defer func() {
		if recovered := recover(); recovered != nil {
			f, ok := recovered.(failure)
			if !ok {
				panic(recovered)
			}
			err = fmt.Errorf("%s:%d: %s", path, lineNumber, f.message)
		}
	}()
	var current *scenario
	finish := func() {
		if current != nil {
			r.emit("end")
			current = nil
		}
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		lineNumber++
		trimmed := strings.Trim(scanner.Text(), " \t\r")
		if trimmed == "" || trimmed[0] == '#' {
			continue
		}
		command, argument := trimmed, ""
		if space := strings.IndexByte(trimmed, ' '); space >= 0 {
			command = trimmed[:space]
			argument = strings.Trim(trimmed[space+1:], " \t\r")
		}
		if command == "scenario" {
			finish()
			current = &scenario{spec: []string{"standard"}, mode: planner.BacktrackingAll, policy: callterm.PolicyFailSilently,
				agent: agentDaemon{value: 1}, debugger: debugger.New()}
			r.emit("scenario " + argument)
			continue
		}
		if current == nil {
			panic(failure{"command outside of a scenario"})
		}
		s := current
		if command != "callterms" && command != "planner" && command != "end" && s.unit == nil {
			panic(failure{"'" + command + "' requires a planner"})
		}
		switch command {
		case "callterms":
			if s.unit != nil {
				panic(failure{"callterms must precede planner"})
			}
			s.spec = strings.Split(argument, " ")
		case "planner":
			r.createPlanner(s, argument)
		case "worldstate":
			if err := s.database.ParseWorldStateFile(filepath.Join(r.root, argument)); err != nil {
				r.emit("worldstate failed " + argument)
			}
		case "fact":
			if !worldstate.ParseText(s.database.WorldState(), argument) {
				r.emit("fact failed " + argument)
			}
		case "remove_fact":
			parts := strings.Split(argument, " ")
			if len(parts) != 3 {
				panic(failure{"remove_fact <name> <arity> <index>"})
			}
			arity, _ := strconv.Atoi(parts[1])
			position, _ := strconv.Atoi(parts[2])
			s.database.WorldState().RemoveFact(parts[0], arity, position)
		case "clear_facts":
			s.database.WorldState().RemoveAllFacts()
		case "mode":
			switch argument {
			case "none":
				s.mode = planner.BacktrackingNone
			case "facts_and_axioms":
				s.mode = planner.BacktrackingFactsAndAxioms
			case "branches":
				s.mode = planner.BacktrackingBranches
			case "all":
				s.mode = planner.BacktrackingAll
			default:
				panic(failure{"unknown mode " + argument})
			}
			s.unit.SetBacktrackingMode(s.mode)
		case "policy":
			switch argument {
			case "unset":
				s.policy = callterm.PolicyUnset
			case "silent":
				s.policy = callterm.PolicyFailSilently
			case "report":
				s.policy = callterm.PolicyReport
			default:
				panic(failure{"unknown policy " + argument})
			}
			s.unit.ExecutionContext().CallTermErrorPolicy = s.policy
		case "daemon":
			switch argument {
			case "on":
				s.hook.Bindings().SetDaemon("agent", &s.agent)
			case "off":
				s.hook.Bindings().SetDaemon("agent", nil)
			default:
				panic(failure{"daemon on|off"})
			}
		case "rebind":
			parts := strings.Split(argument, " ")
			if len(parts) != 2 {
				panic(failure{"rebind <name> <int>"})
			}
			name := parts[0]
			value, _ := strconv.ParseInt(parts[1], 10, 32)
			s.registry.Bind(name, func(args *callterm.Arguments) atom.Atom {
				result := atom.NewInt(int32(value))
				r.trace(name, args, result)
				return result
			})
		case "debugger":
			if !planner.DebugEnabled {
				panic(failure{"the debugger command needs a build with -tags htndebug"})
			}
			if argument != "on" && argument != "off" {
				panic(failure{"debugger expects on or off"})
			}
			s.debuggerEnabled = argument == "on"
			s.debugger.SetEnabled(s.debuggerEnabled)
			s.dumpedRevision = s.debugger.Revision()
			if s.debuggerEnabled {
				s.unit.SetGeneratedDebugger(s.debugger)
			} else {
				s.unit.SetGeneratedDebugger(nil)
			}
		case "call":
			r.emit("call " + argument)
			call, ok := MakeCall(argument)
			if !ok {
				panic(failure{"invalid call: " + argument})
			}
			status := s.unit.DecomposeCall(call)
			r.emit("status " + StatusName(status))
			r.emitPlan(s.unit.LastDecomposition())
			r.dumpDebugger(s)
		case "resolve":
			r.emit("resolve")
			for {
				resolution := s.unit.ResolveCurrentPrimitiveTask()
				r.dumpDebugger(s)
				if resolution == integration.TaskReady {
					task, _ := s.unit.CurrentPrimitiveTask()
					r.emit("exec " + FormatStep(task))
					s.unit.CompleteCurrentPrimitiveTask()
					continue
				}
				if resolution == integration.PlanCompleted {
					r.emit("resolution completed")
				} else {
					r.emit("resolution failed")
				}
				break
			}
		case "raw", "rawdeferred":
			r.emit(command + " " + argument)
			r.runRaw(s, argument, command == "raw")
		case "end":
			finish()
		default:
			panic(failure{"unknown command " + command})
		}
	}
	finish()
	return scanner.Err()
}

// RunFile executes one scenario file. Paths in the scenario are resolved
// against root (the repository root).
func RunFile(path, root string, planners Planners) (string, error) {
	r := &runner{root: root, planners: planners}
	err := r.runFile(path)
	return r.out.String(), err
}

var _ = math.MaxInt32
