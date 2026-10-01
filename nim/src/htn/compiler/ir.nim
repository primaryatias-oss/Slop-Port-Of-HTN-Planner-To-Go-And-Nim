## Compiler IR: the lowered representation of a linked domain consumed by the
## code generator (HTNCompilerIR).

import std/tables
import ../[atom, lexer]
import ast

const
  NoIndex* = 0xFFFFFFFF'u32
    ## Marks an absent IR reference.
  MaxVariableSlots* = 256
    ## Maximum number of distinct variables of a domain.
  VariableSlotMaskWords* = (MaxVariableSlots + 63) div 64

  # Built-in comparison operators.
  CompareEqual* = 0'u32
  CompareNotEqual* = 1'u32
  CompareLess* = 2'u32
  CompareLessEqual* = 3'u32
  CompareGreater* = 4'u32
  CompareGreaterEqual* = 5'u32

  # Built-in list split operations.
  ListSplit* = 0'u32
  ListSplitFront* = 1'u32
  ListSplitBack* = 2'u32

type
  SlotMask* = array[VariableSlotMaskWords, uint64]
    ## A set of variable slots.

  IRConditionKind* = enum
    ## Numeric values of HTNGeneratedConditionKind.
    irFact = 0, irAxiom = 1, irAnd = 2, irOr = 3, irAlt = 4, irNot = 5, irCall = 6, irCallBind = 7,
    irComparison = 8, irListSplit = 9, irAssignment = 10

  IRTaskKind* = enum
    ## Numeric values of HTNGeneratedTaskKind.
    irTaskCompound = 0, irTaskPrimitive = 1, irTaskDeferred = 2

  SourceLocation* = object
    fileIndex*: uint32
    range*: SourceRange

  StringTable* = object
    values*: seq[string]
    indices: Table[string, uint32]

  IRValue* = object
    ## One lowered value. `literal` holds the static value of literals.
    kind*: ValueKind
    text*: uint32
    debugText*: uint32
    sourceLine*: uint32
    variableSlot*: uint32
    staticValueIndex*: uint32
    arithmeticExpression*: uint32
    source*: SourceLocation
    atomType*: AtomKind
    literal*: Atom
    debugAsVariable*: bool

  IRArithmeticExpression* = object
    operator*: ArithmeticOperator
    operands*: seq[IRValue]

  IRStaticValue* = object
    ## A compile-time constant atom.
    text*: uint32
    atomType*: AtomKind
    literal*: Atom

  IRCondition* = object
    assignmentGuardValue*: uint32
      ## Checks an assignment destination before any lowered initializer call.
    kind*: IRConditionKind
    id*: uint32
    firstArgument*: uint32
    argumentCount*: uint32
    firstChildRef*: uint32
    childCount*: uint32
    outputValue*: uint32
    resolvedIndex*: uint32
    sourceLine*: uint32
    domainExpression*: string
    source*: SourceLocation
    debugExpression*: string
    debugSource*: SourceLocation
    debugCondition*: uint32
    debugInternal*: bool

  IRTask* = object
    kind*: IRTaskKind
    id*: uint32
    planStepHead*: uint32
      ## String id of the prefixed plan-step head (NoIndex for compound tasks).
    firstArgument*: uint32
    argumentCount*: uint32
    sourceLine*: uint32
    domainExpression*: string
    source*: SourceLocation

  IRTaskCallExpression* = object
    ## A nested (call ...) evaluated before its task.
    id*: uint32
    callTermSlot*: uint32
    outputSlot*: uint32
    sourceLine*: uint32
    domainExpression*: string
    arguments*: seq[IRValue]
    source*: SourceLocation

  IRBranch* = object
    id*, condition*, firstTask*, taskCount*, sourceLine*: uint32
    source*: SourceLocation

  IRMethod* = object
    id*, firstParameter*, parameterCount*, firstBranch*, branchCount*: uint32
    isTopLevel*, isExternallyDecomposable*: bool
    sourceLine*: uint32
    variableSlotMask*: SlotMask
    source*: SourceLocation

  IRAxiom* = object
    id*, firstParameter*, parameterCount*, condition*, sourceLine*: uint32
    variableSlotMask*: SlotMask
    source*: SourceLocation

  IRConstant* = object
    groupID*, id*, value*, sourceLine*: uint32
    source*: SourceLocation

  IR* = ref object
    ## The complete lowered representation of a linked domain.
    domainID*: string
    sourceFiles*: seq[string]
    runtimeBacktrackingSupport*: bool
    strings*: StringTable
    values*: seq[IRValue]
    arithmeticExpressions*: seq[IRArithmeticExpression]
    staticValues*: seq[IRStaticValue]
    variableStringIDs*: seq[uint32]
    debugInternalVariableStringIDs*: Table[uint32, bool]
    variableSlotByStringID*: Table[uint32, uint32]
    preparedSymbols*: seq[uint32]
    preparedSymbolSlot*: Table[uint32, uint32]
    conditions*: seq[IRCondition]
    factStringIDs*: seq[uint32]
    factSlotByStringID*: Table[uint32, uint32]
    callTermStringIDs*: seq[uint32]
    callTermSlotByStringID*: Table[uint32, uint32]
    conditionChildRefs*: seq[uint32]
    tasks*: seq[IRTask]
    taskCallExpressions*: seq[seq[IRTaskCallExpression]]
    syntheticTaskCallCount*: uint32
    branches*: seq[IRBranch]
    methods*: seq[IRMethod]
    axioms*: seq[IRAxiom]
    constants*: seq[IRConstant]
    error*: string

