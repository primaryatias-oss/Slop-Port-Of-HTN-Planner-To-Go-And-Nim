## Token model and the two lexers of the HTN languages: the domain lexer used
## by the compiler and the world-state lexer used by the fact database parser.
## Both share the identifier, number, string and comment rules of the original
## HTNLexerBase.

import std/[math, tables]
import atom

type
  Position* = object
    ## A source location. `line` and `column` are 1-based, `offset` is a
    ## 0-based byte offset.
    offset*: int
    line*: int
    column*: int

  SourceRange* = object
    ## A half-open source range.
    first*: Position
    last*: Position

  TokenType* = enum
    ## Token kinds in the original declaration order.
    ttColon, ttLeftParenthesis, ttRightParenthesis, ttExclamationMark, ttQuestionMark,
    ttHash, ttAmpersand, ttAt,
    ttAssign, ttEqualEqual, ttNotEqual, ttLess, ttLessEqual, ttGreater, ttGreaterEqual,
    ttPlus, ttMinus, ttIncrement, ttDecrement, ttMultiply, ttDivide, ttModulo,
    ttKeywordDomain, ttKeywordTopLevelDomain, ttKeywordBase, ttKeywordOverrides,
    ttKeywordMethod, ttKeywordTopLevelMethod, ttKeywordAxiom, ttKeywordConstants,
    ttKeywordAnd, ttKeywordOr, ttKeywordAlt, ttKeywordNot, ttKeywordCall,
    ttKeywordTrue, ttKeywordFalse,
    ttIdentifier, ttNumber, ttString,
    ttEndOfFile

  Token* = object
    tokenType*: TokenType
    value*: Atom
    range*: SourceRange
    lexeme*: string

  LexContext = object
    text: string
    tokens: seq[Token]
    position: int
    row: int
    column: int
    tokenStart: int
    tokenStartRow: int
    tokenStartColumn: int
    lastError: string
    lastErrorRange: SourceRange

  DomainLexResult* = object
    tokens*: seq[Token]
    ok*: bool
    error*: string
    errorRange*: SourceRange

const
  DefaultPosition* = Position(offset: 0, line: 1, column: 1)
  DefaultRange* = SourceRange(first: DefaultPosition, last: DefaultPosition)

const tokenTypeNames: array[TokenType, string] = [
  "COLON", "LEFT_PARENTHESIS", "RIGHT_PARENTHESIS", "EXCLAMATION_MARK", "QUESTION_MARK", "HASH", "AMPERSAND", "AT",
  "ASSIGN", "EQUAL_EQUAL", "NOT_EQUAL", "LESS", "LESS_EQUAL", "GREATER", "GREATER_EQUAL",
  "PLUS", "MINUS", "INCREMENT", "DECREMENT", "MULTIPLY", "DIVIDE", "MODULO",
  "HTN_DOMAIN", "HTN_TOP_LEVEL_DOMAIN", "HTN_BASE", "HTN_OVERRIDES", "HTN_METHOD", "HTN_TOP_LEVEL_METHOD",
  "HTN_AXIOM", "HTN_CONSTANTS", "AND", "OR", "ALT", "NOT", "CALL", "TRUE", "FALSE",
  "IDENTIFIER", "NUMBER", "STRING", "END_OF_FILE"]

proc `$`*(t: TokenType): string = tokenTypeNames[t]

proc isValidCharacter*(c: char): bool {.inline.} = c != '\0'

proc isLetter*(c: char): bool {.inline.} =
  ## The original rule, including its quirk that every ASCII character at or
  ## above '_' ('`', '{', '|', '}' and '~' too) is a letter. Non-ASCII bytes are
  ## negative chars in the original and never letters.
  (c >= 'a' and c <= 'z') or (c >= 'A' and c <= 'Z') or (c >= '_' and ord(c) < 0x80)

proc isDigit*(c: char): bool {.inline.} = c >= '0' and c <= '9'

proc isAlphanumeric*(c: char): bool {.inline.} = isLetter(c) or isDigit(c)

proc specialCharacterEscape*(c: char): string =
  case c
  of '\t': "\\t"
  of '\n': "\\n"
  else: $c

proc character(c: LexContext, offset = 0): char {.inline.} =
  let p = c.position + offset
  if p < c.text.len: c.text[p] else: '\0'

proc advance(c: var LexContext, newLine = false) =
  if c.position >= c.text.len: return
  inc c.position
  if newLine:
    inc c.row
    c.column = 0
  else:
    inc c.column

