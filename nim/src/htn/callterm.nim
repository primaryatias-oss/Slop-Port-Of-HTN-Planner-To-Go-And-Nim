## Connects domain `(call ...)` expressions to host procedures
## (HTNCallTermRegistry, HTNCallTermBindingContext and the unified callterm
## error policy of the original framework).
##
## A `Registry` is configured once and may then be shared read-only by many
## planners; each planner owns a `BindingContext` holding its daemon instances.

import std/[macros, tables]
import atom

const
  NoIndex* = high(uint32)
    ## Marks an unavailable index, count or type in `ErrorInfo`.
  MaxDaemonTypes* = 32
    ## Number of distinct daemon types a registry accepts.

type
  ErrorPolicy* = enum
    ## How invocation errors are handled. `epUnset` asserts in the original's
    ## debug builds and fails safely in release builds; this port always fails
    ## safely.
    epUnset, epFailSilently, epReport

  ErrorReason* = enum
    erNotRegistered, erMissingBinding, erMissingInstance, erArgumentCountMismatch,
    erArgumentTypeMismatch, erArgumentConversionFailed, erReturnConversionFailed, erNone

  Source* = object
    ## Domain provenance of a call site ("" stands for a null pointer).
    domain*: string
    file*: string
    line*: uint32
    column*: uint32

  ErrorInfo* = object
    ## One failed invocation; only valid during the callback.
    name*: string
    reason*: ErrorReason
    daemonID*: string
    source*: Source
    argumentIndex*: uint32
    expectedArgumentCount*: uint32
    actualArgumentCount*: uint32
    expectedAtomType*: uint32
    actualAtomType*: uint32
    expectedTypeName*: string

  ErrorCallback* = proc (clientContext: RootRef, info: ErrorInfo) {.closure.}
    ## Receives invocation errors under `epReport`.

  Arguments* = object
    ## The borrowed argument view passed to callterm procedures.
    values: seq[Atom]
    clientContext: RootRef
    err: ptr ErrorInfo

  CallTermFunction* = proc (daemon: RootRef, args: var Arguments): Atom {.closure.}
    ## A raw callterm implementation. `daemon` is the planner's daemon instance
    ## for member bindings (nil for static ones). Returning an unbound atom
    ## fails the call without an error report.

  SignatureType* = object
    ## One expected argument type; `any` accepts every atom type.
    any*: bool
    kind*: AtomKind

  Signature* = seq[SignatureType]

  Entry* = ref object
    name*: string
    function*: CallTermFunction
    signature*: Signature
    hasSignature*: bool
    daemonSlot*: int
    daemonID*: string

  Registry* = ref object
    entries: Table[string, Entry]
    daemonSlots: Table[string, int]

  Invocation* = object
    ## Per-execution options used when invoking callterms.
    bindings*: BindingContext
    clientContext*: RootRef
    policy*: ErrorPolicy
    callback*: ErrorCallback

  Requirement* = object
    ## One call site required by a generated domain.
    name*: string
    source*: Source

  BindingContext* = ref object
    ## One planner's daemon instances for a shared registry.
    registry: Registry
    daemons: array[MaxDaemonTypes, RootRef]

  Slot* = object
    ## A callterm resolved for generated execution: the registry entry is
    ## cached while the name is retained for reporting.
    entry*: Entry
    name*: string

proc `$`*(r: ErrorReason): string =
  const names: array[ErrorReason, string] = ["NotRegistered", "MissingBinding", "MissingInstance",
    "ArgumentCountMismatch", "ArgumentTypeMismatch", "ArgumentConversionFailed", "ReturnConversionFailed", "None"]
  names[r]

proc anyType*(): SignatureType = SignatureType(any: true)
proc kindType*(k: AtomKind): SignatureType = SignatureType(kind: k)

# ---------------------------------------------------------------------------
# Arguments

proc initArguments*(values: seq[Atom]): Arguments = Arguments(values: values)

proc len*(a: Arguments): int {.inline.} = a.values.len
proc `[]`*(a: Arguments, i: int): lent Atom {.inline.} = a.values[i]
proc values*(a: Arguments): lent seq[Atom] {.inline.} = a.values
proc clientContext*(a: Arguments): RootRef {.inline.} = a.clientContext

