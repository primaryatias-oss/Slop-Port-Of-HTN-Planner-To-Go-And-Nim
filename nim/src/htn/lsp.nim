## The HTN language server (port of HTNLanguageServer): JSON-RPC over stdio
## with Content-Length framing, full-document sync, diagnostics,
## go-to-definition, variable completion and the custom "htn/compile"
## request used by the editor's compile command.
##
## Responses are byte-identical to the original server's: the JSON library is
## a port of HTNLspJson and paths follow std::filesystem semantics.

import std/[posix, sets, strutils]
import fspath, lexer, lspjson, tooling
import compiler/[diagnostics, loader]

const ServerName* = "HTNLanguageServer"
  ## The name reported in the initialize response.

type
  ByteSource* = proc (chunk: var string): bool {.closure.}
    ## Replaces `chunk` with the next available input bytes; false at end of
    ## input.
  ByteSink* = proc (data: string) {.closure.}
    ## Writes (and flushes) output bytes.

  Transport* = ref object
    ## Reads and writes LSP messages with Content-Length framing.
    source: ByteSource
    sink: ByteSink
    pending: string
    offset: int
    finished: bool

  Server* = ref object
    ## The language server state.
    transport: Transport
    documents: Store
    shutdownRequested: bool
    exitRequested: bool
    log*: ByteSink
      ## Receives protocol errors (stderr in the command).

proc newTransport*(source: ByteSource, sink: ByteSink): Transport =
  Transport(source: source, sink: sink)

proc stringSource*(data: string): ByteSource =
  ## A source that yields `data` once.
  var delivered = false
  result = proc (chunk: var string): bool =
    if delivered: return false
    delivered = true
    chunk = data
    true

proc fileDescriptorSource*(fd: cint): ByteSource =
  ## A source reading whatever is available on a file descriptor.
  result = proc (chunk: var string): bool =
    chunk.setLen(65536)
    while true:
      let count = posix.read(fd, addr chunk[0], chunk.len)
      if count < 0 and errno == EINTR: continue
      if count <= 0:
        chunk.setLen(0)
        return false
      chunk.setLen(count)
      return true

proc fill(t: Transport): bool =
  ## Appends more input to the pending buffer; false at end of input.
  if t.finished: return false
  var chunk: string
  if not t.source(chunk):
    t.finished = true
    return false
  if t.offset > 0:
    t.pending = t.pending[t.offset .. ^1]
    t.offset = 0
  t.pending.add chunk
  true

proc readLine(t: Transport, line: var string): bool =
  ## std::getline: a line without its '\n'; a final unterminated line counts.
  while true:
    let index = t.pending.find('\n', t.offset)
    if index >= 0:
      line = t.pending[t.offset ..< index]
      t.offset = index + 1
      return true
    if not t.fill():
      if t.offset < t.pending.len:
        line = t.pending[t.offset .. ^1]
        t.offset = t.pending.len
        return true
      return false

proc parseLength(value: string, length: var uint64): bool =
  ## std::from_chars for an unsigned 64-bit decimal.
  if value.len == 0: return false
  var parsed: uint64 = 0
  for c in value:
    if c notin {'0' .. '9'}: return false
    let digit = uint64(ord(c) - ord('0'))
    if parsed > (high(uint64) - digit) div 10: return false
    parsed = parsed * 10 + digit
  length = parsed
  true

proc readMessage*(t: Transport, payload: var string): bool =
  ## Reads one JSON payload; false at end of input or on malformed framing.
  payload = ""
  var contentLength: uint64 = 0
  var hasLength = false
  var line: string
  while t.readLine(line):
    if line.len > 0 and line[^1] == '\r': line.setLen(line.len - 1)
    if line.len == 0: break
    const prefix = "content-length:"
    if line.len >= prefix.len and cmpIgnoreCase(line[0 ..< prefix.len], prefix) == 0:
      var begin = prefix.len
      while begin < line.len and line[begin] in {' ', '\t'}: inc begin
      if begin >= line.len: return false
      if not parseLength(line[begin .. ^1], contentLength): return false
      hasLength = true
  if not hasLength: return false
  let length = int(contentLength)
  while t.pending.len - t.offset < length:
    if not t.fill(): return false
  payload = t.pending[t.offset ..< t.offset + length]
  t.offset += length
  true

proc writeMessage*(t: Transport, payload: string) =
  ## Writes one framed JSON payload.
  t.sink("Content-Length: " & $payload.len & "\r\n\r\n" & payload)

