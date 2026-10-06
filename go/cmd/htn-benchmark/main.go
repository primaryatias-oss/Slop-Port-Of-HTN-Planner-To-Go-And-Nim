// Command htn-benchmark measures generated Go planners through the public
// hooks used by a host application (port of HTNBenchmark).
//
//	htn-benchmark [iterations] [max_threads]
//	htn-benchmark [iterations] [max_threads] --lifecycle
//	htn-benchmark [iterations] [max_threads] --lifecycle-heavy
//
// The regular suite reports throughput, latency, allocations, world-state
// gameplay scenarios and parallel scaling on the ComplexScenario domain.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/callterm"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/integration"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/planner"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/worldstate"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/generated/complexscenario"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/generated/worldstatelookupscenarios"
)

type benchmarkCase struct {
	name, category, worldState string
}

var benchmarkCases = []benchmarkCase{
	{"Idle", "Small / branch-light", "WorldStates/Test/complex_scenario_idle.worldstate"},
	{"Combat", "Branching", "WorldStates/Test/complex_scenario_combat.worldstate"},
	{"Emergency", "Alternative branch", "WorldStates/Test/complex_scenario_emergency.worldstate"},
	{"Mobility", "Callterm / movement", "WorldStates/Test/complex_scenario_mobility.worldstate"},
	{"Recovery", "Conditions / recovery", "WorldStates/Test/complex_scenario_recovery.worldstate"},
	{"FactHeavy100", "Fact-heavy / recursive", "WorldStates/Test/complex_scenario_recursive_100.worldstate"},
}

// resolveRepositoryPath looks for a repository-relative path from the current
// directory upwards.
func resolveRepositoryPath(relative string) string {
	probe, err := os.Getwd()
	if err != nil {
		return ""
	}
	for depth := 0; depth < 6; depth++ {
		candidate := filepath.Join(probe, relative)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return ""
}

func bindBenchmarkCallTerms(r *callterm.Registry) {
	r.Bind("binded_function_with_args", func(a *callterm.Arguments) atom.Atom {
		return atom.NewBool(a.Len() == 1 && a.At(0).IsBound())
	})
	r.Bind("get_health", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 1 && a.At(0).IsBound() {
			return atom.NewInt(50)
		}
		return atom.NewInt(0)
	})
	r.Bind("get_max_speed", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 1 && a.At(0).IsBound() {
			return atom.NewFloat(1)
		}
		return atom.NewFloat(0)
	})
	r.Bind("lt", func(a *callterm.Arguments) atom.Atom {
		return atom.NewBool(a.Len() == 2 && a.At(0).Is(atom.KindInt) && a.At(1).Is(atom.KindInt) &&
			a.At(0).Int() < a.At(1).Int())
	})
	r.Bind("inc", func(a *callterm.Arguments) atom.Atom {
		if a.Len() == 1 && a.At(0).Is(atom.KindInt) {
			return atom.NewInt(a.At(0).Int() + 1)
		}
		return atom.NewInt(0)
	})
}

// runner holds one planner instance: world state, prepared and execution
// storage and the reusable top-level call.
type runner struct {
	database   *integration.DatabaseHook
	definition *planner.Definition
	context    planner.Context
	call       atom.Atom
}

func newRunner(worldStatePath string, definition *planner.Definition, registry *callterm.Registry) (*runner, error) {
	r := &runner{database: integration.NewDatabaseHook(), definition: definition}
	if err := r.database.ParseWorldStateFile(worldStatePath); err != nil {
		return nil, err
	}
	r.context = planner.Context{
		WorldState:          r.database.WorldState(),
		Bindings:            callterm.NewBindingContext(registry),
		BacktrackingMode:    planner.BacktrackingAll,
		Execution:           definition.NewExecutionStorage(),
		Prepared:            definition.NewPreparedStorage(),
		CallTermErrorPolicy: callterm.PolicyFailSilently,
	}
	r.call, _ = atom.MakeCall(atom.Intern("run_scenario"))
	// Warm-up: resolve fact metadata and grow reusable scratch storage.
	if !r.execute() {
		return nil, fmt.Errorf("generated planner warm-up returned failure")
	}
	return r, nil
}

