## HTN value model: the tagged `Atom` value, interned symbols and immutable
## lists (HTNAtom, HtnSymbol and HTNAtomList of the original framework).
##
## The original C representation owns strings and linked lists and deep-copies
## them on assignment. This port gives the same value semantics by making
## string and list payloads immutable, so copying an Atom is O(1) and never
## aliases mutable state.

import std/[hashes, tables]

type
  AtomKind* = enum
    ## Runtime type of an atom. Ordinal values match HTNAtomType.
    akUnbound, akBool, akInt, akFloat, akString, akSymbol, akList

  SymbolObj = object
    text: string
    hash: uint64
    id: uint64

  Symbol* = ptr SymbolObj
    ## An interned symbol. Symbols are compared by identity and live for the
    ## lifetime of the process (they are never freed, so copying a symbol atom
    ## needs no reference counting).

  AtomPayload = ref object
    str: string
    elems: seq[Atom]

  Atom* = object
    ## An HTN value: unbound, bool, int32, float32, string, symbol or list.
    ## The default value is an unbound atom.
    kind: AtomKind
    num: uint32 ## bool (0/1), int32 bits or float32 bits
    sym: Symbol
    payload: AtomPayload

  SplitDirection* = enum
    ## Which element `splitList` extracts.
    splitFront, splitBack

  PlanStepKind* = enum
    planStepInvalid, planStepPrimitiveTask, planStepDeferredCall

const
  VariablePrefix* = '?'
  ConstantPrefix* = '@'
  AxiomCallPrefix* = '#'
  DeferredCallPrefix* = '&'
  PrimitiveTaskPrefix* = '!'
  DeclarationPrefix* = ':'

proc `$`*(k: AtomKind): string =
  case k
  of akUnbound: "unbound"
  of akBool: "bool"
  of akInt: "int32"
  of akFloat: "float"
  of akString: "string"
  of akSymbol: "symbol"
  of akList: "list"

# ---------------------------------------------------------------------------
# Symbols

var
  symbolTable = initTable[string, Symbol]()
  nextSymbolID = 1'u64

proc hashSymbolText(text: string): uint64 =
  result = 14695981039346656037'u64
  for c in text:
    result = result xor uint64(ord(c))
    result = result * 1099511628211'u64

proc intern*(text: string): Symbol =
  ## Returns the unique symbol for `text`, creating it when necessary.
  result = symbolTable.getOrDefault(text)
  if result == nil:
    result = create(SymbolObj)
    result.text = text
    result.hash = hashSymbolText(text)
    result.id = nextSymbolID
    inc nextSymbolID
    symbolTable[text] = result

proc text*(s: Symbol): string {.inline.} =
  ## The symbol text ("" for nil).
  if s == nil: "" else: s.text

proc `$`*(s: Symbol): string = s.text()
proc symbolHash*(s: Symbol): uint64 = s.hash
proc symbolID*(s: Symbol): uint64 = s.id
proc hash*(s: Symbol): Hash = hash(cast[pointer](s))

# ---------------------------------------------------------------------------
# Construction

proc unbound*(): Atom {.inline.} = Atom()

proc newBool*(v: bool): Atom {.inline.} =
  Atom(kind: akBool, num: (if v: 1'u32 else: 0'u32))

proc newInt*(v: int32): Atom {.inline.} = Atom(kind: akInt, num: cast[uint32](v))

proc newFloat*(v: float32): Atom {.inline.} = Atom(kind: akFloat, num: cast[uint32](v))

proc newFloatBits*(bits: uint32): Atom {.inline.} = Atom(kind: akFloat, num: bits)

proc newString*(v: string): Atom = Atom(kind: akString, payload: AtomPayload(str: v))

proc newSymbol*(s: Symbol): Atom {.inline.} =
  ## A symbol atom; a nil symbol produces an unbound atom.
  if s == nil: Atom() else: Atom(kind: akSymbol, sym: s)

proc newSymbolText*(text: string): Atom = newSymbol(intern(text))

proc newListOwned*(elems: sink seq[Atom]): Atom =
  ## A list atom taking ownership of `elems`.
  Atom(kind: akList, payload: AtomPayload(elems: elems))

proc newList*(elems: varargs[Atom]): Atom =
  var copied = newSeq[Atom](elems.len)
  for i, e in elems: copied[i] = e
  newListOwned(copied)

proc emptyList*(): Atom = Atom(kind: akList, payload: AtomPayload())

# ---------------------------------------------------------------------------
# Access