proc newServer*(transport: Transport): Server =
  Server(transport: transport, documents: newStore(), log: proc (data: string) = discard)

proc send(s: Server, value: Json) = s.transport.writeMessage(serialize(value))

proc sendResponse(s: Server, id, result: Json) =
  s.send jsonObject(("jsonrpc", jsonString("2.0")), ("id", id), ("result", result))

proc sendError(s: Server, id: Json, code: int64, text: string) =
  s.send jsonObject(("jsonrpc", jsonString("2.0")), ("id", id),
    ("error", jsonObject(("code", jsonInteger(code)), ("message", jsonString(text)))))

proc sendNotification(s: Server, methodName: string, params: Json) =
  s.send jsonObject(("jsonrpc", jsonString("2.0")), ("method", jsonString(methodName)), ("params", params))

proc hexValue(c: char): int =
  case c
  of '0' .. '9': ord(c) - ord('0')
  of 'a' .. 'f': ord(c) - ord('a') + 10
  of 'A' .. 'F': ord(c) - ord('A') + 10
  else: -1

proc percentDecode(text: string): string =
  var i = 0
  while i < text.len:
    if text[i] == '%' and i + 2 < text.len:
      let hi = hexValue(text[i + 1])
      let lo = hexValue(text[i + 2])
      if hi >= 0 and lo >= 0:
        result.add char(hi shl 4 or lo)
        i += 3
        continue
    result.add text[i]
    inc i

proc percentEncodePath(text: string): string =
  const hex = "0123456789ABCDEF"
  for c in text:
    if c in {'0' .. '9', 'a' .. 'z', 'A' .. 'Z', '-', '_', '.', '~', '/', ':'}:
      result.add c
    else:
      result.add '%'
      result.add hex[ord(c) shr 4]
      result.add hex[ord(c) and 15]

proc uriToPath*(uri: string): string =
  ## Converts a file:// URI into a local path.
  if not uri.startsWith("file://"): percentDecode(uri)
  else: percentDecode(uri["file://".len .. ^1])

proc pathToUri*(path: string): string =
  ## Converts a local path into a file:// URI.
  let (absolutePath, ok) = absolute(path)
  "file://" & percentEncodePath(if ok: absolutePath else: path)

proc positionToOffset*(text: string, line, character: int): int =
  ## Converts a zero-based LSP line and UTF-16 character into a byte offset.
  if line < 0 or character < 0: return 0
  var offset = 0
  var current = 0
  while offset < text.len and current < line:
    if text[offset] == '\n': inc current
    inc offset
  if current != line: return text.len
  var units = 0
  while offset < text.len and text[offset] != '\n' and units < character:
    let lead = uint32(ord(text[offset]))
    var count = 1
    var codePoint = lead
    template byteAt(i: int): uint32 = uint32(ord(text[offset + i])) and 0x3F
    if (lead and 0xE0) == 0xC0 and offset + 1 < text.len:
      count = 2
      codePoint = (lead and 0x1F) shl 6 or byteAt(1)
    elif (lead and 0xF0) == 0xE0 and offset + 2 < text.len:
      count = 3
      codePoint = (lead and 0x0F) shl 12 or byteAt(1) shl 6 or byteAt(2)
    elif (lead and 0xF8) == 0xF0 and offset + 3 < text.len:
      count = 4
      codePoint = (lead and 0x07) shl 18 or byteAt(1) shl 12 or byteAt(2) shl 6 or byteAt(3)
    let width = if codePoint > 0xFFFF: 2 else: 1
    if units + width > character: break
    units += width
    offset += count
  offset

proc makePosition(lineOneBased, columnOneBased: int): Json =
  jsonObject(("line", jsonInteger(int64(max(0, lineOneBased - 1)))),
    ("character", jsonInteger(int64(max(0, columnOneBased - 1)))))

proc documentVersion(params: Json): uint64 =
  var version: Json
  if not params.findNested(["textDocument", "version"], version) or not version.isInteger or
      version.asInteger < 0:
    return 0
  uint64(version.asInteger)

