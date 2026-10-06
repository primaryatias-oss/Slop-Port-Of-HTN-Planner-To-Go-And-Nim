## Nim code generator: emits native Nim planners from the compiler IR.
##
## The emitted code follows the execution model of the original C generator
## (HTNCCodeGenerator) operation by operation: conditions are compiled into
## continuation-passing control flow with explicit checkpoints, facts with
## unbound variables become choice points, axioms are inlined at their call
## sites between generated begin/end helpers, and methods/tasks suspend into
## an explicit call-frame dispatcher instead of recursing natively. Nim has no
## `goto`, so every generated method is a `while true: case state` state
## machine whose states are the labels of the original control flow.

import std/[sets, strutils, tables]
import ../atom
import ../compiler/[ast, irbuilder, ir]
import analysis

type
  BacktrackingPolicy* = enum
    ## How generated planners store pending continuations.
    fixedWithOverflow ## Grow storage beyond the inline capacity.
    fixedCapacity     ## Fail with BACKTRACKING_CAPACITY_EXCEEDED when full.

  Options* = object
    entryPointName*: string
      ## Names the exported accessor `<entryPointName>_GetDefinition`.
    sourceFilePath*: string
      ## Display path of the root domain.
    linkedSourceFiles*: seq[string]
      ## Display paths of every linked source file.
    backtrackingPolicy*: BacktrackingPolicy
    runtimeBacktrackingSupport*: bool
    backtrackingCapacity*: uint32
    callFrameCapacity*: uint32
    toolName*: string
      ## Used in the capacity diagnostic ("htn-translator").

  CommitFn = proc () {.closure.}
  SuccessFn = proc (retry: int, commit: CommitFn) {.closure.}

  FnLine = object
    label: int    ## >= 0 for label lines
    jump: int     ## >= 0 for "goto" lines
    isReturn: bool
    text: string
    indent: int

  FnWriter = ref object
    lines: seq[FnLine]
    decls: seq[(string, string)]
    declared: HashSet[string]
    indent: int

  Generator = ref object
    ir: IR
    options: Options
    nextLabel: int
    symbols: Table[string, string]
    symbolOrder: seq[string]
    callSites: seq[string]
    top: string
    funcs: string
    initBody: string

  MethodGen = object
    g: Generator
    w: FnWriter

const defaultValueContext = "in this generated value context"

proc defaultOptions*(): Options =
  Options(backtrackingCapacity: 32, callFrameCapacity: 8192, toolName: "htn-translator")

proc nimQuote(text: string): string = escape(text)

proc nimComment(expression: string): string =
  expression.replace("\n", "\\n").replace("\r", "\\r")

# ---------------------------------------------------------------------------
# Function body writer

proc newFnWriter(): FnWriter = FnWriter(indent: 1)

proc code(w: FnWriter, text: string) =
  w.lines.add FnLine(label: -1, jump: -1, text: text, indent: w.indent)

proc ret(w: FnWriter, value: string) =
  w.lines.add FnLine(label: -1, jump: -1, isReturn: true, text: "return " & value, indent: w.indent)

proc comment(w: FnWriter, text: string) =
  if text.len > 0: w.code("# " & nimComment(text))

proc label(w: FnWriter, l: int) =
  w.lines.add FnLine(label: l, jump: -1)

proc jump(w: FnWriter, l: int) =
  w.lines.add FnLine(label: -1, jump: l, indent: w.indent)

proc declare(w: FnWriter, name, typ: string) =
  if name notin w.declared:
    w.declared.incl name
    w.decls.add (name, typ)

proc open(w: FnWriter, text: string) =
  w.code(text)
  inc w.indent

proc close(w: FnWriter) = dec w.indent

proc eliminateDeadCode(w: FnWriter, pinned: HashSet[int]): seq[FnLine] =
  ## Drops top-level statements that follow an unconditional jump/return until
  ## the next referenced label, repeating until stable.
  var lines = w.lines
  while true:
    var used = pinned
    for line in lines:
      if line.label < 0 and line.jump >= 0: used.incl line.jump
    var kept: seq[FnLine]
    var reachable = true
    for line in lines:
      if line.label >= 0:
        if line.label in used:
          reachable = true
          kept.add line
        continue
      if not reachable: continue
      kept.add line
      if line.indent == 1 and (line.jump >= 0 or line.isReturn): reachable = false
    if kept.len == lines.len: return kept
    lines = kept

proc renderLine(output: var string, line: FnLine, extra: int) =
  let prefix = repeat("  ", line.indent + extra)
  if line.jump >= 0:
    output.add prefix & "state = " & $line.jump & "\n"
    output.add prefix & "continue\n"
  else:
    output.add prefix & line.text & "\n"

proc render(w: FnWriter, output: var string, pinned: HashSet[int], resumeCases: seq[(int, int)]) =
  ## Renders the body as a `while true: case state` state machine. The code
  ## before the first label is state 0.
  for (name, typ) in w.decls:
    output.add "  var " & name & ": " & typ & "\n"
  output.add "  var state = 0\n"
  if resumeCases.len > 0:
    output.add "  case fr.resume\n"
    for (resume, target) in resumeCases:
      output.add "  of " & $resume & ": state = " & $target & "\n"
    output.add "  else: discard\n"
  let lines = w.eliminateDeadCode(pinned)
  var sections: seq[(int, seq[FnLine])] = @[(0, newSeq[FnLine]())]
  for line in lines:
    if line.label >= 0:
      sections.add (line.label, newSeq[FnLine]())
    else:
      sections[^1][1].add line
  output.add "  while true:\n"
  output.add "    case state\n"
  for index, (state, body) in sections:
    if index == 0 and body.len == 0: continue
    output.add "    of " & $state & ":\n"
    for line in body: renderLine(output, line, 2)
    var terminal = false
    for line in body:
      if line.indent == 1 and (line.jump >= 0 or line.isReturn): terminal = true
    if not terminal:
      if index + 1 < sections.len:
        output.add "      state = " & $sections[index + 1][0] & "\n"
        output.add "      continue\n"
      else:
        output.add "      return 0\n"
  output.add "    else:\n"
  output.add "      return 0\n"

# ---------------------------------------------------------------------------
# Values

proc newLabel(g: Generator): int =
  inc g.nextLabel
  g.nextLabel

proc symbol(g: Generator, text: string): string =
  if text in g.symbols: return g.symbols[text]
  result = "sym" & $g.symbolOrder.len
  g.symbols[text] = result
  g.symbolOrder.add text

proc atomLiteral(g: Generator, a: Atom): string =
  case a.kind
  of akBool: (if a.boolValue: "newBool(true)" else: "newBool(false)")
  of akInt: "newInt(" & $a.intValue & "'i32)"
  of akFloat: "newFloatBits(0x" & toHex(a.floatBits, 8) & "'u32)"
  of akString: "newString(" & nimQuote(a.strValue) & ")"
  of akSymbol: "newSymbol(" & g.symbol(a.symbolValue.text) & ")"
  of akList:
    var parts: seq[string]
    for e in a.elements: parts.add g.atomLiteral(e)
    "newList(" & parts.join(", ") & ")"
  of akUnbound: "Atom()"

proc staticName(index: uint32): string = "sv" & $index

# Debugger events (HTN_GENERATED_EVENT_DEBUG_*): templates of htn/planner that
# expand to nothing unless the planner is built with -d:htnDebug.

proc debugEvent(event: string, argument: auto): string =
  "ex.debug" & event & "(definition, " & $argument & ")"

proc sourceFile(g: Generator, index: uint32): string =
  if int(index) < g.ir.sourceFiles.len: g.ir.sourceFiles[index] else: ""

proc callSource(g: Generator, location: SourceLocation): string =
  result = "cs" & $g.callSites.len
  g.callSites.add "var " & result & " = Source(domain: " & nimQuote(g.ir.domainID) & ", file: " &
    nimQuote(g.sourceFile(location.fileIndex)) & ", line: " & $location.range.first.line & ", column: " &
    $location.range.first.column & ")"

