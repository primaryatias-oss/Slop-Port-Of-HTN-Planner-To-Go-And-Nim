## Editor-facing analysis of HTN domains: the compiler tooling model
## (go-to-definition, completion, token kinds) and the in-memory document
## store used by the language server (HTNCompilerToolingModel and
## HTNDomainDocumentStore).

import std/[algorithm, sets, strutils, tables]
import atom, fspath, lexer, sourcefile
import compiler/[ast, diagnostics, loader]

type
  TokenKind* = enum
    ## Classification of the token under a cursor.
    tokNone, tokVariableValid, tokVariableInvalid, tokConstantValid, tokConstantInvalid,
    tokMethodValid, tokMethodInvalid

  Definition* = object
    ## A go-to-definition target.
    filePath*: string
    range*: SourceRange

  Model* = ref object
    ## The semantic model of one open document.
    filePath: string
    text: string
    result: LoadResult
    diagnostics: seq[Diagnostic]
    loaded: bool

  Document* = ref object
    ## One open editor buffer.
    filePath*: string
    text*: string
    version*: uint64

  CacheEntry = ref object
    model: Model
    generation: uint64
    documentVersion: uint64
    hasModel: bool

  Store* = ref object
    ## Open editor buffers and their cached semantic models. Open buffers win
    ## over the file system when domains are loaded; the store never writes
    ## buffers to disk.
    documents: Table[string, Document]
    cache: Table[string, CacheEntry]
    generation: uint64

proc analyze*(m: Model, filePath, text: string, provider: SourceProvider) =
  ## Loads the document (includes resolved through `provider`) in tooling
  ## mode, which does not require a top-level root domain.
  m.filePath = filePath
  m.text = text
  var sink: DiagnosticSink
  let (loaded, ok) = loadDomainFromSource(filePath, text, provider, sink,
    LoadOptions(requireTopLevelRoot: false))
  m.result = loaded
  m.loaded = ok
  m.diagnostics = sink.diagnostics

proc diagnostics*(m: Model): seq[Diagnostic] = m.diagnostics
proc isLoaded*(m: Model): bool = m.loaded
proc loadResult*(m: Model): LoadResult = m.result

proc contains(r: SourceRange, offset: int): bool =
  offset >= r.first.offset and offset <= r.last.offset

proc isAtomCharacter(c: char): bool =
  c in {'0' .. '9', 'a' .. 'z', 'A' .. 'Z', '_', '-'} or c == VariablePrefix or c == ConstantPrefix or
    c == DeclarationPrefix or c == AxiomCallPrefix or c == DeferredCallPrefix

proc findAtom(text: string, offset: int, first, last: var int): bool =
  ## The identifier-like run around `offset`.
  if text.len == 0: return false
  let clamped = min(offset, text.len)
  var probe = clamped
  if probe == text.len or not isAtomCharacter(text[probe]):
    if probe == 0 or not isAtomCharacter(text[probe - 1]): return false
    dec probe
  first = probe
  while first > 0 and isAtomCharacter(text[first - 1]): dec first
  last = probe + 1
  while last < text.len and isAtomCharacter(text[last]): inc last
  true

proc findAxiomCall(c: Condition, offset: int, id: string): Condition =
  if c == nil or not c.range.contains(offset): return nil
  if c.kind == ckAxiom and valueText(c.id) == id: return c
  for child in c.children:
    let call = findAxiomCall(child, offset, id)
    if call != nil: return call
  nil

proc addVariables(c: Condition, cursor: int, variables: var HashSet[string]) =
  if c == nil or c.range.first.offset >= cursor or c.kind == ckNot: return
  for argument in c.arguments:
    if argument != nil and argument.kind == vkVariable and argument.range.first.offset < cursor:
      variables.incl "?" & valueText(argument)
  if c.output != nil and c.output.range.first.offset < cursor:
    variables.incl "?" & valueText(c.output)
  for child in c.children: addVariables(child, cursor, variables)

proc currentFileIndex(m: Model): int =
  for i, file in m.result.sourceFiles:
    if samePath(file, m.filePath): return i
  -1

proc autocompleteCandidates*(m: Model, cursor: int): seq[string] =
  ## The variables in scope at the cursor of the method branch containing
  ## it, sorted.
  let fileIndex = m.currentFileIndex()
  if fileIndex < 0: return @[]
  var variables = initHashSet[string]()
  if m.result.domain != nil:
    for owner in m.result.domain.methods:
      if owner == nil or owner.fileIndex != uint32(fileIndex) or not owner.range.contains(cursor): continue
      for parameter in owner.parameters: variables.incl "?" & valueText(parameter)
      for branch in owner.branches:
        if branch != nil and branch.range.contains(cursor):
          addVariables(branch.precondition, cursor, variables)
          break
      break
  for variable in variables: result.add variable
  result.sort(system.cmp[string])

