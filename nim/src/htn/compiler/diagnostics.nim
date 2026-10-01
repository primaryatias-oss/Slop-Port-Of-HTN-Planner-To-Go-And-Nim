## Compiler diagnostics (HTNDiagnostic, HTNDiagnosticSink) and the source text
## index that maps byte offsets to line/column positions.

import std/algorithm
import ../lexer

type
  Severity* = enum
    sevInfo, sevWarning, sevError

  Recovery* = enum
    ## Whether processing could continue after a diagnostic.
    recRecoverable, recDependent, recFatal

  Diagnostic* = object
    severity*: Severity
    recovery*: Recovery
    filePath*: string
    range*: SourceRange
    message*: string

  DiagnosticSink* = object
    ## Collects diagnostics and suppresses exact duplicates.
    diagnostics: seq[Diagnostic]

  SourceText* = object
    ## Maps byte offsets to line/column positions.
    text: string
    lineStarts: seq[int]

proc `$`*(s: Severity): string =
  case s
  of sevInfo: "info"
  of sevWarning: "warning"
  of sevError: "error"

proc clear*(s: var DiagnosticSink) = s.diagnostics.setLen(0)

proc report*(s: var DiagnosticSink, d: Diagnostic) =
  ## Adds a diagnostic unless an identical one is already present.
  for existing in s.diagnostics:
    if existing == d: return
  s.diagnostics.add d

proc error*(s: var DiagnosticSink, filePath, message: string, recovery: Recovery, r: SourceRange) =
  s.report Diagnostic(severity: sevError, recovery: recovery, filePath: filePath, range: r, message: message)

proc warning*(s: var DiagnosticSink, filePath, message: string, r: SourceRange) =
  s.report Diagnostic(severity: sevWarning, recovery: recRecoverable, filePath: filePath, range: r, message: message)

proc hasErrors*(s: DiagnosticSink): bool =
  for d in s.diagnostics:
    if d.severity == sevError: return true
  false

proc hasFatalErrors*(s: DiagnosticSink): bool =
  for d in s.diagnostics:
    if d.severity == sevError and d.recovery == recFatal: return true
  false

proc errorCount*(s: DiagnosticSink): int =
  for d in s.diagnostics:
    if d.severity == sevError: inc result

proc diagnostics*(s: DiagnosticSink): seq[Diagnostic] = s.diagnostics

proc firstError*(s: DiagnosticSink): (Diagnostic, bool) =
  for d in s.diagnostics:
    if d.severity == sevError: return (d, true)
  (Diagnostic(), false)

proc initSourceText*(text: string): SourceText =
  result = SourceText(text: text, lineStarts: @[0])
  for i, c in text:
    if c == '\n': result.lineStarts.add i + 1

proc text*(s: SourceText): string = s.text

proc position*(s: SourceText, offset: int): Position =
  ## Converts a byte offset into a position (clamped to the text size).
  let clamped = max(0, min(offset, s.text.len))
  var line = s.lineStarts.upperBound(clamped) - 1
  if line < 0: line = 0
  Position(offset: clamped, line: line + 1, column: clamped - s.lineStarts[line] + 1)

proc offset*(s: SourceText, line, column: int): int =
  ## Converts a 1-based line/column into a clamped byte offset.
  if s.lineStarts.len == 0: return 0
  let l = max(1, min(line, s.lineStarts.len))
  let start = s.lineStarts[l - 1]
  let finish = if l < s.lineStarts.len: s.lineStarts[l] - 1 else: s.text.len
  min(start + max(column, 1) - 1, finish)