proc kind*(a: Atom): AtomKind {.inline.} = a.kind
proc isBound*(a: Atom): bool {.inline.} = a.kind != akUnbound
proc isKind*(a: Atom, k: AtomKind): bool {.inline.} = a.kind == k

proc boolValue*(a: Atom): bool {.inline.} = a.kind == akBool and a.num != 0

proc intValue*(a: Atom): int32 {.inline.} =
  if a.kind == akInt: cast[int32](a.num) else: 0'i32

proc floatValue*(a: Atom): float32 {.inline.} =
  if a.kind == akFloat: cast[float32](a.num) else: 0'f32

proc floatBits*(a: Atom): uint32 {.inline.} = a.num

proc strValue*(a: Atom): string =
  if a.kind == akString: a.payload.str else: ""

proc symbolValue*(a: Atom): Symbol {.inline.} =
  if a.kind == akSymbol: a.sym else: nil

proc len*(a: Atom): int =
  ## Number of list elements, or -1 when the atom is not a list.
  if a.kind != akList: -1 else: a.payload.elems.len

proc isListEmpty*(a: Atom): bool =
  a.kind != akList or a.payload.elems.len == 0

proc at*(a: Atom, i: int): (Atom, bool) =
  ## List element `i`, or (unbound, false) when out of range or not a list.
  if a.kind != akList or i < 0 or i >= a.payload.elems.len:
    return (Atom(), false)
  (a.payload.elems[i], true)

proc elements*(a: Atom): seq[Atom] =
  ## The list elements (a copy of the element references; empty for non-lists).
  if a.kind != akList: @[] else: a.payload.elems

template forElements*(a: Atom, e, body: untyped) =
  ## Iterates the elements of a list atom without copying the sequence.
  if a.kind == akList:
    for e in a.listElems: body

proc listElems*(a: Atom): lent seq[Atom] {.inline.} = a.payload.elems

proc append*(a: Atom, v: Atom): (Atom, bool) =
  ## A new list with `v` appended. An unbound receiver is treated as an empty
  ## list; any other non-list receiver fails.
  case a.kind
  of akUnbound: (newList(v), true)
  of akList:
    var elems = newSeqOfCap[Atom](a.payload.elems.len + 1)
    for e in a.payload.elems: elems.add e
    elems.add v
    (newListOwned(elems), true)
  else: (a, false)

proc removeAt*(a: Atom, i: int): (Atom, bool) =
  ## A new list without element `i`.
  if a.kind != akList or i < 0 or i >= a.payload.elems.len:
    return (a, false)
  var elems = newSeqOfCap[Atom](a.payload.elems.len - 1)
  for k, e in a.payload.elems:
    if k != i: elems.add e
  (newListOwned(elems), true)

proc splitList*(a: Atom, direction: SplitDirection): tuple[element, remainder: Atom, ok: bool] =
  ## Splits a non-empty list into an element and the remaining list.
  if a.kind != akList or a.payload.elems.len == 0:
    return (Atom(), Atom(), false)
  let elems = a.payload.elems
  if direction == splitFront:
    (elems[0], newListOwned(elems[1 .. ^1]), true)
  else:
    (elems[^1], newListOwned(elems[0 ..< elems.len - 1]), true)

proc equal*(a, b: Atom): bool =
  ## HTNAtom_Equals: kinds must match and payloads compare by value.
  if a.kind != b.kind: return false
  case a.kind
  of akUnbound: true
  of akBool, akInt: a.num == b.num
  of akFloat: cast[float32](a.num) == cast[float32](b.num)
  of akString: a.payload.str == b.payload.str
  of akSymbol: a.sym == b.sym
  of akList:
    if a.payload == b.payload: return true
    if a.payload.elems.len != b.payload.elems.len: return false
    for i in 0 ..< a.payload.elems.len:
      if not equal(a.payload.elems[i], b.payload.elems[i]): return false
    true

proc `==`*(a, b: Atom): bool {.inline.} = equal(a, b)

# ---------------------------------------------------------------------------
# Formatting

proc cSnprintf(buffer: cstring, size: csize_t, format: cstring): cint {.importc: "snprintf",
    header: "<stdio.h>", varargs.}

proc formatFloat*(v: float32): string =
  ## Renders a float like std::fixed << std::setprecision(1) (glibc "%.1f").
  var buffer: array[400, char]
  let n = cSnprintf(cast[cstring](addr buffer[0]), csize_t(buffer.len), "%.1f", float64(v))
  result = newString(n)
  for i in 0 ..< n: result[i] = buffer[i]

