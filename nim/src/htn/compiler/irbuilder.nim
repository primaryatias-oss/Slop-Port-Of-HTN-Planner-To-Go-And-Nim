## Lowering of a linked domain into the compiler IR (HTNCompilerIRBuilder):
## nested call expressions become hidden assignment conditions, constants are
## resolved at translation time and callterm output bindings are validated.

import std/[sets, strutils, tables]
import ../[atom, lexer]
import ast, ir

proc formatValueExpression*(v: Value): string =
  ## Renders a value as domain syntax.
  case v.kind
  of vkIdentifier: toString(v.atom, false)
  of vkLiteral: toString(v.atom, true)
  of vkVariable: "?" & toString(v.atom, false)
  of vkConstant: "@" & toString(v.atom, false)
  of vkCall:
    var text = "(call " & formatValueExpression(v.callID)
    for a in v.callArguments: text.add " " & formatValueExpression(a)
    text & ")"
  of vkArithmetic:
    const operators = ["+", "-", "*", "/", "%", "++", "--"]
    var text = "(" & operators[ord(v.arithmeticOp)]
    for o in v.arithmeticOperands: text.add " " & formatValueExpression(o)
    text & ")"

proc formatArguments(arguments: seq[Value]): string =
  for a in arguments: result.add " " & formatValueExpression(a)

proc listSplitOperationName(operation: uint32): string =
  case operation
  of 1: "split_list_front"
  of 2: "split_list_back"
  else: "split_list"

proc formatCondition*(c: Condition): string =
  ## Renders a condition as domain syntax.
  case c.kind
  of ckFact: "(" & formatValueExpression(c.id) & formatArguments(c.arguments) & ")"
  of ckAxiom: "(#" & formatValueExpression(c.id) & formatArguments(c.arguments) & ")"
  of ckCall:
    let invocation = "(call " & formatValueExpression(c.id) & formatArguments(c.arguments) & ")"
    if c.output == nil: invocation
    else: "(" & formatValueExpression(c.output) & " " & invocation & ")"
  of ckAssignment: "(= " & formatValueExpression(c.output) & " " & formatValueExpression(c.arguments[0]) & ")"
  of ckComparison:
    const operators = ["==", "!=", "<", "<=", ">", ">="]
    let op = if int(c.operator) < operators.len: operators[c.operator] else: "?"
    "(" & op & " " & formatValueExpression(c.arguments[0]) & " " & formatValueExpression(c.arguments[1]) & ")"
  of ckSplit:
    let list = formatValueExpression(c.arguments[0])
    let element = formatValueExpression(c.arguments[1])
    let remainder = formatValueExpression(c.arguments[2])
    if c.operator == 2:
      "(" & listSplitOperationName(c.operator) & " " & list & " " & remainder & " " & element & ")"
    else:
      "(" & listSplitOperationName(c.operator) & " " & list & " " & element & " " & remainder & ")"
  of ckAnd, ckOr, ckAlt:
    var text = "(" & (if c.kind == ckAnd: "and" elif c.kind == ckOr: "or" else: "alt")
    for child in c.children: text.add " " & formatCondition(child)
    text & ")"
  of ckNot: "(not " & formatCondition(c.children[0]) & ")"

proc formatTask*(t: Task): string =
  ## Renders a task as domain syntax.
  let prefix = case t.kind
    of tkPrimitive: "!"
    of tkDeferred: "&"
    else: ""
  "(" & prefix & formatValueExpression(t.id) & formatArguments(t.arguments) & ")"

type IRBuilder = object
  ir: IR
  domain: Domain

proc sourceLine(r: SourceLocation): uint32 =
  if r.range.first.line < 1: 1'u32 else: uint32(r.range.first.line)

proc location(n: Node): SourceLocation = SourceLocation(fileIndex: n.fileIndex, range: n.range)

proc allocateVariableSlot(b: var IRBuilder, stringID: uint32): uint32 =
  if stringID in b.ir.variableSlotByStringID: return b.ir.variableSlotByStringID[stringID]
  result = uint32(b.ir.variableStringIDs.len)
  b.ir.variableStringIDs.add stringID
  b.ir.variableSlotByStringID[stringID] = result

