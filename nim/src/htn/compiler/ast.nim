## Compiler-owned AST of HTN domains (HTNCompilerAST).

import ../[atom, lexer]

type
  Node* = object of RootObj
    ## Source location shared by every AST element.
    range*: SourceRange
    fileIndex*: uint32

  ValueKind* = enum
    vkIdentifier, vkLiteral, vkVariable, vkConstant, vkCall, vkArithmetic

  ArithmeticOperator* = enum
    opAdd, opSubtract, opMultiply, opDivide, opModulo, opIncrement, opDecrement

  Value* = ref object of Node
    ## An identifier, literal, variable, constant reference, nested call or
    ## arithmetic expression. Variables and constants store their name (without
    ## prefix) as a string atom.
    kind*: ValueKind
    atom*: Atom
    callID*: Value
    callArguments*: seq[Value]
    arithmeticOp*: ArithmeticOperator
    arithmeticOperands*: seq[Value]

  ConditionKind* = enum
    ckFact, ckAxiom, ckCall, ckAssignment, ckComparison, ckSplit, ckAnd, ckOr, ckAlt, ckNot

  Condition* = ref object of Node
    ## Comparisons store the operator (== != < <= > >= as 0..5) in `operator`;
    ## splits store the operation (split_list, split_list_front,
    ## split_list_back as 0..2).
    kind*: ConditionKind
    id*: Value
    arguments*: seq[Value]
    output*: Value
    operator*: uint32
    children*: seq[Condition]

  TaskKind* = enum
    tkPrimitive, tkCompound, tkDeferred

  Task* = ref object of Node
    kind*: TaskKind
    id*: Value
    arguments*: seq[Value]

  Branch* = ref object of Node
    id*: string
    precondition*: Condition
    tasks*: seq[Task]

  Method* = ref object of Node
    id*: string
    parameters*: seq[Value]
    branches*: seq[Branch]
    topLevel*: bool
    isBase*: bool
    overridesDomain*: string

  Axiom* = ref object of Node
    id*: string
    isBase*: bool
    overridesDomain*: string
    parameters*: seq[Value]
    body*: Condition

  Constant* = ref object of Node
    id*: string
    value*: Value

  ConstantGroup* = ref object of Node
    ## One (:constants ...) block.
    id*: string
    isBase*: bool
    overridesDomain*: string
    constants*: seq[Constant]

  Domain* = ref object
    ## The AST of one domain file or of a linked domain.
    id*: string
    range*: SourceRange
    fileIndex*: uint32
    isTopLevel*: bool
    isBase*: bool
    constantGroups*: seq[ConstantGroup]
    axioms*: seq[Axiom]
    methods*: seq[Method]

proc valueText*(v: Value): string =
  ## The unquoted text of a value's atom ("" for nil).
  if v == nil: "" else: toString(v.atom, false)
