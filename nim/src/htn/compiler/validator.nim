## Semantic validation of linked domain modules: parameter prefixes,
## assignment scopes, variable use, singleton variables, override chains,
## link-time overload resolution and axiom cycles.

import std/[sets, strutils, tables]
import ../lexer
import ast, diagnostics

proc callableSignature*(name: string, argumentCount: int): string =
  ## The internal overload key "name/arity".
  name & "/" & $argumentCount

type
  AssignmentScopeKind = enum
    scopeLeaf, scopeSequence, scopeAlternatives, scopeNegation

  AssignmentScopeNode = object
    kind: AssignmentScopeKind
    destination: string
    rng: SourceRange
    uses: seq[string]
    children: seq[AssignmentScopeNode]

  Validator = object
    files: seq[string]
    diagnostics: ptr DiagnosticSink

  OverrideState = object
    qualifiedParameterCount: Table[string, int]
    effectiveOwner: Table[string, string]
    specialization: Table[string, bool]

proc addNew(s: var HashSet[string], v: string): bool =
  ## Adds `v`; returns false when it was already present.
  if v in s: return false
  s.incl v
  true

proc assignmentParameterIsInitiallyUsed(name: string, isAxiom: bool): bool =
  ## Axiom output parameters declare slots but their first use may initialize
  ## them.
  not isAxiom or (not name.startsWith("out_") and not name.startsWith("io_"))

proc validateAssignmentScope(node: AssignmentScopeNode, seen: var HashSet[string],
    report: proc (r: SourceRange, message: string)): bool =
  result = true
  for use in node.uses: seen.incl use
  if node.destination.len > 0:
    if not seen.addNew(node.destination):
      report(node.rng, "Assignment destination '?" & node.destination &
        "' has already been declared or used; assignment must declare a fresh variable")
      result = false
  let entry = seen
  for child in node.children:
    if node.kind in {scopeAlternatives, scopeNegation}:
      var branch = entry
      result = validateAssignmentScope(child, branch, report) and result
      # A later declaration cannot reuse a name mentioned in any alternative.
      for k in branch: seen.incl k
    else:
      result = validateAssignmentScope(child, seen, report) and result

proc collectAssignmentUses(v: Value, uses: var seq[string]) =
  if v == nil: return
  if v.kind == vkVariable: uses.add valueText(v)
  for a in v.callArguments: collectAssignmentUses(a, uses)
  for o in v.arithmeticOperands: collectAssignmentUses(o, uses)

proc buildAssignmentScope(c: Condition): AssignmentScopeNode =
  if c == nil: return
  result.rng = c.range
  if c.kind == ckAssignment:
    result.destination = valueText(c.output)
  else:
    collectAssignmentUses(c.output, result.uses)
  for a in c.arguments: collectAssignmentUses(a, result.uses)
  result.kind = case c.kind
    of ckAnd: scopeSequence
    of ckNot: scopeNegation
    of ckOr, ckAlt: scopeAlternatives
    else: scopeLeaf
  for child in c.children: result.children.add buildAssignmentScope(child)

proc file(v: Validator, index: uint32): string =
  if int(index) < v.files.len: v.files[index] else: ""

proc errorAt(v: Validator, n: Node, message: string) =
  v.diagnostics[].error(v.file(n.fileIndex), message, recRecoverable, n.range)

proc validateAssignments(v: Validator, c: Condition, parameters: seq[Value], isAxiom: bool): bool =
  var seen = initHashSet[string]()
  for p in parameters:
    if assignmentParameterIsInitiallyUsed(valueText(p), isAxiom): seen.incl valueText(p)
  let scope = buildAssignmentScope(c)
  let fileName = if c != nil: v.file(c.fileIndex) else: ""
  let diagnostics = v.diagnostics
  validateAssignmentScope(scope, seen, proc (r: SourceRange, message: string) =
    diagnostics[].error(fileName, message, recRecoverable, r))

proc declareVariables(c: Condition, variables: var HashSet[string]) =
  if c == nil: return
  template declare(value: Value) =
    if value != nil and value.kind == vkVariable: variables.incl valueText(value)
  case c.kind
  of ckFact, ckAxiom:
    for a in c.arguments: declare(a)
  of ckAssignment, ckCall:
    declare(c.output)
  of ckSplit:
    if c.arguments.len == 3:
      declare(c.arguments[1])
      declare(c.arguments[2])
  else:
    for child in c.children: declareVariables(child, variables)