proc markTokenStart(c: var LexContext) =
  c.tokenStart = c.position
  c.tokenStartRow = c.row
  c.tokenStartColumn = c.column

proc currentPosition(c: LexContext): Position =
  Position(offset: c.position, line: c.row + 1, column: c.column + 1)

proc setLastError(c: var LexContext, message: string, r: SourceRange) =
  c.lastError = message
  c.lastErrorRange = r

proc addToken(c: var LexContext, value: Atom, t: TokenType, lexeme: string) =
  var r: SourceRange
  r.first = Position(offset: c.tokenStart, line: c.tokenStartRow + 1, column: c.tokenStartColumn + 1)
  let finish = max(c.position, c.tokenStart + 1)
  r.last = Position(offset: finish, line: c.row + 1, column: c.column + 1)
  if r.last.offset <= r.first.offset:
    r.last = Position(offset: r.first.offset + 1, line: r.first.line, column: r.first.column + 1)
  if t == ttEndOfFile:
    r.last = r.first
  c.tokens.add Token(tokenType: t, value: value, range: r, lexeme: lexeme)

proc lexIdentifier(c: var LexContext, keywords: Table[string, TokenType]) =
  let start = c.position
  c.advance()
  while isAlphanumeric(c.character()): c.advance()
  let lexeme = c.text[start ..< c.position]
  let t = keywords.getOrDefault(lexeme, ttIdentifier)
  case t
  of ttKeywordTrue: c.addToken(newBool(true), t, lexeme)
  of ttKeywordFalse: c.addToken(newBool(false), t, lexeme)
  else: c.addToken(newString(lexeme), t, lexeme)

proc cStrtof(text: cstring, finish: ptr cstring): cfloat {.importc: "strtof", header: "<stdlib.h>".}