proc allocatePreparedSymbol(b: var IRBuilder, stringID: uint32): uint32 {.discardable.} =
  if stringID in b.ir.preparedSymbolSlot: return b.ir.preparedSymbolSlot[stringID]
  result = uint32(b.ir.preparedSymbols.len)
  b.ir.preparedSymbols.add stringID
  b.ir.preparedSymbolSlot[stringID] = result

proc allocateCallTermSlot(b: var IRBuilder, stringID: uint32): uint32 =
  if stringID in b.ir.callTermSlotByStringID: return b.ir.callTermSlotByStringID[stringID]
  result = uint32(b.ir.callTermStringIDs.len)
  b.ir.callTermStringIDs.add stringID
  b.ir.callTermSlotByStringID[stringID] = result

proc allocateFactSlot(b: var IRBuilder, stringID: uint32): uint32 =
  if stringID in b.ir.factSlotByStringID: return b.ir.factSlotByStringID[stringID]
  result = uint32(b.ir.factStringIDs.len)
  b.ir.factStringIDs.add stringID
  b.ir.factSlotByStringID[stringID] = result
  b.allocatePreparedSymbol(stringID)

proc allocateListSymbols(b: var IRBuilder, a: Atom) =
  case a.kind
  of akList:
    for element in a.elements: b.allocateListSymbols(element)
  of akSymbol:
    b.allocatePreparedSymbol(b.ir.strings.add(toString(a, false)))
  else: discard

proc makeValueRecord(b: var IRBuilder, node: Value): IRValue =
  result = newIRValue()
  if node.kind == vkCall:
    let file = if int(node.fileIndex) < b.ir.sourceFiles.len: b.ir.sourceFiles[node.fileIndex] else: "<domain>"
    b.ir.setError(file & "(" & $node.range.first.line & "," & $node.range.first.column &
      "): error: Call expression '" & formatValueExpression(node) & "' was not lowered to a runtime invocation")
    return
  result.kind = node.kind
  result.text = b.ir.strings.add(toString(node.atom, false))
  result.debugText = b.ir.strings.add(formatValueExpression(node))
  result.atomType = node.atom.kind
  if node.kind == vkLiteral: result.literal = node.atom
  case result.atomType
  of akList: b.allocateListSymbols(node.atom)
  of akSymbol: b.allocatePreparedSymbol(result.text)
  else: discard
  result.source = location(Node(node[]))
  result.sourceLine = sourceLine(result.source)
  if result.kind == vkVariable and not b.ir.strings.get(result.text).startsWith("any_"):
    result.variableSlot = b.allocateVariableSlot(result.text)
  if result.kind == vkArithmetic:
    result.arithmeticExpression = uint32(b.ir.arithmeticExpressions.len)
    b.ir.arithmeticExpressions.add IRArithmeticExpression(operator: node.arithmeticOp)
    for operand in node.arithmeticOperands:
      let operandRecord = b.makeValueRecord(operand)
      b.ir.arithmeticExpressions[result.arithmeticExpression].operands.add operandRecord

proc addValue(b: var IRBuilder, node: Value): uint32 {.discardable.} =
  result = uint32(b.ir.values.len)
  let record = b.makeValueRecord(node)
  b.ir.values.add record

