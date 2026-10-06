## Static analysis used by the code generator: statically bound variables,
## condition writes (checkpoint plans) and compile-time comparisons.

import std/[algorithm, sets, strutils, tables]
import ../atom
import ../compiler/[ast, ir]

type
  BoundSet* = HashSet[uint32]
    ## Variable string ids statically known to be bound.

  ConditionAnalysis* = object
    boundAfter*: BoundSet
    mayProduceMultipleValues*: bool

  CheckpointPlan* = object
    id*: int
    slots*: seq[uint32]

proc isOutputParameterName*(name: string): bool =
  name.startsWith("out_") or name.startsWith("io_")

proc analyzeCondition*(ir: IR, index: uint32, bound: BoundSet): ConditionAnalysis =
  ## The statically guaranteed bindings after a condition and whether a fact
  ## may produce several solutions (AnalyzeCondition).
  result.boundAfter = bound
  if index == NoIndex or int(index) >= ir.conditions.len: return
  let c = ir.conditions[index]
  template bindValue(v: IRValue) =
    if v.kind == vkVariable and v.variableSlot != NoIndex: result.boundAfter.incl v.text
  case c.kind
  of irFact:
    for i in 0'u32 ..< c.argumentCount:
      let v = ir.values[c.firstArgument + i]
      if v.kind == vkVariable and v.variableSlot != NoIndex and v.text notin bound:
        result.mayProduceMultipleValues = true
      bindValue(v)
  of irAxiom:
    let axiomIndex = ir.findAxiom(c.id, c.argumentCount)
    if axiomIndex >= 0:
      let axiom = ir.axioms[axiomIndex]
      for i in 0'u32 ..< axiom.parameterCount:
        if isOutputParameterName(ir.strings.get(ir.values[axiom.firstParameter + i].text)):
          bindValue(ir.values[c.firstArgument + i])
  of irAssignment, irCallBind:
    if c.outputValue != NoIndex: bindValue(ir.values[c.outputValue])
  of irListSplit:
    for i in 1'u32 ..< c.argumentCount: bindValue(ir.values[c.firstArgument + i])
  of irAnd:
    for i in 0'u32 ..< c.childCount:
      result.boundAfter = analyzeCondition(ir, ir.conditionChildRefs[c.firstChildRef + i], result.boundAfter).boundAfter
  of irOr, irAlt:
    # Only bindings produced by every alternative are statically guaranteed.
    for i in 0'u32 ..< c.childCount:
      let child = analyzeCondition(ir, ir.conditionChildRefs[c.firstChildRef + i], bound)
      if i == 0: result.boundAfter = child.boundAfter
      else: result.boundAfter = result.boundAfter * child.boundAfter
  else: discard

proc addWrite(v: IRValue, writes: var OrderedTable[uint32, uint32]) =
  if v.kind == vkVariable and v.variableSlot != NoIndex and v.text notin writes:
    writes[v.text] = v.variableSlot

