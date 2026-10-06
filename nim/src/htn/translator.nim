## Translation entry point shared by the command-line translator and tools:
## loads, links and validates a domain and writes the generated Nim planner
## (HTNTranslateDomain), plus the htn-translator command line.

import std/[os, strutils]
import compiler/[diagnostics, loader]
import codegen/nimgen

type
  Request* = object
    ## Every translation input.
    domainPath*: string
    outputDirectory*: string
    entryPointName*: string
    moduleName*: string
      ## File name (without extension) of the generated module; defaults to
      ## "<domain stem>_generated".
    backtrackingPolicy*: BacktrackingPolicy
    runtimeBacktrackingSupport*: bool
    backtrackingCapacity*: uint32
    callFrameCapacity*: uint32

  Failure* = enum
    ## The stage that stopped a translation.
    failNone, failInvalidOptions, failDomainLoad, failCodeGeneration

  TranslationResult* = object
    succeeded*: bool
    failure*: Failure
    errorMessage*: string
    diagnostics*: seq[Diagnostic]
    domainID*: string
    linkedSourceFileCount*: int
    outputSourcePath*: string
    source*: string

  OptionStatus* = enum
    ## Outcome of `applyOption`.
    optionApplied, optionUnknown, optionInvalid, optionInvalidWithUsage

proc newRequest*(domainPath, entryPoint: string): Request =
  ## A request with the translator defaults.
  Request(domainPath: domainPath, entryPointName: entryPoint, backtrackingCapacity: 32, callFrameCapacity: 8192)