proc buildTaskArgument(b: var IRBuilder, node: Value, calls: var seq[IRTaskCallExpression]): IRValue =
  if node.kind == vkArithmetic:
    let shell = Value()
    shell[] = node[]
    shell.arithmeticOperands = @[]
    result = b.makeValueRecord(shell)
    result.debugText = b.ir.strings.add(formatValueExpression(node))
    for operand in node.arithmeticOperands:
      let prepared = b.buildTaskArgument(operand, calls)
      b.ir.arithmeticExpressions[result.arithmeticExpression].operands.add prepared
    return
  if node.kind == vkCall:
    var call = IRTaskCallExpression(id: NoIndex, callTermSlot: NoIndex, outputSlot: NoIndex)
    call.domainExpression = formatValueExpression(node)
    call.id = b.ir.strings.add(toString(node.callID.atom, false))
    call.callTermSlot = b.allocateCallTermSlot(call.id)
    call.source = location(Node(node[]))
    call.sourceLine = sourceLine(call.source)
    for argument in node.callArguments:
      call.arguments.add b.buildTaskArgument(argument, calls)
    let debugExpression = b.ir.strings.add(call.domainExpression)
    let hiddenName = "__task_call_result_" & $b.ir.syntheticTaskCallCount
    inc b.ir.syntheticTaskCallCount
    let hidden = b.ir.strings.add(hiddenName)
    b.ir.debugInternalVariableStringIDs[hidden] = true
    call.outputSlot = b.allocateVariableSlot(hidden)
    calls.add call
    result = newIRValue()
    result.kind = vkVariable
    result.text = hidden
    result.debugText = debugExpression
    result.variableSlot = call.outputSlot
    result.source = location(Node(node[]))
    result.sourceLine = sourceLine(result.source)
    result.debugAsVariable = false
    return
  b.makeValueRecord(node)

proc hasNestedCall(v: Value, root: bool): bool =
  if not root and v.kind == vkCall: return true
  for a in v.callArguments:
    if hasNestedCall(a, false): return true
  for o in v.arithmeticOperands:
    if hasNestedCall(o, false): return true
  false

proc captureValue(b: var IRBuilder, v: Value, prefix: var seq[Condition]): Value =
  let hiddenName = "$assignment_call_" & $b.ir.syntheticTaskCallCount
  inc b.ir.syntheticTaskCallCount
  result = Value(kind: vkVariable, atom: newString(hiddenName))
  b.ir.debugInternalVariableStringIDs[b.ir.strings.add(hiddenName)] = true
  result.range = v.range
  result.fileIndex = v.fileIndex
  let binding = Condition(kind: ckAssignment, output: result, arguments: @[v])
  binding.range = v.range
  binding.fileIndex = v.fileIndex
  prefix.add binding

proc prepareExpression(b: var IRBuilder, v: Value, prefix: var seq[Condition]): Value =
  result = Value()
  result[] = v[]
  for i in 0 ..< result.callArguments.len:
    result.callArguments[i] = b.captureValue(b.prepareExpression(result.callArguments[i], prefix), prefix)
  for i in 0 ..< result.arithmeticOperands.len:
    # Validate each numeric operand before evaluating subsequent calls.
    # Multiplication by one preserves its numeric type and value.
    let operand = result.arithmeticOperands[i]
    let checked = Value(kind: vkArithmetic, arithmeticOp: opMultiply)
    checked.range = operand.range
    checked.fileIndex = operand.fileIndex
    let one = Value(kind: vkLiteral, atom: newInt(1))
    one.range = DefaultRange
    var prepared = b.prepareExpression(operand, prefix)
    if prepared.kind == vkCall:
      prepared = b.captureValue(prepared, prefix)
    checked.arithmeticOperands = @[prepared, one]
    result.arithmeticOperands[i] = b.captureValue(checked, prefix)

proc preserveLoweredSource(b: var IRBuilder, sequence: uint32, original: Condition) =
  b.ir.conditions[sequence].debugExpression = formatCondition(original)
  b.ir.conditions[sequence].debugSource = location(Node(original[]))
  let c = b.ir.conditions[sequence]
  b.ir.conditions[sequence].debugCondition = b.ir.conditionChildRefs[c.firstChildRef + c.childCount - 1]
  for i in int(sequence) + 1 ..< b.ir.conditions.len:
    b.ir.conditions[i].debugInternal = true

