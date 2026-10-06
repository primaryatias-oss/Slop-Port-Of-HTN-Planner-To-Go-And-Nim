# Slop-Port-Of-HTN-Planner-To-Go-And-Nim
A slop port using Claude Opus 5.5 of https://github.com/urosidoki/htn_planner

Ports of [HTN Planner](https://github.com/urosidoki/htn_planner) 2.0.4, a
hierarchical task network planner for game AI, to **Go** and **Nim** on Linux.

As in the original, domains are written in a small declarative language and
translated ahead of time into native code: the Go port's translator emits Go
and the Nim port's emits Nim, in place of C. The generated planners run without
parsing domain source at runtime. Plans, diagnostics, language-server traffic,
demo traces and debugger output match the original byte for byte. They are
checked against the original C++, built from source on Linux.

- [What is ported](#what-is-ported)
- [Quick start](#quick-start)
- [How the ports are verified](#how-the-ports-are-verified)
- [Differences from the original](#differences-from-the-original)
- [Repository layout](#repository-layout)
- [License](#license)

Port-specific documentation: [go/README.md](go/README.md) and
[nim/README.md](nim/README.md).

## What is ported

| Original component | Go (`go/`) | Nim (`nim/`) |
| --- | --- | --- |
| Domain language frontend: lexer, parser, includes, linker, validator, IR | `htn/lexer`, `htn/compiler` | `src/htn/lexer.nim`, `src/htn/compiler/` |
| Atoms, world state, callterm registry and typed callterms | `htn/atom`, `htn/worldstate`, `htn/callterm` | `atom.nim`, `worldstate.nim`, `callterm.nim` |
| `HTNTranslator` and its code generator | `htn-translator`, emitting Go (`htn/codegen`) | `htn_translator`, emitting Nim (`codegen/nimgen.nim`) |
| Generated-planner runtime (call frames, continuations, backtracking) | `htn/planner` | `planner.nim` |
| `HTNIntegration`: database hook, planner hook, planning unit, daemons | `htn/integration` | `integration.nim` |
| Generated execution debugger (`HTN_DEBUG_DECOMPOSITION`) | `htn/debugger`, build tag `htndebug` | `debugger.nim`, `-d:htnDebug` |
| `HTNLanguageServer` | `htn-lsp` | `htn_lsp` |
| `HTNBenchmark` | `htn-benchmark` | `htn_benchmark` |
| `HTNDemo`: Domain Runner and NPC simulation | `htn-demo` (terminal) | `htn_demo` (terminal) |

Not ported:

- **HTNEditor** and the graphical side of **HTNDemo** (SDL/ImGui windows,
  memory panel). The terminal demos run the same domains, simulation and
  agents, and print the debugger tree with `run --debugger`.
- **HTNVSCode** needs no port. The original extension works with the ports'
  language servers: point its `htn.languageServer.path` setting (command
  *HTN: Select Language Server*) at `htn-lsp` or `htn_lsp`.
- **HTNHotReloadDemo** and **HTNRuntimeBridge**. They reload domain DLLs through
  the C runtime bridge, which has no counterpart: Go and Nim planners are
  compiled into the program.
- Profiling builds (Optick, `HTN_PROFILE_DETAILED`,
  `HTN_GENERATED_EXECUTION_PROFILING`).
- **HTNTest**, the GoogleTest suites. The differential tests below replace them.
- The Windows SDK packaging, Premake and Visual Studio project generation.

## Quick start

Go (1.22 or newer):

```sh
cd go
go run ./examples/quickstart          # plan with a pre-generated Human planner
go run ./cmd/htn-translator ../Domains/Test/human.domain CreateHumanHTN ./myplanner --package=myplanner
go run ./cmd/htn-demo run Human       # Domain Runner in the terminal
go run ./cmd/htn-demo simulate        # NPC simulation (Ctrl-C to quit)
```

Nim (2.2 or newer):

```sh
cd nim
nimble build -d:release               # builds htn_translator and htn_lsp
nim r examples/quickstart.nim
./htn_translator ../Domains/Test/human.domain CreateHumanHTN ./myplanner
nim r -d:release demo/htn_demo.nim run Human
```

[go/examples/quickstart](go/examples/quickstart/main.go) and
[nim/examples/quickstart.nim](nim/examples/quickstart.nim) show the whole
integration: load facts into a world state, attach a generated planner,
decompose a top-level method and execute the plan step by step.

## How the ports are verified

The reference is the original code, unmodified, built on Linux by
[tools/oracle](tools/oracle/CMakeLists.txt) (tested with GCC 13 and CMake
3.28) from a checkout of the original repository:

- `HTNTranslator` and `HTNLanguageServer`, as shipped.
- `htn-oracle`, a scenario runner. It links the C planners that the original
  translator generates for every variant in
  [testdata/planners.txt](testdata/planners.txt) and drives them through the
  original integration layer.
- `htn-oracle-debug`, the same runner built with `HTN_DEBUG_DECOMPOSITION`. It
  dumps the original generated event debugger.
- `htn-demo-oracle`, which drives the original HTNDemo world, daemons and
  agents headlessly.

Golden files recorded from these programs are checked by both ports' test
suites:

| Corpus | Contents |
| --- | --- |
| [testdata/scenarios](testdata/scenarios) | 183 scenarios over 56 planner variants: planning, plan execution and deferred tasks, every backtracking mode, capacity limits, callterm errors and typed signatures (152k output lines) |
| [testdata/translator](testdata/translator) | 124 translator command lines: diagnostics, exit codes and summaries |
| [testdata/lsp](testdata/lsp) | language server sessions, compared on raw bytes |
| [testdata/demo](testdata/demo) | Domain Runner and NPC simulation traces |
| [testdata/debugger](testdata/debugger) | 19 scenarios of generated event debugger dumps, covering every node kind and state |

Differential fuzzers compare the original, Go and Nim on random inputs. The
latest runs found no differences:

- **Random scenarios** (`fuzzscenarios`): 100 seeds, 16,800 scenarios and
  489k output lines.
- **Debugger dumps** (`fuzzscenarios -debugger`): 60 seeds with 26,331 dumps,
  plus every scenario file rerun with the debugger on (4,323 dumps, 1.3M
  nodes).
- **Domain frontend** ([tools/fuzz/frontend_fuzz.py](tools/fuzz/frontend_fuzz.py)):
  3,000 token-level mutations of the repository's domains, compared on exit
  codes and diagnostics.
- **Language server** ([tools/fuzz/lsp_fuzz.py](tools/fuzz/lsp_fuzz.py)):
  1,500 random sessions, compared on raw bytes. Three were skipped because the
  original server crashed (see below).

To rebuild the reference and regenerate the goldens:

```sh
git clone https://github.com/urosidoki/htn_planner ../htn_planner
git -C ../htn_planner checkout 115df665cffa27a4b55616619c7775f80a849c1b
cmake -S tools/oracle -B build/oracle -G Ninja -DCMAKE_BUILD_TYPE=Release -DHTN_ORIGINAL_ROOT=../htn_planner
cmake --build build/oracle
for f in testdata/scenarios/*.scn; do build/oracle/htn-oracle "$f" > "${f%.scn}.golden"; done
build/oracle/htn-oracle-debug testdata/debugger/debugger.scn > testdata/debugger/debugger.golden
python3 tools/translator/make_goldens.py --oracle build/oracle/HTNTranslator
python3 tools/lsp/make_goldens.py --oracle build/oracle/HTNLanguageServer
python3 tools/demo/make_goldens.py --oracle build/oracle/htn-demo-oracle
```

The goldens in this repository were recorded from that commit of the
original. The CI workflow can repeat the whole procedure (run it manually with
the `oracle` input).

## Differences from the original

Deliberate differences, all outside the compared behavior:

- **Generated code.** The translators emit a Go package
  (`<stem>.generated.go`, option `--package=`) or a Nim module
  (`<stem>_generated.nim`, option `--module-name=`; Nim module names cannot
  contain dots) instead of C. There is no C ABI, DLL loading or runtime
  bridge.
- **Tool name.** Messages name the translator `htn-translator` instead of
  `HTNTranslator`, including the call-frame capacity diagnostic of generated
  planners. The golden tools normalize the name.
- **Unbound callterm arguments.** An unbound variable passed to a callterm
  arrives as an unbound atom. The original passes a null pointer, which is
  undefined behavior.
- **`Unset` callterm error policy.** The original asserts in debug builds and
  fails safely in release builds. The ports always fail safely.
- **Directory includes in the language server.** When an `:include` names a
  directory, the original translator reads it as an empty file, but the
  original language server terminates with an uncaught `std::ios_base::failure`.
  The ports' servers read it as an empty file too and keep running.
- **Debugger builds.** The instrumentation is selected with the Go build tag
  `htndebug` or the Nim define `htnDebug` instead of the
  `HTN_DEBUG_DECOMPOSITION` macro. In release builds it compiles to nothing.

## Repository layout

| Path | Contents |
| --- | --- |
| [go/](go) | Go module: libraries under `htn/`, commands under `cmd/`, the generated test planners under `internal/generated` |
| [nim/](nim) | Nim package: library under `src/htn/`, translator and language server in `src/`, demo, benchmark and tests |
| [Domains/](Domains), [WorldStates/](WorldStates) | Domains and world states of the original, used by tests, demos and benchmarks |
| [HTNDiagnosticTests/](HTNDiagnosticTests) | The original's editor diagnostic samples |
| [testdata/](testdata) | Golden files recorded from the original and the planner variant list |
| [tools/](tools) | The reference build (`oracle`), golden recorders and differential fuzzers |

## License

MIT, see [LICENSE](LICENSE). The ports are derived from HTN Planner and
include its domains and world states. That work is MIT licensed by its
authors, and its notice is reproduced in [NOTICE.md](NOTICE.md).
