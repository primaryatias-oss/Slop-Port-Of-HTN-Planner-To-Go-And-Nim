## Ports of HTNDomainDocumentStoreTest: unsaved buffers, include
## invalidation, overload resolution and completion.

import std/[os, strutils, tempfiles]
import htn/tooling

var failures = 0

template check(condition: bool, message: string) =
  if not condition:
    inc failures
    echo "FAIL: ", message

const
  baseV1 = "(:domain Base base\n  (:method (do_combat ?inp_threat) base\n    (branch_base () ((!attack ?inp_threat)))\n  )\n)\n"
  baseV2 = "(:domain Base base\n  (:method (renamed_combat ?inp_threat) base\n    (branch_base () ((!attack ?inp_threat)))\n  )\n)\n"
  mainText = "(:include \"base.domain\")\n(:domain Main top_level_domain\n  (:method (run ?inp_threat) top_level_method\n" &
    "    (branch_main () ((Base::do_combat ?inp_threat)))\n  )\n)\n"

proc openUnsavedIncludeParticipatesInSemanticAnalysis() =
  let root = createTempDir("htn-tooling-", "")
  let store = newStore()
  store.open(root / "base.domain", baseV1, 1)
  store.open(root / "main.domain", mainText, 1)
  let model = store.model(root / "main.domain")
  check model != nil, "model of an open document"
  let offset = mainText.find("Base::do_combat")
  check model.tokenKindAt(offset) == tokMethodValid, "include method is valid"
  var definition: Definition
  check model.definitionAt(offset, definition) and definition.filePath.extractFilename == "base.domain",
    "definition resolves into the unsaved include"
  removeDir(root)

proc changingIncludedBufferInvalidatesDependentSemanticModel() =
  let root = createTempDir("htn-tooling-", "")
  let store = newStore()
  store.open(root / "base.domain", baseV1, 1)
  store.open(root / "main.domain", mainText, 1)
  let offset = mainText.find("Base::do_combat")
  check store.model(root / "main.domain").tokenKindAt(offset) == tokMethodValid, "valid before the rename"
  let previous = store.generation
  check store.update(root / "base.domain", baseV2, 2), "update an open document"
  check store.generation > previous, "generation advances"
  check store.model(root / "main.domain").tokenKindAt(offset) == tokMethodInvalid, "invalid after the rename"
  var text: string
  check store.read(root / "base.domain", text) and text == baseV2, "reads the open buffer"
  removeDir(root)

proc resolvesOverloadedMethodDefinitionsByCallArity() =
  let root = createTempDir("htn-tooling-", "")
  let path = root / "overload_tooling.domain"
  let source = "(:domain Overloads top_level_domain\n (:method (work) (b () ((!zero))))\n" &
    " (:method (work ?inp_x) (b () ((!one ?inp_x))))\n" &
    " (:method (run) top_level_method (b () ((work) (work (+ 1 2)) (&work 3) (Overloads::work 4))))\n)"
  let store = newStore()
  store.open(path, source, 1)
  let model = store.model(path)
  check model.diagnostics.len == 0, "overloads load without diagnostics"
  let run = source.find("(:method (run)")
  for i, call in ["(work)", "(work (+", "(&work", "(Overloads::work"]:
    let offset = source.find(call, run) + 1
    var definition: Definition
    check model.definitionAt(offset, definition) and definition.range.first.line == [2, 3, 3, 3][i],
      "definition of " & call
  removeDir(root)

proc resolvesAxiomOverloadsInsideMethodsAndAxioms() =
  let root = createTempDir("htn-tooling-", "")
  let path = root / "axiom_overload_tooling.domain"
  let source = "(:domain Overloads top_level_domain\n" &
    " (:method (run) top_level_method (b (and (#work) (#work 1) (#Overloads::work 2)) ()))\n" &
    " (:axiom (work) (and (#work 1)))\n (:axiom (work ?inp_x) ())\n)"
  let store = newStore()
  store.open(path, source, 1)
  let model = store.model(path)
  check model.diagnostics.len == 0, "axiom overloads load without diagnostics"
  let offsets = [source.find("#work)"), source.find("#work 1"), source.find("#Overloads::work"), source.rfind("#work 1")]
  for i, offset in offsets:
    var definition: Definition
    check model.definitionAt(offset, definition) and definition.range.first.line == [3, 4, 4, 4][i],
      "axiom definition case " & $i
  removeDir(root)

proc autocompleteListsVariablesInScope() =
  let root = createTempDir("htn-tooling-", "")
  let path = root / "complete.domain"
  let source = "(:domain Complete top_level_domain\n (:method (run ?inp_target) top_level_method\n" &
    "  (b (and (enemy ?enemy) (= ?distance 3)) ((!attack ?enemy))))\n)"
  let store = newStore()
  store.open(path, source, 1)
  check store.model(path).autocompleteCandidates(source.find("(!attack")).join(",") == "?distance,?enemy,?inp_target",
    "completion candidates"
  removeDir(root)

openUnsavedIncludeParticipatesInSemanticAnalysis()
changingIncludedBufferInvalidatesDependentSemanticModel()
resolvesOverloadedMethodDefinitionsByCallArity()
resolvesAxiomOverloadsInsideMethodsAndAxioms()
autocompleteListsVariablesInScope()
if failures > 0: quit 1
echo "tooling tests passed"