proc addCondition(b: var IRBuilder, node: Condition): uint32 =
  if node == nil: return NoIndex
  if node.kind != ckAssignment:
    var hasCalls = false
    for argument in node.arguments:
      hasCalls = hasCalls or hasNestedCall(argument, false)
    if hasCalls:
      var prefix: seq[Condition]
      let condition = Condition()
      condition[] = node[]
      for i in 0 ..< condition.arguments.len:
        # Comparisons and callterms read all operands: capture them in source
        # order so failure stops before later invocations.
        let argument = condition.arguments[i]
        if node.kind == ckComparison or node.kind == ckCall or hasNestedCall(argument, false):
          condition.arguments[i] = b.captureValue(b.prepareExpression(argument, prefix), prefix)
      prefix.add condition
      let sequence = Condition(kind: ckAnd, children: prefix)
      sequence.range = node.range
      sequence.fileIndex = node.fileIndex
      let index = b.addCondition(sequence)
      b.preserveLoweredSource(index, node)
      return index
  if node.kind == ckAssignment and hasNestedCall(node.arguments[0], true):
    var prefix: seq[Condition]
    let expression = b.prepareExpression(node.arguments[0], prefix)
    if prefix.len > 0:
      let binding = Condition()
      binding[] = node[]
      binding.arguments = @[expression]
      prefix.add binding
      let sequence = Condition(kind: ckAnd, children: prefix)
      sequence.range = node.range
      sequence.fileIndex = node.fileIndex
      let index = b.addCondition(sequence)
      let guard = b.addValue(node.output)
      b.ir.conditions[index].assignmentGuardValue = guard
      b.preserveLoweredSource(index, node)
      return index

  var record = IRCondition(assignmentGuardValue: NoIndex, id: NoIndex, outputValue: NoIndex,
    resolvedIndex: NoIndex, debugCondition: NoIndex)
  record.domainExpression = formatCondition(node)
  record.source = location(Node(node[]))
  record.sourceLine = sourceLine(record.source)
  record.debugSource = record.source
  record.debugExpression = record.domainExpression
  case node.kind
  of ckAnd: record.debugExpression = "(and ...)"
  of ckOr: record.debugExpression = "(or ...)"
  of ckAlt: record.debugExpression = "(alt ...)"
  of ckNot: record.debugExpression = "(not ...)"
  else: discard
  let index = uint32(b.ir.conditions.len)
  b.ir.conditions.add record

  case node.kind
  of ckFact:
    record.kind = irFact
    record.id = b.ir.strings.add(toString(node.id.atom, false))
    record.resolvedIndex = b.allocateFactSlot(record.id)
    record.firstArgument = uint32(b.ir.values.len)
    for argument in node.arguments: b.addValue(argument)
    record.argumentCount = uint32(b.ir.values.len) - record.firstArgument
  of ckAxiom:
    record.kind = irAxiom
    record.id = b.ir.strings.add(toString(node.id.atom, false))
    record.firstArgument = uint32(b.ir.values.len)
    for argument in node.arguments: b.addValue(argument)
    record.argumentCount = uint32(b.ir.values.len) - record.firstArgument
  of ckCall:
    record.kind = if node.output != nil: irCallBind else: irCall
    record.id = b.ir.strings.add(toString(node.id.atom, false))
    record.resolvedIndex = b.allocateCallTermSlot(record.id)
    if node.output != nil: record.outputValue = b.addValue(node.output)
    record.firstArgument = uint32(b.ir.values.len)
    for argument in node.arguments: b.addValue(argument)
    record.argumentCount = uint32(b.ir.values.len) - record.firstArgument
  of ckAssignment:
    record.outputValue = b.addValue(node.output)
    let expression = node.arguments[0]
    if expression.kind == vkCall:
      record.kind = irCallBind
      record.source = location(Node(expression[]))
      record.sourceLine = sourceLine(record.source)
      record.id = b.ir.strings.add(toString(expression.callID.atom, false))
      record.resolvedIndex = b.allocateCallTermSlot(record.id)
      record.firstArgument = uint32(b.ir.values.len)
      for argument in expression.callArguments: b.addValue(argument)
      record.argumentCount = uint32(b.ir.values.len) - record.firstArgument
    else:
      record.kind = irAssignment
      record.firstArgument = b.addValue(expression)
      record.argumentCount = 1
  of ckComparison:
    record.kind = irComparison
    record.id = node.operator
    record.firstArgument = uint32(b.ir.values.len)
    b.addValue(node.arguments[0])
    b.addValue(node.arguments[1])
    record.argumentCount = 2
  of ckSplit:
    record.kind = irListSplit
    record.id = node.operator
    record.firstArgument = uint32(b.ir.values.len)
    b.addValue(node.arguments[0])
    b.addValue(node.arguments[1])
    b.addValue(node.arguments[2])
    record.argumentCount = 3
  of ckAnd: record.kind = irAnd
  of ckOr: record.kind = irOr
  of ckAlt: record.kind = irAlt
  of ckNot: record.kind = irNot
  var children: seq[uint32]
  for child in node.children: children.add b.addCondition(child)
  record.firstChildRef = uint32(b.ir.conditionChildRefs.len)
  b.ir.conditionChildRefs.add children
  record.childCount = uint32(children.len)
  b.ir.conditions[index] = record
  index

