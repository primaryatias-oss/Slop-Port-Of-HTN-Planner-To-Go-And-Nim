## Generated execution debugger (port of HTNGeneratedDebugger): records the
## events of generated planners built with `-d:htnDebug` as a tree of
## source-level nodes. It depends only on the generated debug metadata.
##
## .. code-block:: nim
##   let debugger = newGeneratedDebugger()
##   debugger.setEnabled(true)
##   unit.setGeneratedDebugger(debugger)
##   discard unit.decompose()
##   for node in debugger.nodes: echo node.displayName

import std/[algorithm, strutils, tables]
import atom, debugmeta

type
  NodeKind* = enum
    nkPlan = "Plan", nkMethod = "Method", nkBranch = "Branch", nkFact = "Fact", nkAxiom = "Axiom", nkAnd = "And",
    nkOr = "Or", nkAlt = "Alt", nkNot = "Not", nkCall = "Call", nkCallBind = "CallBind",
    nkBuiltinComparison = "BuiltinComparison", nkBuiltinListSplit = "BuiltinListSplit", nkTask = "Task",
    nkUnknownCondition = "UnknownCondition"

  TokenKind* = enum
    ## Display class of a title token.
    tkNormal, tkResult, tkVariable, tkConstant, tkStringLiteral, tkCallExpression

  SourceLocation* = object
    domainPath*: string
    line*, column*, endLine*, endColumn*: uint32

  TitleToken* = object
    kind*: TokenKind
    text*: string
    spaceBefore*: bool

  VariableValue* = object
    ## One bound variable captured at an event.
    slot*: uint32
    name*: string
    value*: Atom

  ConstantValue* = object
    ## One constant referenced by a node.
    name*, value*: string

  Node* = object
    ## One recorded method, branch, condition, axiom, task or plan.
    eventNodeID*: uint32
    metadataIndex*: uint32
    parentEventNodeID*: uint32
    kind*: NodeKind
    source*: SourceLocation
    displayName*: string
    started*, completed*, succeeded*: bool
    titleTokens*: seq[TitleToken]
    constants*: seq[ConstantValue]
    variablesBefore*: seq[VariableValue]
    variablesAfter*: seq[VariableValue]
    scopeVariableMask*: seq[uint64]
    children*: seq[uint32]

  PendingTask = object
    metadataIndex, parentEventNodeID: uint32

  GeneratedDebugger* = ref object
    ## The event recorder.
    enabled: bool
    domainPath: string
    nodes: seq[Node]
    openNodes: seq[uint32]
    conditionVisibility: seq[bool]
      ## Hidden conditions still emit balanced events. Their nesting is kept
      ## separately so their End event cannot close a visible parent.
    pendingTasks: seq[PendingTask]
    revision: uint64

proc newGeneratedDebugger*(): GeneratedDebugger =
  ## A disabled debugger.
  GeneratedDebugger()

proc reset*(d: GeneratedDebugger, domainPath: string) =
  ## Starts a new capture for the domain at `domainPath`.
  d.domainPath = domainPath
  d.nodes.setLen(0)
  d.openNodes.setLen(0)
  d.conditionVisibility.setLen(0)
  d.pendingTasks.setLen(0)
  inc d.revision

proc setEnabled*(d: GeneratedDebugger, enabled: bool) =
  ## Enables capture; disabling also resets the capture.
  d.enabled = enabled
  if not enabled: d.reset("")

proc isEnabled*(d: GeneratedDebugger): bool = d.enabled
proc domainPath*(d: GeneratedDebugger): string = d.domainPath
proc revision*(d: GeneratedDebugger): uint64 = d.revision
  ## Changes on every reset.
proc nodes*(d: GeneratedDebugger): lent seq[Node] = d.nodes
  ## The recorded nodes; a node's eventNodeID is its index.

proc findNode*(d: GeneratedDebugger, id: uint32): ptr Node =
  ## The node with the given id, or nil.
  if int(id) < d.nodes.len: addr d.nodes[id] else: nil

# ---------------------------------------------------------------------------
# Titles

proc resolveString(m: DebugMetadata, id: uint32, fallback: string): string =
  if m != nil and int(id) < m.strings.len: m.strings[id] else: fallback

