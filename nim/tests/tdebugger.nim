## Runs testdata/debugger/*.scn, whose golden files were produced by
## htn-oracle-debug (the oracle built with HTN_DEBUG_DECOMPOSITION), and
## compares the generated event debugger dumps byte for byte. Built with
## -d:htnDebug (tdebugger.nims).

import std/os
import scenario
import generated/registry

proc main() =
  let root = currentSourcePath().parentDir.parentDir.parentDir
  if not checkGoldenDirectory(root, "testdata" / "debugger", planners): quit 1

main()
