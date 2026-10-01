## htn-translator: validates HTN domains and translates them into native Nim
## planners.
##
##   htn-translator <domain-file> <entry-point> [output-directory] [options]
##   htn-translator --check <domain-file>

import std/os
import htn/translator

when isMainModule:
  var stdoutText, stderrText: string
  let code = runCommandLine(commandLineParams(), stdoutText, stderrText)
  stdout.write stdoutText
  stderr.write stderrText
  quit code
