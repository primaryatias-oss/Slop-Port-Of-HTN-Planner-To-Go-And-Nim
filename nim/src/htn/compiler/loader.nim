## Domain loading: include traversal, linking into one effective domain and
## validation (HTNCompilerDomainLoader).

import std/[os, strutils, tables]
import ../[lexer, sourcefile]
import ast, diagnostics, filesyntax, parser, validator

type
  SourceProvider* = proc (path: string, text: var string): bool {.closure.}
    ## Supplies source text for a path (for example unsaved editor buffers).
    ## Returning false falls back to reading the file system.

  LoadOptions* = object
    requireTopLevelRoot*: bool
      ## Demand a top_level_domain root with at least one top_level_method.

  LoadResult* = object
    ## A linked, validated domain.
    domain*: Domain
    linkedSourceText*: string
    sourceFiles*: seq[string]

  LoaderContext = object
    hasRootText: bool
    rootText: string
    provider: SourceProvider
    diagnostics: ptr DiagnosticSink
    stack: seq[string]
    visited: Table[string, bool]
    files: seq[string]
    texts: seq[string]
    linked: string

proc defaultLoadOptions*(): LoadOptions = LoadOptions(requireTopLevelRoot: true)

proc canonicalKey(path: string): string =
  ## Mirrors std::filesystem::weakly_canonical for visit tracking.
  var absolute: string
  try:
    absolute = normalizedPath(absolutePath(path))
  except CatchableError:
    return normalizedPath(path)
  try:
    expandFilename(absolute)
  except CatchableError:
    absolute

proc parentPath(path: string): string =
  ## std::filesystem::path::parent_path for '/' separators.
  let index = path.rfind('/')
  if index < 0: ""
  elif index == 0: "/"
  else: path[0 ..< index]

proc joinIncludePath(including, includePath: string): string =
  ## Resolves an include relative to its including file without normalizing
  ## the result (std::filesystem operator/ semantics).
  if includePath.isAbsolute: return includePath
  let parent = parentPath(including)
  if parent.len == 0: includePath
  elif parent.endsWith("/"): parent & includePath
  else: parent & "/" & includePath

proc visit(c: var LoaderContext, path: string, isRoot: bool, includingFile: string,
    includeRange: SourceRange): (string, bool) =
  let key = canonicalKey(path)
  if c.visited.getOrDefault(key): return ("", true)
  for i, entry in c.stack:
    if entry == key:
      var message = "Circular domain dependency: "
      for item in c.stack[i .. ^1]: message.add item & " -> "
      message.add key
      let reportFile = if includingFile.len == 0: path else: includingFile
      c.diagnostics[].error(reportFile, message, recFatal, includeRange)
      return (message, false)
  var text: string
  var found = false
  if isRoot and c.hasRootText:
    text = c.rootText
    found = true
  elif c.provider != nil:
    found = c.provider(path, text)
  if not found and not readSourceFile(path, text):
    let message = "Could not read included domain '" & path & "'"
    let reportFile = if includingFile.len == 0: path else: includingFile
    c.diagnostics[].error(reportFile, message, recFatal, includeRange)
    return (message, false)
  let (includes, domainText, fileError) = splitDomainFile(text)
  if fileError.hasError:
    c.diagnostics[].error(path, fileError.message, recFatal, fileError.range)
    return (fileError.message, false)
  let parsed = parseDomainSyntax(domainText, 0, c.diagnostics, path)
  if not parsed.ok:
    if not c.diagnostics[].hasErrors:
      c.diagnostics[].error(path, parsed.error, recFatal, parsed.errorRange)
    return (parsed.error, false)
  if not isRoot and parsed.domain.isTopLevel:
    let message = "Included domain '" & path & "' cannot be top_level_domain"
    c.diagnostics[].error(path, message, recRecoverable, parsed.domain.range)
    return (message, false)
  c.stack.add key
  for directive in includes:
    let child = joinIncludePath(path, directive.path)
    let (message, ok) = c.visit(child, false, path, directive.range)
    if not ok: return (message, false)
  c.stack.setLen(c.stack.len - 1)
  c.files.add path
  c.texts.add domainText
  c.linked.add "\n// ---- linked source: " & path & " ----\n" & domainText & "\n"
  c.visited[key] = true
  ("", true)

