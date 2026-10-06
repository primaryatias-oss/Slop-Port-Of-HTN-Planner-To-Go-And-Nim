// Package tooling provides editor-facing analysis of HTN domains: the
// compiler tooling model (go-to-definition, completion, token kinds) and the
// in-memory document store used by the language server
// (HTNCompilerToolingModel and HTNDomainDocumentStore).
package tooling

import (
	"sort"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/internal/fspath"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

// TokenKind classifies the token under a cursor for semantic highlighting.
type TokenKind uint8

const (
	TokenNone TokenKind = iota
	TokenVariableValid
	TokenVariableInvalid
	TokenConstantValid
	TokenConstantInvalid
	TokenMethodValid
	TokenMethodInvalid
)

// Definition is a go-to-definition target.
type Definition struct {
	FilePath string
	Range    lexer.Range
}

// Model is the semantic model of one open document.
type Model struct {
	filePath    string
	text        string
	result      *compiler.LoadResult
	diagnostics []compiler.Diagnostic
	loaded      bool
}

// Analyze loads the document (with includes resolved through provider) in
// tooling mode, which does not require a top-level root domain.
func (m *Model) Analyze(filePath, text string, provider compiler.SourceProvider) {
	m.filePath = filePath
	m.text = text
	var sink compiler.DiagnosticSink
	result, ok := compiler.LoadFromSource(filePath, text, provider, &sink, compiler.LoadOptions{RequireTopLevelRoot: false})
	m.result = result
	m.loaded = ok
	m.diagnostics = sink.Diagnostics()
}

// Diagnostics returns the diagnostics of the last analysis.
func (m *Model) Diagnostics() []compiler.Diagnostic { return m.diagnostics }

// IsLoaded reports whether the last analysis succeeded.
func (m *Model) IsLoaded() bool { return m.loaded }

// LoadResult returns the linked domain of the last successful analysis.
func (m *Model) LoadResult() *compiler.LoadResult { return m.result }

// SamePath compares two paths after std::filesystem::weakly_canonical.
func SamePath(a, b string) bool { return fspath.Same(a, b) }

func contains(r lexer.Range, offset int) bool {
	return offset >= r.Begin.Offset && offset <= r.End.Offset
}

func valueText(v *compiler.Value) string {
	if v == nil {
		return ""
	}
	return atom.ToString(v.Atom, false)
}

func isAtomCharacter(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == '-' ||
		c == atom.VariablePrefix || c == atom.ConstantPrefix || c == atom.DeclarationPrefix ||
		c == atom.AxiomCallPrefix || c == atom.DeferredCallPrefix
}

// findAtom returns the identifier-like run around offset.
func findAtom(text string, offset int) (begin, end int, ok bool) {
	if text == "" {
		return 0, 0, false
	}
	if offset > len(text) {
		offset = len(text)
	}
	probe := offset
	if probe == len(text) || !isAtomCharacter(text[probe]) {
		if probe == 0 || !isAtomCharacter(text[probe-1]) {
			return 0, 0, false
		}
		probe--
	}
	begin = probe
	for begin > 0 && isAtomCharacter(text[begin-1]) {
		begin--
	}
	end = probe + 1
	for end < len(text) && isAtomCharacter(text[end]) {
		end++
	}
	return begin, end, true
}

func findAxiomCall(c *compiler.Condition, offset int, id string) *compiler.Condition {
	if c == nil || !contains(c.Range, offset) {
		return nil
	}
	if c.Kind == compiler.CondAxiom && valueText(c.ID) == id {
		return c
	}
	for _, child := range c.Children {
		if call := findAxiomCall(child, offset, id); call != nil {
			return call
		}
	}
	return nil
}

func addVariables(c *compiler.Condition, cursor int, variables map[string]bool) {
	if c == nil || c.Range.Begin.Offset >= cursor || c.Kind == compiler.CondNot {
		return
	}
	for _, argument := range c.Arguments {
		if argument != nil && argument.Kind == compiler.ValueVariable && argument.Range.Begin.Offset < cursor {
			variables["?"+valueText(argument)] = true
		}
	}
	if c.Output != nil && c.Output.Range.Begin.Offset < cursor {
		variables["?"+valueText(c.Output)] = true
	}
	for _, child := range c.Children {
		addVariables(child, cursor, variables)
	}
}

func (m *Model) currentFileIndex() int {
	if m.result == nil {
		return -1
	}
	for i, file := range m.result.SourceFiles {
		if SamePath(file, m.filePath) {
			return i
		}
	}
	return -1
}

// AutocompleteCandidates returns the variables in scope at the cursor of the
// method branch containing it, sorted.
func (m *Model) AutocompleteCandidates(cursor int) []string {
	fileIndex := m.currentFileIndex()
	if fileIndex < 0 {
		return nil
	}
	variables := map[string]bool{}
	for _, method := range m.result.Domain.Methods {
		if method == nil || method.FileIndex != uint32(fileIndex) || !contains(method.Range, cursor) {
			continue
		}
		for _, parameter := range method.Parameters {
			variables["?"+valueText(parameter)] = true
		}
		for _, branch := range method.Branches {
			if branch != nil && contains(branch.Range, cursor) {
				addVariables(branch.Precondition, cursor, variables)
				break
			}
		}
		break
	}
	candidates := make([]string, 0, len(variables))
	for variable := range variables {
		candidates = append(candidates, variable)
	}
	sort.Strings(candidates)
	return candidates
}

// DefinitionAt resolves the constant, axiom or method referenced at cursor.
func (m *Model) DefinitionAt(cursor int) (Definition, bool) {
	begin, end, ok := findAtom(m.text, cursor)
	if !ok || m.result == nil {
		return Definition{}, false
	}
	symbol := m.text[begin:end]
	lookup := symbol
	if symbol != "" && (symbol[0] == atom.DeferredCallPrefix || symbol[0] == atom.AxiomCallPrefix) {
		lookup = symbol[1:]
	}
	files := m.result.SourceFiles
	domain := m.result.Domain
	if symbol != "" && symbol[0] == atom.ConstantPrefix {
		id := symbol[1:]
		for _, group := range domain.ConstantGroups {
			for _, constant := range group.Constants {
				if constant.ID == id && int(constant.FileIndex) < len(files) {
					return Definition{FilePath: files[constant.FileIndex], Range: constant.Range}, true
				}
			}
		}
		return Definition{}, false
	}
	fileIndex := m.currentFileIndex()
	var axiomCall *compiler.Condition
	for _, owner := range domain.Methods {
		if owner == nil || int(owner.FileIndex) != fileIndex {
			continue
		}
		for _, branch := range owner.Branches {
			if call := findAxiomCall(branch.Precondition, begin, lookup); call != nil {
				axiomCall = call
			}
		}
	}
	for _, owner := range domain.Axioms {
		if owner != nil && int(owner.FileIndex) == fileIndex {
			if call := findAxiomCall(owner.Body, begin, lookup); call != nil {
				axiomCall = call
			}
		}
	}
	if axiomCall != nil {
		for _, axiom := range domain.Axioms {
			if axiom != nil && axiom.ID == lookup && len(axiom.Parameters) == len(axiomCall.Arguments) &&
				int(axiom.FileIndex) < len(files) {
				return Definition{FilePath: files[axiom.FileIndex], Range: axiom.Range}, true
			}
		}
		return Definition{}, false
	}
	if symbol != "" && symbol[0] == atom.AxiomCallPrefix {
		return Definition{}, false
	}
	argumentCount, hasCall := 0, false
	for _, owner := range domain.Methods {
		if owner == nil || int(owner.FileIndex) != fileIndex {
			continue
		}
		for _, branch := range owner.Branches {
			for _, task := range branch.Tasks {
				if task != nil && contains(task.Range, begin) && valueText(task.ID) == lookup {
					hasCall = true
					argumentCount = len(task.Arguments)
				}
			}
		}
	}
	var match *compiler.Method
	for _, method := range domain.Methods {
		if method == nil || int(method.FileIndex) >= len(files) || method.ID != lookup {
			continue
		}
		if hasCall && len(method.Parameters) != argumentCount {
			continue
		}
		if match != nil {
			return Definition{}, false // Without a call, an overloaded name is ambiguous.
		}
		match = method
	}
	if match != nil {
		return Definition{FilePath: files[match.FileIndex], Range: match.Range}, true
	}
	return Definition{}, false
}

// TokenKindAt classifies the token at offset.
func (m *Model) TokenKindAt(offset int) TokenKind {
	begin, end, ok := findAtom(m.text, offset)
	if !ok {
		return TokenNone
	}
	symbol := m.text[begin:end]
	if symbol != "" && symbol[0] == atom.VariablePrefix {
		for _, candidate := range m.AutocompleteCandidates(begin) {
			if candidate == symbol {
				return TokenVariableValid
			}
		}
		return TokenVariableInvalid
	}
	if symbol != "" && symbol[0] == atom.ConstantPrefix {
		if _, found := m.DefinitionAt(begin); found {
			return TokenConstantValid
		}
		return TokenConstantInvalid
	}
	if _, found := m.DefinitionAt(begin); found {
		return TokenMethodValid
	}
	if strings.Contains(symbol, "::") {
		return TokenMethodInvalid
	}
	return TokenNone
}