proc validateValueUse(v: Validator, value: Value, variables: HashSet[string], usage: string): bool =
  if value == nil: return true
  if value.kind == vkVariable and valueText(value) notin variables:
    v.errorAt(Node(value[]), "Unknown variable '" & valueText(value) & "' used by " & usage & ".")
    return false
  result = true
  if value.kind == vkCall:
    for a in value.callArguments:
      result = v.validateValueUse(a, variables, "call expression '" & valueText(value.callID) & "'") and result
  if value.kind == vkArithmetic:
    for o in value.arithmeticOperands:
      result = v.validateValueUse(o, variables, usage) and result

proc validateConditionVariables(v: Validator, c: Condition, variables: HashSet[string],
    singletons: var HashSet[string]): bool =
  if c == nil: return true
  var valid = true
  proc checkSingletonUse(value: Value, usage: string) =
    if value != nil and value.kind == vkVariable and valueText(value).startsWith("any_"):
      v.errorAt(Node(value[]), "Singleton variable '?" & valueText(value) &
        "' may only be used as a fact argument; it is never bound and cannot be consumed by " & usage & ".")
      valid = false
  case c.kind
  of ckFact:
    for a in c.arguments:
      if a.kind == vkVariable and valueText(a).startsWith("any_") and not singletons.addNew(valueText(a)):
        v.errorAt(Node(a[]), "Singleton variable '?" & valueText(a) & "' may only appear once in the same scope.")
        valid = false
  of ckAssignment, ckCall:
    let isAssignment = c.kind == ckAssignment
    let usage = if isAssignment: "an assignment" else: "callterm '" & valueText(c.id) & "'"
    for a in c.arguments:
      checkSingletonUse(a, usage)
      valid = v.validateValueUse(a, variables, usage) and valid
    checkSingletonUse(c.output, if isAssignment: "an assignment destination" else: "a callterm output")
  of ckComparison, ckSplit:
    for i, a in c.arguments:
      let usage = if c.kind == ckComparison: "a built-in comparison"
                  elif i == 0: "split_list input"
                  else: "split_list output"
      checkSingletonUse(a, usage)
      valid = v.validateValueUse(a, variables, usage) and valid
  else:
    for child in c.children:
      valid = v.validateConditionVariables(child, variables, singletons) and valid
  valid

proc validateMethodVariables(v: Validator, m: Method): bool =
  var parameters = initHashSet[string]()
  for p in m.parameters: parameters.incl valueText(p)
  result = true
  for branch in m.branches:
    var variables = parameters
    var singletons = initHashSet[string]()
    result = v.validateAssignments(branch.precondition, m.parameters, false) and result
    declareVariables(branch.precondition, variables)
    result = v.validateConditionVariables(branch.precondition, variables, singletons) and result
    for task in branch.tasks:
      let usage = (case task.kind
        of tkPrimitive: "primitive task '"
        of tkDeferred: "deferred call '"
        else: "compound task '") & valueText(task.id) & "'"
      for a in task.arguments:
        if a.kind == vkVariable and valueText(a).startsWith("any_"):
          v.errorAt(Node(a[]), "Singleton variable '?" & valueText(a) &
            "' may only be used as a fact argument; it is never bound and cannot be consumed by " & usage & ".")
          result = false
        result = v.validateValueUse(a, variables, usage) and result

proc collectAxiomCalls(c: Condition, calls: var seq[string]) =
  if c == nil: return
  if c.kind == ckAxiom: calls.add callableSignature(valueText(c.id), c.arguments.len)
  for child in c.children: collectAxiomCalls(child, calls)

proc validateAxiomCycles(v: Validator, axioms: seq[Axiom]): bool =
  var indexByID = initTable[string, int]()
  for i, a in axioms:
    let key = callableSignature(a.id, a.parameters.len)
    if key notin indexByID: indexByID[key] = i
  var states = newSeq[uint8](axioms.len)
  var stack: seq[int]
  proc visit(index: int): bool =
    states[index] = 1
    stack.add index
    var calls: seq[string]
    collectAxiomCalls(axioms[index].body, calls)
    for call in calls:
      let target = indexByID.getOrDefault(call, -1)
      if target < 0: continue
      if states[target] == 0:
        if not visit(target): return false
      elif states[target] == 1:
        var first = 0
        for i, s in stack:
          if s == target:
            first = i
            break
        var cycle: seq[string]
        for item in stack[first .. ^1]:
          cycle.add callableSignature(axioms[item].id, axioms[item].parameters.len)
        var smallest = 0
        for i in 0 ..< cycle.len:
          if cycle[i] < cycle[smallest]: smallest = i
        let rotated = cycle[smallest .. ^1] & cycle[0 ..< smallest]
        var message = "Cyclic axiom dependency: "
        for id in rotated: message.add id & " -> "
        message.add rotated[0]
        v.errorAt(Node(axioms[index][]), message)
        return false
    stack.setLen(stack.len - 1)
    states[index] = 2
    true
  for i in 0 ..< axioms.len:
    if states[i] == 0 and not visit(i): return false
  true

