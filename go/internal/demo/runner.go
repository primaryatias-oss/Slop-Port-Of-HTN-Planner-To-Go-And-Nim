package demo

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/callterm"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/debugger"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/integration"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/planner"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/generated"
)

// Domain is one entry of the demo's domain table.
type Domain struct {
	Name    string
	Variant string // planner variant (testdata/planners.txt); "RT" is appended for runtime backtracking
	Source  string // world-state stem preferred by the domain
	Methods []string
}

// Domains is the demo's domain table (HTNDemo/src/main.cpp).
var Domains = []Domain{
	{"AAACombatNPC", "AAACombatNPC", "AAACombatNPC", []string{"run"}},
	{"AtomListDemo", "AtomListDemo", "atom_list_demo", []string{"show_atom_list", "split_list_basic",
		"split_list_single_element", "split_list_empty_fails", "split_list_bound_outputs", "split_list_rollback",
		"split_list_front_basic", "split_list_back_basic", "split_list_back_bound_outputs"}},
	{"EliteNinja", "EliteNinja", "EliteNinja", []string{"run"}},
	{"Grunt", "Grunt", "Grunt", []string{"run"}},
	{"NormalNinja", "NormalNinja", "NormalNinja", []string{"run"}},
	{"CallTermsDemo", "Callterms", "callterms", []string{"test_callterms",
		"callterm_creates_fact_visible_immediately", "callterm_world_state_mutation_survives_backtracking"}},
	{"ComplexScenario", "ComplexScenario", "complex_scenario", []string{"run_scenario"}},
	{"Human", "Human", "human", []string{"behave", "behave_upper_body"}},
	{"NestedCallsDemo", "NestedCalls", "nested_calls", []string{"test_nested_calls"}},
	{"IncludeDemo", "IncludeDemo", "include_demo", []string{"run_include_demo"}},
	{"Wanderer", "Wanderer", "Wanderer", []string{"run"}},
	{"BuiltinComparisonsDemo", "BuiltinComparisonsDemo", "BuiltinComparisonsDemo", []string{"run"}},
	{"HierarchicalBacktracking", "HierarchicalBacktracking", "hierarchical_backtracking", []string{
		"validate_parent_guard", "validate_child_guard", "validate_fact_backtracking", "validate_axiom_backtracking"}},
	{"RuntimeBacktrackingDemo", "RuntimeBacktrackingDemo", "RuntimeBacktrackingDemo", []string{
		"demo_fact_alternatives", "demo_axiom_alternatives", "demo_hierarchical_branches",
		"demo_direct_branch_fallback"}},
	{"NumericExpressionsDemo", "NumericExpressions", "numeric_expressions", []string{"run", "division_by_zero",
		"invalid_operand_type"}},
}

// FindDomain returns the domain named name (case-insensitively).
func FindDomain(name string) (Domain, bool) {
	for _, d := range Domains {
		if strings.EqualFold(d.Name, name) {
			return d, true
		}
	}
	return Domain{}, false
}

// Definition returns the generated planner of a domain, optionally the
// variant generated with runtime backtracking support.
func (d Domain) Definition(runtimeBacktracking bool) *planner.Definition {
	variant := d.Variant
	if runtimeBacktracking {
		variant += "RT"
	}
	getter := generated.Planners[variant]
	if getter == nil {
		return nil
	}
	return getter()
}

// BacktrackingModes lists the runtime backtracking modes in display order.
var BacktrackingModes = []planner.BacktrackingMode{planner.BacktrackingNone, planner.BacktrackingFactsAndAxioms,
	planner.BacktrackingBranches, planner.BacktrackingAll}

// ModeName returns the display name of a backtracking mode.
func ModeName(mode planner.BacktrackingMode) string {
	switch mode {
	case planner.BacktrackingNone:
		return "None"
	case planner.BacktrackingFactsAndAxioms:
		return "Facts and axioms"
	case planner.BacktrackingBranches:
		return "Branches"
	case planner.BacktrackingAll:
		return "All"
	}
	return "Unknown"
}

// comparePaths orders paths element by element like std::filesystem::path.
func comparePaths(a, b string) bool {
	left, right := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return len(left) < len(right)
}

