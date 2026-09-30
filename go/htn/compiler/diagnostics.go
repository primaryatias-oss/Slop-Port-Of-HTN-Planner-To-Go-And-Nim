// Package compiler is the HTN domain compiler frontend: lexing, syntax
// parsing into a compiler-owned AST, include traversal and linking, semantic
// validation and lowering into the compiler IR consumed by code generators.
package compiler

import (
	"sort"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

// Severity of a diagnostic.
type Severity uint8

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityError
)

// String returns "info", "warning" or "error".
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	}
	return "error"
}

// Recovery classifies whether processing could continue after a diagnostic.
type Recovery uint8

const (
	RecoveryRecoverable Recovery = iota
	RecoveryDependent
	RecoveryFatal
)

// Diagnostic is one compiler message with its source location.
type Diagnostic struct {
	Severity Severity
	Recovery Recovery
	FilePath string
	Range    lexer.Range
	Message  string
}

// DiagnosticSink collects diagnostics and suppresses exact duplicates.
type DiagnosticSink struct {
	diagnostics []Diagnostic
}

// Clear removes every diagnostic.
func (s *DiagnosticSink) Clear() { s.diagnostics = nil }

// Report adds a diagnostic unless an identical one is already present.
func (s *DiagnosticSink) Report(d Diagnostic) {
	for _, existing := range s.diagnostics {
		if existing == d {
			return
		}
	}
	s.diagnostics = append(s.diagnostics, d)
}

// Error reports an error diagnostic.
func (s *DiagnosticSink) Error(filePath, message string, recovery Recovery, r lexer.Range) {
	s.Report(Diagnostic{Severity: SeverityError, Recovery: recovery, FilePath: filePath, Range: r, Message: message})
}

// Warning reports a warning diagnostic.
func (s *DiagnosticSink) Warning(filePath, message string, r lexer.Range) {
	s.Report(Diagnostic{Severity: SeverityWarning, Recovery: RecoveryRecoverable, FilePath: filePath, Range: r, Message: message})
}

// HasErrors reports whether any error was recorded.
func (s *DiagnosticSink) HasErrors() bool {
	for _, d := range s.diagnostics {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// HasFatalErrors reports whether any fatal error was recorded.
func (s *DiagnosticSink) HasFatalErrors() bool {
	for _, d := range s.diagnostics {
		if d.Severity == SeverityError && d.Recovery == RecoveryFatal {
			return true
		}
	}
	return false
}

// ErrorCount returns the number of errors.
func (s *DiagnosticSink) ErrorCount() int {
	count := 0
	for _, d := range s.diagnostics {
		if d.Severity == SeverityError {
			count++
		}
	}
	return count
}

// Diagnostics returns every diagnostic in report order.
func (s *DiagnosticSink) Diagnostics() []Diagnostic { return s.diagnostics }

// FirstError returns the first error, or nil.
func (s *DiagnosticSink) FirstError() *Diagnostic {
	for i := range s.diagnostics {
		if s.diagnostics[i].Severity == SeverityError {
			return &s.diagnostics[i]
		}
	}
	return nil
}

// SourceText maps byte offsets to line/column positions.
type SourceText struct {
	text       string
	lineStarts []int
}

// NewSourceText indexes text.
func NewSourceText(text string) *SourceText {
	s := &SourceText{text: text, lineStarts: []int{0}}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			s.lineStarts = append(s.lineStarts, i+1)
		}
	}
	return s
}

// Text returns the indexed text.
func (s *SourceText) Text() string { return s.text }

// Position converts a byte offset into a position (clamped to the text size).
func (s *SourceText) Position(offset int) lexer.Position {
	if offset > len(s.text) {
		offset = len(s.text)
	}
	if offset < 0 {
		offset = 0
	}
	line := sort.Search(len(s.lineStarts), func(i int) bool { return s.lineStarts[i] > offset }) - 1
	if line < 0 {
		line = 0
	}
	return lexer.Position{Offset: offset, Line: line + 1, Column: offset - s.lineStarts[line] + 1}
}

// Offset converts a 1-based line/column into a clamped byte offset.
func (s *SourceText) Offset(line, column int) int {
	if len(s.lineStarts) == 0 {
		return 0
	}
	if line < 1 {
		line = 1
	}
	if line > len(s.lineStarts) {
		line = len(s.lineStarts)
	}
	start := s.lineStarts[line-1]
	end := len(s.text)
	if line < len(s.lineStarts) {
		end = s.lineStarts[line] - 1
	}
	if column < 1 {
		column = 1
	}
	offset := start + column - 1
	if offset > end {
		offset = end
	}
	return offset
}