proc arithmeticError(g: Generator, v: IRValue, context: string): string =
  let file = if int(v.source.fileIndex) < g.ir.sourceFiles.len: g.ir.sourceFiles[v.source.fileIndex]
             else: "<unknown domain>"
  let line = max(v.source.range.first.line, 1)
  let column = max(v.source.range.first.column, 1)
  let expression = if int(v.debugText) < g.ir.strings.values.len: g.ir.strings.values[v.debugText]
                   else: "<unknown expression>"
  file & "(" & $line & "," & $column & "): error: Arithmetic expression '" & expression &
    "' cannot be used " & context

proc recordRef(g: Generator, v: IRValue, context: string): string =
  if v.kind == vkArithmetic:
    g.ir.setError(g.arithmeticError(v, context))
    return "Atom()"
  if v.kind == vkVariable:
    if v.variableSlot == NoIndex: return "Atom()"
    return "ex.v[" & $v.variableSlot & "]"
  if v.staticValueIndex == NoIndex:
    g.ir.setError("Generated static value has no prepared-value index")
    return "Atom()"
  staticName(v.staticValueIndex)

proc valueRef(g: Generator, index: uint32, context: string): string =
  ## A Nim expression of type Atom for a value reference
  ## (BuildGeneratedValueAtomReference). An unbound atom stands for NULL.
  if int(index) >= g.ir.values.len:
    g.ir.setError("Generated value reference is out of range")
    return "Atom()"
  g.recordRef(g.ir.values[index], context)

proc arithmeticValue(g: Generator, v: IRValue, context: string): string =
  ## A Nim expression evaluating an arithmetic operand tree
  ## (EmitGeneratedArithmeticValue).
  case v.kind
  of vkVariable:
    if v.variableSlot == NoIndex: return "Atom()"
    return "ex.v[" & $v.variableSlot & "]"
  of vkConstant:
    for constant in g.ir.constants:
      if constant.id == v.text: return g.valueRef(constant.value, defaultValueContext)
    return "Atom()"
  of vkArithmetic:
    discard
  else:
    case v.atomType
    of akInt: return "newInt(" & $v.literal.intValue & "'i32)"
    of akFloat: return "newFloatBits(0x" & toHex(v.literal.floatBits, 8) & "'u32)"
    else: return "Atom()"
  if int(v.arithmeticExpression) >= g.ir.arithmeticExpressions.len:
    g.ir.setError(g.arithmeticError(v, context))
    return "Atom()"
  let expression = g.ir.arithmeticExpressions[v.arithmeticExpression]
  var operands: seq[string]
  for operand in expression.operands: operands.add g.arithmeticValue(operand, context)
  "arith(" & $ord(expression.operator) & "'u32, [" & operands.join(", ") & "])"

proc argumentValue(g: Generator, index: uint32, context: string): string =
  ## Evaluates a condition/task argument: arithmetic expressions are computed,
  ## other values are referenced.
  let v = g.ir.values[index]
  if v.kind == vkArithmetic: g.arithmeticValue(v, context)
  else: g.valueRef(index, defaultValueContext)

proc displaySourceFile(g: Generator): string =
  if g.options.sourceFilePath.len == 0: "<domain>" else: g.options.sourceFilePath

proc slotList(slots: seq[uint32]): string =
  var parts: seq[string]
  for s in slots: parts.add $s & "'u32"
  if parts.len == 0: "newSeq[uint32]()" else: "@[" & parts.join(", ") & "]"

proc atomList(values: seq[string]): string =
  ## A seq[Atom] expression.
  if values.len == 0: "newSeq[Atom]()" else: "@[" & values.join(", ") & "]"

proc atomArray(values: seq[string]): string =
  ## An openArray[Atom] argument expression.
  if values.len == 0: "newSeq[Atom]()" else: "[" & values.join(", ") & "]"

proc callTermRequirements(g: Generator): seq[string] =
  let ir = g.ir
  var seen = initHashSet[(uint32, uint32, int, int)]()
  template emit(id: uint32, location: SourceLocation) =
    let key = (id, location.fileIndex, location.range.first.line, location.range.first.column)
    if key notin seen:
      seen.incl key
      result.add "Requirement(name: " & nimQuote(ir.strings.get(id)) & ", source: Source(domain: " &
        nimQuote(ir.domainID) & ", file: " & nimQuote(g.sourceFile(location.fileIndex)) & ", line: " &
        $location.range.first.line & ", column: " & $location.range.first.column & "))"
  for c in ir.conditions:
    if c.kind == irCall or c.kind == irCallBind: emit(c.id, c.source)
  for calls in ir.taskCallExpressions:
    for call in calls: emit(call.id, call.source)

proc taskRestoreSlots(g: Generator): (seq[seq[uint32]], int) =
  ## For every task after the first of its branch, the slots its immediately
  ## preceding sibling may mutate.
  let ir = g.ir
  var restore = newSeq[seq[uint32]](ir.tasks.len)
  proc mutation(task: uint32): SlotMask =
    if int(task) >= ir.tasks.len: return
    if int(task) < ir.taskCallExpressions.len:
      for call in ir.taskCallExpressions[task]: result.add call.outputSlot
    let t = ir.tasks[task]
    if t.kind == irTaskCompound:
      let target = ir.findMethod(t.id, t.argumentCount)
      if target >= 0:
        for w in 0 ..< VariableSlotMaskWords:
          result[w] = result[w] or ir.methods[target].variableSlotMask[w]
  var maxRestore = 0
  for b in ir.branches:
    for local in 1'u32 ..< b.taskCount:
      let task = b.firstTask + local
      restore[task] = mutation(task - 1).slots
      maxRestore = max(maxRestore, restore[task].len)
  (restore, maxRestore)

proc emitBranchContinuations(g: Generator, restore: seq[seq[uint32]]) =
  let ir = g.ir
  for bi, b in ir.branches:
    if b.taskCount == 0: continue
    g.top.add "var bc" & $bi & ": BranchContinuations\n"
    var tasks: seq[string]
    var total = 0
    for i in 0'u32 ..< b.taskCount:
      let task = b.firstTask + (b.taskCount - 1 - i)
      let slots = restore[task]
      total += slots.len
      if slots.len == 0: tasks.add "PendingTask(fn: task" & $task & ")"
      else: tasks.add "PendingTask(fn: task" & $task & ", restore: " & slotList(slots) & ")"
    g.initBody.add "bc" & $bi & " = BranchContinuations(tasks: @[" & tasks.join(", ") & "], totalRestore: " &
      $total & ")\n"
  g.top.add "\n"

# ---------------------------------------------------------------------------
# Axioms

proc axiomDirection(ir: IR, parameter: IRValue): (bool, bool) =
  ## (input, output) direction of an axiom parameter.
  if parameter.kind != vkVariable or parameter.variableSlot == NoIndex or
      int(parameter.text) >= ir.strings.values.len:
    ir.setError("Generated axiom parameter has no compile-time variable slot")
    return (true, false)
  let name = ir.strings.values[parameter.text]
  if name.startsWith("out_"): (false, true)
  elif name.startsWith("io_"): (true, true)
  else: (true, false)