proc conditionKind(kind: uint32): NodeKind =
  case kind
  of dcFact: nkFact
  of dcAxiom: nkAxiom
  of dcAnd: nkAnd
  of dcOr: nkOr
  of dcAlt: nkAlt
  of dcNot: nkNot
  of dcCall: nkCall
  of dcAssignment, dcCallBind: nkCallBind
  of dcBuiltinComparison: nkBuiltinComparison
  of dcBuiltinListSplit: nkBuiltinListSplit
  else: nkUnknownCondition

proc builtinComparisonName(operator: uint32): string =
  case operator
  of 0: "=="
  of 1: "!="
  of 2: "<"
  of 3: "<="
  of 4: ">"
  of 5: ">="
  else: "comparison"

proc conditionName(kind: uint32): string =
  case kind
  of dcAnd: "and"
  of dcOr: "or"
  of dcAlt: "alt"
  of dcNot: "not"
  of dcCall: "call"
  of dcAssignment: "="
  of dcCallBind: "call-bind"
  of dcBuiltinComparison: "comparison"
  of dcBuiltinListSplit: "split_list"
  of dcAxiom: "axiom"
  of dcFact: "fact"
  else: "condition"

proc conditionDisplayName(m: DebugMetadata, c: DebugCondition): string =
  if c.kind == dcBuiltinComparison: builtinComparisonName(c.id)
  else: resolveString(m, c.id, conditionName(c.kind))

proc buildDeclarationTitle(m: DebugMetadata, declaration: string, firstParameter, parameterCount: uint32,
    node: var Node) =
  var title = "(:" & declaration & " (" & node.displayName
  var i = 0'u32
  while i < parameterCount and int(firstParameter + i) < m.values.len:
    title.add " " & resolveString(m, m.values[firstParameter + i].text, "?")
    inc i
  node.displayName = title & ") ...)"

proc applySourceLocation(node: var Node, m: DebugMetadata, ranges: seq[DebugSourceRange], index, count: uint32) =
  if m == nil or ranges.len == 0 or index >= count or int(index) >= ranges.len: return
  let r = ranges[index]
  if int(r.sourceFileIndex) < m.sourceFiles.len: node.source.domainPath = m.sourceFiles[r.sourceFileIndex]
  node.source.line = r.beginLine
  node.source.column = r.beginColumn
  node.source.endLine = r.endLine
  node.source.endColumn = r.endColumn

proc addTitleToken(node: var Node, kind: TokenKind, text: string) =
  if text.len == 0: return
  node.titleTokens.add TitleToken(kind: kind, text: text, spaceBefore: true)

proc addConstantValue(node: var Node, name, value: string) =
  for existing in node.constants:
    if existing.name == name: return
  node.constants.add ConstantValue(name: name, value: value)

proc addValueTitleToken(m: DebugMetadata, valueIndex: uint32, node: var Node) =
  if m == nil or int(valueIndex) >= m.values.len:
    node.addTitleToken(tkNormal, "<invalid>")
    return
  let value = m.values[valueIndex]
  # The metadata keeps the original domain expression (constant names, '?'
  # prefixes and string quotes) rather than the resolved value.
  let text = resolveString(m, value.text, "?")
  if (value.flags and dvVariable) != 0:
    node.addTitleToken(tkVariable, text)
  elif text.len > 0 and text[0] == ConstantPrefix:
    node.addTitleToken(tkConstant, text)
    node.addConstantValue(text, resolveString(m, value.resolvedText, "<unresolved>"))
  elif (value.flags and dvStringLiteral) != 0:
    node.addTitleToken(tkStringLiteral, text)
  elif (value.flags and dvCallExpression) != 0:
    node.addTitleToken(tkCallExpression, text)
  else:
    node.addTitleToken(tkNormal, text)

proc buildTaskTitleTokens(m: DebugMetadata, task: DebugTask, node: var Node) =
  node.titleTokens.setLen(0)
  var head: string
  if (task.kind == dtPrimitive or task.kind == dtDeferred) and task.planStepHead != NoIndex:
    head = resolveString(m, task.planStepHead, if task.kind == dtPrimitive: "!primitive" else: "&deferred")
  else:
    head = resolveString(m, task.id, if task.kind == dtPrimitive: "!primitive" else: "compound")
    if task.kind == dtPrimitive and (head.len == 0 or head[0] != PrimitiveTaskPrefix): head = "!" & head
  node.addTitleToken(tkNormal, "(" & head)
  for i in 0'u32 ..< task.argumentCount: addValueTitleToken(m, task.firstArgument + i, node)
  node.addTitleToken(tkNormal, ")")
  node.titleTokens[^1].spaceBefore = false
  var name = ""
  for token in node.titleTokens:
    if name.len > 0 and token.spaceBefore: name.add ' '
    name.add token.text
  node.displayName = name