proc collectConditionSlots(b: var IRBuilder, conditionIndex: uint32, mask: var SlotMask) =
  if conditionIndex == NoIndex or int(conditionIndex) >= b.ir.conditions.len: return
  let c = b.ir.conditions[conditionIndex]
  for i in 0'u32 ..< c.argumentCount:
    b.ir.markValueSlots(mask, b.ir.values[c.firstArgument + i])
  if c.outputValue != NoIndex:
    b.ir.markValueSlots(mask, b.ir.values[c.outputValue])
  for i in 0'u32 ..< c.childCount:
    b.collectConditionSlots(b.ir.conditionChildRefs[c.firstChildRef + i], mask)

proc collectTaskSlots(b: var IRBuilder, taskIndex: uint32, mask: var SlotMask) =
  if int(taskIndex) >= b.ir.tasks.len: return
  let t = b.ir.tasks[taskIndex]
  for i in 0'u32 ..< t.argumentCount:
    b.ir.markValueSlots(mask, b.ir.values[t.firstArgument + i])
  if int(taskIndex) >= b.ir.taskCallExpressions.len: return
  for call in b.ir.taskCallExpressions[taskIndex]:
    mask.add call.outputSlot
    for argument in call.arguments: b.ir.markValueSlots(mask, argument)

proc addTask(b: var IRBuilder, node: Task) =
  var record = IRTask(planStepHead: NoIndex)
  record.domainExpression = formatTask(node)
  record.kind = case node.kind
    of tkPrimitive: irTaskPrimitive
    of tkDeferred: irTaskDeferred
    else: irTaskCompound
  let id = toString(node.id.atom, false)
  record.id = b.ir.strings.add(id)
  if record.kind == irTaskPrimitive:
    record.planStepHead = b.ir.strings.add("!" & id)
    b.allocatePreparedSymbol(record.planStepHead)
  elif record.kind == irTaskDeferred:
    record.planStepHead = b.ir.strings.add("&" & id)
    b.allocatePreparedSymbol(record.planStepHead)
  record.source = location(Node(node[]))
  record.sourceLine = sourceLine(record.source)
  var calls: seq[IRTaskCallExpression]
  var arguments: seq[IRValue]
  for argument in node.arguments:
    arguments.add b.buildTaskArgument(argument, calls)
  record.firstArgument = uint32(b.ir.values.len)
  b.ir.values.add arguments
  record.argumentCount = uint32(arguments.len)
  b.ir.tasks.add record
  b.ir.taskCallExpressions.add calls

