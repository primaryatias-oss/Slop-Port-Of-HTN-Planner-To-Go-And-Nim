## Domain syntax parser (HTNCompilerDomainSyntaxParser): reads the token
## stream into nested forms and builds the compiler AST.

import std/strutils
import ../[atom, lexer]
import ast, diagnostics, filesyntax

type
  Form = ref object
    tokenType: TokenType
    value: Atom
    rng: SourceRange
    isList: bool
    items: seq[Form]

  SyntaxParser = object
    errorRange: SourceRange
    err: ParserError
    fileIndex: uint32

  ParseResult* = object
    domain*: Domain
    ok*: bool
    error*: string
    errorRange*: SourceRange
    parseError*: ParserError

proc emptyForm(): Form = Form(tokenType: ttEndOfFile, rng: DefaultRange)

proc invalid(p: var SyntaxParser, code: ParserErrorCode, message: string) =
  if not p.err.hasError:
    p.err = ParserError(code: code, message: message, range: p.errorRange)

proc failed(p: SyntaxParser): bool {.inline.} = p.err.hasError

proc readForm(p: var SyntaxParser, tokens: seq[Token], position: var int): Form =
  if position >= tokens.len:
    p.invalid(errTokenOutOfBounds, "Unexpected end of compiler syntax")
    return emptyForm()
  let token = tokens[position]
  inc position
  p.errorRange = token.range
  result = Form(tokenType: token.tokenType, value: token.value, rng: token.range)
  if result.tokenType == ttRightParenthesis or result.tokenType == ttEndOfFile:
    p.invalid(errUnexpectedToken, "Unexpected compiler syntax token")
    return emptyForm()
  if result.tokenType != ttLeftParenthesis:
    return
  result.isList = true
  while position < tokens.len and tokens[position].tokenType != ttRightParenthesis:
    if tokens[position].tokenType == ttEndOfFile:
      p.invalid(errUnclosedList, "Unclosed compiler syntax list")
      return emptyForm()
    result.items.add p.readForm(tokens, position)
    if p.failed: return emptyForm()
  if position >= tokens.len:
    p.invalid(errUnclosedList, "Unclosed compiler syntax list")
    return emptyForm()
  result.rng.last = tokens[position].range.last
  inc position

proc at(p: var SyntaxParser, items: seq[Form], index: int): Form =
  if index >= items.len:
    p.invalid(errIncompleteSyntax, "Incomplete compiler syntax")
    return emptyForm()
  items[index]

proc isToken(f: Form, t: TokenType): bool {.inline.} = not f.isList and f.tokenType == t

proc name(p: var SyntaxParser, f: Form): string =
  if not isToken(f, ttIdentifier):
    p.invalid(errExpectedIdentifier, "Expected compiler identifier")
    return ""
  f.value.strValue

proc source(p: SyntaxParser, n: var Node, r: SourceRange) =
  n.range = r
  n.fileIndex = p.fileIndex

proc identifier(p: var SyntaxParser, f: Form, name = ""): Value =
  result = Value(kind: vkIdentifier)
  let text = if name.len == 0: p.name(f) else: name
  result.atom = newString(text)
  p.source(Node(result[]), f.rng)

proc literal(p: var SyntaxParser, f: Form): Atom =
  if f.isList:
    if f.items.len == 0:
      p.invalid(errEmptyLiteralList, "Empty literal list")
      return newString("")
    var elems = newSeqOfCap[Atom](f.items.len)
    for item in f.items:
      let element = p.literal(item)
      if p.failed: return newString("")
      elems.add element
    return newListOwned(elems)
  if isToken(f, ttIdentifier):
    return newSymbolText(p.name(f))
  if not isToken(f, ttKeywordTrue) and not isToken(f, ttKeywordFalse) and not isToken(f, ttNumber) and
      not isToken(f, ttString):
    p.invalid(errExpectedLiteral, "Expected compiler literal")
    return newString("")
  f.value

proc arithmeticOperatorOf(f: Form): (ArithmeticOperator, bool) =
  if isToken(f, ttPlus): (opAdd, true)
  elif isToken(f, ttMinus): (opSubtract, true)
  elif isToken(f, ttIncrement): (opIncrement, true)
  elif isToken(f, ttDecrement): (opDecrement, true)
  elif isToken(f, ttMultiply): (opMultiply, true)
  elif isToken(f, ttDivide): (opDivide, true)
  elif isToken(f, ttModulo): (opModulo, true)
  else: (opAdd, false)

proc arithmeticOperatorHead(head: Form): (ArithmeticOperator, bool) =
  if not head.isList or head.items.len == 0: return (opAdd, false)
  arithmeticOperatorOf(head.items[0])

