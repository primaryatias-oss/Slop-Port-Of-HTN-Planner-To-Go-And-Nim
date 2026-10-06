# HTN Planner: Nim port

The `htn` Nim package ports HTN Planner 2.0.4. Its translator turns HTN
domains into Nim modules, and its runtime and integration layer plan with
them. See the [repository README](../README.md) for the overview, the
verification method and the differences from the original.

Requirements: Nim 2.2 or newer and a C compiler, on Linux.

## Building

```sh
cd nim
nimble build -d:release        # ./htn_translator and ./htn_lsp
nimble install                 # or install the package and both binaries
nimble test                    # the test suite (see Tests)
```

The other programs are plain Nim files. `config.nims` puts `src/` on the
module path for everything under `nim/`:

```sh
nim c -d:release tools/htn_benchmark.nim   # htn_benchmark.nims adds --mm:atomicArc --threads:on
nim c -d:release demo/htn_demo.nim
```

| Program | Port of | Usage |
| --- | --- | --- |
| `src/htn_translator.nim` | HTNTranslator | `htn_translator <domain-file> <entry-point> [output-directory] [options]`, or `htn_translator --check <domain-file>` |
| `src/htn_lsp.nim` | HTNLanguageServer | JSON-RPC over stdio for editors (diagnostics, go-to-definition, completion, `htn/compile`) |
| `tools/htn_benchmark.nim` | HTNBenchmark | `htn_benchmark [iterations] [max_threads] [--lifecycle\|--lifecycle-heavy]` |
| `demo/htn_demo.nim` | HTNDemo | `htn_demo list`, `run <domain> [method]`, `simulate`, `trace` (see `htn_demo help`) |

Translator options:

```
--backtracking-policy=fixed-with-overflow|fixed-capacity  (default: fixed-with-overflow)
--backtracking-capacity=<positive integer>                (default: 32)
--call-frame-capacity=<positive integer>                  (default: 8192)
--runtime-backtracking-support=disabled|enabled           (default: disabled)
--module-name=<nim module name>                           (default: <domain stem>_generated)
```

A generated module imports `htn/[atom, callterm, planner, worldstate]` and
exports `<entry-point>_GetDefinition()`. Compile it with the installed
package, or with `--path:<repository>/nim/src`.

## Using a generated planner

[examples/quickstart.nim](examples/quickstart.nim) is a complete program
(`nim r examples/quickstart.nim`) using the planner in
`examples/human_generated.nim`. The essential steps:

```nim
let database = newDatabaseHook()                   # owns the world state
discard database.parseWorldStateText("item \"apple\"")   # or parseWorldStateFile
let registry = newRegistry()                       # host procs for (call ...)
let hook = newPlannerHook(database.worldState, registry)
discard hook.setGeneratedPlannerDefinition(CreateHumanHTN_GetDefinition())
let unit = newPlanningUnit(database, hook, "behave")

if unit.decomposeTopLevelMethod(intern("behave")) == dsSucceeded:
  while unit.resolveCurrentPrimitiveTask() == taskReady:
    let (task, _) = unit.currentPrimitiveTask()   # e.g. (!take "apple")
    # ... perform the task, then:
    unit.completeCurrentPrimitiveTask()
```

`resolveCurrentPrimitiveTask` also expands deferred (`&`) tasks by planning
them when they are reached.

Callterms are plain Nim procs. Typed bindings derive their signatures from
the parameters. Member callterms receive the daemon (a `ref object of
RootObj`) that each planner binds:

```nim
registry.bindFunc("distance", proc (fromCell, toCell: int32): float32 = float32(toCell - fromCell))
discard registry.bindMemberFunc("health", "agent", proc (a: Agent): int32 = a.health)
discard hook.bindings.setDaemon("agent", npc)
```

`unit.executionContext` selects the callterm error policy and callback.
`unit.setBacktrackingMode(...)` takes effect on planners generated with
`--runtime-backtracking-support=enabled`.

## The generated execution debugger

Generated planners contain debug metadata and event templates. Both expand to
nothing unless the program is compiled with `-d:htnDebug`:

```nim
import htn/debugger

let d = newGeneratedDebugger()
d.setEnabled(true)
unit.setGeneratedDebugger(d)   # reset at the start of every decomposition
discard unit.decomposeTopLevelMethod(intern("behave"))
stdout.write d.text()          # the recorded tree; d.nodes for the data
```

`nim r -d:release -d:htnDebug demo/htn_demo.nim run Human --debugger` shows
the tree for any demo domain.

## Modules

| Module | Contents |
| --- | --- |
| `htn/atom` | The value model: atoms, interned symbols, immutable lists |
| `htn/lexer` | Domain and world-state lexers |
| `htn/compiler/*` | Parser, includes and linking, validation, lowering to IR |
| `htn/codegen/nimgen` | Nim code generator (port of HTNCCodeGenerator) |
| `htn/translator` | Translation entry point shared by the CLI and tools |
| `htn/planner` | Runtime of generated planners: execution storage, call frames, continuations, debugger events |
| `htn/worldstate` | Fact database and world-state file parser |
| `htn/callterm` | Callterm registry, typed and member bindings, error policies |
| `htn/integration` | Database hook, planner hook, planning unit, daemons |
| `htn/debugger`, `htn/debugmeta` | Generated execution debugger and the metadata it reads |
| `htn/lsp`, `htn/lspjson`, `htn/tooling` | Language server, its JSON model, document store and tooling model |

## Tests

`nimble test` runs everything below. It also checks that `tests/generated`
matches the generator (`nim r tools/genplanners.nim --check`;
`nimble generate` regenerates it).

| Test | Compares with the original |
| --- | --- |
| `tests/tscenarios.nim` | scenario goldens (`testdata/scenarios`) |
| `tests/tdebugger.nim` | debugger goldens (`testdata/debugger`), built with `-d:htnDebug` by `tdebugger.nims` |
| `tests/ttranslator.nim` | translator goldens (`testdata/translator`) |
| `tests/tlsp.nim`, `tests/ttooling.nim` | language server sessions and tooling model |
| `tests/tdemo.nim` | demo traces (`testdata/demo`) |

`tools/runscenario.nim` prints the output of scenario files. Running the same
files through the reference `htn-oracle` must produce the same bytes (see
`../tools/oracle`).