proc build(b: var IRBuilder) =
  for group in b.domain.constantGroups:
    let groupID = b.ir.strings.add(group.id)
    for constant in group.constants:
      var record = IRConstant(groupID: groupID, id: b.ir.strings.add(constant.id))
      record.source = location(Node(constant[]))
      record.sourceLine = sourceLine(record.source)
      record.value = b.addValue(constant.value)
      b.ir.constants.add record
  for axiom in b.domain.axioms:
    var record = IRAxiom(id: b.ir.strings.add(axiom.id), condition: NoIndex)
    record.source = location(Node(axiom[]))
    record.sourceLine = sourceLine(record.source)
    record.firstParameter = uint32(b.ir.values.len)
    for parameter in axiom.parameters: b.addValue(parameter)
    record.parameterCount = uint32(b.ir.values.len) - record.firstParameter
    record.condition = b.addCondition(axiom.body)
    for i in 0'u32 ..< record.parameterCount:
      b.ir.markValueSlots(record.variableSlotMask, b.ir.values[record.firstParameter + i])
    b.collectConditionSlots(record.condition, record.variableSlotMask)
    b.ir.axioms.add record
  for m in b.domain.methods:
    var record = IRMethod(id: b.ir.strings.add(m.id))
    record.source = location(Node(m[]))
    record.sourceLine = sourceLine(record.source)
    record.firstParameter = uint32(b.ir.values.len)
    for parameter in m.parameters: b.addValue(parameter)
    record.parameterCount = uint32(b.ir.values.len) - record.firstParameter
    record.firstBranch = uint32(b.ir.branches.len)
    record.isTopLevel = m.topLevel
    record.isExternallyDecomposable = record.isTopLevel
    if record.isExternallyDecomposable: b.allocatePreparedSymbol(record.id)
    for branch in m.branches:
      var br = IRBranch(id: b.ir.strings.add(branch.id))
      br.source = location(Node(branch[]))
      br.sourceLine = sourceLine(br.source)
      br.condition = b.addCondition(branch.precondition)
      br.firstTask = uint32(b.ir.tasks.len)
      for task in branch.tasks: b.addTask(task)
      br.taskCount = uint32(b.ir.tasks.len) - br.firstTask
      b.ir.branches.add br
    record.branchCount = uint32(b.ir.branches.len) - record.firstBranch
    # Compound calls get fresh logical frames but slots are domain-global: the
    # mask identifies every slot this method may observe or mutate.
    for i in 0'u32 ..< record.parameterCount:
      b.ir.markValueSlots(record.variableSlotMask, b.ir.values[record.firstParameter + i])
    for bi in 0'u32 ..< record.branchCount:
      let br = b.ir.branches[record.firstBranch + bi]
      b.collectConditionSlots(br.condition, record.variableSlotMask)
      for ti in 0'u32 ..< br.taskCount:
        b.collectTaskSlots(br.firstTask + ti, record.variableSlotMask)
    b.ir.methods.add record
  # Methods referenced by &deferred calls must be reachable through the
  # generated dispatch without becoming public top-level methods.
  for task in b.ir.tasks:
    if task.kind != irTaskDeferred: continue
    for mi in 0 ..< b.ir.methods.len:
      if b.ir.methods[mi].id != task.id or b.ir.methods[mi].parameterCount != task.argumentCount: continue
      b.ir.methods[mi].isExternallyDecomposable = true
      b.allocatePreparedSymbol(b.ir.methods[mi].id)
      break

proc findConstant(b: IRBuilder, text: uint32): int =
  for i, c in b.ir.constants:
    if c.id == text: return i
  -1

proc resolveFromOriginal(b: IRBuilder, original: seq[IRValue], start: uint32): (IRValue, bool) =
  var index = start
  for depth in 0 ..< 64:
    if int(index) >= original.len: return (IRValue(), false)
    let value = original[index]
    if value.kind != vkConstant: return (value, true)
    let constant = b.findConstant(value.text)
    if constant < 0: return (IRValue(), false)
    index = b.ir.constants[constant].value
  (IRValue(), false)

