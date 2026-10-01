package tooling_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/tooling"
)

// Ports of HTNDomainDocumentStoreTest.

func TestOpenUnsavedIncludeParticipatesInSemanticAnalysis(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "base.domain")
	mainPath := filepath.Join(root, "main.domain")
	baseText := "(:domain Base base\n  (:method (do_combat ?inp_threat) base\n    (branch_base () ((!attack ?inp_threat)))\n  )\n)\n"
	mainText := "(:include \"base.domain\")\n(:domain Main top_level_domain\n  (:method (run ?inp_threat) top_level_method\n" +
		"    (branch_main () ((Base::do_combat ?inp_threat)))\n  )\n)\n"
	store := tooling.NewStore()
	store.Open(basePath, baseText, 1)
	store.Open(mainPath, mainText, 1)
	model := store.Model(mainPath)
	if model == nil {
		t.Fatal("no model")
	}
	offset := strings.Index(mainText, "Base::do_combat")
	if kind := model.TokenKindAt(offset); kind != tooling.TokenMethodValid {
		t.Fatalf("token kind = %v", kind)
	}
	definition, ok := model.DefinitionAt(offset)
	if !ok || filepath.Base(definition.FilePath) != "base.domain" {
		t.Fatalf("definition = %+v %v", definition, ok)
	}
}

func TestChangingIncludedBufferInvalidatesDependentSemanticModel(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "base.domain")
	mainPath := filepath.Join(root, "main.domain")
	baseV1 := "(:domain Base base\n  (:method (do_combat ?inp_threat) base\n    (branch_base () ((!attack ?inp_threat)))\n  )\n)\n"
	baseV2 := "(:domain Base base\n  (:method (renamed_combat ?inp_threat) base\n    (branch_base () ((!attack ?inp_threat)))\n  )\n)\n"
	mainText := "(:include \"base.domain\")\n(:domain Main top_level_domain\n  (:method (run ?inp_threat) top_level_method\n" +
		"    (branch_main () ((Base::do_combat ?inp_threat)))\n  )\n)\n"
	store := tooling.NewStore()
	store.Open(basePath, baseV1, 1)
	store.Open(mainPath, mainText, 1)
	offset := strings.Index(mainText, "Base::do_combat")
	if kind := store.Model(mainPath).TokenKindAt(offset); kind != tooling.TokenMethodValid {
		t.Fatalf("first token kind = %v", kind)
	}
	previous := store.Generation()
	if !store.Update(basePath, baseV2, 2) {
		t.Fatal("update failed")
	}
	if store.Generation() <= previous {
		t.Fatal("generation did not advance")
	}
	if kind := store.Model(mainPath).TokenKindAt(offset); kind != tooling.TokenMethodInvalid {
		t.Fatalf("second token kind = %v", kind)
	}
	if text, ok := store.Read(basePath); !ok || text != baseV2 {
		t.Fatalf("read = %q %v", text, ok)
	}
}

func TestResolvesOverloadedMethodDefinitionsByCallArity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overload_tooling.domain")
	source := "(:domain Overloads top_level_domain\n (:method (work) (b () ((!zero))))\n" +
		" (:method (work ?inp_x) (b () ((!one ?inp_x))))\n" +
		" (:method (run) top_level_method (b () ((work) (work (+ 1 2)) (&work 3) (Overloads::work 4))))\n)"
	store := tooling.NewStore()
	store.Open(path, source, 1)
	model := store.Model(path)
	if len(model.Diagnostics()) != 0 {
		t.Fatalf("diagnostics: %+v", model.Diagnostics())
	}
	run := strings.Index(source, "(:method (run)")
	for i, call := range []string{"(work)", "(work (+", "(&work", "(Overloads::work"} {
		offset := strings.Index(source[run:], call) + run + 1
		definition, ok := model.DefinitionAt(offset)
		if !ok || definition.Range.Begin.Line != []int{2, 3, 3, 3}[i] {
			t.Errorf("%s: definition = %+v %v", call, definition, ok)
		}
	}
}

func TestResolvesAxiomOverloadsInsideMethodsAndAxioms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "axiom_overload_tooling.domain")
	source := "(:domain Overloads top_level_domain\n" +
		" (:method (run) top_level_method (b (and (#work) (#work 1) (#Overloads::work 2)) ()))\n" +
		" (:axiom (work) (and (#work 1)))\n (:axiom (work ?inp_x) ())\n)"
	store := tooling.NewStore()
	store.Open(path, source, 1)
	model := store.Model(path)
	if len(model.Diagnostics()) != 0 {
		t.Fatalf("diagnostics: %+v", model.Diagnostics())
	}
	offsets := []int{strings.Index(source, "#work)"), strings.Index(source, "#work 1"),
		strings.Index(source, "#Overloads::work"), strings.LastIndex(source, "#work 1")}
	for i, offset := range offsets {
		definition, ok := model.DefinitionAt(offset)
		if !ok || definition.Range.Begin.Line != []int{3, 4, 4, 4}[i] {
			t.Errorf("case %d: definition = %+v %v", i, definition, ok)
		}
	}
}

func TestAutocompleteListsVariablesInScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "complete.domain")
	source := "(:domain Complete top_level_domain\n (:method (run ?inp_target) top_level_method\n" +
		"  (b (and (enemy ?enemy) (= ?distance 3)) ((!attack ?enemy))))\n)"
	store := tooling.NewStore()
	store.Open(path, source, 1)
	candidates := store.Model(path).AutocompleteCandidates(strings.Index(source, "(!attack"))
	if strings.Join(candidates, ",") != "?distance,?enemy,?inp_target" {
		t.Fatalf("candidates = %v", candidates)
	}
}