proc has*(m: SlotMask, slot: uint32): bool =
  if slot >= MaxVariableSlots: false
  else: (m[slot shr 6] and (1'u64 shl (slot and 63))) != 0

proc add*(m: var SlotMask, slot: uint32) =
  ## Inserts `slot` (slots beyond the maximum are ignored; the builder reports
  ## that overflow separately).
  if slot == NoIndex or slot >= MaxVariableSlots: return
  m[slot shr 6] = m[slot shr 6] or (1'u64 shl (slot and 63))

proc slots*(m: SlotMask): seq[uint32] =
  ## The slots in ascending order.
  for word in 0'u32 ..< VariableSlotMaskWords:
    var bits = m[word]
    var bit = 0'u32
    while bits != 0:
      if (bits and 1) != 0: result.add word * 64 + bit
      bits = bits shr 1
      inc bit

proc count*(m: SlotMask): int = m.slots.len

proc add*(t: var StringTable, value: string): uint32 =
  ## Interns `value` and returns its index.
  if value in t.indices: return t.indices[value]
  result = uint32(t.values.len)
  t.values.add value
  t.indices[value] = result

proc find*(t: StringTable, value: string): uint32 = t.indices.getOrDefault(value, NoIndex)

proc get*(t: StringTable, index: uint32): string =
  if int(index) < t.values.len: t.values[index] else: ""

proc newIRValue*(): IRValue =
  IRValue(variableSlot: NoIndex, staticValueIndex: NoIndex, arithmeticExpression: NoIndex, debugAsVariable: true)

proc setError*(ir: IR, message: string) =
  ## Records the first error.
  if ir.error.len == 0: ir.error = message

proc hasError*(ir: IR): bool = ir.error.len > 0

proc findMethod*(ir: IR, stringID, argumentCount: uint32): int =
  ## The first method with the given name string id and arity, or -1.
  for i, m in ir.methods:
    if m.id == stringID and m.parameterCount == argumentCount: return i
  -1

proc findAxiom*(ir: IR, stringID, argumentCount: uint32): int =
  ## The first axiom with the given name string id and arity, or -1.
  for i, a in ir.axioms:
    if a.id == stringID and a.parameterCount == argumentCount: return i
  -1

proc childConditions*(ir: IR, c: IRCondition): seq[uint32] =
  ir.conditionChildRefs[c.firstChildRef ..< c.firstChildRef + c.childCount]

proc markValueSlots*(ir: IR, mask: var SlotMask, value: IRValue) =
  ## Adds the variable slots used by `value` (including arithmetic operands).
  if value.kind == vkVariable and value.variableSlot != NoIndex:
    mask.add value.variableSlot
  if value.kind == vkArithmetic and int(value.arithmeticExpression) < ir.arithmeticExpressions.len:
    for operand in ir.arithmeticExpressions[value.arithmeticExpression].operands:
      ir.markValueSlots(mask, operand)