proc definitionAt*(m: Model, cursor: int, definition: var Definition): bool =
  ## Resolves the constant, axiom or method referenced at `cursor`.
  var first, last: int
  if not findAtom(m.text, cursor, first, last) or m.result.domain == nil: return false
  let symbol = m.text[first ..< last]
  let lookup = if symbol.len > 0 and symbol[0] in {DeferredCallPrefix, AxiomCallPrefix}: symbol[1 .. ^1]
               else: symbol
  let files = m.result.sourceFiles
  let domain = m.result.domain
  if symbol.len > 0 and symbol[0] == ConstantPrefix:
    let id = symbol[1 .. ^1]
    for group in domain.constantGroups:
      for constant in group.constants:
        if constant.id == id and int(constant.fileIndex) < files.len:
          definition = Definition(filePath: files[constant.fileIndex], range: constant.range)
          return true
    return false
  let fileIndex = m.currentFileIndex()
  var axiomCall: Condition = nil
  for owner in domain.methods:
    if owner == nil or int(owner.fileIndex) != fileIndex: continue
    for branch in owner.branches:
      let call = findAxiomCall(branch.precondition, first, lookup)
      if call != nil: axiomCall = call
  for owner in domain.axioms:
    if owner != nil and int(owner.fileIndex) == fileIndex:
      let call = findAxiomCall(owner.body, first, lookup)
      if call != nil: axiomCall = call
  if axiomCall != nil:
    for axiom in domain.axioms:
      if axiom != nil and axiom.id == lookup and axiom.parameters.len == axiomCall.arguments.len and
          int(axiom.fileIndex) < files.len:
        definition = Definition(filePath: files[axiom.fileIndex], range: axiom.range)
        return true
    return false
  if symbol.len > 0 and symbol[0] == AxiomCallPrefix: return false
  var argumentCount = 0
  var hasCall = false
  for owner in domain.methods:
    if owner == nil or int(owner.fileIndex) != fileIndex: continue
    for branch in owner.branches:
      for task in branch.tasks:
        if task != nil and task.range.contains(first) and valueText(task.id) == lookup:
          hasCall = true
          argumentCount = task.arguments.len
  var match: Method = nil
  for candidate in domain.methods:
    if candidate == nil or int(candidate.fileIndex) >= files.len or candidate.id != lookup: continue
    if hasCall and candidate.parameters.len != argumentCount: continue
    if match != nil: return false  # Without a call, an overloaded name is ambiguous.
    match = candidate
  if match != nil:
    definition = Definition(filePath: files[match.fileIndex], range: match.range)
    return true
  false

proc tokenKindAt*(m: Model, offset: int): TokenKind =
  ## Classifies the token at `offset`.
  var first, last: int
  if not findAtom(m.text, offset, first, last): return tokNone
  let symbol = m.text[first ..< last]
  var definition: Definition
  if symbol.len > 0 and symbol[0] == VariablePrefix:
    return if symbol in m.autocompleteCandidates(first): tokVariableValid else: tokVariableInvalid
  if symbol.len > 0 and symbol[0] == ConstantPrefix:
    return if m.definitionAt(first, definition): tokConstantValid else: tokConstantInvalid
  if m.definitionAt(first, definition): return tokMethodValid
  if "::" in symbol: return tokMethodInvalid
  tokNone

proc newStore*(): Store =
  Store(documents: initTable[string, Document](), cache: initTable[string, CacheEntry](), generation: 1)

proc invalidate(s: Store) =
  inc s.generation
  if s.generation == 0:
    s.generation = 1
    s.cache.clear()

proc open*(s: Store, path, text: string, version: uint64) =
  ## Opens (or replaces) a document.
  let key = pathKey(path)
  var document = s.documents.getOrDefault(key)
  if document == nil:
    document = Document()
    s.documents[key] = document
  document.filePath = path
  document.text = text
  document.version = version
  s.invalidate()

proc update*(s: Store, path, text: string, version: uint64): bool =
  ## Replaces the text of an open document; false when it is not open.
  let document = s.documents.getOrDefault(pathKey(path))
  if document == nil: return false
  document.text = text
  document.version = version
  s.invalidate()
  true

proc close*(s: Store, path: string): bool =
  ## Closes a document; false when it is not open.
  let key = pathKey(path)
  if key notin s.documents: return false
  s.documents.del key
  s.cache.del key
  s.invalidate()
  true

proc clear*(s: Store) =
  ## Closes every document.
  if s.documents.len == 0 and s.cache.len == 0: return
  s.documents.clear()
  s.cache.clear()
  s.invalidate()

proc isOpen*(s: Store, path: string): bool = pathKey(path) in s.documents

proc document*(s: Store, path: string): Document =
  ## An open document or nil.
  s.documents.getOrDefault(pathKey(path))

proc generation*(s: Store): uint64 = s.generation

proc read*(s: Store, path: string, text: var string): bool =
  ## Open buffers win; other files are read from disk.
  let document = s.documents.getOrDefault(pathKey(path))
  if document != nil:
    text = document.text
    return true
  readSourceFile(path, text)

proc provider*(s: Store): SourceProvider =
  ## The store as a loader source provider.
  result = proc (path: string, text: var string): bool = s.read(path, text)

proc model*(s: Store, path: string): Model =
  ## The cached semantic model of an open document (nil when it is not
  ## open). A change to any open document invalidates every cached model
  ## because includes make documents depend on each other's unsaved text.
  let key = pathKey(path)
  let document = s.documents.getOrDefault(key)
  if document == nil: return nil
  var entry = s.cache.getOrDefault(key)
  if entry == nil:
    entry = CacheEntry(model: Model())
    s.cache[key] = entry
  if not entry.hasModel or entry.generation != s.generation or entry.documentVersion != document.version:
    entry.model.analyze(document.filePath, document.text, s.provider)
    entry.generation = s.generation
    entry.documentVersion = document.version
    entry.hasModel = true
  entry.model