proc parseFloatLiteral*(lexeme: string): (float32, bool) =
  ## Parses a decimal literal like std::from_chars<float>: correctly rounded,
  ## failing on overflow and on non-zero literals that underflow to zero.
  let value = float32(cStrtof(cstring(lexeme), nil))
  if classify(value) in {fcInf, fcNegInf, fcNan}: return (0'f32, false)
  if value == 0'f32:
    for ch in lexeme:
      if ch in {'1' .. '9'}: return (0'f32, false)
  (value, true)

proc parseIntLiteral*(lexeme: string): (int32, bool) =
  ## Parses a non-negative decimal int32 like std::from_chars<int32_t>.
  var value = 0'i64
  for ch in lexeme:
    value = value * 10 + int64(ord(ch) - ord('0'))
    if value > int64(high(int32)): return (0'i32, false)
  (int32(value), true)

proc lexNumber(c: var LexContext): bool =
  let start = c.position
  let first = Position(offset: start, line: c.row + 1, column: c.column + 1)
  c.advance()
  while isDigit(c.character()): c.advance()
  let isFloat = c.character() == '.' and isDigit(c.character(1))
  if isFloat:
    c.advance()
    while isDigit(c.character()): c.advance()
  let lexeme = c.text[start ..< c.position]
  var value: Atom
  var ok: bool
  if isFloat:
    let (f, parsed) = parseFloatLiteral(lexeme)
    ok = parsed
    if ok: value = newFloat(f)
  else:
    let (i, parsed) = parseIntLiteral(lexeme)
    ok = parsed
    if ok: value = newInt(i)
  if not ok:
    c.setLastError("Number out of bounds", SourceRange(first: first, last: c.currentPosition()))
    return false
  c.addToken(value, ttNumber, lexeme)
  true

proc lexString(c: var LexContext): bool =
  let start = c.position
  c.advance()
  var ch = c.character()
  while ch != '"' and c.position < c.text.len:
    c.advance(ch == '\n')
    ch = c.character()
  if c.character() != '"':
    let p = c.currentPosition()
    c.setLastError("Character '\"' could not be found at end of the string", SourceRange(first: p, last: p))
    return false
  let finish = c.position
  c.advance()
  let lexeme = c.text[start + 1 ..< finish]
  c.addToken(newString(lexeme), ttString, lexeme)
  true

proc lexComment(c: var LexContext) =
  var ch = c.character()
  while ch != '\n' and c.position < c.text.len:
    c.advance()
    ch = c.character()
  if c.character() == '\n':
    c.advance(true)

let worldStateKeywords = {"call": ttKeywordCall, "true": ttKeywordTrue, "false": ttKeywordFalse}.toTable

let domainKeywords = {
  "domain": ttKeywordDomain, "top_level_domain": ttKeywordTopLevelDomain,
  "base": ttKeywordBase, "overrides": ttKeywordOverrides,
  "method": ttKeywordMethod, "top_level_method": ttKeywordTopLevelMethod,
  "axiom": ttKeywordAxiom, "constants": ttKeywordConstants,
  "and": ttKeywordAnd, "or": ttKeywordOr, "alt": ttKeywordAlt, "not": ttKeywordNot,
  "call": ttKeywordCall, "true": ttKeywordTrue, "false": ttKeywordFalse}.toTable

proc lexWorldState*(text: string): (seq[Token], bool) =
  ## Tokenizes world-state text. Returns false when any lexical error occurred;
  ## tokens up to the end of the text are still produced.
  var c = LexContext(text: text)
  var result0 = true
  var ch = c.character()
  while isValidCharacter(ch):
    c.markTokenStart()
    case ch
    of '(':
      c.addToken(Atom(), ttLeftParenthesis, "(")
      c.advance()
    of ')':
      c.addToken(Atom(), ttRightParenthesis, ")")
      c.advance()
    of '/':
      if c.character(1) == '/':
        c.lexComment()
      else:
        result0 = false
        c.advance()
    of '"':
      result0 = c.lexString() and result0
    of '\r', ' ':
      c.advance()
    of '\n':
      c.advance(true)
    else:
      if isDigit(ch):
        result0 = c.lexNumber() and result0
      elif isLetter(ch):
        c.lexIdentifier(worldStateKeywords)
      else:
        result0 = false
        c.advance()
    ch = c.character()
  c.markTokenStart()
  c.addToken(Atom(), ttEndOfFile, "")
  (c.tokens, result0)

proc lexDomain*(text: string): DomainLexResult =
  ## Tokenizes domain text with the compiler lexer rules.
  var c = LexContext(text: text)
  var ok = true
  template single(t: TokenType, lexeme: string) =
    c.addToken(Atom(), t, lexeme)
    c.advance()
  template double(t: TokenType, lexeme: string) =
    c.addToken(Atom(), t, lexeme)
    c.advance()
    c.advance()
  var ch = c.character()
  while isValidCharacter(ch):
    c.markTokenStart()
    case ch
    of ':': single(ttColon, ":")
    of '(': single(ttLeftParenthesis, "(")
    of ')': single(ttRightParenthesis, ")")
    of '!':
      if c.character(1) == '=': double(ttNotEqual, "!=")
      else: single(ttExclamationMark, "!")
    of '=':
      if c.character(1) == '=': double(ttEqualEqual, "==")
      else: single(ttAssign, "=")
    of '<':
      if c.character(1) == '=': double(ttLessEqual, "<=")
      else: single(ttLess, "<")
    of '>':
      if c.character(1) == '=': double(ttGreaterEqual, ">=")
      else: single(ttGreater, ">")
    of '+':
      if c.character(1) == '+': double(ttIncrement, "++")
      else: single(ttPlus, "+")
    of '-':
      if c.character(1) == '-': double(ttDecrement, "--")
      else: single(ttMinus, "-")
    of '*': single(ttMultiply, "*")
    of '%': single(ttModulo, "%")
    of '?': single(ttQuestionMark, "?")
    of '#': single(ttHash, "#")
    of '&': single(ttAmpersand, "&")
    of '@': single(ttAt, "@")
    of '/':
      if c.character(1) == '/': c.lexComment()
      else: single(ttDivide, "/")
    of '"':
      ok = c.lexString() and ok
    of '\r', ' ', '\t':
      c.advance()
    of '\n':
      c.advance(true)
    else:
      if isDigit(ch):
        ok = c.lexNumber() and ok
      elif isLetter(ch):
        c.lexIdentifier(domainKeywords)
      else:
        let p = c.currentPosition()
        c.setLastError("Character [" & specialCharacterEscape(ch) & "] not recognized",
          SourceRange(first: p, last: Position(offset: p.offset + 1, line: p.line, column: p.column + 1)))
        ok = false
        c.advance()
    ch = c.character()
  c.markTokenStart()
  c.addToken(Atom(), ttEndOfFile, "")
  DomainLexResult(tokens: c.tokens, ok: ok, error: c.lastError, errorRange: c.lastErrorRange)