proc argument(p: var SyntaxParser, items: seq[Form], index: var int): Value =
  let head = p.at(items, index)
  inc index
  result = Value()
  p.source(Node(result[]), head.rng)
  let (op, isArithmetic) = arithmeticOperatorHead(head)
  if isToken(head, ttQuestionMark) or isToken(head, ttAt):
    let id = p.at(items, index)
    inc index
    if p.failed: return nil
    result.kind = if isToken(head, ttQuestionMark): vkVariable else: vkConstant
    result.atom = newString(p.name(id))
    result.range.last = id.rng.last
  elif isArithmetic:
    result.kind = vkArithmetic
    result.arithmeticOp = op
    result.atom = Atom()
    result.range = head.rng
    var i = 1
    while i < head.items.len:
      result.arithmeticOperands.add p.argument(head.items, i)
      if p.failed: return nil
    let count = result.arithmeticOperands.len
    let valid = case op
      of opIncrement, opDecrement: count == 1
      of opSubtract: count >= 1
      of opModulo: count == 2
      else: count >= 2
    if not valid:
      p.invalid(errInvalidArithmeticArity, "Invalid arithmetic expression arity")
      return nil
  elif head.isList and head.items.len > 0 and isToken(head.items[0], ttKeywordCall):
    result.kind = vkCall
    result.callID = p.identifier(p.at(head.items, 1))
    if p.failed: return nil
    result.atom = result.callID.atom
    var i = 2
    while i < head.items.len:
      result.callArguments.add p.argument(head.items, i)
      if p.failed: return nil
  else:
    result.kind = vkLiteral
    result.atom = p.literal(head)
  if p.failed: return nil

proc qualifiedIdentifier(p: var SyntaxParser, items: seq[Form], start: int): (Value, int) =
  let first = p.at(items, start)
  var id = p.name(first)
  if p.failed: return (nil, start)
  var next = start + 1
  if next + 2 < items.len and isToken(items[next], ttColon) and isToken(items[next + 1], ttColon):
    id.add "::" & p.name(items[next + 2])
    if p.failed: return (nil, start)
    next += 3
  (p.identifier(first, id), next)

proc condition(p: var SyntaxParser, f: Form): Condition =
  p.errorRange = f.rng
  if not f.isList or f.items.len == 0:
    p.invalid(errExpectedCondition, "Expected compiler condition")
    return nil
  let items = f.items
  result = Condition()
  p.source(Node(result[]), f.rng)
  let first = items[0]
  if isToken(first, ttKeywordAnd) or isToken(first, ttKeywordOr) or isToken(first, ttKeywordAlt):
    result.kind = if isToken(first, ttKeywordAnd): ckAnd elif isToken(first, ttKeywordOr): ckOr else: ckAlt
    result.range.first = first.rng.first
    result.range.last = if items.len > 1: items[^1].rng.last else: first.rng.last
    for i in 1 ..< items.len:
      result.children.add p.condition(items[i])
      if p.failed: return nil
    return
  if isToken(first, ttKeywordNot):
    if items.len != 2:
      p.invalid(errInvalidNotCondition, "Invalid not condition")
      return nil
    result.kind = ckNot
    result.children.add p.condition(p.at(items, 1))
    result.range.last = items[^1].rng.last
    return
  let firstType = first.tokenType
  if firstType in {ttEqualEqual, ttNotEqual, ttLess, ttLessEqual, ttGreater, ttGreaterEqual}:
    result.kind = ckComparison
    result.operator = case firstType
      of ttEqualEqual: 0'u32
      of ttNotEqual: 1'u32
      of ttLess: 2'u32
      of ttLessEqual: 3'u32
      of ttGreater: 4'u32
      else: 5'u32
    result.range.first = first.rng.first
    result.range.last = items[^1].rng.last
    var i = 1
    while i < items.len:
      result.arguments.add p.argument(items, i)
      if p.failed: return nil
    if result.arguments.len != 2:
      p.invalid(errInvalidComparisonArity, "Comparison requires two arguments")
      return nil
    return
  var index = 0
  if isToken(items[index], ttQuestionMark):
    p.invalid(errImplicitAssignment, ImplicitAssignmentDiagnostic)
    return nil
  elif isToken(items[index], ttAssign):
    result.kind = ckAssignment
    inc index
    if index >= items.len or not isToken(items[index], ttQuestionMark):
      p.invalid(errInvalidAssignment, InvalidAssignmentDiagnostic)
      return nil
    result.output = p.argument(items, index)
    if p.failed: return nil
    if index >= items.len:
      p.invalid(errInvalidAssignment, InvalidAssignmentDiagnostic)
      return nil
    result.arguments.add p.argument(items, index)
    if index != items.len:
      p.invalid(errInvalidAssignment, InvalidAssignmentDiagnostic)
  elif isToken(items[index], ttKeywordCall):
    result.kind = ckCall
    result.id = p.identifier(p.at(items, 1))
    var i = 2
    while i < items.len:
      result.arguments.add p.argument(items, i)
      if p.failed: return nil
  else:
    let isAxiom = isToken(items[index], ttHash)
    if isAxiom: inc index
    let (id, next) = p.qualifiedIdentifier(items, index)
    if p.failed: return nil
    result.id = id
    index = next
    if not isAxiom and valueText(result.id).contains("::"):
      p.invalid(errQualifiedFact, "Fact condition cannot be qualified")
    while index < items.len:
      result.arguments.add p.argument(items, index)
      if p.failed: return nil
    let idName = valueText(result.id)
    if not isAxiom and idName in ["split_list", "split_list_front", "split_list_back"]:
      if result.arguments.len != 3:
        p.invalid(errInvalidSplitArity, "split_list requires three arguments")
      result.kind = ckSplit
      result.operator = if idName == "split_list_back": 2'u32 elif idName == "split_list_front": 1'u32 else: 0'u32
      if result.operator == 2 and result.arguments.len == 3:
        swap(result.arguments[1], result.arguments[2])
    elif isAxiom:
      result.kind = ckAxiom
    else:
      result.kind = ckFact
  result.range.last = items[^1].rng.last