proc emitAxiomHelpers(g: Generator, condition: uint32) =
  let ir = g.ir
  let c = ir.conditions[condition]
  let axiom = ir.axioms[c.resolvedIndex]
  if axiom.parameterCount != c.argumentCount:
    ir.setError("Axiom call/parameter arity mismatch while emitting direct axiom helpers")
    return
  let axiomName = if int(c.id) >= ir.strings.values.len: "<unknown>" else: ir.strings.get(c.id)
  var f = "proc axiomBegin" & $condition & "(ex: Exec, scope: var AxiomScope) {.nimcall.} =\n"
  f.add "  for i in 0 ..< scope.args.len: scope.args[i] = Atom()\n"
  for i in 0'u32 ..< c.argumentCount:
    let parameter = ir.values[axiom.firstParameter + i]
    let (_, output) = axiomDirection(ir, parameter)
    let context = "as argument " & $(i + 1) & " of axiom '" & axiomName & "'"
    let caller = ir.values[c.firstArgument + i]
    let reference = if caller.kind == vkArithmetic: g.arithmeticValue(caller, context)
                    else: g.valueRef(c.firstArgument + i, context)
    f.add "  let in" & $i & " = " & reference & "\n"
    if output:
      f.add "  if in" & $i & ".isBound: scope.args[" & $i & "] = in" & $i & "\n"
  for k, slot in axiom.variableSlotMask.slots:
    f.add "  scope.saved[" & $k & "] = ex.v[" & $slot & "]\n  ex.v[" & $slot & "] = Atom()\n"
  f.add "  scope.callerFrame = ex.currentFrameID\n  ex.enterFrame()\n"
  for i in 0'u32 ..< c.argumentCount:
    let parameter = ir.values[axiom.firstParameter + i]
    let (input, _) = axiomDirection(ir, parameter)
    if not input: continue
    if parameter.variableSlot == NoIndex:
      ir.setError("Generated axiom input parameter has no variable slot")
      return
    f.add "  if in" & $i & ".isBound: ex.setIfChanged(" & $parameter.variableSlot & ", in" & $i & ")\n"
  f.add "  " & debugEvent("BeginAxiom", c.resolvedIndex) & "\n"
  f.add "\n"

  f.add "proc axiomEnd" & $condition & "(ex: Exec, succeeded: bool, scope: var AxiomScope): bool {.nimcall, discardable.} =\n"
  f.add "  var valid = succeeded\n"
  for i in 0'u32 ..< c.argumentCount:
    let parameter = ir.values[axiom.firstParameter + i]
    let (_, output) = axiomDirection(ir, parameter)
    if not output: continue
    let caller = ir.values[c.firstArgument + i]
    if parameter.variableSlot == NoIndex:
      ir.setError("Generated axiom output parameter has no variable slot")
      return
    f.add "  if valid:\n"
    f.add "    if not ex.v[" & $parameter.variableSlot & "].isBound:\n      valid = false\n"
    f.add "    elif scope.args[" & $i & "].isBound and not equal(ex.v[" & $parameter.variableSlot & "], scope.args[" &
      $i & "]):\n      valid = false\n"
    if caller.kind != vkVariable:
      f.add "    elif not scope.args[" & $i & "].isBound:\n      valid = false\n"
  for i in 0'u32 ..< c.argumentCount:
    let caller = ir.values[c.firstArgument + i]
    let parameter = ir.values[axiom.firstParameter + i]
    let (_, output) = axiomDirection(ir, parameter)
    if caller.kind != vkVariable or not output: continue
    for j in 0'u32 ..< i:
      let other = ir.values[c.firstArgument + j]
      let otherParameter = ir.values[axiom.firstParameter + j]
      let (_, otherOutput) = axiomDirection(ir, otherParameter)
      if other.kind == vkVariable and other.variableSlot == caller.variableSlot and otherOutput:
        f.add "  if valid and not equal(ex.v[" & $parameter.variableSlot & "], ex.v[" & $otherParameter.variableSlot &
          "]):\n    valid = false\n"
  var outputs: seq[uint32]
  for i in 0'u32 ..< c.argumentCount:
    let parameter = ir.values[axiom.firstParameter + i]
    let caller = ir.values[c.firstArgument + i]
    let (_, output) = axiomDirection(ir, parameter)
    if not output or caller.kind != vkVariable: continue
    if caller.variableSlot == NoIndex or parameter.variableSlot == NoIndex:
      ir.setError("Generated axiom variable output has no caller/parameter slot")
      return
    outputs.add i
    f.add "  var out" & $i & ": Atom\n"
  if outputs.len > 0:
    f.add "  if valid:\n"
    for i in outputs:
      f.add "    out" & $i & " = ex.v[" & $ir.values[axiom.firstParameter + i].variableSlot & "]\n"
  f.add "  " & debugEvent("EndAxiom", "valid") & "\n"
  for k, slot in axiom.variableSlotMask.slots:
    f.add "  ex.v[" & $slot & "] = scope.saved[" & $k & "]\n"
  f.add "  ex.currentFrameID = scope.callerFrame\n"
  if outputs.len > 0:
    f.add "  if valid:\n"
    for i in outputs:
      f.add "    ex.setIfChanged(" & $ir.values[c.firstArgument + i].variableSlot & ", out" & $i & ")\n"
  f.add "  valid\n\n"
  g.funcs.add f

# ---------------------------------------------------------------------------
# Facts

proc staticFactMatch(g: Generator, index: uint32, cell: string): string =
  let (v, ok) = resolveStaticValue(g.ir, index)
  if not ok or v.kind == vkVariable:
    g.ir.setError("Static fact argument could not be resolved at translation time")
    return "false"
  "equal(" & cell & ", " & g.valueRef(index, defaultValueContext) & ")"

proc emitFactChoiceHelper(g: Generator, condition: uint32) =
  let ir = g.ir
  let c = ir.conditions[condition]
  if c.resolvedIndex == NoIndex:
    ir.setError("Generated fact choice has no compile-time fact slot")
    return
  let table = "ft.tables[" & $c.argumentCount & "]"
  proc cell(i: uint32): string = table & ".rows[row][" & $i & "]"
  var checks: seq[string]
  var first = initTable[uint32, uint32]()
  var firstOrder: seq[uint32]
  for i in 0'u32 ..< c.argumentCount:
    let index = c.firstArgument + i
    if int(index) >= ir.values.len: continue
    let v = ir.values[index]
    if v.kind == vkVariable and v.variableSlot == NoIndex: continue
    if v.kind == vkVariable:
      checks.add "if ex.v[" & $v.variableSlot & "].isBound and not equal(" & cell(i) & ", ex.v[" & $v.variableSlot &
        "]): continue"
    else:
      checks.add "if not " & g.staticFactMatch(index, cell(i)) & ": continue"
    if v.kind == vkVariable and v.variableSlot != NoIndex:
      if v.text in first:
        checks.add "if not equal(" & cell(first[v.text]) & ", " & cell(i) & "): continue"
      else:
        first[v.text] = i
        firstOrder.add v.text
  var f = "proc factChoice" & $condition & "(ex: Exec, target: uint32): bool {.nimcall.} =\n"
  f.add "  let ft = ex.factTables[" & $c.resolvedIndex & "]\n  var solution = 0'u32\n"
  f.add "  for row in 0 ..< " & table & ".rows.len:\n"
  for check in checks: f.add "    " & check & "\n"
  f.add "    if solution != target:\n      inc solution\n      continue\n"
  for text in firstOrder:
    let argument = first[text]
    let slot = ir.values[c.firstArgument + argument].variableSlot
    f.add "    if not ex.v[" & $slot & "].isBound and " & cell(argument) & ".isBound: ex.v[" & $slot & "] = " &
      cell(argument) & "\n"
  f.add "    return true\n  false\n\n"
  g.funcs.add f

proc intLiteral(v: int32): string =
  if v == low(int32): "low(int32)" else: $v & "'i32"

# ---------------------------------------------------------------------------
# Tasks

