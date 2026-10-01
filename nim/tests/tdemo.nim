## Compares the Nim demo's traces with the goldens recorded from the original
## HTNDemo sources (tools/demo/make_goldens.py).

import std/[os, strutils]
import ../demo/runner

proc compare(root, name, actual: string): bool =
  let golden = readFile(root / "testdata" / "demo" / name)
  if actual == golden: return true
  let want = golden.splitLines()
  let got = actual.splitLines()
  for i in 0 ..< max(want.len, got.len):
    let w = if i < want.len: want[i] else: ""
    let g = if i < got.len: got[i] else: ""
    if w != g:
      echo "MISMATCH ", name, " line ", i + 1, "\nwant: ", w, "\ngot:  ", g
      break
  false

proc main() =
  let root = currentSourcePath().parentDir.parentDir.parentDir
  var failures = 0
  for runtimeBacktracking in [false, true]:
    var output = ""
    runnerTrace(proc (text: string) = output.add(text), root, runtimeBacktracking)
    if not compare(root, (if runtimeBacktracking: "runner_rt.golden" else: "runner.golden"), output): inc failures
    var simulation = ""
    simulationTrace(proc (text: string) = simulation.add(text), 8, 3600, 300, runtimeBacktracking)
    if not compare(root, "simulate.golden", simulation): inc failures
  if failures > 0: quit 1
  echo "the demo's domain runner and NPC simulation match the original HTNDemo"

main()
