## Replays testdata/lsp/*.session through the Nim language server and
## compares every response with the ones recorded from the original
## HTNLanguageServer (tools/lsp/make_goldens.py).

import std/[algorithm, os, strutils]
import htn/lsp

proc percentEncodePath(path: string): string =
  for c in path:
    if c in {'0' .. '9', 'a' .. 'z', 'A' .. 'Z', '-', '_', '.', '~', '/', ':'}: result.add c
    else: result.add "%" & toHex(ord(c), 2)

proc replay(sessionPath, root: string): string =
  let rootUri = "file://" & percentEncodePath(root)
  var input = ""
  let records = readFile(sessionPath).split("--- message")
  for record in records[1 .. ^1]:
    let newline = record.find('\n')
    let header = record[0 ..< newline]
    var payload = record[newline + 1 .. ^1]
    if payload.endsWith("\n"): payload.setLen(payload.len - 1)
    payload = payload.replace("$ROOT_URI", rootUri).replace("$ROOT", root)
    if header.strip() == "lowercase":
      input.add "content-length: " & $payload.len &
        "\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n"
    else:
      input.add "Content-Length: " & $payload.len & "\r\n\r\n"
    input.add payload
  var output, log: string
  let server = newServer(newTransport(stringSource(input), proc (data: string) = output.add data))
  server.log = proc (data: string) = log.add data
  let code = server.run()
  proc normalize(text: string): string = text.replace(rootUri, "$ROOT_URI").replace(root, "$ROOT")
  var offset = 0
  const prefix = "Content-Length: "
  while offset < output.len:
    doAssert output.continuesWith(prefix, offset), "unexpected output framing"
    let headerEnd = output.find("\r\n\r\n", offset)
    let length = parseInt(output[offset + prefix.len ..< headerEnd])
    let payload = output[headerEnd + 4 ..< headerEnd + 4 + length]
    result.add "--- message\n" & normalize(payload) & "\n"
    offset = headerEnd + 4 + length
  result.add "--- stderr\n" & normalize(log) & "--- exit " & $code & "\n"

proc main() =
  let root = currentSourcePath().parentDir.parentDir.parentDir
  setCurrentDir(root)
  var sessions: seq[string]
  for path in walkFiles(root / "testdata" / "lsp" / "*.session"): sessions.add path
  sessions.sort()
  doAssert sessions.len > 0, "no sessions found"
  var failures = 0
  for path in sessions:
    let actual = replay(path, root)
    let golden = readFile(path.changeFileExt("golden"))
    if actual != golden:
      inc failures
      let want = golden.splitLines()
      let got = actual.splitLines()
      for i in 0 ..< max(want.len, got.len):
        let w = if i < want.len: want[i] else: ""
        let g = if i < got.len: got[i] else: ""
        if w != g:
          echo "MISMATCH ", path.extractFilename, " line ", i + 1, "\nwant: ", w, "\ngot:  ", g
          break
  if failures > 0: quit 1
  echo "all ", sessions.len, " language server sessions match the original HTNLanguageServer"

main()