proc setError*(a: var Arguments, reason: ErrorReason, index = NoIndex, expectedType = NoIndex,
    expectedName = "") =
  ## Records the first invocation error; the registry reports it after the
  ## procedure returns.
  if a.err == nil or a.err.reason != erNone: return
  a.err.reason = reason
  a.err.argumentIndex = index
  a.err.expectedAtomType = expectedType
  a.err.expectedTypeName = expectedName
  if index != NoIndex and int(index) < a.values.len:
    a.err.actualAtomType = uint32(ord(a.values[index].kind))

# ---------------------------------------------------------------------------
# Registry

proc newRegistry*(): Registry =
  Registry(entries: initTable[string, Entry](), daemonSlots: initTable[string, int]())

proc store(r: Registry, id: string, entry: Entry) =
  ## Rebinding replaces the existing entry in place, so callterm slots already
  ## resolved by generated planners observe the new binding (the original keeps
  ## registry entries at stable addresses).
  let existing = r.entries.getOrDefault(id)
  if existing != nil:
    existing[] = entry[]
  else:
    r.entries[id] = entry

proc validateArguments(signature: Signature, args: var Arguments): bool =
  if args.len != signature.len:
    args.setError(erArgumentCountMismatch)
    return false
  for i, expected in signature:
    if not expected.any and args[i].kind != expected.kind:
      args.setError(erArgumentTypeMismatch, uint32(i), uint32(ord(expected.kind)))
      return false
  true

proc bindRaw*(r: Registry, id: string, fn: proc (args: var Arguments): Atom {.closure.}) =
  ## Registers an untyped callterm receiving every argument unchanged.
  var wrapped: CallTermFunction
  if fn != nil:
    wrapped = proc (daemon: RootRef, args: var Arguments): Atom = fn(args)
  r.store(id, Entry(name: id, function: wrapped, daemonSlot: -1))

proc bindWithSignature*(r: Registry, id: string, fn: proc (args: var Arguments): Atom {.closure.},
    signature: Signature) =
  ## Registers a callterm whose arguments are validated against `signature`.
  var wrapped: CallTermFunction
  if fn != nil:
    wrapped = proc (daemon: RootRef, args: var Arguments): Atom =
      if not validateArguments(signature, args): return Atom()
      fn(args)
  r.store(id, Entry(name: id, function: wrapped, signature: signature, hasSignature: true, daemonSlot: -1))

proc bindMember*(r: Registry, id, daemonID: string, fn: CallTermFunction, signature: Signature): bool =
  ## Registers a stateful callterm whose daemon instance is supplied by each
  ## planner's `BindingContext`. Fails when the daemon-type capacity is
  ## exhausted.
  var slot = r.daemonSlots.getOrDefault(daemonID, -1)
  if slot < 0:
    if r.daemonSlots.len >= MaxDaemonTypes: return false
    slot = r.daemonSlots.len
    r.daemonSlots[daemonID] = slot
  var wrapped: CallTermFunction
  if fn != nil:
    wrapped = proc (daemon: RootRef, args: var Arguments): Atom =
      if not validateArguments(signature, args): return Atom()
      fn(daemon, args)
  r.store(id, Entry(name: id, function: wrapped, signature: signature, hasSignature: true,
    daemonSlot: slot, daemonID: daemonID))
  true

proc isBound*(r: Registry, id: string): bool =
  let entry = r.entries.getOrDefault(id)
  entry != nil and entry.function != nil

proc resolve*(r: Registry, id: string): Entry = r.entries.getOrDefault(id)

proc findDaemonSlot(r: Registry, id: string): int = r.daemonSlots.getOrDefault(id, -1)

# ---------------------------------------------------------------------------
# Binding contexts

proc newBindingContext*(registry: Registry = nil): BindingContext =
  ## A binding context for `registry` (an empty registry when nil).
  BindingContext(registry: if registry == nil: newRegistry() else: registry)

proc registry*(b: BindingContext): Registry = b.registry