proc appendString(b: var string, a: Atom, quote: bool) =
  case a.kind
  of akUnbound: discard
  of akBool: b.add(if a.num != 0: "true" else: "false")
  of akInt: b.add $cast[int32](a.num)
  of akFloat: b.add formatFloat(cast[float32](a.num))
  of akString:
    if quote:
      b.add '"'
      b.add a.payload.str
      b.add '"'
    else:
      b.add a.payload.str
  of akSymbol:
    if a.sym != nil: b.add a.sym.text
  of akList:
    b.add '('
    for i, e in a.payload.elems:
      if i > 0: b.add ' '
      appendString(b, e, quote)
    b.add ')'

proc toString*(a: Atom, quoteStrings: bool): string =
  ## HTNAtomToString: floats with one decimal, optional string quotes, lists
  ## space separated inside parentheses, "" for unbound atoms.
  appendString(result, a, quoteStrings)

proc `$`*(a: Atom): string = toString(a, true)

proc text*(a: Atom): string = toString(a, false)

# ---------------------------------------------------------------------------
# Calls and plan steps: a call is a list (head arg0 ... argN).

proc makeCall*(head: Symbol, args: openArray[Atom]): (Atom, bool) =
  ## Builds (head args...). Fails when head is nil or an argument is unbound.
  if head == nil: return (Atom(), false)
  var elems = newSeqOfCap[Atom](args.len + 1)
  elems.add newSymbol(head)
  for arg in args:
    if not arg.isBound: return (Atom(), false)
    elems.add arg
  (newListOwned(elems), true)

proc isValidCall*(call: Atom): bool =
  if call.kind != akList or call.payload.elems.len < 1: return false
  let head = call.payload.elems[0]
  head.kind == akSymbol and head.sym != nil

proc callHead*(call: Atom): Symbol =
  if not isValidCall(call): nil else: call.payload.elems[0].sym

proc callArgumentCount*(call: Atom): int =
  if not isValidCall(call): 0 else: call.payload.elems.len - 1

proc callArgument*(call: Atom, i: int): (Atom, bool) =
  if not isValidCall(call) or i < 0 or i >= call.payload.elems.len - 1:
    return (Atom(), false)
  (call.payload.elems[i + 1], true)

proc callArguments*(call: Atom): seq[Atom] =
  if not isValidCall(call): @[] else: call.payload.elems[1 .. ^1]

proc isPrimitiveTaskHead*(head: Symbol): bool =
  head != nil and head.text.len > 0 and head.text[0] == PrimitiveTaskPrefix

proc isDeferredCallHead*(head: Symbol): bool =
  head != nil and head.text.len > 0 and head.text[0] == DeferredCallPrefix

proc makePrimitiveTaskHead*(name: string): Symbol =
  if name.len > 0 and name[0] == PrimitiveTaskPrefix: intern(name)
  else: intern(PrimitiveTaskPrefix & name)

proc makeDeferredCallHead*(name: string): Symbol =
  if name.len > 0 and name[0] == DeferredCallPrefix: intern(name)
  else: intern(DeferredCallPrefix & name)

proc getPlanStepKind*(step: Atom): PlanStepKind =
  let head = callHead(step)
  if isPrimitiveTaskHead(head): planStepPrimitiveTask
  elif isDeferredCallHead(head): planStepDeferredCall
  else: planStepInvalid

proc makeCallFromDeferredPlanStep*(step: Atom): (Atom, bool) =
  ## Converts (&name args...) into (name args...).
  if getPlanStepKind(step) != planStepDeferredCall: return (Atom(), false)
  let head = callHead(step)
  var elems = newSeqOfCap[Atom](step.payload.elems.len)
  elems.add newSymbol(intern(head.text[1 .. ^1]))
  for i in 1 ..< step.payload.elems.len: elems.add step.payload.elems[i]
  (newListOwned(elems), true)

proc formatPlanStep*(step: Atom): string =
  ## "head arg1 arg2" with quoted strings (the original test-suite format).
  let head = callHead(step)
  result = if head != nil: head.text else: "<invalid task>"
  if isValidCall(step):
    for i in 1 ..< step.payload.elems.len:
      result.add ' '
      result.add toString(step.payload.elems[i], true)

proc formatPlan*(plan: Atom): seq[string] =
  if plan.kind == akList:
    for step in plan.payload.elems: result.add formatPlanStep(step)