proc collectConditionWrites*(ir: IR, index: uint32, bound: BoundSet, writes: var OrderedTable[uint32, uint32]) =
  ## The slots a condition may bind (CollectGeneratedConditionWrites).
  if index == NoIndex or int(index) >= ir.conditions.len: return
  let c = ir.conditions[index]
  case c.kind
  of irFact:
    for i in 0'u32 ..< c.argumentCount:
      let v = ir.values[c.firstArgument + i]
      if v.kind == vkVariable and v.variableSlot != NoIndex and v.text notin bound: addWrite(v, writes)
  of irListSplit:
    for i in 1'u32 ..< c.argumentCount:
      let v = ir.values[c.firstArgument + i]
      if v.kind == vkVariable and v.variableSlot != NoIndex and v.text notin bound: addWrite(v, writes)
  of irAssignment, irCallBind:
    if c.outputValue != NoIndex and int(c.outputValue) < ir.values.len:
      let v = ir.values[c.outputValue]
      if v.kind == vkVariable and v.text notin bound: addWrite(v, writes)
  of irAxiom:
    let axiomIndex = ir.findAxiom(c.id, c.argumentCount)
    if axiomIndex < 0: return
    let axiom = ir.axioms[axiomIndex]
    let count = min(axiom.parameterCount, c.argumentCount)
    for i in 0'u32 ..< count:
      let parameter = ir.values[axiom.firstParameter + i]
      if int(parameter.text) >= ir.strings.values.len or not isOutputParameterName(ir.strings.values[parameter.text]):
        continue
      addWrite(ir.values[c.firstArgument + i], writes)
  of irAnd:
    var current = bound
    for i in 0'u32 ..< c.childCount:
      let reference = c.firstChildRef + i
      if int(reference) >= ir.conditionChildRefs.len: break
      let child = ir.conditionChildRefs[reference]
      collectConditionWrites(ir, child, current, writes)
      current = analyzeCondition(ir, child, current).boundAfter
  of irOr, irAlt:
    for i in 0'u32 ..< c.childCount:
      let reference = c.firstChildRef + i
      if int(reference) < ir.conditionChildRefs.len:
        collectConditionWrites(ir, ir.conditionChildRefs[reference], bound, writes)
  of irNot:
    if c.childCount != 0 and int(c.firstChildRef) < ir.conditionChildRefs.len:
      collectConditionWrites(ir, ir.conditionChildRefs[c.firstChildRef], bound, writes)
  else: discard

proc buildCheckpointPlan*(ir: IR, id: int, condition: uint32, bound: BoundSet): CheckpointPlan =
  var writes = initOrderedTable[uint32, uint32]()
  collectConditionWrites(ir, condition, bound, writes)
  result.id = id
  for slot in writes.values: result.slots.add slot
  result.slots.sort()

proc resolveStaticValue*(ir: IR, start: uint32): (IRValue, bool) =
  ## Follows constant aliases (ResolveGeneratedStaticValue).
  var index = start
  for depth in 0 ..< 16:
    if int(index) >= ir.values.len: return (IRValue(), false)
    let v = ir.values[index]
    if v.kind != vkConstant: return (v, true)
    var found = false
    for constant in ir.constants:
      if constant.id == v.text:
        index = constant.value
        found = true
        break
    if not found: return (IRValue(), false)
  (IRValue(), false)

proc staticComparison*(ir: IR, c: IRCondition): (bool, bool) =
  ## Evaluates a comparison of two compile-time constants
  ## (TryEvaluateStaticBuiltinComparison). Returns (result, ok).
  if c.kind != irComparison or c.argumentCount != 2: return (false, false)
  let (left, leftOK) = resolveStaticValue(ir, c.firstArgument)
  let (right, rightOK) = resolveStaticValue(ir, c.firstArgument + 1)
  if not leftOK or not rightOK or left.kind == vkVariable or right.kind == vkVariable or
      left.kind == vkArithmetic or right.kind == vkArithmetic:
    return (false, false)
  proc numeric(v: IRValue): (float64, bool) =
    case v.atomType
    of akInt: (float64(v.literal.intValue), true)
    of akFloat: (float64(v.literal.floatValue), true)
    else: (0.0, false)
  let (l, leftNumeric) = numeric(left)
  let (r, rightNumeric) = numeric(right)
  if c.id == CompareEqual or c.id == CompareNotEqual:
    var equalValues: bool
    if leftNumeric and rightNumeric:
      equalValues = l == r
    elif left.atomType == right.atomType:
      case left.atomType
      of akBool: equalValues = left.literal.boolValue == right.literal.boolValue
      of akString, akSymbol: equalValues = left.text == right.text
      else: return (false, false)
    else:
      equalValues = false
    return ((if c.id == CompareEqual: equalValues else: not equalValues), true)
  if not leftNumeric or not rightNumeric: return (false, false)
  case c.id
  of CompareLess: (l < r, true)
  of CompareLessEqual: (l <= r, true)
  of CompareGreater: (l > r, true)
  of CompareGreaterEqual: (l >= r, true)
  else: (false, false)