proc diagnosticFields(d: Diagnostic, source: string): Json =
  let beginLine = max(1, d.range.first.line)
  let beginColumn = max(1, d.range.first.column)
  let endLine = max(beginLine, d.range.last.line)
  var endColumn = max(1, d.range.last.column)
  # Semantic/link diagnostics do not all carry exact ranges: keep them
  # visible with a one-character fallback range.
  if endLine == beginLine and endColumn <= beginColumn: endColumn = beginColumn + 1
  let severity = case d.severity
    of sevError: 1'i64
    of sevWarning: 2'i64
    of sevInfo: 3'i64
  jsonObject(
    ("range", jsonObject(("start", makePosition(beginLine, beginColumn)), ("end", makePosition(endLine, endColumn)))),
    ("severity", jsonInteger(severity)),
    ("source", jsonString(source)),
    ("message", jsonString(d.message)))

proc publishDiagnostics(s: Server, uri, path: string) =
  var items = jsonArray()
  let document = s.documents.document(path)
  if document != nil:
    var sink: DiagnosticSink
    discard loadDomainFromSource(path, document.text, s.documents.provider, sink,
      LoadOptions(requireTopLevelRoot: false))
    let current = pathKey(path)
    for d in sink.diagnostics:
      let file = if d.filePath.len == 0: path else: d.filePath
      # didOpen/didChange publishes diagnostics of this document only;
      # htn/compile reports every linked file.
      if pathKey(file) != current: continue
      items.add diagnosticFields(d, "htn")
  s.sendNotification("textDocument/publishDiagnostics",
    jsonObject(("uri", jsonString(uri)), ("diagnostics", items)))

proc handleDidOpen(s: Server, params: Json) =
  var uri, text: Json
  if not params.findNested(["textDocument", "uri"], uri) or not params.findNested(["textDocument", "text"], text) or
      not uri.isString or not text.isString:
    return
  let path = uriToPath(uri.asString)
  s.documents.open(path, text.asString, documentVersion(params))
  s.publishDiagnostics(uri.asString, path)

proc handleDidChange(s: Server, params: Json) =
  var uri, changes, text: Json
  if not params.findNested(["textDocument", "uri"], uri) or not uri.isString or
      not params.find("contentChanges", changes) or not changes.isArray or changes.asArray.len == 0:
    return
  # Full document sync: the first change carries the complete text.
  if not changes.asArray[0].find("text", text) or not text.isString: return
  let version = documentVersion(params)
  let path = uriToPath(uri.asString)
  if not s.documents.update(path, text.asString, version):
    s.documents.open(path, text.asString, version)
  s.publishDiagnostics(uri.asString, path)

proc handleDidClose(s: Server, params: Json) =
  var uri: Json
  if not params.findNested(["textDocument", "uri"], uri) or not uri.isString: return
  discard s.documents.close(uriToPath(uri.asString))
  s.sendNotification("textDocument/publishDiagnostics",
    jsonObject(("uri", uri), ("diagnostics", jsonArray())))

proc positionParams(params: Json, uri: var string, line, character: var int): bool =
  ## The document URI and position of a request. The position is truncated
  ## to int like the original's static_cast<int>.
  var uriValue, lineValue, characterValue: Json
  if not params.findNested(["textDocument", "uri"], uriValue) or not uriValue.isString or
      not params.findNested(["position", "line"], lineValue) or not lineValue.isInteger or
      not params.findNested(["position", "character"], characterValue) or not characterValue.isInteger:
    return false
  uri = uriValue.asString
  line = int(cast[int32](lineValue.asInteger))
  character = int(cast[int32](characterValue.asInteger))
  true

proc handleDefinition(s: Server, id, params: Json) =
  var uri: string
  var line, character: int
  if not positionParams(params, uri, line, character):
    s.sendError(id, -32602, "Invalid definition params")
    return
  let path = uriToPath(uri)
  let document = s.documents.document(path)
  if document == nil:
    s.sendResponse(id, jsonNull())
    return
  let offset = positionToOffset(document.text, line, character)
  let model = s.documents.model(path)
  var definition: Definition
  if model != nil and model.definitionAt(offset, definition):
    s.sendResponse(id, jsonObject(
      ("uri", jsonString(pathToUri(definition.filePath))),
      ("range", jsonObject(
        ("start", makePosition(definition.range.first.line, definition.range.first.column)),
        ("end", makePosition(definition.range.last.line, definition.range.last.column))))))
    return
  s.sendResponse(id, jsonNull())