proc emitTask(g: Generator, taskIndex: uint32) =
  let ir = g.ir
  let task = ir.tasks[taskIndex]
  let taskName = ir.strings.get(task.id)
  var f = "proc task" & $taskIndex & "(ex: Exec): int {.nimcall.} =\n"
  f.add "  if ex.frame().resume != 0:\n"
  f.add "    " & debugEvent("EndTask", "ex.frame().childResult != 0") & "\n"
  f.add "    return ex.frame().childResult\n"
  f.add "  " & debugEvent("BeginTask", taskIndex) & "\n"
  let endFailed = debugEvent("EndTask", false)
  if int(taskIndex) < ir.taskCallExpressions.len:
    for call in ir.taskCallExpressions[taskIndex]:
      let callName = if int(call.id) >= ir.strings.values.len: "<unknown>" else: ir.strings.get(call.id)
      var arguments: seq[string]
      for ai, argument in call.arguments:
        case argument.kind
        of vkArithmetic:
          arguments.add g.arithmeticValue(argument, "as argument " & $(ai + 1) & " of callterm '" & callName & "'")
        of vkVariable:
          if argument.variableSlot == NoIndex: arguments.add "Atom()"
          else: arguments.add "ex.v[" & $argument.variableSlot & "]"
        else:
          if argument.staticValueIndex == NoIndex:
            ir.setError("Generated task call argument has no prepared-value index")
            return
          arguments.add staticName(argument.staticValueIndex)
      if int(call.callTermSlot) >= ir.callTermStringIDs.len:
        ir.setError("Generated task call expression has invalid callterm slot")
        return
      let source = g.callSource(call.source)
      f.add "  # " & nimComment(call.domainExpression) & "\n"
      f.add "  block:\n"
      f.add "    let (callResult, ok) = ex.invoke(" & $call.callTermSlot & ", " & atomList(arguments) & ", addr " &
        source & ", factSymbols)\n"
      f.add "    if not ok:\n      " & endFailed & "\n      return 0\n"
      f.add "    ex.setIfChanged(" & $call.outputSlot & ", callResult)\n"
  case task.kind
  of irTaskPrimitive, irTaskDeferred:
    var arguments: seq[string]
    for ai in 0'u32 ..< task.argumentCount:
      let index = task.firstArgument + ai
      if int(index) >= ir.values.len:
        ir.setError("Generated primitive task argument is out of range")
        return
      let argument = ir.values[index]
      if argument.kind == vkArithmetic:
        arguments.add g.arithmeticValue(argument, "as argument " & $(ai + 1) & " of primitive task '" & taskName & "'")
      else:
        arguments.add g.valueRef(index, defaultValueContext)
    f.add "  # " & nimComment(task.domainExpression) & "\n"
    f.add "  if not ex.appendPlanStep(" & g.symbol(ir.strings.get(task.planStepHead)) & ", " &
      atomArray(arguments) & "):\n    " & endFailed & "\n    return 0\n"
    f.add "  " & debugEvent("EndTask", true) & "\n  return 1\n\n"
  of irTaskCompound:
    let target = ir.findMethod(task.id, task.argumentCount)
    if target < 0:
      f.add "  " & endFailed & "\n  return 0 # unresolved compound task\n\n"
      g.funcs.add f
      return
    f.add "  # " & nimComment(task.domainExpression) & "\n"
    let targetMethod = ir.methods[target]
    if task.argumentCount != targetMethod.parameterCount:
      ir.setError("Generated compound task parameter count mismatch")
      return
    for ai in 0'u32 ..< task.argumentCount:
      let index = task.firstArgument + ai
      if int(index) >= ir.values.len:
        ir.setError("Generated compound task argument is out of range")
        return
      let argument = ir.values[index]
      let reference = if argument.kind == vkArithmetic:
          g.arithmeticValue(argument, "as argument " & $(ai + 1) & " of compound task '" & taskName & "'")
        else:
          g.valueRef(index, defaultValueContext)
      f.add "  let arg" & $ai & " = " & reference & "\n"
    if task.argumentCount > 0:
      var checks: seq[string]
      for ai in 0'u32 ..< task.argumentCount: checks.add "not arg" & $ai & ".isBound"
      f.add "  if " & checks.join(" or ") & ":\n    " & endFailed & "\n    return 0\n"
    for slot in targetMethod.variableSlotMask.slots:
      f.add "  ex.v[" & $slot & "] = Atom()\n"
    f.add "  ex.enterFrame()\n"
    for ai in 0'u32 ..< task.argumentCount:
      let parameter = ir.values[targetMethod.firstParameter + ai]
      if parameter.kind != vkVariable or parameter.variableSlot == NoIndex:
        ir.setError("Generated compound target parameter has no variable slot")
        return
      f.add "  ex.setIfChanged(" & $parameter.variableSlot & ", arg" & $ai & ")\n"
    f.add "  ex.frame().resume = 1\n  ex.next = method" & $target & "\n  return 2\n\n"
  g.funcs.add f

# ---------------------------------------------------------------------------
# Methods and conditions

proc debug(m: MethodGen, calls: varargs[string]) =
  ## Emits debugger event calls.
  for call in calls: m.w.code(call)

proc beginCondition(m: MethodGen, condition: uint32) = m.debug(debugEvent("BeginCondition", condition))

proc endCondition(m: MethodGen, outcome: auto) = m.debug(debugEvent("EndCondition", outcome))

proc declareCheckpoint(m: MethodGen, plan: CheckpointPlan) =
  for slot in plan.slots: m.w.declare("cp" & $plan.id & "_" & $slot, "Atom")

proc pushCheckpoint(m: MethodGen, plan: CheckpointPlan) =
  for slot in plan.slots: m.w.code("cp" & $plan.id & "_" & $slot & " = ex.v[" & $slot & "]")

proc rollbackCheckpoint(m: MethodGen, plan: CheckpointPlan) =
  for slot in plan.slots: m.w.code("ex.v[" & $slot & "] = cp" & $plan.id & "_" & $slot)

proc unboundGuard(m: MethodGen, v: IRValue, failure: int) =
  if v.kind != vkVariable or v.variableSlot == NoIndex:
    m.w.jump(failure)
    return
  m.w.open("if ex.v[" & $v.variableSlot & "].isBound:")
  m.w.jump(failure)
  m.w.close()

proc emitDeterministicFact(m: MethodGen, condition: uint32, success, failure: int) =
  let ir = m.g.ir
  let c = ir.conditions[condition]
  if c.resolvedIndex == NoIndex:
    ir.setError("Generated fact has no compile-time fact slot")
    return
  let table = "ft.tables[" & $c.argumentCount & "]"
  var checks: seq[string]
  for i in 0'u32 ..< c.argumentCount:
    let index = c.firstArgument + i
    if int(index) >= ir.values.len: continue
    let v = ir.values[index]
    if v.kind == vkVariable and v.variableSlot == NoIndex: continue
    let cell = table & ".rows[row][" & $i & "]"
    if v.kind == vkVariable:
      # Statically bound variable (unbound variables make the fact a choice
      # point instead).
      checks.add "if not equal(" & cell & ", ex.v[" & $v.variableSlot & "]): continue"
      continue
    checks.add "if not " & m.g.staticFactMatch(index, cell) & ": continue"
  let w = m.w
  w.open("block:")
  m.beginCondition(condition)
  w.code("let ft = ex.factTables[" & $c.resolvedIndex & "]")
  w.code("var matched = false")
  w.open("for row in 0 ..< " & table & ".rows.len:")
  for check in checks: w.code(check)
  w.code("matched = true")
  w.code("break")
  w.close()
  m.endCondition("matched")
  w.open("if matched:")
  w.jump(success)
  w.close()
  w.jump(failure)
  w.close()