func (r *runner) execute() bool {
	_, status := r.definition.DecomposeCall(&r.context, r.call, true)
	return status == planner.Succeeded
}

type allocationStats struct{ allocations, bytes float64 }

func measureAllocations(iterations int, run func()) allocationStats {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < iterations; i++ {
		run()
	}
	runtime.ReadMemStats(&after)
	return allocationStats{
		allocations: float64(after.Mallocs-before.Mallocs) / float64(iterations),
		bytes:       float64(after.TotalAlloc-before.TotalAlloc) / float64(iterations),
	}
}

func percentile(sorted []time.Duration, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(p * float64(len(sorted)-1))
	return float64(sorted[index].Nanoseconds()) / 1000
}

func runSuite(definition *planner.Definition, registry *callterm.Registry, iterations int) (int, string) {
	latencySamples := iterations
	if latencySamples > 5000 {
		latencySamples = 5000
	}
	allocationIterations := iterations
	if allocationIterations > 1000 {
		allocationIterations = 1000
	}
	fmt.Println("scenario      category                    plans/s      us/plan   p50 us   p95 us   p99 us   max us  allocs/plan  bytes/plan  failures")
	failures := 0
	factHeavy := ""
	for _, c := range benchmarkCases {
		path := resolveRepositoryPath(c.worldState)
		if path == "" {
			fmt.Fprintf(os.Stderr, "Could not locate %s.\n", c.worldState)
			os.Exit(2)
		}
		r, err := newRunner(path, definition, registry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize benchmark scenario '%s': %v\n", c.name, err)
			os.Exit(3)
		}
		caseFailures := 0
		start := time.Now()
		for i := 0; i < iterations; i++ {
			if !r.execute() {
				caseFailures++
			}
		}
		elapsed := time.Since(start)
		samples := make([]time.Duration, latencySamples)
		for i := range samples {
			sampleStart := time.Now()
			if !r.execute() {
				caseFailures++
			}
			samples[i] = time.Since(sampleStart)
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		allocations := measureAllocations(allocationIterations, func() {
			if !r.execute() {
				caseFailures++
			}
		})
		seconds := elapsed.Seconds()
		fmt.Printf("%-13s %-24s %11.0f %12.3f %8.2f %8.2f %8.2f %8.2f %12.1f %11.0f %9d\n",
			c.name, c.category, float64(iterations)/seconds, seconds*1e6/float64(iterations),
			percentile(samples, 0.50), percentile(samples, 0.95), percentile(samples, 0.99),
			percentile(samples, 1), allocations.allocations, allocations.bytes, caseFailures)
		failures += caseFailures
		if c.name == "FactHeavy100" {
			factHeavy = path
		}
	}
	return failures, factHeavy
}

var worldStateScenarioFacts = []string{
	"active_entity", "entity_state", "entity_target", "entity_squad", "entity_weapon", "entity_cover",
	"entity_route", "target_status", "squad_order", "weapon_status", "cover_quality", "route_status", "ammo_available",
}

// rebuildWorldStateScenario writes a full snapshot: the selected entity is the
// last row so linear lookups must traverse the tables.
func rebuildWorldStateScenario(w *worldstate.WorldState, rows int) bool {
	w.RemoveAllFacts()
	symbol := func(name string) *atom.Symbol { return atom.Intern(name) }
	i := func(v int) atom.Atom { return atom.NewInt(int32(v)) }
	if !w.WriteFact(symbol("active_entity"), i(rows-1)) {
		return false
	}
	for row := 0; row < rows; row++ {
		id := i(row)
		ok := w.WriteFact(symbol("entity_state"), id, i(row%4)) &&
			w.WriteFact(symbol("entity_target"), id, id) && w.WriteFact(symbol("entity_squad"), id, id) &&
			w.WriteFact(symbol("entity_weapon"), id, id) && w.WriteFact(symbol("entity_cover"), id, id) &&
			w.WriteFact(symbol("entity_route"), id, id) && w.WriteFact(symbol("target_status"), id, i(1)) &&
			w.WriteFact(symbol("squad_order"), id, i(2)) && w.WriteFact(symbol("weapon_status"), id, i(1)) &&
			w.WriteFact(symbol("cover_quality"), id, i(3)) && w.WriteFact(symbol("route_status"), id, i(1)) &&
			w.WriteFact(symbol("ammo_available"), id)
		if !ok {
			return false
		}
	}
	return true
}

func runWorldStateScenarios(registry *callterm.Registry) int {
	path := resolveRepositoryPath("WorldStates/Test/worldstate_lookup_scenarios.worldstate")
	if path == "" {
		fmt.Println("\nGenerated WorldState scenarios: SKIPPED (world state not found)")
		return 0
	}
	fmt.Println("\nGenerated WorldState gameplay scenarios (real generated decomposition)")
	fmt.Println("  each frame performs RemoveAllFacts -> full WriteFact snapshot -> generated Decompose")
	fmt.Println("  scenario              rows/fact  fact types    iterations      us/frame      frames/s   failures")
	definition := worldstatelookupscenarios.CreateWorldstateLookupScenariosHTN_GetDefinition()
	failures := 0
	for _, scenario := range []struct {
		name       string
		rows       int
		iterations int
	}{{"TypicalGameAI", 5, 10000}, {"CrowdAI", 50, 5000}, {"LargeFactDatabase", 500, 500}} {
		r, err := newRunner(path, definition, registry)
		if err != nil {
			fmt.Printf("  %-20s initialization failed\n", scenario.name)
			continue
		}
		registryForFacts := worldstate.NewFactRegistry()
		for _, name := range worldStateScenarioFacts {
			registryForFacts.Register(atom.Intern(name))
		}
		world := r.database.WorldState()
		world.SetFactRegistry(registryForFacts)
		if !rebuildWorldStateScenario(world, scenario.rows) || !r.execute() {
			fmt.Printf("  %-20s warm-up failed\n", scenario.name)
			continue
		}
		scenarioFailures := 0
		start := time.Now()
		for i := 0; i < scenario.iterations; i++ {
			if !rebuildWorldStateScenario(world, scenario.rows) || !r.execute() {
				scenarioFailures++
			}
		}
		seconds := time.Since(start).Seconds()
		fmt.Printf("  %-20s %10d %11d %13d %13.3f %13.2f %10d\n", scenario.name, scenario.rows,
			len(worldStateScenarioFacts), scenario.iterations, seconds*1e6/float64(scenario.iterations),
			float64(scenario.iterations)/seconds, scenarioFailures)
		failures += scenarioFailures
	}
	return failures
}

func runThreadScaling(path string, definition *planner.Definition, registry *callterm.Registry, iterationsPerWorker, maxThreads int) int {
	fmt.Println("\nParallel scaling: FactHeavy100 (independent WorldState + execution storage per worker)")
	baseline := 0.0
	failures := 0
	for _, workers := range []int{1, 2, 4, 8} {
		if workers > maxThreads {
			break
		}
		runners := make([]*runner, workers)
		for i := range runners {
			r, err := newRunner(path, definition, registry)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to initialize benchmark worker %d: %v\n", i, err)
				return failures + 1
			}
			runners[i] = r
		}
		var wg sync.WaitGroup
		var mutex sync.Mutex
		start := make(chan struct{})
		for _, r := range runners {
			wg.Add(1)
			go func(r *runner) {
				defer wg.Done()
				<-start
				local := 0
				for i := 0; i < iterationsPerWorker; i++ {
					if !r.execute() {
						local++
					}
				}
				mutex.Lock()
				failures += local
				mutex.Unlock()
			}(r)
		}
		begin := time.Now()
		close(start)
		wg.Wait()
		seconds := time.Since(begin).Seconds()
		throughput := float64(iterationsPerWorker*workers) / seconds
		if workers == 1 {
			baseline = throughput
		}
		fmt.Printf("  %d thread(s): %.2f plans/s  speedup=%.2fx\n", workers, throughput, throughput/baseline)
	}
	return failures
}