proc validateOverride[T: Method | Axiom](v: Validator, module: Domain, declaration: T, kind: string,
    baseDomains: Table[string, bool], state: var OverrideState): bool =
  let key = callableSignature(declaration.id, declaration.parameters.len)
  let qualifiedID = module.id & "::" & key
  if declaration.isBase and not module.isBase:
    v.errorAt(Node(declaration[]), kind & " '" & qualifiedID & "' is declared base but domain '" &
      module.id & "' is not a base domain")
    return false
  if qualifiedID in state.qualifiedParameterCount:
    v.errorAt(Node(declaration[]), "Duplicate " & kind & " declaration '" & qualifiedID & "'")
    return false
  if declaration.overridesDomain.len > 0:
    let baseQualifiedID = declaration.overridesDomain & "::" & key
    if baseQualifiedID notin state.qualifiedParameterCount or
        declaration.overridesDomain notin baseDomains or not baseDomains[declaration.overridesDomain] or
        not state.specialization.getOrDefault(baseQualifiedID) or
        state.qualifiedParameterCount[baseQualifiedID] != declaration.parameters.len or
        state.effectiveOwner.getOrDefault(key) != declaration.overridesDomain:
      v.errorAt(Node(declaration[]), "Invalid " & kind & " override '" & qualifiedID & "' of '" &
        baseQualifiedID & "'")
      return false
    state.specialization[qualifiedID] = module.isBase
  else:
    if key in state.effectiveOwner:
      v.errorAt(Node(declaration[]), kind & " '" & declaration.id & "' already exists in domain '" &
        state.effectiveOwner[key] & "'")
      return false
    state.specialization[qualifiedID] = declaration.isBase
  state.qualifiedParameterCount[qualifiedID] = declaration.parameters.len
  state.effectiveOwner[key] = module.id
  true

proc validateOverrideChains(v: Validator, modules: seq[Domain]): bool =
  var baseDomains = initTable[string, bool]()
  for m in modules:
    if m.id notin baseDomains: baseDomains[m.id] = m.isBase
  var qualifiedConstants = initHashSet[string]()
  var effectiveConstantOwner = initTable[string, string]()
  var constantSpecialization = initTable[string, bool]()
  var axiomState, methodState: OverrideState
  result = true
  for module in modules:
    for group in module.constantGroups:
      if group.isBase and not module.isBase:
        v.errorAt(Node(group[]), "Constants block '" & module.id & "::" & group.id &
          "' is declared base but domain '" & module.id & "' is not a base domain")
        result = false
        continue
      if group.overridesDomain.len > 0:
        if not baseDomains.getOrDefault(group.overridesDomain, false):
          v.errorAt(Node(group[]), "Constants block '" & module.id & "::" & group.id &
            "' overrides missing or non-base domain '" & group.overridesDomain & "'")
          result = false
          continue
      for constant in group.constants:
        let qualifiedID = module.id & "::" & constant.id
        if group.overridesDomain.len == 0:
          if constant.id in effectiveConstantOwner:
            v.errorAt(Node(constant[]), "Constant '" & constant.id & "' already exists in domain '" &
              effectiveConstantOwner[constant.id] & "'")
            result = false
            continue
          constantSpecialization[qualifiedID] = group.isBase
        else:
          let baseQualifiedID = group.overridesDomain & "::" & constant.id
          if baseQualifiedID notin qualifiedConstants or not constantSpecialization.getOrDefault(baseQualifiedID) or
              effectiveConstantOwner.getOrDefault(constant.id) != group.overridesDomain:
            v.errorAt(Node(constant[]), "Invalid constant override '" & qualifiedID & "' of '" &
              baseQualifiedID & "'")
            result = false
            continue
          constantSpecialization[qualifiedID] = module.isBase
        qualifiedConstants.incl qualifiedID
        effectiveConstantOwner[constant.id] = module.id
    for axiom in module.axioms:
      result = v.validateOverride(module, axiom, "Axiom", baseDomains, axiomState) and result
    for m in module.methods:
      result = v.validateOverride(module, m, "Method", baseDomains, methodState) and result