proc body(p: var SyntaxParser, f: Form): Condition =
  p.errorRange = f.rng
  if not f.isList:
    p.invalid(errExpectedConditionBody, "Expected compiler condition body")
    return nil
  if f.items.len == 0: return nil
  let first = f.items[0]
  if isToken(first, ttKeywordAnd) or isToken(first, ttKeywordOr) or isToken(first, ttKeywordAlt):
    return p.condition(f)
  p.invalid(errExpectedConditionBody, "Expected and, or or alt condition body")
  nil

proc task(p: var SyntaxParser, f: Form): Task =
  p.errorRange = f.rng
  if not f.isList or f.items.len == 0:
    p.invalid(errExpectedTask, "Expected compiler task")
    return nil
  result = Task()
  p.source(Node(result[]), f.rng)
  var index = 0
  if isToken(f.items[index], ttExclamationMark):
    result.kind = tkPrimitive
    inc index
  elif isToken(f.items[index], ttAmpersand):
    result.kind = tkDeferred
    inc index
  elif isToken(f.items[index], ttHash):
    p.invalid(errAxiomPrefixInTaskList, AxiomPrefixInTaskDiagnostic)
    return nil
  else:
    result.kind = tkCompound
  let (id, next) = p.qualifiedIdentifier(f.items, index)
  if p.failed: return nil
  if result.kind == tkPrimitive and valueText(id).contains("::"):
    p.invalid(errQualifiedPrimitiveTask, "Primitive task cannot be qualified")
  result.id = id
  index = next
  while index < f.items.len:
    result.arguments.add p.argument(f.items, index)
    if p.failed: return nil

proc branch(p: var SyntaxParser, f: Form): Branch =
  p.errorRange = f.rng
  if not f.isList or f.items.len != 3 or not f.items[1].isList or not f.items[2].isList:
    p.invalid(errExpectedBranch, "Expected compiler branch")
    return nil
  result = Branch()
  p.source(Node(result[]), f.rng)
  result.id = p.name(f.items[0])
  result.precondition = p.body(f.items[1])
  if p.failed: return nil
  for item in f.items[2].items:
    result.tasks.add p.task(item)
    if p.failed: return nil