proc emitLeaf(m: MethodGen, condition: uint32, bound: BoundSet, success, failure: int) =
  let ir = m.g.ir
  let g = m.g
  let c = ir.conditions[condition]
  let w = m.w
  w.comment(c.domainExpression)
  case c.kind
  of irAssignment:
    let output = ir.values[c.outputValue]
    let input = ir.values[c.firstArgument]
    let reference = if input.kind == vkArithmetic: g.arithmeticValue(input, defaultValueContext)
                    else: g.valueRef(c.firstArgument, defaultValueContext)
    m.beginCondition(condition)
    w.open("block:")
    w.code("let value = " & reference)
    w.open("if value.isBound and not ex.v[" & $output.variableSlot & "].isBound:")
    w.code("ex.v[" & $output.variableSlot & "] = value")
    m.endCondition(true)
    w.jump(success)
    w.close()
    m.endCondition(false)
    w.jump(failure)
    w.close()
    return
  of irComparison:
    if c.argumentCount != 2:
      ir.setError("Built-in comparison must contain exactly two operands")
      return
    let (staticResult, isStatic) = staticComparison(ir, c)
    if isStatic:
      m.beginCondition(condition)
      m.endCondition(staticResult)
      w.jump(if staticResult: success else: failure)
      return
    m.beginCondition(condition)
    let left = g.argumentValue(c.firstArgument, defaultValueContext)
    let right = g.argumentValue(c.firstArgument + 1, defaultValueContext)
    w.open("if compare(" & left & ", " & right & ", " & $c.id & "'u32):")
    m.endCondition(true)
    w.jump(success)
    w.close()
    m.endCondition(false)
    w.jump(failure)
    return
  of irListSplit:
    if c.argumentCount != 3:
      ir.setError("Built-in list split must contain exactly three arguments")
      return
    let elementOutput = ir.values[c.firstArgument + 1]
    let remainderOutput = ir.values[c.firstArgument + 2]
    let listReference = g.valueRef(c.firstArgument, defaultValueContext)
    let elementReference = g.valueRef(c.firstArgument + 1, defaultValueContext)
    let remainderReference = g.valueRef(c.firstArgument + 2, defaultValueContext)
    let direction = if c.id == ListSplitBack: "splitBack" else: "splitFront"
    let elementVariable = elementOutput.kind == vkVariable and elementOutput.variableSlot != NoIndex
    let remainderVariable = remainderOutput.kind == vkVariable and remainderOutput.variableSlot != NoIndex
    m.beginCondition(condition)
    w.open("block:")
    w.code("var valid = false")
    w.code("let (element, remainder, ok) = splitList(" & listReference & ", " & direction & ")")
    w.open("if ok:")
    w.code("valid = true")
    if elementVariable:
      w.code("if ex.v[" & $elementOutput.variableSlot & "].isBound and not equal(ex.v[" &
        $elementOutput.variableSlot & "], element): valid = false")
    else:
      w.code("if not " & elementReference & ".isBound or not equal(" & elementReference & ", element): valid = false")
    if remainderVariable:
      w.code("if ex.v[" & $remainderOutput.variableSlot & "].isBound and not equal(ex.v[" &
        $remainderOutput.variableSlot & "], remainder): valid = false")
    else:
      w.code("if not " & remainderReference & ".isBound or not equal(" & remainderReference &
        ", remainder): valid = false")
    if elementVariable and remainderVariable and elementOutput.variableSlot == remainderOutput.variableSlot:
      w.code("if not ex.v[" & $elementOutput.variableSlot & "].isBound and not equal(element, remainder): valid = false")
    w.open("if valid:")
    if elementVariable:
      w.code("if not ex.v[" & $elementOutput.variableSlot & "].isBound: ex.v[" & $elementOutput.variableSlot &
        "] = element")
    if remainderVariable:
      w.code("if not ex.v[" & $remainderOutput.variableSlot & "].isBound: ex.v[" & $remainderOutput.variableSlot &
        "] = remainder")
    if not elementVariable and not remainderVariable:
      w.code("discard")
    w.close()
    w.close()
    m.endCondition("valid")
    w.open("if valid:")
    w.jump(success)
    w.close()
    w.jump(failure)
    w.close()
    return
  of irFact:
    m.emitDeterministicFact(condition, success, failure)
    return
  else:
    discard
  var arguments: seq[string]
  for i in 0'u32 ..< c.argumentCount:
    arguments.add g.argumentValue(c.firstArgument + i, defaultValueContext)
  case c.kind
  of irCall:
    if int(c.resolvedIndex) >= ir.callTermStringIDs.len:
      ir.setError("Generated callterm condition has invalid callterm slot")
      return
    let source = g.callSource(c.source)
    m.beginCondition(condition)
    w.open("block:")
    w.code("let (callResult, ok) = ex.invoke(" & $c.resolvedIndex & ", " & atomList(arguments) & ", addr " & source &
      ", factSymbols)")
    w.open("if ok and callResult.isKind(akBool) and callResult.boolValue:")
    m.endCondition(true)
    w.jump(success)
    w.close()
    w.close()
    m.endCondition(false)
    w.jump(failure)
  of irCallBind:
    if int(c.resolvedIndex) >= ir.callTermStringIDs.len:
      ir.setError("Generated call-bind condition has invalid callterm slot")
      return
    if c.outputValue == NoIndex or int(c.outputValue) >= ir.values.len or ir.values[c.outputValue].kind != vkVariable:
      ir.setError("Generated call-bind output is not a variable")
      return
    let output = ir.values[c.outputValue]
    let source = g.callSource(c.source)
    m.beginCondition(condition)
    w.open("if not ex.v[" & $output.variableSlot & "].isBound:")
    w.code("let (callResult, ok) = ex.invoke(" & $c.resolvedIndex & ", " & atomList(arguments) & ", addr " & source &
      ", factSymbols)")
    w.open("if ok:")
    w.code("ex.setIfChanged(" & $output.variableSlot & ", callResult)")
    m.endCondition(true)
    w.jump(success)
    w.close()
    w.close()
    m.endCondition(false)
    w.jump(failure)
  else:
    ir.setError("HTNTranslator attempted to emit an unsupported generated-condition fallback for condition " &
      $condition)

proc emitConditionContinuation(m: MethodGen, condition: uint32, bound: BoundSet, failure: int, success: SuccessFn)

proc emitSequence(m: MethodGen, andIndex: uint32, offset: uint32, bound: BoundSet, failure: int, success: SuccessFn) =
  let andCondition = m.g.ir.conditions[andIndex]
  if offset == andCondition.childCount:
    success(failure, proc () = discard)
    return
  let child = m.g.ir.conditionChildRefs[andCondition.firstChildRef + offset]
  m.emitConditionContinuation(child, bound, failure, proc (retry: int, commit: CommitFn) =
    m.emitSequence(andIndex, offset + 1, analyzeCondition(m.g.ir, child, bound).boundAfter, retry,
      proc (suffixRetry: int, suffixCommit: CommitFn) =
        success(suffixRetry, proc () =
          suffixCommit()
          commit())))

proc emitAxiomCall(m: MethodGen, condition: uint32, fail: int, checkpoint: CheckpointPlan, succeed: SuccessFn) =
  let ir = m.g.ir
  let w = m.w
  let c = ir.conditions[condition]
  if c.resolvedIndex == NoIndex or int(c.resolvedIndex) >= ir.axioms.len:
    ir.setError("Generated axiom scope has no resolved axiom")
    return
  let axiom = ir.axioms[c.resolvedIndex]
  let scope = "as" & $condition & "_" & $fail
  let bodyFailure = m.g.newLabel()
  var bodyBound = initHashSet[uint32]()
  for i in 0'u32 ..< axiom.parameterCount:
    let parameter = ir.values[axiom.firstParameter + i]
    let name = ir.strings.get(parameter.text)
    if name.startsWith("inp_"): bodyBound.incl parameter.text
    if name.startsWith("out_"): m.unboundGuard(ir.values[c.firstArgument + i], fail)
  let maskSlots = axiom.variableSlotMask.slots
  w.declare(scope, "AxiomScope")
  w.declare(scope & "Frame", "uint64")
  w.code(scope & ".saved.setLen(" & $maskSlots.len & ")")
  w.code(scope & ".args.setLen(" & $c.argumentCount & ")")
  w.code("axiomBegin" & $condition & "(ex, " & scope & ")")
  w.code(scope & "Frame = ex.currentFrameID")
  m.emitConditionContinuation(axiom.condition, bodyBound, bodyFailure, proc (retry: int, commit: CommitFn) =
    # Suspend the axiom's local frame while the caller checks its suffix.
    let locals = CheckpointPlan(id: m.g.newLabel(), slots: maskSlots)
    m.declareCheckpoint(locals)
    m.pushCheckpoint(locals)
    w.declare(scope & "Copy", "AxiomScope")
    w.code(scope & "Copy = " & scope)
    let resume = m.g.newLabel()
    w.open("if not axiomEnd" & $condition & "(ex, true, " & scope & "Copy):")
    w.jump(resume)
    w.close()
    succeed(resume, proc () = commit())
    w.label(resume)
    m.rollbackCheckpoint(checkpoint)
    m.pushCheckpoint(checkpoint)
    m.rollbackCheckpoint(locals)
    w.code("ex.currentFrameID = " & scope & "Frame")
    m.debug(debugEvent("BeginAxiom", c.resolvedIndex))
    w.jump(retry))
  w.label(bodyFailure)
  w.code("axiomEnd" & $condition & "(ex, false, " & scope & ")")
  w.jump(fail)

