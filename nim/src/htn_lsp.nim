## htn_lsp: the HTN language server (JSON-RPC over stdio for editors:
## diagnostics, go-to-definition, completion, htn/compile).

import htn/lsp

proc writeStdout(data: string) =
  stdout.write(data)
  stdout.flushFile()

proc writeStderr(data: string) =
  stderr.write(data)
  stderr.flushFile()

when isMainModule:
  let server = newServer(newTransport(fileDescriptorSource(0), writeStdout))
  server.log = writeStderr
  quit(server.run())