proc buildLinkedDomain(c: var LoaderContext, requireTopLevelRoot: bool): (LoadResult, string, bool) =
  var modules: seq[Domain]
  for index, file in c.files:
    let parsed = parseDomainSyntax(c.texts[index], uint32(index), c.diagnostics, file)
    if not parsed.ok:
      if not c.diagnostics[].hasErrors:
        c.diagnostics[].error(file, parsed.error, recFatal, parsed.errorRange)
      return (LoadResult(), parsed.error, false)
    modules.add parsed.domain
  if modules.len == 0:
    return (LoadResult(), "Compiler domain has no source modules", false)
  if not validateDomainModules(modules, c.files, requireTopLevelRoot, c.diagnostics[]):
    return (LoadResult(), "Compiler syntax validation failed", false)
  # Effective declarations in post-order: later modules replace earlier
  # declarations with the same name/arity.
  var effectiveConstants = initTable[string, Constant]()
  var effectiveAxioms = initTable[string, Axiom]()
  var effectiveMethods = initTable[string, Method]()
  for module in modules:
    for group in module.constantGroups:
      for constant in group.constants: effectiveConstants[constant.id] = constant
    for axiom in module.axioms:
      effectiveAxioms[callableSignature(axiom.id, axiom.parameters.len)] = axiom
    for m in module.methods:
      effectiveMethods[callableSignature(m.id, m.parameters.len)] = m
  let domain = Domain(id: modules[^1].id)
  for module in modules:
    for group in module.constantGroups:
      let effectiveGroup = ConstantGroup()
      effectiveGroup[] = group[]
      effectiveGroup.constants = @[]
      for constant in group.constants:
        if effectiveConstants[constant.id] == constant: effectiveGroup.constants.add constant
      if effectiveGroup.constants.len > 0: domain.constantGroups.add effectiveGroup
    for axiom in module.axioms:
      let qualified = Axiom()
      qualified[] = axiom[]
      qualified.id = module.id & "::" & axiom.id
      domain.axioms.add qualified
    for m in module.methods:
      let qualified = Method()
      qualified[] = m[]
      qualified.id = module.id & "::" & m.id
      qualified.topLevel = false
      domain.methods.add qualified
  for module in modules:
    for axiom in module.axioms:
      if effectiveAxioms[callableSignature(axiom.id, axiom.parameters.len)] == axiom:
        domain.axioms.add axiom
  for module in modules:
    for m in module.methods:
      if effectiveMethods[callableSignature(m.id, m.parameters.len)] == m:
        domain.methods.add m
  (LoadResult(domain: domain, linkedSourceText: c.linked, sourceFiles: c.files), "", true)

proc load(rootPath: string, hasRootText: bool, rootText: string, provider: SourceProvider,
    diagnostics: var DiagnosticSink, options: LoadOptions): (LoadResult, bool) =
  diagnostics.clear()
  var c = LoaderContext(hasRootText: hasRootText, rootText: rootText, provider: provider,
    diagnostics: addr diagnostics, visited: initTable[string, bool]())
  var (message, ok) = c.visit(rootPath, true, "", DefaultRange)
  if ok:
    let (loaded, buildMessage, built) = buildLinkedDomain(c, options.requireTopLevelRoot)
    if built: return (loaded, true)
    message = buildMessage
  if not diagnostics.hasErrors:
    diagnostics.error(rootPath, message, recFatal, DefaultRange)
  (LoadResult(), false)

proc loadDomain*(rootPath: string, diagnostics: var DiagnosticSink,
    options = defaultLoadOptions()): (LoadResult, bool) =
  ## Reads, links and validates the domain rooted at `rootPath`. Diagnostics
  ## are cleared first and receive every reported problem.
  load(rootPath, false, "", nil, diagnostics, options)

proc loadDomainFromSource*(rootPath, rootText: string, provider: SourceProvider,
    diagnostics: var DiagnosticSink, options = defaultLoadOptions()): (LoadResult, bool) =
  ## `loadDomain` with in-memory root text and an optional provider for
  ## included files.
  load(rootPath, true, rootText, provider, diagnostics, options)