proc handleCompletion(s: Server, id, params: Json) =
  var uri: string
  var line, character: int
  if not positionParams(params, uri, line, character):
    s.sendError(id, -32602, "Invalid completion params")
    return
  let path = uriToPath(uri)
  let document = s.documents.document(path)
  var items = jsonArray()
  if document == nil:
    s.sendResponse(id, items)
    return
  let offset = positionToOffset(document.text, line, character)
  let model = s.documents.model(path)
  if model != nil:
    var added = initHashSet[string]()
    for candidate in model.autocompleteCandidates(offset):
      if added.containsOrIncl(candidate): continue
      items.add jsonObject(("label", jsonString(candidate)), ("kind", jsonInteger(6)),
        ("insertText", jsonString(candidate)))
  s.sendResponse(id, items)

proc handleCompile(s: Server, id, params: Json) =
  var uri: Json
  if not params.findNested(["textDocument", "uri"], uri) or not uri.isString:
    s.sendError(id, -32602, "Invalid compile params")
    return
  let path = uriToPath(uri.asString)
  let document = s.documents.document(path)
  if document == nil:
    s.sendResponse(id, jsonObject(("success", jsonBool(false)),
      ("message", jsonString("The active HTN document is not open in the language server.")),
      ("diagnostics", jsonArray())))
    return
  # Compile exactly what the editor sees: open buffers win over disk.
  var sink: DiagnosticSink
  let (loaded, ok) = loadDomainFromSource(path, document.text, s.documents.provider, sink)
  var items = jsonArray()
  for d in sink.diagnostics:
    var fields = diagnosticFields(d, "htn-compile")
    let file = if d.filePath.len == 0: path else: d.filePath
    fields.set("uri", jsonString(pathToUri(file)))
    items.add fields
  if ok and not sink.hasErrors:
    s.sendResponse(id, jsonObject(("success", jsonBool(true)),
      ("message", jsonString("Compile succeeded: " & loaded.domain.id & " (" & $loaded.sourceFiles.len &
        " linked source file(s))")),
      ("diagnostics", items)))
    return
  s.sendResponse(id, jsonObject(("success", jsonBool(false)),
    ("message", jsonString("Compile failed with " & $sink.errorCount & " error(s).")),
    ("diagnostics", items)))

proc handleRequest(s: Server, id: Json, methodName: string, params: Json, hasParams: bool) =
  case methodName
  of "initialize":
    s.sendResponse(id, jsonObject(
      ("capabilities", jsonObject(
        ("textDocumentSync", jsonInteger(1)),
        ("definitionProvider", jsonBool(true)),
        ("completionProvider", jsonObject(("resolveProvider", jsonBool(false)))))),
      ("serverInfo", jsonObject(("name", jsonString(ServerName)), ("version", jsonString("0.1.0"))))))
  of "textDocument/definition":
    if not hasParams: s.sendError(id, -32602, "Missing definition params")
    else: s.handleDefinition(id, params)
  of "textDocument/completion":
    if not hasParams: s.sendError(id, -32602, "Missing completion params")
    else: s.handleCompletion(id, params)
  of "htn/compile":
    if not hasParams: s.sendError(id, -32602, "Missing compile params")
    else: s.handleCompile(id, params)
  of "shutdown":
    s.shutdownRequested = true
    s.sendResponse(id, jsonNull())
  else:
    s.sendError(id, -32601, "Method not found: " & methodName)

proc handleNotification(s: Server, methodName: string, params: Json, hasParams: bool) =
  case methodName
  of "exit":
    s.exitRequested = true
    return
  of "initialized":
    return
  else: discard
  if not hasParams: return
  case methodName
  of "textDocument/didOpen": s.handleDidOpen(params)
  of "textDocument/didChange": s.handleDidChange(params)
  of "textDocument/didClose": s.handleDidClose(params)
  else: discard

proc handleMessage(s: Server, message: Json) =
  var methodValue, params, id: Json
  if not message.find("method", methodValue) or not methodValue.isString: return
  let hasParams = message.find("params", params)
  if message.find("id", id):
    s.handleRequest(id, methodValue.asString, params, hasParams)
  else:
    s.handleNotification(methodValue.asString, params, hasParams)

proc run*(s: Server): int =
  ## Processes messages until "exit" or end of input. Returns 0 when a
  ## shutdown was requested first, 1 otherwise.
  var payload: string
  while not s.exitRequested and s.transport.readMessage(payload):
    var error: string
    let (message, ok) = parseJson(payload, error)
    if not ok:
      s.log(ServerName & ": invalid JSON-RPC payload: " & error & "\n")
      continue
    s.handleMessage(message)
  if s.shutdownRequested: 0 else: 1