proc declaration(p: var SyntaxParser, f: Form, domain: Domain): bool =
  p.errorRange = f.rng
  if not f.isList or f.items.len < 2 or not isToken(f.items[0], ttColon):
    p.invalid(errExpectedDeclaration, "Expected compiler declaration")
    return false
  let items = f.items
  if isToken(items[1], ttKeywordConstants):
    let group = ConstantGroup()
    p.source(Node(group[]), f.rng)
    var index = 2
    group.id = "unnamed"
    if index < items.len and isToken(items[index], ttIdentifier):
      group.id = p.name(items[index])
      inc index
    if index < items.len and isToken(items[index], ttKeywordBase):
      group.isBase = true
      inc index
    elif index < items.len and isToken(items[index], ttKeywordOverrides):
      group.overridesDomain = p.name(p.at(items, index + 1))
      if p.failed: return false
      index += 2
    while index < items.len:
      let entry = items[index]
      if not entry.isList or entry.items.len != 2:
        p.invalid(errExpectedConstant, "Expected compiler constant")
        return false
      let constant = Constant()
      p.source(Node(constant[]), entry.rng)
      constant.id = p.name(p.at(entry.items, 0))
      if p.failed: return false
      var valueIndex = 1
      constant.value = p.argument(entry.items, valueIndex)
      if p.failed: return false
      if constant.value.kind != vkLiteral or valueIndex != entry.items.len:
        p.invalid(errExpectedConstantLiteral, "Expected constant literal")
        return false
      group.constants.add constant
      inc index
    domain.constantGroups.add group
  elif isToken(items[1], ttKeywordAxiom) or isToken(items[1], ttKeywordMethod):
    let isAxiom = isToken(items[1], ttKeywordAxiom)
    let signature = p.at(items, 2)
    let id = p.name(p.at(signature.items, 0))
    if p.failed: return false
    var parameters: seq[Value]
    var i = 1
    while i < signature.items.len:
      let parameter = p.argument(signature.items, i)
      if p.failed: return false
      if parameter.kind != vkVariable:
        p.invalid(errExpectedParameterVariable, "Expected parameter variable")
        return false
      parameters.add parameter
    var index = 3
    var topLevel, isBase = false
    var overrides = ""
    if index < items.len and isToken(items[index], ttKeywordTopLevelMethod):
      topLevel = true
      inc index
    elif index < items.len and isToken(items[index], ttKeywordBase):
      isBase = true
      inc index
    elif index < items.len and isToken(items[index], ttKeywordOverrides):
      overrides = p.name(p.at(items, index + 1))
      if p.failed: return false
      index += 2
    if isAxiom:
      if topLevel:
        p.invalid(errInvalidAxiomVisibility, "Axiom cannot be a top_level_method")
        return false
      let axiom = Axiom(id: id, isBase: isBase, overridesDomain: overrides, parameters: parameters)
      p.source(Node(axiom[]), f.rng)
      axiom.body = p.body(p.at(items, index))
      if p.failed: return false
      if index + 1 != items.len:
        p.invalid(errUnexpectedAxiomSyntax, "Unexpected axiom syntax")
        return false
      domain.axioms.add axiom
    else:
      let m = Method(id: id, parameters: parameters, topLevel: topLevel, isBase: isBase, overridesDomain: overrides)
      p.source(Node(m[]), f.rng)
      while index < items.len:
        m.branches.add p.branch(items[index])
        if p.failed: return false
        inc index
      domain.methods.add m
  else:
    p.invalid(errUnknownDeclaration, "Unknown compiler declaration")
    return false
  true

proc parseDomainSyntax*(source: string, fileIndex: uint32, diagnostics: ptr DiagnosticSink,
    filePath: string): ParseResult =
  ## Parses domain source text (include directives already masked) into
  ## compiler syntax. Recoverable declaration errors are reported to
  ## `diagnostics` (when non-nil) and parsing continues with the next
  ## declaration; the returned domain is nil on any error.
  var p = SyntaxParser(fileIndex: fileIndex, errorRange: DefaultRange)
  template fail(): untyped =
    ParseResult(ok: false, error: p.err.message, errorRange: p.err.range, parseError: p.err)
  let lexed = lexDomain(source)
  if not lexed.ok:
    let message = if lexed.error.len == 0: "Compiler source lexing failed" else: lexed.error
    p.errorRange = lexed.errorRange
    p.err = ParserError(code: errLexingFailed, message: message, range: lexed.errorRange)
    if diagnostics != nil:
      diagnostics[].error(filePath, message, recFatal, lexed.errorRange)
    return fail()
  let tokens = lexed.tokens
  var position = 0
  let root = p.readForm(tokens, position)
  if p.failed: return fail()
  if position >= tokens.len or tokens[position].tokenType != ttEndOfFile:
    if position < tokens.len: p.errorRange = tokens[position].range
    p.invalid(errTrailingSource, "Unexpected source after compiler domain")
    return fail()
  if not root.isList or root.items.len < 3 or not isToken(root.items[0], ttColon) or
      not isToken(root.items[1], ttKeywordDomain):
    p.invalid(errExpectedDomain, "Expected compiler domain")
    return fail()
  let domain = Domain()
  domain.id = p.name(root.items[2])
  if p.failed: return fail()
  domain.range = root.rng
  domain.fileIndex = fileIndex
  var index = 3
  if index < root.items.len and isToken(root.items[index], ttKeywordTopLevelDomain):
    domain.isTopLevel = true
    inc index
  elif index < root.items.len and isToken(root.items[index], ttKeywordBase):
    domain.isBase = true
    inc index
  result = ParseResult(ok: true)
  while index < root.items.len:
    let declaration = root.items[index]
    p.errorRange = declaration.rng
    if not declaration.isList or declaration.items.len < 2:
      p.invalid(errExpectedDeclaration, "Expected compiler declaration")
    else:
      discard p.declaration(declaration, domain)
    if p.failed:
      if result.ok:
        result.error = p.err.message
        result.parseError = p.err
        result.errorRange = p.err.range
      if diagnostics != nil:
        diagnostics[].error(filePath, p.err.message, recRecoverable, p.err.range)
      result.ok = false
      p.err = ParserError()
    inc index
  if result.ok:
    result.domain = domain
