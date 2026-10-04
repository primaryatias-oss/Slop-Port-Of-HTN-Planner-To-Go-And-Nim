## Runs every testdata/scenarios/*.scn file on the Nim port and compares the
## output with the golden file produced by the C++ oracle.

import std/os
import scenario
import generated/registry
import htn/planner
when not htnDebugEnabled:
  import std/strutils

proc main() =
  let root = currentSourcePath().parentDir.parentDir.parentDir
  var ok = checkGoldenDirectory(root, "testdata" / "scenarios", planners)
  when not htnDebugEnabled:
    # Release builds carry no debug events, so the debugger command is
    # rejected like by the release oracle (htn-oracle).
    let file = getTempDir() / "htn_nim_debugger_" & $getCurrentProcessId() & ".scn"
    writeFile(file, "scenario s\nplanner Human\ndebugger on\nend\n")
    let (_, error) = runScenarioFile(file, root, planners)
    removeFile(file)
    if "htnDebug" notin error:
      echo "[FAIL] the debugger command must need a -d:htnDebug build, got: ", error
      ok = false
  if not ok: quit 1

main()