proc buildConditionTitleTokens(m: DebugMetadata, condition: DebugCondition, node: var Node) =
  node.titleTokens.setLen(0)
  node.displayName = condition.expression
  let expression = condition.expression
  var head, callName = false
  # Split the compiler-formatted source for display only.
  var i = 0
  while i < expression.len:
    if expression[i] == ' ':
      inc i
      continue
    let begin = i
    let first = expression[i]
    inc i
    if first == '"':
      while i < expression.len:
        let c = expression[i]
        inc i
        if c == '\\' and i < expression.len: inc i
        elif c == '"': break
    elif first != '(' and first != ')':
      while i < expression.len and expression[i] notin {' ', '(', ')'}: inc i
    let text = expression[begin ..< i]
    var kind = tkNormal
    if first == VariablePrefix: kind = tkVariable
    elif first == ConstantPrefix: kind = tkConstant
    elif first == '"': kind = tkStringLiteral
    elif text == "call" or callName: kind = tkCallExpression
    elif head and first != ')': kind = tkResult
    node.addTitleToken(kind, text)
    node.titleTokens[^1].spaceBefore = begin == 0 or expression[begin - 1] == ' '
    head = first == '('
    callName = text == "call"
    if kind == tkConstant and m != nil:
      for v in m.values:
        if text == resolveString(m, v.text, ""):
          node.addConstantValue(text, resolveString(m, v.resolvedText, "<unresolved>"))
          break

# ---------------------------------------------------------------------------
# Scopes