proc parsePositive(text: string): (uint32, bool) =
  if text.len == 0: return (0'u32, false)
  var value = 0'u64
  for c in text:
    if c notin {'0' .. '9'}: return (0'u32, false)
    value = value * 10 + uint64(ord(c) - ord('0'))
    if value > uint64(high(uint32)): return (0'u32, false)
  if value == 0: return (0'u32, false)
  (uint32(value), true)

proc applyOption*(r: var Request, argument: string): (OptionStatus, string) =
  ## Applies one "--name=value" translator option. Returns the status and,
  ## for invalid values, the error message.
  if argument.startsWith("--backtracking-policy="):
    let value = argument["--backtracking-policy=".len .. ^1]
    case value
    of "fixed-with-overflow": r.backtrackingPolicy = fixedWithOverflow
    of "fixed-capacity": r.backtrackingPolicy = fixedCapacity
    else: return (optionInvalidWithUsage, "unknown backtracking policy '" & value & "'")
  elif argument.startsWith("--call-frame-capacity="):
    let (value, ok) = parsePositive(argument["--call-frame-capacity=".len .. ^1])
    if not ok: return (optionInvalid, "call frame capacity must be a positive integer")
    r.callFrameCapacity = value
  elif argument.startsWith("--backtracking-capacity="):
    let (value, ok) = parsePositive(argument["--backtracking-capacity=".len .. ^1])
    if not ok: return (optionInvalid, "backtracking capacity must be a positive integer")
    r.backtrackingCapacity = value
  elif argument.startsWith("--runtime-backtracking-support="):
    let value = argument["--runtime-backtracking-support=".len .. ^1]
    case value
    of "disabled": r.runtimeBacktrackingSupport = false
    of "enabled": r.runtimeBacktrackingSupport = true
    else: return (optionInvalidWithUsage, "unknown runtime backtracking support mode '" & value & "'")
  elif argument.startsWith("--module-name="):
    r.moduleName = argument["--module-name=".len .. ^1]
  else:
    return (optionUnknown, "")
  (optionApplied, "")

proc portableDomainPath*(path: string): string =
  ## Shortens a path to its "Domains/..." suffix (or file name) so generated
  ## code does not embed machine-specific paths.
  let generic = path.replace('\\', '/')
  let index = generic.find("Domains/")
  if index >= 0: generic[index .. ^1] else: extractFilename(generic)

proc moduleNameFor*(domainPath: string): string =
  ## A Nim module name derived from a domain file path: "<stem>_generated".
  let stem = splitFile(domainPath).name
  var name = ""
  for c in stem:
    if c in {'a' .. 'z', 'A' .. 'Z', '0' .. '9'}: name.add c
    elif name.len > 0 and name[^1] != '_': name.add '_'
  name = name.strip(leading = false, chars = {'_'})
  if name.len == 0 or name[0] in {'0' .. '9'}: name = "domain_" & name
  name.strip(leading = false, chars = {'_'}) & "_generated"

proc generate*(request: Request): TranslationResult =
  ## Loads a domain and produces the generated Nim source without writing it.
  if request.domainPath.len == 0:
    return TranslationResult(failure: failInvalidOptions, errorMessage: "A domain file must be specified.")
  if request.entryPointName.len == 0:
    return TranslationResult(failure: failInvalidOptions, errorMessage: "An entry point name must be specified.")
  if request.callFrameCapacity == 0:
    return TranslationResult(failure: failInvalidOptions, errorMessage: "Call frame capacity must be greater than zero.")
  if request.backtrackingCapacity == 0:
    return TranslationResult(failure: failInvalidOptions, errorMessage: "Backtracking capacity must be greater than zero.")
  var sink: DiagnosticSink
  let (loaded, ok) = loadDomain(request.domainPath, sink)
  result.diagnostics = sink.diagnostics
  if not ok or sink.hasErrors:
    result.failure = failDomainLoad
    result.errorMessage = "Loading/linking failed for '" & request.domainPath & "'."
    return
  var options = defaultOptions()
  options.entryPointName = request.entryPointName
  options.sourceFilePath = portableDomainPath(request.domainPath)
  options.backtrackingPolicy = request.backtrackingPolicy
  options.runtimeBacktrackingSupport = request.runtimeBacktrackingSupport
  options.backtrackingCapacity = request.backtrackingCapacity
  options.callFrameCapacity = request.callFrameCapacity
  for file in loaded.sourceFiles: options.linkedSourceFiles.add portableDomainPath(file)
  let (source, error) = nimgen.generate(loaded.domain, options)
  if error.len > 0:
    result.failure = failCodeGeneration
    result.errorMessage = error
    return
  result.succeeded = true
  result.domainID = loaded.domain.id
  result.linkedSourceFileCount = loaded.sourceFiles.len
  result.source = source

proc translate*(request: Request): TranslationResult =
  ## Loads a domain and writes `<module name>.nim` into the output directory
  ## (the domain's directory when empty).
  result = generate(request)
  if not result.succeeded: return
  var directory = request.outputDirectory
  if directory.len == 0:
    directory = parentDir(request.domainPath)
  let moduleName = if request.moduleName.len > 0: request.moduleName else: moduleNameFor(request.domainPath)
  result.outputSourcePath = if directory.len == 0: moduleName & ".nim" else: directory / (moduleName & ".nim")
  try:
    if directory.len > 0: createDir(directory)
  except CatchableError as e:
    result.succeeded = false
    result.failure = failCodeGeneration
    result.errorMessage = "Could not create output directory: " & e.msg
    return
  try:
    writeFile(result.outputSourcePath, result.source)
  except CatchableError:
    result.succeeded = false
    result.failure = failCodeGeneration
    result.errorMessage = "Could not write output file: " & result.outputSourcePath

const usage = "htn-translator <domain-file> <entry-point> [output-directory] [options]\n" &
  "htn-translator --check <domain-file>\n" &
  "Options:\n" &
  "  --backtracking-policy=fixed-with-overflow|fixed-capacity (default: fixed-with-overflow)\n" &
  "  --backtracking-capacity=<positive integer> (default: 32)\n" &
  "  --call-frame-capacity=<positive integer> (default: 8192)\n" &
  "  --runtime-backtracking-support=disabled|enabled (default: disabled)\n" &
  "  --module-name=<nim module name> (default: <domain stem>_generated)\n" &
  "Example: htn-translator Domains/Test/human.domain CreateHumanHTN Generated\n"

proc formatDiagnostics(diagnostics: seq[Diagnostic], fallback: string): string =
  for d in diagnostics:
    let file = if d.filePath.len == 0: fallback else: d.filePath
    result.add file & "(" & $max(1, d.range.first.line) & "," & $max(1, d.range.first.column) & "): " &
      $d.severity & ": " & d.message & "\n"

proc runCommandLine*(args: seq[string], stdoutText, stderrText: var string): int =
  ## Runs the htn-translator command line and returns its exit code: 0
  ## success, 1 usage error, 4 domain load/validation failure, 5 code
  ## generation or output failure.
  if args.len == 2 and args[0] == "--check":
    var sink: DiagnosticSink
    let (loaded, ok) = loadDomain(args[1], sink)
    stderrText.add formatDiagnostics(sink.diagnostics, args[1])
    if not ok or sink.hasErrors:
      stderrText.add "htn-translator: check failed for '" & args[1] & "' with " & $sink.errorCount & " error(s).\n"
      return 4
    stdoutText.add "htn-translator: '" & args[1] & "' compiled successfully (" & $loaded.sourceFiles.len &
      " linked source file(s)).\n"
    return 0
  if args.len < 2:
    stdoutText.add usage
    return 1
  var request = newRequest(args[0], args[1])
  var outputSpecified = false
  for argument in args[2 .. ^1]:
    if argument.startsWith("-"):
      let (status, message) = request.applyOption(argument)
      case status
      of optionApplied: continue
      of optionUnknown:
        stderrText.add "htn-translator: unknown option '" & argument & "'.\n"
        stdoutText.add usage
      of optionInvalid:
        stderrText.add "htn-translator: " & message & ".\n"
      of optionInvalidWithUsage:
        stderrText.add "htn-translator: " & message & ".\n"
        stdoutText.add usage
      return 1
    if outputSpecified:
      stderrText.add "htn-translator: more than one output directory was specified.\n"
      stdoutText.add usage
      return 1
    request.outputDirectory = argument
    outputSpecified = true
  let translated = translate(request)
  if not translated.succeeded:
    stderrText.add formatDiagnostics(translated.diagnostics, request.domainPath)
    stderrText.add "htn-translator: " & translated.errorMessage & "\n"
    return if translated.failure == failDomainLoad: 4 else: 5
  let policy = if request.backtrackingPolicy == fixedCapacity: "fixed-capacity" else: "fixed-with-overflow"
  let support = if request.runtimeBacktrackingSupport: "enabled" else: "disabled"
  stdoutText.add "Translated domain '" & translated.domainID & "' (" & $translated.linkedSourceFileCount &
    " linked source file(s)).\n"
  stdoutText.add "  Entry point: " & request.entryPointName & "\n"
  stdoutText.add "  Backtracking: " & policy & " (capacity " & $request.backtrackingCapacity & ")\n"
  stdoutText.add "  Call frames: fixed capacity " & $request.callFrameCapacity & "\n"
  stdoutText.add "  Runtime backtracking support: " & support & "\n"
  stdoutText.add "  Output: " & translated.outputSourcePath & "\n"
  0
