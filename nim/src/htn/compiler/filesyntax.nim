## Domain file syntax: parser error codes and the extraction of leading
## `(:include "path")` directives (HTNDomainFileSyntax).

import std/strutils
import ../lexer
import diagnostics

type
  ParserErrorCode* = enum
    ## Syntax error kinds (HTNParserErrorCode).
    errNone, errTokenOutOfBounds, errUnexpectedToken, errAxiomPrefixInTaskList, errUnclosedList,
    errIncompleteSyntax, errExpectedIdentifier, errEmptyLiteralList, errExpectedLiteral,
    errInvalidArithmeticArity, errExpectedCondition, errInvalidNotCondition, errInvalidComparisonArity,
    errInvalidAssignment, errImplicitAssignment, errExpectedBoundCall, errUnexpectedBoundCallSyntax,
    errQualifiedFact, errInvalidSplitArity, errExpectedConditionBody, errExpectedTask,
    errQualifiedPrimitiveTask, errExpectedBranch, errExpectedDeclaration, errExpectedConstant,
    errExpectedConstantLiteral, errExpectedParameterVariable, errInvalidAxiomVisibility,
    errUnexpectedAxiomSyntax, errUnknownDeclaration, errTrailingSource, errExpectedDomain,
    errLexingFailed, errExpectedIncludePath, errUnterminatedIncludePath, errExpectedIncludeEnd,
    errMisplacedInclude

  ParserError* = object
    ## The first syntax error found by a parse.
    code*: ParserErrorCode
    message*: string
    range*: SourceRange

  Include* = object
    ## One `(:include "path")` directive.
    path*: string
    range*: SourceRange

const
  InvalidAssignmentDiagnostic* = "Assignment requires a variable destination and exactly one value expression"
  ImplicitAssignmentDiagnostic* = "Implicit call-result binding is no longer supported; use an explicit assignment condition"
  AxiomPrefixInTaskDiagnostic* = "'#' is reserved for axiom calls; use '&' for deferred method calls"

proc hasError*(e: ParserError): bool = e.code != errNone

proc isCSpace(c: char): bool = c in {' ', '\t', '\n', '\v', '\f', '\r'}

proc isCAlnum(c: char): bool = c in {'0' .. '9', 'a' .. 'z', 'A' .. 'Z'}

proc skipTrivia(text: string, start: int): int =
  var p = start
  while p < text.len:
    if isCSpace(text[p]):
      inc p
    elif text.continuesWith("//", p):
      let finish = text.find('\n', p)
      p = if finish < 0: text.len else: finish + 1
    else:
      break
  p

proc readIncludePrefix(text: string, start: int): (int, bool) =
  if start >= text.len or text[start] != '(': return (0, false)
  var p = skipTrivia(text, start + 1)
  if p >= text.len or text[p] != ':': return (0, false)
  p = skipTrivia(text, p + 1)
  if not text.continuesWith("include", p): return (0, false)
  p += "include".len
  if p < text.len and (isCAlnum(text[p]) or text[p] == '_'): return (0, false)
  (p, true)

proc splitDomainFile*(text: string): tuple[includes: seq[Include], domainText: string, error: ParserError] =
  ## Extracts leading include directives and masks them with spaces so the
  ## remaining domain text keeps its original coordinates.
  var masked = text
  let source = initSourceText(text)
  template fail(errorCode: ParserErrorCode, errorMessage: string, errorFirst, errorLast: int) =
    return (newSeq[Include](), text, ParserError(code: errorCode, message: errorMessage,
      range: SourceRange(first: source.position(errorFirst), last: source.position(errorLast))))
  var includes: seq[Include]
  var cursor = 0
  while true:
    cursor = skipTrivia(text, cursor)
    var (position, ok) = readIncludePrefix(text, cursor)
    if not ok: break
    position = skipTrivia(text, position)
    if position >= text.len or text[position] != '"':
      fail(errExpectedIncludePath, "Expected quoted path after :include", position, min(position + 1, text.len))
    let quote = position
    inc position
    let finish = text.find('"', position)
    if finish < 0:
      fail(errUnterminatedIncludePath, "Unterminated :include path", quote, text.len)
    let path = text[position ..< finish]
    if path.len == 0:
      fail(errExpectedIncludePath, "Expected non-empty :include path", quote, finish + 1)
    position = skipTrivia(text, finish + 1)
    if position >= text.len or text[position] != ')':
      fail(errExpectedIncludeEnd, "Expected ')' after :include path", position, min(position + 1, text.len))
    inc position
    includes.add Include(path: path, range: SourceRange(first: source.position(cursor), last: source.position(position)))
    for i in cursor ..< position:
      if masked[i] != '\n' and masked[i] != '\r': masked[i] = ' '
    cursor = position
  if cursor == text.len:
    fail(errExpectedDomain, "Missing :domain form", cursor, cursor)
  # Ignore quoted text and comments when checking for misplaced directives.
  while cursor < text.len:
    cursor = skipTrivia(text, cursor)
    if cursor == text.len: break
    if text[cursor] == '"':
      let finish = text.find('"', cursor + 1)
      if finish < 0: break
      cursor = finish + 1
      continue
    let (finish, ok) = readIncludePrefix(text, cursor)
    if ok:
      fail(errMisplacedInclude, ":include directives are only allowed before :domain", cursor, finish)
    inc cursor
  (includes, masked, ParserError())
