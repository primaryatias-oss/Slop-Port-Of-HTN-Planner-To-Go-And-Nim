## Debug metadata of generated planners (HTNGeneratedDebugMetadata): the
## compiled-domain description that the generated execution debugger needs to
## turn execution events into source-level nodes.
##
## Generated planners always carry the metadata, compactly, but build it only
## when compiled with `-d:htnDebug` (HTN_DEBUG_DECOMPOSITION of the original).

from callterm import NoIndex
export NoIndex ## An absent index (HTN_GENERATED_NO_INDEX).

const
  DebugSlotMaskWords* = 4
    ## Number of 64-bit words of a variable slot mask.

  # Condition kinds (HTNGeneratedConditionKind).
  dcFact* = 0'u32
  dcAxiom* = 1'u32
  dcAnd* = 2'u32
  dcOr* = 3'u32
  dcAlt* = 4'u32
  dcNot* = 5'u32
  dcCall* = 6'u32
  dcCallBind* = 7'u32
  dcBuiltinComparison* = 8'u32
  dcBuiltinListSplit* = 9'u32
  dcAssignment* = 10'u32

  # Task kinds (HTNGeneratedTaskKind).
  dtCompound* = 0'u32
  dtPrimitive* = 1'u32
  dtDeferred* = 2'u32

  # Value flags (HTNGeneratedDebugValueFlags).
  dvVariable* = 1'u32
  dvStringLiteral* = 2'u32
  dvCallExpression* = 4'u32

type
  SlotMaskWords* = array[DebugSlotMaskWords, uint64]

  DebugValue* = object
    ## One compiled value.
    flags*: uint32
    text*: uint32         ## Original source expression.
    resolvedText*: uint32 ## Compile-time resolved value text.
    sourceLine*: uint32
    variableSlot*: uint32

  DebugCondition* = object
    ## One compiled condition node.
    kind*, id*, firstArgument*, argumentCount*, firstChildRef*, childCount*: uint32
    outputValue*, resolvedIndex*, sourceLine*: uint32
    expression*: string ## Original domain syntax.
    internal*: bool     ## Compiler-generated step without a source row.

  DebugTask* = object
    ## One compiled task occurrence.
    kind*, id*, firstArgument*, argumentCount*, sourceLine*, planStepHead*: uint32

  DebugBranch* = object
    ## One compiled branch.
    id*, condition*, firstTask*, taskCount*, sourceLine*: uint32

  DebugMethod* = object
    ## One compiled method.
    id*, firstParameter*, parameterCount*, firstBranch*, branchCount*, sourceLine*: uint32
    variableSlotMask*: SlotMaskWords

  DebugAxiom* = object
    ## One compiled axiom.
    id*, firstParameter*, parameterCount*, condition*, sourceLine*: uint32
    variableSlotMask*: SlotMaskWords

  DebugConstant* = object
    ## One constant.
    groupID*, id*, value*, sourceLine*: uint32

  DebugSourceRange* = object
    ## A 1-based source range of a compiled element.
    sourceFileIndex*, beginLine*, beginColumn*, endLine*, endColumn*: uint32

  DebugMetadata* = ref object
    ## The compiled domain as seen by debuggers.
    sourceFile*: string
    strings*: seq[string]
    values*: seq[DebugValue]
    variableStringIDs*: seq[uint32] ## NoIndex marks compiler-internal slots.
    conditions*: seq[DebugCondition]
    conditionChildRefs*: seq[uint32]
    tasks*: seq[DebugTask]
    branches*: seq[DebugBranch]
    methods*: seq[DebugMethod]
    axioms*: seq[DebugAxiom]
    constants*: seq[DebugConstant]
    callTermSlotCount*: uint32
    factSlotCount*: uint32
    sourceFiles*: seq[string]
    valueSources*, conditionSources*, taskSources*, branchSources*: seq[DebugSourceRange]
    methodSources*, axiomSources*, constantSources*: seq[DebugSourceRange]

  DebugTables* = object
    ## The compact form emitted by generated planners: records flattened into
    ## fixed-width integer rows, in the field order of the Debug* objects.
    sourceFile*: string
    strings*: seq[string]
    values*: seq[uint32] ## flags, text, resolvedText, sourceLine, variableSlot
    variableStringIDs*: seq[uint32]
    conditions*: seq[uint32]
      ## kind, id, firstArgument, argumentCount, firstChildRef, childCount,
      ## outputValue, resolvedIndex, sourceLine, internal
    conditionExpressions*: seq[string]
    conditionChildRefs*: seq[uint32]
    tasks*: seq[uint32] ## kind, id, firstArgument, argumentCount, sourceLine, planStepHead
    branches*: seq[uint32] ## id, condition, firstTask, taskCount, sourceLine
    methods*: seq[uint64]
      ## id, firstParameter, parameterCount, firstBranch, branchCount,
      ## sourceLine, variableSlotMask words
    axioms*: seq[uint64]
      ## id, firstParameter, parameterCount, condition, sourceLine,
      ## variableSlotMask words
    constants*: seq[uint32] ## groupID, id, value, sourceLine
    callTermSlotCount*: uint32
    factSlotCount*: uint32
    sourceFiles*: seq[string]
    valueSources*, conditionSources*, taskSources*, branchSources*: seq[uint32]
      ## sourceFileIndex, beginLine, beginColumn, endLine, endColumn
    methodSources*, axiomSources*, constantSources*: seq[uint32]

