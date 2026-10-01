## The HTN fact database (HTNWorldState): facts are grouped by interned symbol
## and arity, rows are appended in insertion order and duplicate rows are
## preserved. Includes the domain fact registry and the world-state file
## parser.

import std/tables
import atom, lexer, sourcefile

const
  MaxFactArguments* = 10
    ## Maximum number of arguments of one fact.
  FactArgumentsSize* = MaxFactArguments + 1
    ## Number of arity tables of a fact (0..MaxFactArguments).

type
  FactSlot* = uint32
    ## A compact domain-specific fact index.

  FactTable* = object
    ## Every row of one fact/arity pair.
    rows*: seq[seq[Atom]]

  FactTables* = ref object
    ## The arity tables of one fact symbol.
    tables*: array[FactArgumentsSize, FactTable]

  FactRegistry* = ref object
    ## Maps the fact symbols referenced by one domain to compact slots.
    slots: Table[Symbol, FactSlot]
    symbols: seq[Symbol]

  WorldState* = ref object
    ## The fact database used by generated planners.
    facts: Table[Symbol, FactTables]
    order: seq[Symbol]
    generation: uint64
    registry: FactRegistry

const InvalidFactSlot* = FactSlot(0xFFFFFFFF'u32)

let emptyTables* = FactTables()
  ## Shared by generated planners for facts that are absent from a world
  ## state: every arity has zero rows.

# ---------------------------------------------------------------------------
# Fact tables

proc rowCount*(t: FactTable): int {.inline.} = t.rows.len

proc removeRow*(t: var FactTable, i: int): bool =
  if i < 0 or i >= t.rows.len: return false
  t.rows.delete(i)
  true

proc clear*(t: var FactTable) = t.rows.setLen(0)

proc contains*(t: FactTable, args: openArray[Atom]): bool =
  for row in t.rows:
    var match = true
    for i in 0 ..< args.len:
      if not equal(row[i], args[i]):
        match = false
        break
    if match: return true
  false

# ---------------------------------------------------------------------------
# Fact registry

proc newFactRegistry*(): FactRegistry = FactRegistry(slots: initTable[Symbol, FactSlot]())

proc reset*(r: FactRegistry) =
  r.slots.clear()
  r.symbols.setLen(0)

proc register*(r: FactRegistry, symbol: Symbol): FactSlot =
  ## Returns the slot of `symbol`, registering it when needed.
  if symbol == nil: return InvalidFactSlot
  if symbol in r.slots: return r.slots[symbol]
  result = FactSlot(r.symbols.len)
  r.slots[symbol] = result
  r.symbols.add symbol

proc findSlot*(r: FactRegistry, symbol: Symbol): FactSlot =
  if r == nil or symbol == nil: return InvalidFactSlot
  r.slots.getOrDefault(symbol, InvalidFactSlot)

proc symbol*(r: FactRegistry, slot: FactSlot): Symbol =
  if int(slot) < r.symbols.len: r.symbols[slot] else: nil

proc slotCount*(r: FactRegistry): int = r.symbols.len

# ---------------------------------------------------------------------------
# World state

proc newWorldState*(): WorldState =
  WorldState(facts: initTable[Symbol, FactTables](), generation: 1)

proc setFactRegistry*(w: WorldState, r: FactRegistry) =
  ## Associates the world state with the fact slots of one domain (borrowed).
  w.registry = r

proc factRegistry*(w: WorldState): FactRegistry = w.registry

proc findFactSlot*(w: WorldState, fact: Symbol): FactSlot =
  if w.registry == nil: InvalidFactSlot else: w.registry.findSlot(fact)

proc generation*(w: WorldState): uint64 {.inline.} =
  ## Changes whenever a new fact entry is created. Generated execution storage
  ## uses it to refresh cached fact tables.
  w.generation

proc findOrCreate(w: WorldState, fact: Symbol): FactTables =
  result = w.facts.getOrDefault(fact)
  if result == nil:
    result = FactTables()
    w.facts[fact] = result
    w.order.add fact
    inc w.generation
    if w.generation == 0: w.generation = 1

proc addFactSymbol*(w: WorldState, fact: Symbol, args: openArray[Atom]): bool =
  ## Appends one row for the fact symbol.
  if fact == nil or args.len > MaxFactArguments: return false
  w.findOrCreate(fact).tables[args.len].rows.add @args
  true

proc addFact*(w: WorldState, fact: string, args: varargs[Atom]): bool {.discardable.} =
  ## Appends one row for the named fact (the arity is the argument count). It
  ## does not consult the fact registry, like HTNWorldState::AddFact.
  w.addFactSymbol(intern(fact), args)

proc writeFact*(w: WorldState, fact: Symbol, args: varargs[Atom]): bool =
  ## Appends one row after checking that the fact belongs to the associated
  ## registry and that every argument is bound.
  if fact == nil or w.findFactSlot(fact) == InvalidFactSlot or args.len > MaxFactArguments:
    return false
  for arg in args:
    if not arg.isBound: return false
  w.findOrCreate(fact).tables[args.len].rows.add @args
  true

proc clearFact*(w: WorldState, fact: Symbol, arity: int): bool =
  ## Removes every row of one registered fact/arity.
  if fact == nil or w.findFactSlot(fact) == InvalidFactSlot or arity < 0 or arity >= FactArgumentsSize:
    return false
  let tables = w.facts.getOrDefault(fact)
  if tables != nil: tables.tables[arity].clear()
  true

proc removeFact*(w: WorldState, fact: string, arity: int, index: int) =
  ## Removes one row. Removing an absent fact is a no-op.
  let tables = w.facts.getOrDefault(intern(fact))
  if tables == nil or arity < 0 or arity >= FactArgumentsSize: return
  discard tables.tables[arity].removeRow(index)

proc removeAllFacts*(w: WorldState) =
  ## Clears every row while keeping the fact tables allocated.
  for tables in w.facts.values:
    for i in 0 ..< FactArgumentsSize: tables.tables[i].clear()

proc findTables*(w: WorldState, fact: Symbol): FactTables =
  ## The arity tables of a fact, or nil when absent.
  w.facts.getOrDefault(fact)

proc resolveGeneratedTables*(w: WorldState, fact: Symbol): FactTables =
  ## The fact's tables, or the shared empty tables when the fact is absent.
  result = w.facts.getOrDefault(fact)
  if result == nil: result = emptyTables

proc rowCount*(w: WorldState, fact: string, arity: int): int =
  let tables = w.facts.getOrDefault(intern(fact))
  if tables == nil or arity < 0 or arity >= FactArgumentsSize: 0
  else: tables.tables[arity].rowCount

proc containsFact*(w: WorldState, fact: string, args: varargs[Atom]): bool =
  let tables = w.facts.getOrDefault(intern(fact))
  tables != nil and args.len < FactArgumentsSize and tables.tables[args.len].contains(args)

proc facts*(w: WorldState): seq[Symbol] = w.order
  ## The fact symbols in creation order.

proc tableCount*(w: WorldState, fact: string): int =
  ## The number of non-empty arity tables of a fact.
  let tables = w.facts.getOrDefault(intern(fact))
  if tables == nil: return 0
  for i in 0 ..< FactArgumentsSize:
    if tables.tables[i].rowCount > 0: inc result

# ---------------------------------------------------------------------------
# World-state files (one fact per line):
#
#   <fact>     ::= <identifier> <argument>* <end-of-line>
#   <argument> ::= '(' <argument>+ ')' | 'true' | 'false' | number | string | identifier-as-symbol

type Parser = object
  tokens: seq[Token]
  position: int

proc accept(p: var Parser, t: TokenType): int =
  ## The index of the accepted token, or -1.
  if p.position < p.tokens.len and p.tokens[p.position].tokenType == t:
    inc p.position
    return p.position - 1
  -1

proc parseArgument(p: var Parser): (Atom, bool) =
  let start = p.position
  if p.accept(ttLeftParenthesis) >= 0:
    var elems: seq[Atom]
    while true:
      let (element, ok) = p.parseArgument()
      if not ok: break
      elems.add element
    if elems.len == 0 or p.accept(ttRightParenthesis) < 0:
      p.position = start
      return (Atom(), false)
    return (newListOwned(elems), true)
  for t in [ttKeywordTrue, ttKeywordFalse, ttNumber, ttString]:
    let index = p.accept(t)
    if index >= 0: return (p.tokens[index].value, true)
  let identifier = p.accept(ttIdentifier)
  if identifier >= 0:
    return (newSymbolText(p.tokens[identifier].value.strValue), true)
  p.position = start
  (Atom(), false)

proc parseFact(p: var Parser, w: WorldState): bool =
  let start = p.position
  let identifier = p.accept(ttIdentifier)
  if identifier < 0:
    p.position = start
    return false
  # Facts are line-delimited: bare identifiers are valid symbol arguments, so
  # the line boundary separates consecutive facts.
  let line = p.tokens[identifier].range.first.line
  var args: seq[Atom]
  while p.position < p.tokens.len:
    let token = p.tokens[p.position]
    if token.tokenType == ttEndOfFile or token.range.first.line != line: break
    let (argument, ok) = p.parseArgument()
    if not ok: break
    args.add argument
  if args.len > MaxFactArguments: return false
  w.addFact(p.tokens[identifier].value.strValue, args)
  true

proc parseText*(w: WorldState, text: string): bool =
  ## Parses world-state text into `w`, appending rows. Returns false on lexical
  ## or syntax errors; facts parsed before the error are kept.
  let (tokens, ok) = lexWorldState(text)
  if not ok: return false
  var p = Parser(tokens: tokens)
  while p.accept(ttEndOfFile) < 0:
    if not p.parseFact(w): return false
  true

proc parseFile*(w: WorldState, path: string): bool =
  ## Reads and parses a world-state file into `w`.
  var text: string
  if not readSourceFile(path, text): return false
  w.parseText(text)
