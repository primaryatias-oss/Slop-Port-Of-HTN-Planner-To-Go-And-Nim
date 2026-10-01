## Runs every testdata/scenarios/*.scn file on the Nim port and compares the
## output with the golden file produced by the C++ oracle.

import std/[algorithm, os, strutils]
import scenario
import generated/registry

proc main() =
  let root = currentSourcePath().parentDir.parentDir.parentDir
  var files: seq[string]
  for file in walkFiles(root / "testdata" / "scenarios" / "*.scn"): files.add file
  files.sort()
  if files.len == 0:
    echo "no scenario files found"
    quit 1
  var failures = 0
  for file in files:
    let golden = readFile(file.changeFileExt("golden"))
    let (output, error) = runScenarioFile(file, root, planners)
    let name = extractFilename(file)
    if error.len > 0:
      echo "[FAIL] ", name, ": ", error
      inc failures
      continue
    if output == golden:
      echo "[OK]   ", name
      continue
    inc failures
    let want = golden.split('\n')
    let got = output.split('\n')
    var scenarioName = ""
    for i in 0 ..< max(want.len, got.len):
      let w = if i < want.len: want[i] else: ""
      let g = if i < got.len: got[i] else: ""
      if w.startsWith("scenario "): scenarioName = w
      if w != g:
        echo "[FAIL] ", name, " ", scenarioName, ": first difference at line ", i + 1
        for k in max(0, i - 8) ..< i: echo "    ", want[k]
        echo "  want: ", w
        echo "  got:  ", g
        break
  if failures > 0:
    echo failures, " scenario file(s) failed"
    quit 1
  echo "all ", files.len, " scenario files match the oracle"

main()