proc validateDomainModules*(modules: seq[Domain], sourceFiles: seq[string], requireTopLevelRoot: bool,
    diagnostics: var DiagnosticSink): bool =
  ## Validates linked modules given in dependency-first order with the root
  ## module last.
  if modules.len == 0: return false
  let v = Validator(files: sourceFiles, diagnostics: addr diagnostics)
  var valid = true
  let root = modules[^1]
  if requireTopLevelRoot and not root.isTopLevel:
    diagnostics.error(v.file(root.fileIndex), "Root domain must be top_level_domain", recRecoverable, root.range)
    valid = false
  var domainIDs = initHashSet[string]()
  var topLevelMethods = 0
  for module in modules:
    if not domainIDs.addNew(module.id):
      diagnostics.error(v.file(module.fileIndex), "Duplicate domain id '" & module.id & "'", recDependent, module.range)
      valid = false
    if module != root and module.isTopLevel:
      diagnostics.error(v.file(module.fileIndex), "Included domain cannot be top_level_domain", recRecoverable,
        module.range)
      valid = false
    for m in module.methods:
      if m.topLevel: inc topLevelMethods
      if module != root and m.topLevel:
        v.errorAt(Node(m[]), "Included domain '" & module.id & "' cannot declare top_level_method '" & m.id & "'")
        valid = false
      for parameter in m.parameters:
        if not valueText(parameter).startsWith("inp_"):
          v.errorAt(Node(parameter[]), "Method '" & m.id & "' parameter '?" & valueText(parameter) &
            "' must use the inp_ prefix")
          valid = false
      valid = v.validateMethodVariables(m) and valid
    for axiom in module.axioms:
      valid = v.validateAssignments(axiom.body, axiom.parameters, true) and valid
      var variables = initHashSet[string]()
      var singletons = initHashSet[string]()
      for parameter in axiom.parameters: variables.incl valueText(parameter)
      declareVariables(axiom.body, variables)
      valid = v.validateConditionVariables(axiom.body, variables, singletons) and valid
    for axiom in module.axioms:
      for parameter in axiom.parameters:
        let name = valueText(parameter)
        if not name.startsWith("inp_") and not name.startsWith("out_") and not name.startsWith("io_"):
          v.errorAt(Node(parameter[]), "Axiom '" & axiom.id & "' parameter '?" & name &
            "' must use an inp_, out_ or io_ prefix")
          valid = false
  if requireTopLevelRoot and root.isTopLevel and topLevelMethods == 0:
    diagnostics.error(v.file(root.fileIndex), "Root top_level_domain has no top_level_method", recRecoverable,
      root.range)
    valid = false
  valid = v.validateOverrideChains(modules) and valid

  var methods = initHashSet[string]()
  for module in modules:
    for m in module.methods:
      methods.incl callableSignature(module.id & "::" & m.id, m.parameters.len)
      methods.incl callableSignature(m.id, m.parameters.len)
  for module in modules:
    for m in module.methods:
      for branch in m.branches:
        for task in branch.tasks:
          if task.kind == tkPrimitive: continue
          let id = valueText(task.id)
          if callableSignature(id, task.arguments.len) notin methods:
            let prefix = if task.kind == tkDeferred: "Deferred method call '" else: "Compound method call '"
            v.errorAt(Node(task[]), prefix & id & "' cannot be resolved at link time: no overload accepts " &
              $task.arguments.len & " argument(s)")
            valid = false

  var linked: seq[Axiom]
  for module in modules:
    for axiom in module.axioms:
      let qualified = Axiom()
      qualified[] = axiom[]
      qualified.id = module.id & "::" & axiom.id
      linked.add qualified
  var effective = initTable[string, Axiom]()
  for module in modules:
    for axiom in module.axioms:
      effective[callableSignature(axiom.id, axiom.parameters.len)] = axiom
  for module in modules:
    for axiom in module.axioms:
      if effective[callableSignature(axiom.id, axiom.parameters.len)] == axiom:
        linked.add axiom
  var axiomSignatures = initHashSet[string]()
  for axiom in linked: axiomSignatures.incl callableSignature(axiom.id, axiom.parameters.len)
  proc validateAxiomCall(c: Condition) =
    if c == nil: return
    if c.kind == ckAxiom and callableSignature(valueText(c.id), c.arguments.len) notin axiomSignatures:
      v.errorAt(Node(c[]), "Axiom call '" & valueText(c.id) & "' cannot be resolved at link time: no overload accepts " &
        $c.arguments.len & " argument(s)")
      valid = false
    for child in c.children: validateAxiomCall(child)
  for module in modules:
    for m in module.methods:
      for branch in m.branches: validateAxiomCall(branch.precondition)
    for axiom in module.axioms: validateAxiomCall(axiom.body)
  valid = v.validateAxiomCycles(linked) and valid
  valid and not diagnostics.hasErrors