proc sourceRanges(rows: seq[uint32]): seq[DebugSourceRange] =
  result = newSeq[DebugSourceRange](rows.len div 5)
  for i in 0 ..< result.len:
    let r = i * 5
    result[i] = DebugSourceRange(sourceFileIndex: rows[r], beginLine: rows[r + 1], beginColumn: rows[r + 2],
      endLine: rows[r + 3], endColumn: rows[r + 4])

proc slotMask(rows: seq[uint64], first: int): SlotMaskWords =
  for w in 0 ..< DebugSlotMaskWords: result[w] = rows[first + w]

proc newDebugMetadata*(t: DebugTables): DebugMetadata =
  ## Expands the compact tables of a generated planner.
  result = DebugMetadata(sourceFile: t.sourceFile, strings: t.strings, variableStringIDs: t.variableStringIDs,
    conditionChildRefs: t.conditionChildRefs, callTermSlotCount: t.callTermSlotCount,
    factSlotCount: t.factSlotCount, sourceFiles: t.sourceFiles,
    valueSources: sourceRanges(t.valueSources), conditionSources: sourceRanges(t.conditionSources),
    taskSources: sourceRanges(t.taskSources), branchSources: sourceRanges(t.branchSources),
    methodSources: sourceRanges(t.methodSources), axiomSources: sourceRanges(t.axiomSources),
    constantSources: sourceRanges(t.constantSources))
  result.values = newSeq[DebugValue](t.values.len div 5)
  for i in 0 ..< result.values.len:
    let r = i * 5
    result.values[i] = DebugValue(flags: t.values[r], text: t.values[r + 1], resolvedText: t.values[r + 2],
      sourceLine: t.values[r + 3], variableSlot: t.values[r + 4])
  result.conditions = newSeq[DebugCondition](t.conditions.len div 10)
  for i in 0 ..< result.conditions.len:
    let r = i * 10
    result.conditions[i] = DebugCondition(kind: t.conditions[r], id: t.conditions[r + 1],
      firstArgument: t.conditions[r + 2], argumentCount: t.conditions[r + 3], firstChildRef: t.conditions[r + 4],
      childCount: t.conditions[r + 5], outputValue: t.conditions[r + 6], resolvedIndex: t.conditions[r + 7],
      sourceLine: t.conditions[r + 8], expression: t.conditionExpressions[i], internal: t.conditions[r + 9] != 0)
  result.tasks = newSeq[DebugTask](t.tasks.len div 6)
  for i in 0 ..< result.tasks.len:
    let r = i * 6
    result.tasks[i] = DebugTask(kind: t.tasks[r], id: t.tasks[r + 1], firstArgument: t.tasks[r + 2],
      argumentCount: t.tasks[r + 3], sourceLine: t.tasks[r + 4], planStepHead: t.tasks[r + 5])
  result.branches = newSeq[DebugBranch](t.branches.len div 5)
  for i in 0 ..< result.branches.len:
    let r = i * 5
    result.branches[i] = DebugBranch(id: t.branches[r], condition: t.branches[r + 1], firstTask: t.branches[r + 2],
      taskCount: t.branches[r + 3], sourceLine: t.branches[r + 4])
  const methodWidth = 6 + DebugSlotMaskWords
  result.methods = newSeq[DebugMethod](t.methods.len div methodWidth)
  for i in 0 ..< result.methods.len:
    let r = i * methodWidth
    result.methods[i] = DebugMethod(id: uint32(t.methods[r]), firstParameter: uint32(t.methods[r + 1]),
      parameterCount: uint32(t.methods[r + 2]), firstBranch: uint32(t.methods[r + 3]),
      branchCount: uint32(t.methods[r + 4]), sourceLine: uint32(t.methods[r + 5]),
      variableSlotMask: slotMask(t.methods, r + 6))
  const axiomWidth = 5 + DebugSlotMaskWords
  result.axioms = newSeq[DebugAxiom](t.axioms.len div axiomWidth)
  for i in 0 ..< result.axioms.len:
    let r = i * axiomWidth
    result.axioms[i] = DebugAxiom(id: uint32(t.axioms[r]), firstParameter: uint32(t.axioms[r + 1]),
      parameterCount: uint32(t.axioms[r + 2]), condition: uint32(t.axioms[r + 3]),
      sourceLine: uint32(t.axioms[r + 4]), variableSlotMask: slotMask(t.axioms, r + 5))
  result.constants = newSeq[DebugConstant](t.constants.len div 4)
  for i in 0 ..< result.constants.len:
    let r = i * 4
    result.constants[i] = DebugConstant(groupID: t.constants[r], id: t.constants[r + 1], value: t.constants[r + 2],
      sourceLine: t.constants[r + 3])