proc emitConditionContinuation(m: MethodGen, condition: uint32, bound: BoundSet, failure: int, success: SuccessFn) =
  if condition == NoIndex:
    success(failure, proc () = discard)
    return
  let ir = m.g.ir
  let w = m.w
  let c = ir.conditions[condition]
  w.comment(c.domainExpression)
  let analysis = analyzeCondition(ir, condition, bound)
  let fail = m.g.newLabel()
  let checkpoint = buildCheckpointPlan(ir, m.g.newLabel(), condition, bound)
  m.declareCheckpoint(checkpoint)
  m.pushCheckpoint(checkpoint)
  # Composite conditions report their own events; leaves report theirs where
  # they are evaluated.
  let composite = c.kind in {irAnd, irOr, irAlt, irNot, irAxiom}
  if composite: m.beginCondition(condition)
  if c.assignmentGuardValue != NoIndex:
    m.unboundGuard(ir.values[c.assignmentGuardValue], fail)
  let succeed: SuccessFn = proc (retry: int, commit: CommitFn) =
    if not composite:
      success(retry, proc () = commit())
      return
    # A retry re-enters the composite condition.
    let resume = m.g.newLabel()
    m.endCondition(true)
    success(resume, proc () = commit())
    w.label(resume)
    m.beginCondition(condition)
    w.jump(retry)
  if c.kind == irAnd:
    m.emitSequence(condition, 0, bound, fail, succeed)
  elif c.kind == irOr or c.kind == irAlt:
    let runtimeAlt = c.kind == irAlt and ir.runtimeBacktrackingSupport
    let altVar = "alt" & $fail
    if runtimeAlt:
      w.declare(altVar, "bool")
      w.code(altVar & " = false")
    for i in 0'u32 ..< c.childCount:
      let next = m.g.newLabel()
      let isOr = c.kind == irOr
      m.emitConditionContinuation(ir.conditionChildRefs[c.firstChildRef + i], bound, next,
        proc (retry: int, commit: CommitFn) =
          if isOr:
            # OR commits to its first successful child.
            commit()
            succeed(fail, proc () = discard)
          else:
            if runtimeAlt: w.code(altVar & " = true")
            succeed(retry, commit))
      w.label(next)
      if runtimeAlt:
        w.open("if " & altVar & " and (ex.ctx.backtrackingMode and bmFactsAndAxioms) == 0:")
        w.jump(fail)
        w.close()
    w.jump(fail)
  elif c.kind == irNot:
    let absent = m.g.newLabel()
    if c.childCount > 0:
      m.emitConditionContinuation(ir.conditionChildRefs[c.firstChildRef], bound, absent,
        proc (retry: int, commit: CommitFn) =
          commit()
          w.jump(fail))
    else:
      w.jump(absent)
    w.label(absent)
    succeed(fail, proc () = discard)
  elif c.kind == irAxiom:
    m.emitAxiomCall(condition, fail, checkpoint, succeed)
  elif c.kind == irFact and analysis.mayProduceMultipleValues:
    let retry = m.g.newLabel()
    let next = m.g.newLabel()
    let cursor = "fc" & $retry
    w.declare(cursor, "uint32")
    w.code(cursor & " = 0")
    w.label(next)
    m.beginCondition(condition)
    w.code("inc " & cursor)
    w.open("if not factChoice" & $condition & "(ex, " & cursor & " - 1):")
    m.endCondition(false)
    w.jump(fail)
    w.close()
    m.endCondition(true)
    succeed(retry, proc () = discard)
    w.label(retry)
    if ir.runtimeBacktrackingSupport:
      w.open("if (ex.ctx.backtrackingMode and bmFactsAndAxioms) == 0:")
      w.jump(fail)
      w.close()
    m.rollbackCheckpoint(checkpoint)
    m.pushCheckpoint(checkpoint)
    w.jump(next)
  else:
    let matched = m.g.newLabel()
    m.emitLeaf(condition, bound, matched, fail)
    w.label(matched)
    succeed(fail, proc () = discard)
  w.label(fail)
  m.rollbackCheckpoint(checkpoint)
  if composite: m.endCondition(false)
  w.jump(failure)

proc emitMethod(g: Generator, methodIndex: uint32) =
  let ir = g.ir
  let meth = ir.methods[methodIndex]
  var f = "# method" & $methodIndex & ": " & nimComment(ir.strings.get(meth.id)) & "/" & $meth.parameterCount & "\n"
  f.add "proc method" & $methodIndex & "(ex: Exec): int {.nimcall.} =\n"
  let beginMethod = debugEvent("BeginMethod", methodIndex)
  let endMethodFailed = debugEvent("EndMethod", false)
  let endBranchFailed = debugEvent("EndBranch", false)
  if meth.branchCount == 0:
    f.add "  " & beginMethod & "\n  " & endMethodFailed & "\n  return 0\n\n"
    g.funcs.add f
    return
  var bound = initHashSet[uint32]()
  for i in 0'u32 ..< meth.parameterCount:
    let parameter = ir.values[meth.firstParameter + i]
    if parameter.kind == vkVariable: bound.incl parameter.text
  let slots = meth.variableSlotMask.slots
  let methodSlots = "ms" & $methodIndex
  var needsSlots = false
  let w = newFnWriter()
  let m = MethodGen(g: g, w: w)
  let methodFailure = g.newLabel()
  var branchLabels = newSeq[int](meth.branchCount)
  for i in 0 ..< branchLabels.len: branchLabels[i] = g.newLabel()
  var resumeLabels = newSeq[int](meth.branchCount)
  var resumeCases: seq[(int, int)]
  var pinned = initHashSet[int]()
  for bi in 0'u32 ..< meth.branchCount:
    if ir.branches[meth.firstBranch + bi].taskCount == 0: continue
    resumeLabels[bi] = g.newLabel()
    resumeCases.add (int(bi) + 1, resumeLabels[bi])
    pinned.incl resumeLabels[bi]
  m.debug(beginMethod)
  w.jump(branchLabels[0])
  for bi in 0'u32 ..< meth.branchCount:
    let branchIndex = meth.firstBranch + bi
    let branch = ir.branches[branchIndex]
    let branchSuccess = g.newLabel()
    let branchFailed = g.newLabel()
    let branchFailure = if bi + 1 < meth.branchCount: branchLabels[bi + 1] else: methodFailure
    let canRetry = bi + 1 < meth.branchCount and branch.taskCount != 0
    w.label(branchLabels[bi])
    w.comment("branch " & ir.strings.get(branch.id))
    if canRetry:
      needsSlots = true
      w.code("ex.saveRetry(fr, " & methodSlots & ")")
    m.debug(debugEvent("BeginBranch", branchIndex))
    if branch.condition == NoIndex:
      w.jump(branchSuccess)
    else:
      m.emitConditionContinuation(branch.condition, bound, branchFailed, proc (retry: int, commit: CommitFn) =
        commit()
        w.jump(branchSuccess))
    w.label(branchFailed)
    if canRetry: w.code("ex.releaseRetry(fr)")
    m.debug(endBranchFailed)
    w.jump(branchFailure)
    w.label(branchSuccess)
    if branch.taskCount != 0:
      w.open("if not ex.pushBranch(addr bc" & $branchIndex & "):")
      if canRetry: w.code("ex.releaseRetry(fr)")
      m.debug(endBranchFailed, endMethodFailed)
      w.ret("0")
      w.close()
      for ti in 0'u32 ..< branch.taskCount:
        m.debug("ex.debugCapturePendingTask(" & $(branch.firstTask + (branch.taskCount - 1 - ti)) & ")")
      let base = if canRetry: "fr.retryPendingBase" else: "0"
      let loop = g.newLabel()
      let commitLabel = g.newLabel()
      w.label(loop)
      w.open("if ex.pendingCount > " & base & ":")
      w.code("let next = ex.popPending()")
      w.open("if next != nil:")
      w.code("fr.resume = " & $(bi + 1))
      w.code("ex.next = next")
      w.ret("2")
      w.close()
      w.code("fr.childResult = 0")
      w.jump(resumeLabels[bi])
      w.close()
      w.jump(commitLabel)
      w.label(resumeLabels[bi])
      w.open("if fr.childResult == 0:")
      if canRetry:
        w.open("if ex.failureState != dsNoPlan:")
        w.code("ex.releaseRetry(fr)")
        m.debug(endBranchFailed, endMethodFailed)
        w.ret("0")
        w.close()
        if ir.runtimeBacktrackingSupport:
          w.open("if (ex.ctx.backtrackingMode and bmBranches) == 0:")
          w.code("ex.releaseRetry(fr)")
          m.debug(endBranchFailed, endMethodFailed)
          w.ret("0")
          w.close()
        w.code("ex.restoreRetry(fr, " & methodSlots & ")")
        m.debug(endBranchFailed)
        w.jump(branchFailure)
      else:
        m.debug(endBranchFailed, endMethodFailed)
        w.ret("0")
      w.close()
      w.jump(loop)
      w.label(commitLabel)
    if canRetry: w.code("ex.releaseRetry(fr)")
    m.debug(debugEvent("EndBranch", true), debugEvent("EndMethod", true))
    w.ret("1")
  w.label(methodFailure)
  m.debug(endMethodFailed)
  w.ret("0")
  if needsSlots:
    g.top.add "let " & methodSlots & ": seq[uint32] = " & slotList(slots) & "\n"
  f.add "  let fr = ex.frame()\n"
  w.render(f, pinned, resumeCases)
  f.add "\n"
  g.funcs.add f