proc resolveCompileTimeReferences(b: var IRBuilder): bool =
  let original = b.ir.values
  for i in 0 ..< b.ir.values.len:
    if b.ir.values[i].kind != vkConstant: continue
    let (resolved, ok) = b.resolveFromOriginal(original, uint32(i))
    if not ok or resolved.kind == vkVariable or resolved.kind == vkConstant:
      b.ir.setError("Generated constant could not be resolved to a static value at translation time")
      return false
    let value = b.ir.values[i]
    b.ir.values[i] = resolved
    b.ir.values[i].debugText = value.debugText
    b.ir.values[i].sourceLine = value.sourceLine
    b.ir.values[i].source = value.source
    b.ir.values[i].variableSlot = NoIndex
  for ti in 0 ..< b.ir.taskCallExpressions.len:
    for ci in 0 ..< b.ir.taskCallExpressions[ti].len:
      for ai in 0 ..< b.ir.taskCallExpressions[ti][ci].arguments.len:
        let value = b.ir.taskCallExpressions[ti][ci].arguments[ai]
        if value.kind != vkConstant: continue
        let constant = b.findConstant(value.text)
        if constant < 0:
          b.ir.setError("Generated task call constant could not be resolved at translation time")
          return false
        let (resolved, ok) = b.resolveFromOriginal(original, b.ir.constants[constant].value)
        if not ok or resolved.kind == vkVariable or resolved.kind == vkConstant:
          b.ir.setError("Generated task call constant could not be resolved to a static value at translation time")
          return false
        var updated = resolved
        updated.debugText = value.debugText
        updated.sourceLine = value.sourceLine
        updated.source = value.source
        updated.variableSlot = NoIndex
        b.ir.taskCallExpressions[ti][ci].arguments[ai] = updated
  b.ir.staticValues.setLen(0)
  proc allocateStatic(ir: IR, value: var IRValue) =
    value.staticValueIndex = NoIndex
    if value.kind == vkVariable or value.kind == vkArithmetic: return
    value.staticValueIndex = uint32(ir.staticValues.len)
    ir.staticValues.add IRStaticValue(text: value.text, atomType: value.atomType, literal: value.literal)
  for i in 0 ..< b.ir.values.len:
    allocateStatic(b.ir, b.ir.values[i])
  for ti in 0 ..< b.ir.taskCallExpressions.len:
    for ci in 0 ..< b.ir.taskCallExpressions[ti].len:
      for ai in 0 ..< b.ir.taskCallExpressions[ti][ci].arguments.len:
        allocateStatic(b.ir, b.ir.taskCallExpressions[ti][ci].arguments[ai])
  for ci in 0 ..< b.ir.conditions.len:
    if b.ir.conditions[ci].kind != irAxiom: continue
    var found = false
    for ai, axiom in b.ir.axioms:
      if axiom.id == b.ir.conditions[ci].id and axiom.parameterCount == b.ir.conditions[ci].argumentCount:
        b.ir.conditions[ci].resolvedIndex = uint32(ai)
        found = true
        break
    if not found:
      b.ir.setError("Generated axiom reference could not be resolved at translation time")
      return false
  true

