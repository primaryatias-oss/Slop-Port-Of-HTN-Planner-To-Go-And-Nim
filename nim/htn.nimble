# Package

version       = "2.0.4"
author        = "primaryatias-oss"
description   = "Nim port of the htn_planner HTN planner: domain translator, generated-planner runtime, integration layer and language server"
license       = "MIT"
srcDir        = "src"
installDirs   = @["htn"]
bin           = @["htn_translator", "htn_lsp"]

# Dependencies

requires "nim >= 2.2.0"

# Tasks (run from the nim/ directory)

task test, "Run the Nim test suite (tdebugger builds with -d:htnDebug)":
  exec "nim r --hints:off tools/genplanners.nim --check"
  for test in ["tscenarios", "tdebugger", "ttranslator", "tlsp", "ttooling", "tdemo"]:
    exec "nim c -r -d:release --hints:off --outdir:build tests/" & test & ".nim"
  exec "nim c -r -d:release --hints:off --outdir:build examples/quickstart.nim"

task generate, "Regenerate the test planners from testdata/planners.txt":
  exec "nim r tools/genplanners.nim"