proc emitEntryPoint(g: Generator) =
  let ir = g.ir
  var f = "proc decomposeCall(ctx: var Context, call: Atom, requireTopLevel: bool): (Atom, DecompositionStatus) {.nimcall.} =\n"
  f.add "  let empty = emptyList()\n"
  f.add "  if ctx.execution == nil or ctx.prepared == nil: return (empty, dsInvalidContext)\n"
  f.add "  let ex = ctx.execution\n  ex.resetDiagnostics()\n"
  f.add "  if ctx.worldState == nil or ctx.bindings == nil: return (empty, dsInvalidContext)\n"
  f.add "  if not call.isKind(akList) or call.len < 1: return (empty, dsInvalidCall)\n"
  f.add "  let head = call.at(0)[0]\n  if not head.isKind(akSymbol): return (empty, dsInvalidCall)\n"
  f.add "  let argumentCount = call.len - 1\n"
  f.add "  ex.begin(ctx, factSymbols, callNames)\n  var entry = -1\n"
  var cases = ""
  for mi, meth in ir.methods:
    if not meth.isExternallyDecomposable: continue
    f.add "  if entry < 0 and head.symbolValue == " & g.symbol(ir.strings.get(meth.id)) & " and argumentCount == " &
      $meth.parameterCount & ":\n"
    if not meth.isTopLevel:
      f.add "    if requireTopLevel: return (empty, dsInvalidCall)\n"
    f.add "    entry = " & $mi & "\n"
    cases.add "  of " & $mi & ":\n"
    for pi in 0'u32 ..< meth.parameterCount:
      let parameter = ir.values[meth.firstParameter + pi]
      if parameter.kind != vkVariable or parameter.variableSlot == NoIndex:
        ir.setError("Generated top-level method parameter has no variable slot")
        return
      cases.add "    block:\n      let argument = call.at(" & $(pi + 1) & ")[0]\n"
      cases.add "      if not argument.isBound: return (empty, dsInvalidCall)\n"
      cases.add "      ex.v[" & $parameter.variableSlot & "] = argument\n"
    cases.add "    runResult = ex.run(method" & $mi & ")\n"
  f.add "  if entry < 0: return (empty, dsInvalidCall)\n"
  f.add "  " & debugEvent("BeginPlan", "uint32(entry)") & "\n"
  f.add "  var runResult = 0\n"
  if cases.len > 0:
    f.add "  case entry\n" & cases & "  else: discard\n"
  f.add "  if runResult == 0:\n    " & debugEvent("EndPlan", false) & "\n    return (empty, ex.failureState)\n"
  f.add "  while ex.pendingCount != 0:\n    let next = ex.popPending()\n    if next == nil: break\n"
  f.add "    if ex.run(next) == 0:\n      " & debugEvent("EndPlan", false) & "\n      return (empty, ex.failureState)\n"
  f.add "  " & debugEvent("EndPlan", true) & "\n"
  f.add "  (ex.planAtom(), dsSucceeded)\n"
  g.funcs.add f

proc emitDebugMetadata(g: Generator): string =
  ## `debugTables`, the compiled-domain description consumed by debuggers
  ## (HTN_DEBUG_DECOMPOSITION metadata), compiled only with -d:htnDebug.
  ## Records are flattened into the rows of DebugTables.
  let ir = g.ir
  proc index(v: uint32): string = (if v == NoIndex: "NoIndex" else: $v)
  proc wideIndex(v: uint32): string = (if v == NoIndex: "uint64(NoIndex)" else: $v)
  proc source(s: SourceLocation): seq[string] =
    @[$s.fileIndex, $s.range.first.line, $s.range.first.column, $s.range.last.line, $s.range.last.column]
  proc maskWords(mask: SlotMask): seq[string] =
    for w in mask: result.add "0x" & toHex(w, 16).toLowerAscii & "'u64"
  var output = "when htnDebugEnabled:\n  proc debugTables(): DebugTables =\n    DebugTables(\n"
  output.add "      sourceFile: " & nimQuote(g.displaySourceFile()) & ",\n"
  proc table(name, typ, suffix: string, rows: seq[seq[string]]) =
    ## One table field; the first number carries the element type.
    if rows.len == 0:
      output.add "      " & name & ": newSeq[" & typ & "](),\n"
      return
    output.add "      " & name & ": @[\n"
    for i, row in rows:
      var cells = row
      if i == 0 and suffix.len > 0 and cells[0].len > 0 and cells[0][0] in {'0' .. '9'} and "'" notin cells[0]:
        cells[0].add suffix
      output.add "        " & cells.join(", ") & (if i + 1 < rows.len: ",\n" else: "],\n")
  proc strings(values: seq[string]): seq[seq[string]] =
    for v in values: result.add @[nimQuote(v)]
  table("strings", "string", "", strings(ir.strings.values))
  var rows: seq[seq[string]]
  for v in ir.values:
    var flags = 0'u32
    if v.kind == vkVariable and v.debugAsVariable: flags = flags or 1
    if v.kind == vkLiteral and v.atomType == akString: flags = flags or 2
    if v.kind == vkVariable and not v.debugAsVariable and v.debugText != v.text: flags = flags or 4
    rows.add @[$flags, index(v.debugText), index(v.text), $v.sourceLine, index(v.variableSlot)]
  table("values", "uint32", "'u32", rows)
  rows = @[]
  for id in ir.variableStringIDs:
    rows.add @[if ir.debugInternalVariableStringIDs.getOrDefault(id): "NoIndex" else: index(id)]
  table("variableStringIDs", "uint32", "'u32", rows)
  rows = @[]
  var expressions: seq[string]
  for v in ir.conditions:
    let d = if v.debugCondition != NoIndex: ir.conditions[v.debugCondition] else: v
    rows.add @[$ord(d.kind), index(d.id), index(d.firstArgument), index(d.argumentCount), index(v.firstChildRef),
      index(v.childCount), index(d.outputValue), index(d.resolvedIndex), $v.debugSource.range.first.line,
      (if v.debugInternal: "1" else: "0")]
    expressions.add v.debugExpression
  table("conditions", "uint32", "'u32", rows)
  table("conditionExpressions", "string", "", strings(expressions))
  rows = @[]
  for reference in ir.conditionChildRefs: rows.add @[index(reference)]
  table("conditionChildRefs", "uint32", "'u32", rows)
  rows = @[]
  for t in ir.tasks:
    rows.add @[$ord(t.kind), index(t.id), index(t.firstArgument), index(t.argumentCount), $t.sourceLine,
      index(t.planStepHead)]
  table("tasks", "uint32", "'u32", rows)
  rows = @[]
  for b in ir.branches:
    rows.add @[index(b.id), index(b.condition), index(b.firstTask), index(b.taskCount), $b.sourceLine]
  table("branches", "uint32", "'u32", rows)
  rows = @[]
  for m in ir.methods:
    rows.add @[wideIndex(m.id), wideIndex(m.firstParameter), wideIndex(m.parameterCount), wideIndex(m.firstBranch),
      wideIndex(m.branchCount), $m.sourceLine] & maskWords(m.variableSlotMask)
  table("methods", "uint64", "'u64", rows)
  rows = @[]
  for a in ir.axioms:
    rows.add @[wideIndex(a.id), wideIndex(a.firstParameter), wideIndex(a.parameterCount), wideIndex(a.condition),
      $a.sourceLine] & maskWords(a.variableSlotMask)
  table("axioms", "uint64", "'u64", rows)
  rows = @[]
  for c in ir.constants: rows.add @[index(c.groupID), index(c.id), index(c.value), $c.sourceLine]
  table("constants", "uint32", "'u32", rows)
  output.add "      callTermSlotCount: " & $ir.callTermStringIDs.len & ", factSlotCount: " & $ir.factStringIDs.len & ",\n"
  let sourceFiles = if ir.sourceFiles.len == 0: @[g.displaySourceFile()] else: ir.sourceFiles
  table("sourceFiles", "string", "", strings(sourceFiles))
  proc sources(name: string, locations: seq[SourceLocation]) =
    var rows: seq[seq[string]]
    for location in locations: rows.add source(location)
    table(name, "uint32", "'u32", rows)
  var locations: seq[SourceLocation]
  for v in ir.values: locations.add v.source
  sources("valueSources", locations)
  locations = @[]
  for c in ir.conditions: locations.add c.debugSource
  sources("conditionSources", locations)
  locations = @[]
  for t in ir.tasks: locations.add t.source
  sources("taskSources", locations)
  locations = @[]
  for b in ir.branches: locations.add b.source
  sources("branchSources", locations)
  locations = @[]
  for m in ir.methods: locations.add m.source
  sources("methodSources", locations)
  locations = @[]
  for a in ir.axioms: locations.add a.source
  sources("axiomSources", locations)
  locations = @[]
  for c in ir.constants: locations.add c.source
  sources("constantSources", locations)
  output.setLen(output.len - 2) # the last ",\n"
  output.add ")\n\n"
  output

