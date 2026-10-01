## Replays testdata/translator/cases.txt through the Nim htn-translator and
## compares exit codes and outputs with the ones recorded from the original
## HTNTranslator.

import std/[os, strutils, tempfiles]
import htn/translator

const portUsageLine = "  --module-name=<nim module name> (default: <domain stem>_generated)\n"

proc main() =
  let root = currentSourcePath().parentDir.parentDir.parentDir
  let golden = readFile(root / "testdata" / "translator" / "cases.golden").split("--- end\n")
  let output = createTempDir("htn-translator-", "")
  setCurrentDir(root)
  var index, failures = 0
  for line in lines(root / "testdata" / "translator" / "cases.txt"):
    if line.startsWith("#"): continue
    var args: seq[string]
    for field in line.splitWhitespace(): args.add field.replace("$OUT", output)
    var stdoutText, stderrText: string
    let code = runCommandLine(args, stdoutText, stderrText)
    proc normalize(text: string): string =
      text.replace(portUsageLine, "").replace(output, "$OUT").replace(root, "$ROOT").replace("_generated.nim",
        ".generated.<ext>")
    let actual = "case " & line & "\nexit " & $code & "\n--- stdout\n" & normalize(stdoutText) & "--- stderr\n" &
      normalize(stderrText)
    if actual != golden[index]:
      inc failures
      echo "MISMATCH ", line
      echo "--- want\n", golden[index], "\n--- got\n", actual
    inc index
  removeDir(output)
  if failures > 0:
    echo failures, " translator case(s) differ"
    quit 1
  echo "all ", index, " translator cases match the original HTNTranslator"

main()