proc validateCallTermCondition(b: IRBuilder, index: uint32, mayBeBound: var HashSet[uint32]): (string, bool) =
  if index == NoIndex or int(index) >= b.ir.conditions.len: return ("", true)
  let c = b.ir.conditions[index]
  case c.kind
  of irFact:
    for i in 0'u32 ..< c.argumentCount:
      let v = b.ir.values[c.firstArgument + i]
      if v.kind == vkVariable: mayBeBound.incl v.text
  of irAxiom:
    let axiomIndex = b.ir.findAxiom(c.id, c.argumentCount)
    if axiomIndex < 0: return ("", true)
    let axiom = b.ir.axioms[axiomIndex]
    let count = min(axiom.parameterCount, c.argumentCount)
    for i in 0'u32 ..< count:
      let name = b.ir.strings.get(b.ir.values[axiom.firstParameter + i].text)
      if not name.startsWith("out_") and not name.startsWith("io_"): continue
      let caller = b.ir.values[c.firstArgument + i]
      if caller.kind == vkVariable: mayBeBound.incl caller.text
  of irListSplit:
    for i in 1'u32 ..< c.argumentCount:
      let v = b.ir.values[c.firstArgument + i]
      if v.kind == vkVariable: mayBeBound.incl v.text
  of irCallBind, irAssignment:
    if c.outputValue == NoIndex or int(c.outputValue) >= b.ir.values.len: return ("", true)
    let output = b.ir.values[c.outputValue]
    if output.kind != vkVariable: return ("", true)
    if output.text in mayBeBound:
      let callTerm = if int(c.id) < b.ir.strings.values.len: b.ir.strings.values[c.id] else: "<unknown>"
      var message = "Callterm output variable '?" & b.ir.strings.get(output.text) &
        "' may already be bound before call '" & callTerm & "'"
      if c.sourceLine != 0: message.add " at domain line " & $c.sourceLine
      return (message, false)
    mayBeBound.incl output.text
  of irAnd:
    for i in 0'u32 ..< c.childCount:
      let (message, ok) = b.validateCallTermCondition(b.ir.conditionChildRefs[c.firstChildRef + i], mayBeBound)
      if not ok: return (message, false)
  of irOr, irAlt:
    var union = mayBeBound
    for i in 0'u32 ..< c.childCount:
      var branch = mayBeBound
      let (message, ok) = b.validateCallTermCondition(b.ir.conditionChildRefs[c.firstChildRef + i], branch)
      if not ok: return (message, false)
      for k in branch: union.incl k
    mayBeBound = union
  of irNot:
    for i in 0'u32 ..< c.childCount:
      var local = mayBeBound
      let (message, ok) = b.validateCallTermCondition(b.ir.conditionChildRefs[c.firstChildRef + i], local)
      if not ok: return (message, false)
  else: discard
  ("", true)

proc validateCallTermBindings(b: IRBuilder): (string, bool) =
  for m in b.ir.methods:
    var initial = initHashSet[uint32]()
    for i in 0'u32 ..< m.parameterCount:
      let p = b.ir.values[m.firstParameter + i]
      if p.kind == vkVariable: initial.incl p.text
    for bi in 0'u32 ..< m.branchCount:
      var bindings = initial
      let (message, ok) = b.validateCallTermCondition(b.ir.branches[m.firstBranch + bi].condition, bindings)
      if not ok: return (message, false)
  for a in b.ir.axioms:
    var initial = initHashSet[uint32]()
    for i in 0'u32 ..< a.parameterCount:
      let p = b.ir.values[a.firstParameter + i]
      if p.kind != vkVariable: continue
      if b.ir.strings.get(p.text).startsWith("inp_"): initial.incl p.text
    let (message, ok) = b.validateCallTermCondition(a.condition, initial)
    if not ok: return (message, false)
  ("", true)

proc buildIR*(domain: Domain, sourceFiles: seq[string], runtimeBacktrackingSupport: bool): (IR, string) =
  ## Lowers a linked domain into the compiler IR. `sourceFiles` holds the
  ## display paths of the linked source files.
  for m in domain.methods:
    for parameter in m.parameters:
      let name = parameter.atom.strValue
      if not name.startsWith("inp_"):
        return (nil, "Method '" & m.id & "' parameter '?" & name & "' must use the inp_ prefix")
  for axiom in domain.axioms:
    for parameter in axiom.parameters:
      let name = parameter.atom.strValue
      if not name.startsWith("inp_") and not name.startsWith("out_") and not name.startsWith("io_"):
        return (nil, "Axiom '" & axiom.id & "' parameter '?" & name & "' must use an inp_, out_ or io_ prefix")
  let ir = IR(domainID: domain.id, sourceFiles: sourceFiles, runtimeBacktrackingSupport: runtimeBacktrackingSupport)
  var b = IRBuilder(ir: ir, domain: domain)
  b.build()
  if ir.hasError: return (nil, ir.error)
  if not b.resolveCompileTimeReferences(): return (nil, ir.error)
  let (message, ok) = b.validateCallTermBindings()
  if not ok: return (nil, message)
  if ir.variableStringIDs.len > MaxVariableSlots:
    return (nil, "Generated domain requires " & $ir.variableStringIDs.len &
      " variable slots, but HTN_GENERATED_MAX_VARIABLE_SLOTS is " & $MaxVariableSlots)
  (ir, "")