proc makeSource(g: Generator): string =
  let ir = g.ir
  var output = "# Code generated by " & g.options.toolName & ". DO NOT EDIT.\n"
  output.add "# Source domain: " & nimComment(g.displaySourceFile()) & "\n"
  if g.options.linkedSourceFiles.len > 1:
    output.add "# Linked domain sources:\n"
    for file in g.options.linkedSourceFiles: output.add "#   " & nimComment(file) & "\n"
  output.add "\n{.warning[UnusedImport]: off.}\n{.warning[UnreachableCode]: off.}\n{.hint[XDeclaredButNotUsed]: off.}\n\n"
  output.add "import htn/[atom, callterm, planner, worldstate]\n\n"

  var statics = ""
  for i, sv in ir.staticValues:
    let literal = if sv.literal.isBound: g.atomLiteral(sv.literal)
                  else: "newString(" & nimQuote(ir.strings.get(sv.text)) & ")"
    statics.add "let " & staticName(uint32(i)) & " = " & literal & "\n"

  var factSymbols, factNames, callNames: seq[string]
  for id in ir.factStringIDs:
    factSymbols.add g.symbol(ir.strings.get(id))
    factNames.add nimQuote(ir.strings.get(id))
  for id in ir.callTermStringIDs: callNames.add nimQuote(ir.strings.get(id))

  let (restoreSlots, maxRestore) = g.taskRestoreSlots()
  var snapshotCapacity = int(g.options.backtrackingCapacity) * maxRestore
  if g.options.backtrackingPolicy != fixedCapacity and snapshotCapacity > MaxVariableSlots:
    snapshotCapacity = MaxVariableSlots

  let requirements = g.callTermRequirements()

  # Emission order mirrors the original generator so that the first
  # translation error is the same.
  for i, c in ir.conditions:
    if c.kind != irAxiom or c.resolvedIndex == NoIndex or int(c.resolvedIndex) >= ir.axioms.len: continue
    g.emitAxiomHelpers(uint32(i))
  for i, c in ir.conditions:
    if c.kind != irFact: continue
    var hasVariable = false
    for a in 0'u32 ..< c.argumentCount:
      if int(c.firstArgument + a) < ir.values.len and ir.values[c.firstArgument + a].kind == vkVariable:
        hasVariable = true
        break
    if hasVariable: g.emitFactChoiceHelper(uint32(i))
  g.emitBranchContinuations(restoreSlots)
  for t in 0 ..< ir.tasks.len: g.emitTask(uint32(t))
  for mi in 0 ..< ir.methods.len: g.emitMethod(uint32(mi))
  g.emitEntryPoint()

  for i, text in g.symbolOrder:
    output.add "let sym" & $i & " = intern(" & nimQuote(text) & ")\n"
  output.add "\n" & statics & "\n"
  output.add "let factSymbols: seq[Symbol] = @[" & factSymbols.join(", ") & "]\n"
  output.add "let callNames: seq[string] = @[" & callNames.join(", ") & "]\n\n"
  for site in g.callSites: output.add site & "\n"
  output.add "\n" & g.top
  for t in 0 ..< ir.tasks.len: output.add "proc task" & $t & "(ex: Exec): int {.nimcall.}\n"
  for mi in 0 ..< ir.methods.len: output.add "proc method" & $mi & "(ex: Exec): int {.nimcall.}\n"
  output.add "\n"

  let capacityError = g.displaySourceFile() & ": Domain '" & ir.domainID & "' exceeded its call-frame capacity (" &
    $g.options.callFrameCapacity & "). Regenerate the domain with " & g.options.toolName &
    " --call-frame-capacity=<larger value> and recompile the generated code."
  output.add "proc newExecution(): Exec {.nimcall.} =\n  newExec(Config(variableCount: " & $ir.variableStringIDs.len &
    ", factCount: " & $ir.factStringIDs.len & ", callTermCount: " & $ir.callTermStringIDs.len &
    ", callFrameCapacity: " & $g.options.callFrameCapacity & ", fixedCapacity: " &
    $(g.options.backtrackingPolicy == fixedCapacity) & ", backtrackingCapacity: " & $g.options.backtrackingCapacity &
    ", snapshotCapacity: " & $snapshotCapacity & ", capacityError: " & nimQuote(capacityError) & "))\n\n"
  output.add "type PreparedData = ref object of RootObj\n  domain: string\n\n"
  output.add "let prepared = PreparedData(domain: " & nimQuote(ir.domainID) & ")\n\n"
  output.add "proc newPrepared(): RootRef {.nimcall.} = prepared\n\n"
  output.add "var definition: Definition\n\n"
  output.add "proc " & g.options.entryPointName & "_GetDefinition*(): Definition =\n"
  output.add "  ## The generated planner definition of domain " & nimComment(ir.domainID) & ".\n  definition\n\n"
  output.add g.funcs & "\n"
  output.add g.initBody
  let features = if ir.runtimeBacktrackingSupport: "featureRuntimeBacktracking" else: "featureNone"
  output.add "definition = Definition(abiVersion: ABIVersion, features: " & features & ", domainID: " &
    nimQuote(ir.domainID) & ", sourceFile: " & nimQuote(g.displaySourceFile()) &
    ",\n  newPreparedStorage: newPrepared, newExecutionStorage: newExecution, decomposeCall: decomposeCall,\n" &
    "  factNames: @[" & factNames.join(", ") & "],\n  callTermRequirements: @[" & requirements.join(",\n    ") & "])\n"
  output.add "\n" & g.emitDebugMetadata()
  output.add "when htnDebugEnabled:\n  definition.debugMetadata = newDebugMetadata(debugTables())\n"
  output

proc isNimIdentifier(name: string): bool =
  if name.len == 0 or name[0] notin {'a' .. 'z', 'A' .. 'Z'} or name[^1] == '_' or "__" in name: return false
  for c in name:
    if c notin {'a' .. 'z', 'A' .. 'Z', '0' .. '9', '_'}: return false
  true

proc generate*(domain: Domain, options: Options): (string, string) =
  ## Emits the Nim source of a planner for a linked domain. Returns
  ## (source, error); the error is empty on success.
  var options = options
  if options.entryPointName.len == 0: return ("", "Entry point name must not be empty")
  if options.callFrameCapacity == 0: return ("", "Call frame capacity must be greater than zero")
  if options.backtrackingCapacity == 0: return ("", "Backtracking capacity must be greater than zero")
  if not isNimIdentifier(options.entryPointName):
    return ("", "Entry point must be a Nim identifier: " & options.entryPointName)
  if options.toolName.len == 0: options.toolName = "htn-translator"
  var sourceFiles = options.linkedSourceFiles
  if sourceFiles.len == 0:
    sourceFiles = @[if options.sourceFilePath.len == 0: "<domain>" else: options.sourceFilePath]
  let (ir, message) = buildIR(domain, sourceFiles, options.runtimeBacktrackingSupport)
  if ir == nil: return ("", message)
  let g = Generator(ir: ir, options: options)
  let source = g.makeSource()
  if ir.hasError: return ("", ir.error)
  (source, "")