// FindWorldStates lists the .worldstate files under root/WorldStates in the
// demo's order.
func FindWorldStates(root string) []string {
	var paths []string
	filepath.WalkDir(filepath.Join(root, "WorldStates"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if dot := strings.LastIndexByte(name, '.'); dot <= 0 || name[dot:] != ".worldstate" {
			return nil
		}
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Slice(paths, func(i, j int) bool { return comparePaths(paths[i], paths[j]) })
	return paths
}

// BestWorldState returns the index of the world state named after the
// domain's source (or 0, -1 when there is none).
func BestWorldState(d Domain, worldStates []string) int {
	for i, path := range worldStates {
		stem := strings.TrimSuffix(filepath.Base(path), ".worldstate")
		if stem == d.Source || strings.HasPrefix(stem, d.Source+"_") {
			return i
		}
	}
	if len(worldStates) == 0 {
		return -1
	}
	return 0
}

// FormatPlanStep renders a plan step for the Domain Runner.
func FormatPlanStep(step atom.Atom) string {
	head := atom.CallHead(step)
	text := "<invalid>"
	if head != nil {
		text = head.Text()
	}
	for _, argument := range atom.CallArguments(step) {
		text += " " + atom.ToString(argument, true)
	}
	return text
}

// Runner is the Domain Runner: one planner hook and planning unit for the
// selected domain and method over a shared database.
type Runner struct {
	Registry *callterm.Registry
	Database *integration.DatabaseHook
	Report   callterm.ErrorCallback
	// Debugger, when set before Select, records every decomposition
	// (planners built with the "htndebug" tag only).
	Debugger *debugger.Debugger
	hook     *integration.PlannerHook
	unit     *integration.PlanningUnit
}

// NewRunner creates a runner with the demo callterms; report receives
// callterm errors.
func NewRunner(report callterm.ErrorCallback) *Runner {
	registry := callterm.NewRegistry()
	BindCallTerms(registry)
	return &Runner{Registry: registry, Database: integration.NewDatabaseHook(), Report: report}
}

// Select creates a fresh planner hook and planning unit (the demo's reload).
func (r *Runner) Select(definition *planner.Definition, method string) bool {
	r.hook = integration.NewPlannerHook(r.Database.WorldState(), r.Registry)
	r.unit = nil
	if !r.hook.SetGeneratedPlannerDefinition(definition) {
		return false
	}
	r.unit = integration.NewPlanningUnit(r.Database, r.hook, method)
	r.unit.ExecutionContext().CallTermErrorPolicy = callterm.PolicyReport
	r.unit.ExecutionContext().CallTermErrorCallback = r.Report
	if r.Debugger != nil {
		r.unit.SetGeneratedDebugger(r.Debugger)
	}
	return true
}

// Run decomposes method on the loaded world state with mode.
func (r *Runner) Run(method string, mode planner.BacktrackingMode) (planner.DecompositionStatus, []string) {
	r.unit.SetBacktrackingMode(mode)
	status := r.unit.DecomposeTopLevelMethod(atom.Intern(method))
	var steps []string
	if status == planner.Succeeded {
		for _, step := range r.unit.LastDecomposition().Elements() {
			steps = append(steps, FormatPlanStep(step))
		}
	}
	return status, steps
}

// RunnerTrace runs every demo domain and top-level method like the Domain
// Runner tab (each mode on the preferred world state, then every world state
// with all backtracking) and writes the htn-demo-oracle "runner" output.
func RunnerTrace(w io.Writer, root string, runtimeBacktracking bool) {
	runner := NewRunner(CallTermErrorReporter(w))
	worldStates := FindWorldStates(root)
	for _, d := range Domains {
		definition := d.Definition(runtimeBacktracking)
		for _, method := range d.Methods {
			fmt.Fprintf(w, "domain %s method %s\n", d.Name, method)
			if !runner.Select(definition, method) {
				fmt.Fprintln(w, "reload failed")
				continue
			}
			type run struct {
				index int
				mode  planner.BacktrackingMode
			}
			best := BestWorldState(d, worldStates)
			var runs []run
			for _, mode := range BacktrackingModes {
				runs = append(runs, run{best, mode})
			}
			for i := range worldStates {
				runs = append(runs, run{i, planner.BacktrackingAll})
			}
			for _, r := range runs {
				relative, _ := filepath.Rel(root, worldStates[r.index])
				fmt.Fprintf(w, "run %s %s\n", filepath.ToSlash(relative), ModeName(r.mode))
				if runner.Database.ParseWorldStateFile(worldStates[r.index]) != nil {
					fmt.Fprintln(w, "load failed")
					continue
				}
				status, steps := runner.Run(method, r.mode)
				fmt.Fprintf(w, "status %s\n", status)
				for _, step := range steps {
					fmt.Fprintf(w, "step %s\n", step)
				}
			}
		}
	}
}

// formatFloat formats a float like printf("%.9g").
func formatFloat(v float32) string { return strconv.FormatFloat(float64(v), 'g', 9, 64) }

// Simulation is the NPC Simulation tab: a shared terrain and Wanderer NPCs.
type Simulation struct {
	Terrain    *Terrain
	Agents     []*Agent
	Age        float32
	registry   *callterm.Registry
	definition *planner.Definition
	report     callterm.ErrorCallback
	nextID     uint32
}

// NewSimulation creates the default terrain and spawns agents NPCs.
func NewSimulation(agents int, runtimeBacktracking bool, report callterm.ErrorCallback) *Simulation {
	registry := callterm.NewRegistry()
	BindCallTerms(registry)
	wanderer, _ := FindDomain("Wanderer")
	s := &Simulation{Terrain: NewDefaultTerrain(), registry: registry, definition: wanderer.Definition(runtimeBacktracking),
		report: report, nextID: 1}
	s.Spawn(agents)
	return s
}

// Spawn adds count NPCs, each starting at waypoint (id - 1) % 12.
func (s *Simulation) Spawn(count int) {
	for i := 0; i < count; i++ {
		agent := NewAgent(s.nextID, s.definition, int(s.nextID-1)%WaypointCount, s.Terrain, s.registry, s.report)
		s.nextID++
		agent.Initialize()
		s.Agents = append(s.Agents, agent)
	}
}

// Update advances every NPC by deltaTime seconds.
func (s *Simulation) Update(deltaTime float32) {
	deltaTime = max(0, deltaTime)
	s.Age += deltaTime
	for _, agent := range s.Agents {
		agent.Update(deltaTime)
	}
}

// SimulationTrace runs the simulation with a fixed 1/60 s step and writes
// the htn-demo-oracle "simulate" output.
func SimulationTrace(w io.Writer, agents, steps, snapshotEvery int, runtimeBacktracking bool) {
	s := NewSimulation(agents, runtimeBacktracking, CallTermErrorReporter(w))
	t := s.Terrain
	fmt.Fprintf(w, "terrain %d %d interactables %d\n", t.Width(), t.Height(), len(t.Interactables()))
	for y := t.Height() - 1; y >= 0; y-- {
		row := make([]byte, t.Width())
		for x := range row {
			switch t.CellType(Cell{int32(x), int32(y)}) {
			case CellBlocked:
				row[x] = '#'
			case CellInteractable:
				row[x] = 'o'
			default:
				row[x] = '.'
			}
		}
		fmt.Fprintf(w, "map %2d %s\n", y, row)
	}
	for _, interactable := range t.Interactables() {
		fmt.Fprintf(w, "interactable %s %s %s %s %s\n", interactable.ID, interactable.Type.Name(), interactable.Location,
			interactable.ContextAnimation, formatFloat(interactable.UsageTimeSeconds))
	}
	if len(s.Agents) > 0 {
		var waypoints strings.Builder
		for i := 0; i < WaypointCount; i++ {
			waypoints.WriteString(" " + s.Agents[0].Wanderer().Waypoint(i).String())
		}
		fmt.Fprintf(w, "waypoints%s\n", waypoints.String())
	}
	const deltaTime = float32(1.0) / 60
	age := float32(0)
	snapshot := func(step int) {
		fmt.Fprintf(w, "frame %d age %s\n", step, formatFloat(age))
		for _, agent := range s.Agents {
			wanderer := agent.Wanderer()
			ok := 0
			if agent.LastPlanSucceeded() {
				ok = 1
			}
			fmt.Fprintf(w, "npc %d %s at %s to %s task %s remaining %s plans %d completed %d journeys %d ok %d\n",
				agent.ID(), wanderer.StateName(), wanderer.Location(), wanderer.Destination(), agent.CurrentTaskName(),
				formatFloat(agent.RemainingSeconds()), agent.PlanCount(), agent.CompletedTaskCount(),
				wanderer.Journeys(), ok)
			for i, step := range agent.CurrentPlan() {
				marker := "  "
				if i == agent.CurrentTaskIndex() {
					marker = "> "
				}
				fmt.Fprintf(w, "  %s%s\n", marker, agent.FormatTask(step))
			}
		}
	}
	snapshot(0)
	for step := 1; step <= steps; step++ {
		age += deltaTime
		for _, agent := range s.Agents {
			agent.Update(deltaTime)
		}
		if snapshotEvery > 0 && step%snapshotEvery == 0 {
			snapshot(step)
		}
	}
	for _, agent := range s.Agents {
		fmt.Fprintf(w, "history %d\n", agent.ID())
		for _, entry := range agent.History() {
			fmt.Fprintf(w, "  %s %s\n", formatFloat(entry.AgeSeconds), entry.Text)
		}
	}
}
