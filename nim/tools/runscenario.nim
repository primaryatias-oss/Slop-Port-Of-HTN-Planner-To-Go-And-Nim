## Prints the Nim port's output for scenario files (the counterpart of the
## C++ htn-oracle and the Go runscenario tool).
##
##   runscenario [--root=..] file.scn...

import std/[os, strutils]
import ../tests/scenario
import ../tests/generated/registry

proc main() =
  var root = ".."
  var files: seq[string]
  for argument in commandLineParams():
    if argument.startsWith("--root="): root = argument["--root=".len .. ^1]
    else: files.add argument
  for file in files:
    let (output, error) = runScenarioFile(file, root, planners)
    stdout.write output
    if error.len > 0:
      stderr.writeLine error
      quit 2

main()