proc markValueSlot(m: DebugMetadata, valueIndex: uint32, mask: var seq[uint64]) =
  if m == nil or int(valueIndex) >= m.values.len: return
  let value = m.values[valueIndex]
  if (value.flags and dvVariable) == 0 or value.variableSlot == NoIndex: return
  let word = int(value.variableSlot shr 6)
  if word >= mask.len: return
  mask[word] = mask[word] or (1'u64 shl (value.variableSlot and 63))

proc collectConditionScopeSlots(m: DebugMetadata, condition: uint32, mask: var seq[uint64], exported: bool) =
  ## Exported scopes skip NOT subtrees: their bindings are local and never
  ## part of the enclosing branch scope.
  if m == nil or int(condition) >= m.conditions.len: return
  let c = m.conditions[condition]
  for i in 0'u32 ..< c.argumentCount: markValueSlot(m, c.firstArgument + i, mask)
  if c.outputValue != NoIndex: markValueSlot(m, c.outputValue, mask)
  if (exported and c.kind == dcNot) or m.conditionChildRefs.len == 0: return
  for i in 0'u32 ..< c.childCount:
    let reference = c.firstChildRef + i
    if int(reference) < m.conditionChildRefs.len:
      collectConditionScopeSlots(m, m.conditionChildRefs[reference], mask, exported)

proc buildBranchScopeMask(m: DebugMetadata, branch: DebugBranch): seq[uint64] =
  result = newSeq[uint64](DebugSlotMaskWords)
  collectConditionScopeSlots(m, branch.condition, result, exported = true)
  if m.tasks.len == 0 or m.values.len == 0: return
  for i in 0'u32 ..< branch.taskCount:
    let taskIndex = branch.firstTask + i
    if int(taskIndex) >= m.tasks.len: continue
    let task = m.tasks[taskIndex]
    for a in 0'u32 ..< task.argumentCount: markValueSlot(m, task.firstArgument + a, result)

# ---------------------------------------------------------------------------
# Node tree

proc isConditionNodeKind(kind: NodeKind): bool =
  kind in {nkFact, nkAxiom, nkAnd, nkOr, nkAlt, nkNot, nkCall, nkCallBind, nkBuiltinComparison,
    nkBuiltinListSplit, nkUnknownCondition}

proc findPendingConditionChild(d: GeneratedDebugger, parent, condition: uint32): uint32 =
  if parent == NoIndex or int(parent) >= d.nodes.len: return NoIndex
  for child in d.nodes[parent].children:
    if int(child) >= d.nodes.len: continue
    let node = addr d.nodes[child]
    if not node.started and node.metadataIndex == condition and isConditionNodeKind(node.kind): return child
  NoIndex

proc createConditionMetadataChildren(d: GeneratedDebugger, m: DebugMetadata, parent: uint32,
    condition: DebugCondition)

proc createConditionMetadataNode(d: GeneratedDebugger, m: DebugMetadata, condition, parent: uint32): uint32 =
  ## Pre-creates the not-yet-started node of a condition and its subtree.
  if m == nil or int(condition) >= m.conditions.len: return NoIndex
  let debugCondition = m.conditions[condition]
  if debugCondition.internal: return NoIndex
  var node = Node(eventNodeID: uint32(d.nodes.len), metadataIndex: condition, parentEventNodeID: parent,
    kind: conditionKind(debugCondition.kind),
    source: SourceLocation(domainPath: d.domainPath, line: debugCondition.sourceLine,
      endLine: debugCondition.sourceLine))
  node.applySourceLocation(m, m.conditionSources, condition, uint32(m.conditions.len))
  node.displayName = conditionDisplayName(m, debugCondition)
  buildConditionTitleTokens(m, debugCondition, node)
  if parent != NoIndex and int(parent) < d.nodes.len:
    node.scopeVariableMask = d.nodes[parent].scopeVariableMask
    d.nodes[parent].children.add node.eventNodeID
  if debugCondition.kind == dcNot:
    if node.scopeVariableMask.len == 0: node.scopeVariableMask = newSeq[uint64](DebugSlotMaskWords)
    collectConditionScopeSlots(m, condition, node.scopeVariableMask, exported = false)
  d.nodes.add node
  result = uint32(d.nodes.len - 1)
  d.createConditionMetadataChildren(m, result, debugCondition)

proc createConditionMetadataChildren(d: GeneratedDebugger, m: DebugMetadata, parent: uint32,
    condition: DebugCondition) =
  if m == nil or m.conditionChildRefs.len == 0 or condition.childCount == 0: return
  for i in 0'u32 ..< condition.childCount:
    let reference = condition.firstChildRef + i
    if int(reference) >= m.conditionChildRefs.len: continue
    discard d.createConditionMetadataNode(m, m.conditionChildRefs[reference], parent)

proc beginNode(d: GeneratedDebugger, kind: NodeKind, metadataIndex, line: uint32, name: string,
    parentOverride: uint32) =
  var node = Node(eventNodeID: uint32(d.nodes.len), metadataIndex: metadataIndex, kind: kind, started: true,
    source: SourceLocation(domainPath: d.domainPath, line: line, endLine: line), displayName: name)
  node.parentEventNodeID =
    if parentOverride != NoIndex: parentOverride
    elif d.openNodes.len > 0: d.openNodes[^1]
    else: NoIndex
  if node.parentEventNodeID != NoIndex and int(node.parentEventNodeID) < d.nodes.len:
    node.scopeVariableMask = d.nodes[node.parentEventNodeID].scopeVariableMask
    d.nodes[node.parentEventNodeID].children.add node.eventNodeID
  d.nodes.add node
  d.openNodes.add uint32(d.nodes.len - 1)

proc lastNode(d: GeneratedDebugger): var Node = d.nodes[^1]

proc setCurrentNodeScopeMask(d: GeneratedDebugger, mask: SlotMaskWords) =
  if d.openNodes.len == 0: return
  d.nodes[d.openNodes[^1]].scopeVariableMask = @mask

proc addParentMethodParametersToScope(d: GeneratedDebugger, m: DebugMetadata, node: var Node) =
  if m == nil or m.methods.len == 0 or node.parentEventNodeID == NoIndex or
      int(node.parentEventNodeID) >= d.nodes.len:
    return
  let parent = addr d.nodes[node.parentEventNodeID]
  if (parent.kind != nkMethod and parent.kind != nkPlan) or int(parent.metadataIndex) >= m.methods.len: return
  let meth = m.methods[parent.metadataIndex]
  for i in 0'u32 ..< meth.parameterCount: markValueSlot(m, meth.firstParameter + i, node.scopeVariableMask)

proc captureCurrentVariables(d: GeneratedDebugger, m: DebugMetadata, values: openArray[Atom], before: bool) =
  if not d.enabled or d.openNodes.len == 0 or m == nil or values.len == 0 or m.variableStringIDs.len == 0: return
  let current = addr d.nodes[d.openNodes[^1]]
  var captured: seq[VariableValue]
  for slot in 0 ..< min(values.len, m.variableStringIDs.len):
    if m.variableStringIDs[slot] == NoIndex or not values[slot].isBound: continue
    if current.scopeVariableMask.len > 0:
      let word = slot shr 6
      if word >= current.scopeVariableMask.len or
          (current.scopeVariableMask[word] and (1'u64 shl (slot and 63))) == 0:
        continue
    captured.add VariableValue(slot: uint32(slot), name: resolveString(m, m.variableStringIDs[slot], "?"),
      value: values[slot])
  if before: current.variablesBefore = captured
  else: current.variablesAfter = captured

proc endNode(d: GeneratedDebugger, m: DebugMetadata, values: openArray[Atom], succeeded: bool) =
  if not d.enabled or d.openNodes.len == 0: return
  d.captureCurrentVariables(m, values, before = false)
  let id = d.openNodes.pop()
  d.nodes[id].completed = true
  d.nodes[id].succeeded = succeeded

# ---------------------------------------------------------------------------
# Events (HTNGeneratedEventDebug_*). `values` are the execution's variable
# slots; a slot is bound when its atom is bound.

proc beginPlan*(d: GeneratedDebugger, m: DebugMetadata, meth: uint32, values: openArray[Atom]) =
  if not d.enabled or m == nil or int(meth) >= m.methods.len: return
  let debugMethod = m.methods[meth]
  d.beginNode(nkPlan, meth, debugMethod.sourceLine, resolveString(m, debugMethod.id, "plan"), NoIndex)
  d.lastNode.displayName = "(" & d.lastNode.displayName & ")"
  d.lastNode.applySourceLocation(m, m.methodSources, meth, uint32(m.methods.len))
  d.setCurrentNodeScopeMask(debugMethod.variableSlotMask)
  d.captureCurrentVariables(m, values, before = true)

proc endPlan*(d: GeneratedDebugger, m: DebugMetadata, values: openArray[Atom], succeeded: bool) =
  d.endNode(m, values, succeeded)

proc beginMethod*(d: GeneratedDebugger, m: DebugMetadata, meth: uint32, values: openArray[Atom]) =
  if not d.enabled or m == nil or int(meth) >= m.methods.len: return
  let debugMethod = m.methods[meth]
  d.beginNode(nkMethod, meth, debugMethod.sourceLine, resolveString(m, debugMethod.id, "method"), NoIndex)
  buildDeclarationTitle(m, "method", debugMethod.firstParameter, debugMethod.parameterCount, d.lastNode)
  d.lastNode.applySourceLocation(m, m.methodSources, meth, uint32(m.methods.len))
  d.setCurrentNodeScopeMask(debugMethod.variableSlotMask)
  d.captureCurrentVariables(m, values, before = true)

proc endMethod*(d: GeneratedDebugger, m: DebugMetadata, values: openArray[Atom], succeeded: bool) =
  d.endNode(m, values, succeeded)

proc beginBranch*(d: GeneratedDebugger, m: DebugMetadata, branch: uint32, values: openArray[Atom]) =
  if not d.enabled or m == nil or int(branch) >= m.branches.len: return
  let debugBranch = m.branches[branch]
  d.beginNode(nkBranch, branch, debugBranch.sourceLine, resolveString(m, debugBranch.id, "branch"), NoIndex)
  d.lastNode.displayName = "(" & d.lastNode.displayName & " ...)"
  d.lastNode.applySourceLocation(m, m.branchSources, branch, uint32(m.branches.len))
  d.lastNode.scopeVariableMask = buildBranchScopeMask(m, debugBranch)
  var node = d.lastNode
  d.addParentMethodParametersToScope(m, node)
  d.lastNode.scopeVariableMask = node.scopeVariableMask
  d.captureCurrentVariables(m, values, before = true)

proc endBranch*(d: GeneratedDebugger, m: DebugMetadata, values: openArray[Atom], succeeded: bool) =
  d.endNode(m, values, succeeded)

proc capturePendingTask*(d: GeneratedDebugger, task: uint32) =
  ## Records the node that scheduled `task` (its parent when it runs).
  if not d.enabled: return
  let parent = if d.openNodes.len > 0: d.openNodes[^1] else: NoIndex
  d.pendingTasks.add PendingTask(metadataIndex: task, parentEventNodeID: parent)

proc beginTask*(d: GeneratedDebugger, m: DebugMetadata, task: uint32, values: openArray[Atom]) =
  if not d.enabled or m == nil or int(task) >= m.tasks.len: return
  let debugTask = m.tasks[task]
  var parentOverride = NoIndex
  for i in countdown(d.pendingTasks.len - 1, 0):
    if d.pendingTasks[i].metadataIndex == task:
      parentOverride = d.pendingTasks[i].parentEventNodeID
      d.pendingTasks.delete(i)
      break
  let fallback = if debugTask.kind == dtPrimitive: "primitive" else: "compound"
  d.beginNode(nkTask, task, debugTask.sourceLine, resolveString(m, debugTask.id, fallback), parentOverride)
  d.lastNode.applySourceLocation(m, m.taskSources, task, uint32(m.tasks.len))
  buildTaskTitleTokens(m, debugTask, d.lastNode)
  d.captureCurrentVariables(m, values, before = true)

proc endTask*(d: GeneratedDebugger, m: DebugMetadata, values: openArray[Atom], succeeded: bool) =
  d.endNode(m, values, succeeded)

proc beginAxiom*(d: GeneratedDebugger, m: DebugMetadata, axiom: uint32, values: openArray[Atom]) =
  if not d.enabled or m == nil or int(axiom) >= m.axioms.len: return
  let debugAxiom = m.axioms[axiom]
  d.beginNode(nkAxiom, axiom, debugAxiom.sourceLine, resolveString(m, debugAxiom.id, "axiom"), NoIndex)
  buildDeclarationTitle(m, "axiom", debugAxiom.firstParameter, debugAxiom.parameterCount, d.lastNode)
  d.lastNode.applySourceLocation(m, m.axiomSources, axiom, uint32(m.axioms.len))
  d.setCurrentNodeScopeMask(debugAxiom.variableSlotMask)
  d.captureCurrentVariables(m, values, before = true)

proc endAxiom*(d: GeneratedDebugger, m: DebugMetadata, values: openArray[Atom], succeeded: bool) =
  d.endNode(m, values, succeeded)

proc beginCondition*(d: GeneratedDebugger, m: DebugMetadata, condition: uint32, values: openArray[Atom]) =
  if not d.enabled: return
  let visible = m != nil and int(condition) < m.conditions.len and not m.conditions[condition].internal
  d.conditionVisibility.add visible
  if not visible: return
  # Composite conditions pre-create their metadata subtree so every
  # precondition shows, including terms skipped by short-circuiting. When
  # execution reaches one of them, reuse that pending node.
  let parent = if d.openNodes.len > 0: d.openNodes[^1] else: NoIndex
  let existing = d.findPendingConditionChild(parent, condition)
  if existing != NoIndex:
    d.nodes[existing].started = true
    d.openNodes.add existing
  else:
    let debugCondition = m.conditions[condition]
    d.beginNode(conditionKind(debugCondition.kind), condition, debugCondition.sourceLine,
      conditionDisplayName(m, debugCondition), NoIndex)
    let id = d.lastNode.eventNodeID
    d.lastNode.applySourceLocation(m, m.conditionSources, condition, uint32(m.conditions.len))
    d.lastNode.started = true
    buildConditionTitleTokens(m, debugCondition, d.lastNode)
    if debugCondition.kind == dcNot:
      if d.lastNode.scopeVariableMask.len == 0: d.lastNode.scopeVariableMask = newSeq[uint64](DebugSlotMaskWords)
      collectConditionScopeSlots(m, condition, d.lastNode.scopeVariableMask, exported = false)
    d.createConditionMetadataChildren(m, id, debugCondition)
  d.captureCurrentVariables(m, values, before = true)

proc endCondition*(d: GeneratedDebugger, m: DebugMetadata, values: openArray[Atom], succeeded: bool) =
  if not d.enabled or d.conditionVisibility.len == 0: return
  if d.conditionVisibility.pop(): d.endNode(m, values, succeeded)

# ---------------------------------------------------------------------------
# Dump

proc formatIndex(index: uint32): string =
  if index == NoIndex: "-" else: $index

const tokenKindLetters: array[TokenKind, string] = ["n", "r", "v", "c", "s", "x"]

proc dump*(d: GeneratedDebugger): seq[string] =
  ## Every recorded node in the text format of the C++ reference oracle
  ## (tools/oracle/oracle.cpp, DumpDebugger).
  result.add "debugger domain=" & (if d.domainPath.len == 0: "-" else: d.domainPath) & " nodes=" & $d.nodes.len
  proc flag(value: bool, set: string): string = (if value: set else: "-")
  for n in d.nodes:
    let path = if n.source.domainPath.len == 0: "-" else: n.source.domainPath
    result.add "node " & $n.eventNodeID & " parent=" & formatIndex(n.parentEventNodeID) & " kind=" & $n.kind &
      " meta=" & formatIndex(n.metadataIndex) & " state=" & flag(n.started, "S") & flag(n.completed, "C") &
      flag(n.succeeded, "+") & " source=" & path & ":" & $n.source.line & ":" & $n.source.column & "-" &
      $n.source.endLine & ":" & $n.source.endColumn
    result.add "  title " & n.displayName
    if n.titleTokens.len > 0:
      var line = "  tokens"
      for token in n.titleTokens:
        if token.spaceBefore: line.add ' '
        line.add "[" & tokenKindLetters[token.kind] & "]" & token.text
      result.add line
    if n.constants.len > 0:
      var line = "  constants"
      for constant in n.constants: line.add " " & constant.name & "=" & constant.value & ";"
      result.add line
    for (label, variables) in [("before", n.variablesBefore), ("after", n.variablesAfter)]:
      if variables.len == 0: continue
      var line = "  " & label
      for v in variables: line.add " " & $v.slot & ":" & v.name & "=" & toString(v.value, true) & ";"
      result.add line
    if n.scopeVariableMask.len > 0:
      var line = "  scope"
      for word in n.scopeVariableMask: line.add " " & toHex(word, 16).toLowerAscii
      result.add line
    if n.children.len > 0:
      var line = "  children"
      for child in n.children: line.add " " & $child
      result.add line

# ---------------------------------------------------------------------------
# Text tree

proc text*(d: GeneratedDebugger, verbose = false): string =
  ## The recorded nodes as an indented tree, the terminal counterpart of the
  ## original demo's debugger tree and watch panels. Each line shows the
  ## node's state ([OK], [FAIL], [...] running, [--] not reached), title and
  ## source line (with the file when it is not the domain's), followed by the
  ## constants it references and its variables: the bindings it changed or,
  ## when verbose, every bound variable before and after it. Without verbose
  ## only executed nodes are shown.
  var output = ""
  proc visit(id: uint32, depth: int) =
    let n = addr d.nodes[id]
    if not verbose and not n.started: return
    let indent = repeat("  ", depth)
    let status = if n.completed and n.succeeded: "[OK]  "
                 elif n.completed: "[FAIL]"
                 elif n.started: "[...] "
                 else: "[--]  "
    output.add indent & status & " " & n.displayName
    if n.source.line != 0:
      if n.source.domainPath.len == 0 or n.source.domainPath == d.domainPath:
        output.add "  (line " & $n.source.line & ")"
      else:
        output.add "  (" & n.source.domainPath & ":" & $n.source.line & ")"
    output.add '\n'
    let detail = indent & "       "
    if n.constants.len > 0:
      var parts: seq[string]
      for c in n.constants: parts.add c.name & " = " & c.value
      output.add detail & "constants: " & parts.join(", ") & "\n"
    # The watch panel: variables bound before and after, by name.
    var names: seq[string]
    var before, after: Table[string, string]
    for v in n.variablesBefore:
      if v.name notin names: names.add v.name
      before[v.name] = toString(v.value, true)
    for v in n.variablesAfter:
      if v.name notin names: names.add v.name
      after[v.name] = toString(v.value, true)
    names.sort()
    for name in names:
      let b = before.getOrDefault(name, "<unbound>")
      let a = after.getOrDefault(name, "<unbound>")
      if verbose or a != b: output.add detail & name & ": " & b & " -> " & a & "\n"
    for child in n.children:
      if int(child) < d.nodes.len: visit(child, depth + 1)
  for i in 0 ..< d.nodes.len:
    if d.nodes[i].parentEventNodeID == NoIndex: visit(uint32(i), 0)
  output