proc setDaemon*(b: BindingContext, id: string, daemon: RootRef): bool {.discardable.} =
  ## Installs the daemon instance for daemon type `id`.
  let slot = b.registry.findDaemonSlot(id)
  if slot < 0 or slot >= MaxDaemonTypes: return false
  b.daemons[slot] = daemon
  true

proc daemon(b: BindingContext, slot: int): RootRef =
  if slot < 0 or slot >= MaxDaemonTypes: nil else: b.daemons[slot]

proc resolveSlot*(b: BindingContext, name: string): Slot =
  if b == nil: Slot(name: name)
  else: Slot(entry: b.registry.entries.getOrDefault(name), name: name)

proc checkEntry(entry: Entry, bindings: BindingContext): (RootRef, ErrorReason) =
  if entry == nil: return (nil, erNotRegistered)
  if entry.function == nil: return (nil, erMissingBinding)
  if entry.daemonSlot >= 0:
    let daemon = if bindings != nil: bindings.daemon(entry.daemonSlot) else: nil
    if daemon == nil: return (nil, erMissingInstance)
    return (daemon, erNone)
  (nil, erNone)

proc invokeEntry*(entry: Entry, name: string, args: seq[Atom], source: ptr Source,
    inv: Invocation): Atom =
  ## Runs one callterm and applies the error policy. Returns an unbound atom
  ## when the invocation failed or the callable returned unbound.
  var info = ErrorInfo(name: name, reason: erNone, argumentIndex: NoIndex, expectedArgumentCount: NoIndex,
    actualArgumentCount: uint32(args.len), expectedAtomType: NoIndex, actualAtomType: NoIndex)
  if entry != nil:
    info.daemonID = entry.daemonID
    if entry.hasSignature: info.expectedArgumentCount = uint32(entry.signature.len)
  if source != nil: info.source = source[]
  let (daemon, reason) = checkEntry(entry, inv.bindings)
  if reason != erNone:
    info.reason = reason
  else:
    var arguments = Arguments(values: args, clientContext: inv.clientContext, err: addr info)
    result = entry.function(daemon, arguments)
  if info.reason == erNone: return
  if inv.policy == epReport and inv.callback != nil:
    inv.callback(inv.clientContext, info)
  result = Atom()

proc execute*(r: Registry, id: string, inv: Invocation, args: seq[Atom], source: ptr Source = nil): Atom =
  ## Invokes the callterm registered as `id` (HTNCallTermRegistry::Execute).
  invokeEntry(r.entries.getOrDefault(id), id, args, source, inv)

proc validateRequirements*(r: Registry, requirements: openArray[Requirement], bindings: BindingContext,
    callback: ErrorCallback = nil, clientContext: RootRef = nil): bool =
  ## Checks every call site without executing callterms. Reports
  ## NotRegistered, MissingBinding and MissingInstance through `callback`.
  if bindings == nil or bindings.registry != r: return false
  result = true
  for requirement in requirements:
    let entry = r.entries.getOrDefault(requirement.name)
    let (_, reason) = checkEntry(entry, bindings)
    if reason != erNone:
      result = false
      if callback != nil:
        var info = ErrorInfo(name: requirement.name, reason: reason, source: requirement.source,
          argumentIndex: NoIndex, expectedArgumentCount: NoIndex, actualArgumentCount: NoIndex,
          expectedAtomType: NoIndex, actualAtomType: NoIndex)
        if entry != nil: info.daemonID = entry.daemonID
        callback(clientContext, info)

# ---------------------------------------------------------------------------
# Typed conversions (HTNTypeTraits/HTNTypeConverter). Overload `fromAtom`,
# `toAtom`, `atomKindOf` and `atomTypeName` to support custom types.

type AtomList* = distinct Atom
  ## The native representation of an HTN list argument or result.

proc atom*(l: AtomList): Atom {.inline.} = Atom(l)

template declareFixed(T: typedesc, k: AtomKind, typeName: string) =
  proc atomKindOf*(t: typedesc[T]): (bool, AtomKind) = (true, k)
  proc atomTypeName*(t: typedesc[T]): string = typeName

