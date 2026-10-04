# HTN Planner: Go port

The Go module `github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go`
ports HTN Planner 2.0.4. Its translator turns HTN domains into Go packages,
and its runtime and integration layer plan with them. See the
[repository README](../README.md) for the overview, the verification method
and the differences from the original.

Requirements: Go 1.22 or newer (tested with 1.24) on Linux.

## Commands

| Command | Port of | Usage |
| --- | --- | --- |
| `cmd/htn-translator` | HTNTranslator | `htn-translator <domain-file> <entry-point> [output-directory] [options]`, or `htn-translator --check <domain-file>` |
| `cmd/htn-lsp` | HTNLanguageServer | JSON-RPC over stdio for editors (diagnostics, go-to-definition, completion, `htn/compile`) |
| `cmd/htn-benchmark` | HTNBenchmark | `htn-benchmark [iterations] [max_threads] [--lifecycle\|--lifecycle-heavy]` |
| `cmd/htn-demo` | HTNDemo | `htn-demo list`, `run <domain> [method]`, `simulate`, `trace` (see `htn-demo help`) |

Translator options:

```
--backtracking-policy=fixed-with-overflow|fixed-capacity  (default: fixed-with-overflow)
--backtracking-capacity=<positive integer>                (default: 32)
--call-frame-capacity=<positive integer>                  (default: 8192)
--runtime-backtracking-support=disabled|enabled           (default: disabled)
--package=<go package name>                               (default: derived from the domain file name)
```

The output is `<domain stem>.generated.go`. The entry point names its
accessor, `<entry-point>_GetDefinition()`.

## Using a generated planner

[examples/quickstart](examples/quickstart/main.go) is a complete program
(`go run ./examples/quickstart`). Its `//go:generate` line regenerates the
planner in `examples/quickstart/human`. The essential steps:

```go
database := integration.NewDatabaseHook()          // owns the world state
database.ParseWorldStateText(`item "apple"`)       // or ParseWorldStateFile
registry := callterm.NewRegistry()                 // host functions for (call ...)
hook := integration.NewPlannerHook(database.WorldState(), registry)
hook.SetGeneratedPlannerDefinition(human.CreateHumanHTN_GetDefinition())
unit := integration.NewPlanningUnit(database, hook, "behave")

status := unit.DecomposeTopLevelMethod(atom.Intern("behave"))
for status == planner.Succeeded && unit.ResolveCurrentPrimitiveTask() == integration.TaskReady {
	task, _ := unit.CurrentPrimitiveTask()   // e.g. (!take "apple")
	// ... perform the task, then:
	unit.CompleteCurrentPrimitiveTask()
}
```

`ResolveCurrentPrimitiveTask` also expands deferred (`&`) tasks by planning
them when they are reached.

Callterms are plain Go functions. Typed bindings derive their signatures from
the parameters. Member callterms receive the daemon that each planner binds:

```go
registry.MustBindFunc("distance", func(from, to int32) float32 { return float32(to - from) })
registry.BindMemberFunc("health", "agent", func(a *Agent) int32 { return a.health })
hook.Bindings().SetDaemon("agent", npc)
```

`unit.ExecutionContext()` selects the callterm error policy and callback.
`unit.SetBacktrackingMode(...)` takes effect on planners generated with
`--runtime-backtracking-support=enabled`.

## The generated execution debugger

Generated planners always contain debug metadata and event calls. These
compile to nothing unless the program is built with the `htndebug` tag:

```sh
go build -tags htndebug ./...
```

```go
d := debugger.New()
d.SetEnabled(true)
unit.SetGeneratedDebugger(d)   // reset at the start of every decomposition
unit.DecomposeTopLevelMethod(atom.Intern("behave"))
fmt.Print(d.Text(false))       // the recorded tree; d.Nodes() for the data
```

`go run -tags htndebug ./cmd/htn-demo run Human --debugger` shows the tree for
any demo domain.

## Packages

| Package | Contents |
| --- | --- |
| `htn/atom` | The value model: atoms, interned symbols, immutable lists |
| `htn/lexer` | Domain and world-state lexers |
| `htn/compiler` | Parser, includes and linking, validation, lowering to IR |
| `htn/codegen` | Go code generator (port of HTNCCodeGenerator) |
| `htn/translator` | Translation entry point shared by the CLI and tools |
| `htn/planner` | Runtime of generated planners: execution storage, call frames, continuations, debugger events |
| `htn/worldstate` | Fact database and world-state file parser |
| `htn/callterm` | Callterm registry, typed and member bindings, error policies |
| `htn/integration` | Database hook, planner hook, planning unit, daemons |
| `htn/debugger` | Generated execution debugger |
| `htn/lsp`, `htn/tooling` | Language server, its document store and tooling model |
| `internal/generated` | Planners of every variant in `testdata/planners.txt` (`go generate ./internal/generated`) |
| `internal/scenario`, `internal/demo` | Scenario interpreter of the golden tests; demo library |
| `internal/tools/*` | `genplanners`, `runscenario`, `fuzzscenarios`, `listmethods` |

## Tests

```sh
go vet ./... && go test ./...      # goldens: scenarios, translator, LSP, demo
go test -tags htndebug ./...       # also the debugger goldens
go generate ./... && git diff --exit-code   # generated planners are current
```

`go run ./internal/tools/runscenario -root .. file.scn` prints the output of
a scenario file. Running the same file through the reference `htn-oracle`
must produce the same bytes (see `../tools/oracle`).