func runLifecycle(definition *planner.Definition, registry *callterm.Registry, iterations int, heavy bool) int {
	fmt.Println("\nGenerated public API lifecycle: entity-cold setup / first plan / warmed plans")
	fmt.Println("scenario,backend,phase,ops,us/op,allocs/op,bytes/op,failures")
	failures := 0
	for _, c := range benchmarkCases {
		factHeavy := c.name == "FactHeavy100"
		if factHeavy && !heavy {
			fmt.Fprintln(os.Stderr, "FactHeavy100 skipped: use --lifecycle-heavy.")
			continue
		}
		count := iterations
		if factHeavy && count > 5 {
			count = 5
		}
		path := resolveRepositoryPath(c.worldState)
		caseFailures := 0
		type phase struct {
			name      string
			ops       int
			duration  time.Duration
			allocated allocationStats
		}
		var phases []phase
		measure := func(name string, ops int, run func()) {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			run()
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			phases = append(phases, phase{name, ops, elapsed, allocationStats{
				float64(after.Mallocs-before.Mallocs) / float64(ops), float64(after.TotalAlloc-before.TotalAlloc) / float64(ops)}})
		}
		database := integration.NewDatabaseHook()
		measure("worldstate", 1, func() {
			if database.ParseWorldStateFile(path) != nil {
				caseFailures++
			}
		})
		var unit *integration.PlanningUnit
		measure("setup", 1, func() {
			hook := integration.NewPlannerHook(database.WorldState(), registry)
			if !hook.SetGeneratedPlannerDefinition(definition) {
				caseFailures++
			}
			unit = integration.NewPlanningUnit(database, hook, "run_scenario")
		})
		decompose := func() {
			if unit.Decompose() != planner.Succeeded {
				caseFailures++
			}
		}
		measure("first", 1, decompose)
		measure("steady", count, func() {
			for i := 0; i < count; i++ {
				decompose()
			}
		})
		for _, p := range phases {
			fmt.Printf("%s,generated,%s,%d,%.3f,%.1f,%.0f,%d\n", c.name, p.name, p.ops,
				float64(p.duration.Nanoseconds())/1000/float64(p.ops), p.allocated.allocations, p.allocated.bytes, caseFailures)
		}
		failures += caseFailures
	}
	return failures
}