declareFixed(bool, akBool, "bool")
declareFixed(int32, akInt, "int32")
declareFixed(int, akInt, "int32")
declareFixed(float32, akFloat, "float")
declareFixed(float, akFloat, "float")
declareFixed(string, akString, "string")
declareFixed(Symbol, akSymbol, "HtnSymbol")
declareFixed(AtomList, akList, "HTNAtomList")
proc atomKindOf*(t: typedesc[Atom]): (bool, AtomKind) = (false, akUnbound)
proc atomTypeName*(t: typedesc[Atom]): string = "HTNAtom"

proc fromAtom*(ctx: RootRef, a: Atom, v: var bool): bool = (v = a.boolValue; a.isKind(akBool))
proc fromAtom*(ctx: RootRef, a: Atom, v: var int32): bool = (v = a.intValue; a.isKind(akInt))
proc fromAtom*(ctx: RootRef, a: Atom, v: var int): bool = (v = int(a.intValue); a.isKind(akInt))
proc fromAtom*(ctx: RootRef, a: Atom, v: var float32): bool = (v = a.floatValue; a.isKind(akFloat))
proc fromAtom*(ctx: RootRef, a: Atom, v: var float): bool = (v = float(a.floatValue); a.isKind(akFloat))
proc fromAtom*(ctx: RootRef, a: Atom, v: var string): bool = (v = a.strValue; a.isKind(akString))
proc fromAtom*(ctx: RootRef, a: Atom, v: var Symbol): bool = (v = a.symbolValue; a.isKind(akSymbol))
proc fromAtom*(ctx: RootRef, a: Atom, v: var Atom): bool = (v = a; true)
proc fromAtom*(ctx: RootRef, a: Atom, v: var AtomList): bool = (v = AtomList(a); a.isKind(akList))

proc toAtom*(ctx: RootRef, v: bool, a: var Atom): bool = (a = newBool(v); true)
proc toAtom*(ctx: RootRef, v: int32, a: var Atom): bool = (a = newInt(v); true)
proc toAtom*(ctx: RootRef, v: int, a: var Atom): bool =
  if v < int(low(int32)) or v > int(high(int32)): return false
  a = newInt(int32(v))
  true
proc toAtom*(ctx: RootRef, v: float32, a: var Atom): bool = (a = newFloat(v); true)
proc toAtom*(ctx: RootRef, v: float, a: var Atom): bool = (a = newFloat(float32(v)); true)
proc toAtom*(ctx: RootRef, v: string, a: var Atom): bool = (a = newString(v); true)
proc toAtom*(ctx: RootRef, v: Symbol, a: var Atom): bool = (a = newSymbol(v); v != nil)
proc toAtom*(ctx: RootRef, v: Atom, a: var Atom): bool = (a = v; true)
proc toAtom*(ctx: RootRef, v: AtomList, a: var Atom): bool =
  if not Atom(v).isKind(akList): return false
  a = Atom(v)
  true

proc signatureTypeOfValue*[T](v: T): SignatureType =
  ## The signature entry of the type of `v` (the value is only a type carrier).
  let (fixed, kind) = atomKindOf(T)
  if fixed: kindType(kind) else: anyType()

proc expectedKindIndexOf*[T](v: T): uint32 =
  let (fixed, kind) = atomKindOf(T)
  if fixed: uint32(ord(kind)) else: NoIndex

proc typeNameOf*[T](v: T): string = atomTypeName(T)

macro typedAdapter(fn: typed, hasDaemon: static[bool]): untyped =
  ## Builds `proc (daemon: RootRef, args: var Arguments): Atom` calling `fn`
  ## with converted arguments, plus its signature expression.
  let fnType = getTypeImpl(fn)
  expectKind(fnType, nnkProcTy)
  let formals = fnType[0]
  let daemonSym = genSym(nskParam, "daemon")
  let argsSym = genSym(nskParam, "args")
  var body = newStmtList()
  var callArgs: seq[NimNode]
  var signature = newNimNode(nnkBracket)
  var index = 0
  for i in 1 ..< formals.len:
    let identDefs = formals[i]
    let paramType = identDefs[^2]
    for _ in 0 ..< identDefs.len - 2:
      if hasDaemon and index == 0 and callArgs.len == 0:
        let daemonValue = genSym(nskVar, "daemonValue")
        body.add quote do:
          var `daemonValue`: `paramType`
          if `daemonSym` == nil or not (`daemonSym` of typeof(`daemonValue`)):
            `argsSym`.setError(erMissingInstance)
            return Atom()
          `daemonValue` = typeof(`daemonValue`)(`daemonSym`)
        callArgs.add daemonValue
        continue
      let position = newLit(callArgs.len - (if hasDaemon: 1 else: 0))
      let value = genSym(nskVar, "value")
      body.add quote do:
        var `value`: `paramType`
        if not fromAtom(`argsSym`.clientContext, `argsSym`[`position`], `value`):
          `argsSym`.setError(erArgumentConversionFailed, uint32(`position`), expectedKindIndexOf(`value`),
            typeNameOf(`value`))
          return Atom()
      let carrier = genSym(nskVar, "carrier")
      signature.add quote do:
        (block:
          var `carrier`: `paramType`
          signatureTypeOfValue(`carrier`))
      callArgs.add value
      inc index
  let callNode = newCall(fn, callArgs)
  let resultAtom = genSym(nskVar, "resultAtom")
  body.add quote do:
    var `resultAtom`: Atom
    let returned = `callNode`
    if not toAtom(`argsSym`.clientContext, returned, `resultAtom`):
      `argsSym`.setError(erReturnConversionFailed, NoIndex, NoIndex, typeNameOf(returned))
      return Atom()
    return `resultAtom`
  let lambda = quote do:
    proc (`daemonSym`: RootRef, `argsSym`: var Arguments): Atom {.closure.} =
      discard `daemonSym`
      `body`
  # An empty bracket has no element type: spell out the sequence type.
  let signatureSeq = if signature.len == 0: newCall(nnkBracketExpr.newTree(ident"newSeq", ident"SignatureType"))
                     else: prefix(signature, "@")
  result = quote do:
    (`lambda`, `signatureSeq`)

template bindFunc*(r: Registry, id: string, fn: typed) =
  ## Registers a typed static callterm. `fn` is any procedure whose parameter
  ## and result types have `fromAtom`/`toAtom` conversions, for example
  ## `proc (entity: int32): float32`. The signature is derived from the
  ## parameters.
  block:
    let (adapter, signature) = typedAdapter(fn, false)
    r.bindWithSignature(id, proc (args: var Arguments): Atom = adapter(nil, args), signature)

template bindMemberFunc*(r: Registry, id, daemonID: string, fn: typed): bool =
  ## Registers a typed stateful callterm. `fn` takes the daemon instance (a
  ## `ref object of RootObj`) as its first parameter. Each planner supplies the
  ## daemon with `BindingContext.setDaemon(daemonID, instance)`.
  block:
    let (adapter, signature) = typedAdapter(fn, true)
    r.bindMember(id, daemonID, adapter, signature)

# ---------------------------------------------------------------------------
# Standard list callterms (AIHtnListDaemon). Lists are values: every operation
# returns a new list.

proc listAdd*(list: AtomList, value: Atom): Atom =
  let (appended, ok) = Atom(list).append(value)
  if ok: appended else: Atom()

proc listRemoveAt*(list: AtomList, index: int32): Atom =
  if index < 0: return Atom()
  let (removed, ok) = Atom(list).removeAt(int(index))
  if ok: removed else: Atom()

proc listGet*(list: AtomList, index: int32): Atom =
  if index < 0: return Atom()
  Atom(list).at(int(index))[0]

proc listSize*(list: AtomList): Atom = newInt(int32(Atom(list).len))

proc listClear*(list: AtomList): Atom = emptyList()

proc bindListCallTerms*(r: Registry) =
  ## Registers list_add, list_remove_at, list_get, list_size and list_clear.
  r.bindFunc("list_add", listAdd)
  r.bindFunc("list_remove_at", listRemoveAt)
  r.bindFunc("list_get", listGet)
  r.bindFunc("list_size", listSize)
  r.bindFunc("list_clear", listClear)

proc toAtomValue*[T](clientContext: RootRef, value: T): (Atom, bool) =
  ## Converts a native value through its `toAtom` conversion.
  var a: Atom
  let ok = toAtom(clientContext, value, a)
  (a, ok)