func main() {
	iterations := 10000
	maxThreads := runtime.NumCPU()
	if maxThreads > 8 {
		maxThreads = 8
	}
	if len(os.Args) > 1 {
		if value, err := strconv.Atoi(os.Args[1]); err == nil && value > 0 {
			iterations = value
		}
	}
	if len(os.Args) > 2 {
		if value, err := strconv.Atoi(os.Args[2]); err == nil && value > 0 {
			maxThreads = value
		}
	}
	definition := complexscenario.CreateComplexScenarioHTN_GetDefinition()
	registry := callterm.NewRegistry()
	bindBenchmarkCallTerms(registry)
	if len(os.Args) > 3 && (os.Args[3] == "--lifecycle" || os.Args[3] == "--lifecycle-heavy") {
		if runLifecycle(definition, registry, iterations, os.Args[3] == "--lifecycle-heavy") != 0 {
			os.Exit(4)
		}
		return
	}
	fmt.Printf("HTN generated planner baseline benchmark (Go port)\n  domain:            ComplexScenario\n"+
		"  iterations/case:   %d\n  max threads:       %d\n  go:                %s %s/%s\n\n",
		iterations, maxThreads, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	failures, factHeavy := runSuite(definition, registry, iterations)
	failures += runWorldStateScenarios(registry)
	if factHeavy != "" {
		perWorker := iterations / 10
		if perWorker < 100 {
			perWorker = 100
		}
		failures += runThreadScaling(factHeavy, definition, registry, perWorker, maxThreads)
	}
	result := "PASS"
	if failures != 0 {
		result = "FAIL"
	}
	fmt.Printf("\nBaseline result: %s  total_failures=%d\n", result, failures)
	if failures != 0 {
		os.Exit(4)
	}
}
