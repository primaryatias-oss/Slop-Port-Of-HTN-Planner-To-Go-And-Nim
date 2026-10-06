package compiler

import (
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

// ParserErrorCode enumerates syntax error kinds (HTNParserErrorCode).
type ParserErrorCode uint8

const (
	ErrNone ParserErrorCode = iota
	ErrTokenOutOfBounds
	ErrUnexpectedToken
	ErrAxiomPrefixInTaskList
	ErrUnclosedList
	ErrIncompleteSyntax
	ErrExpectedIdentifier
	ErrEmptyLiteralList
	ErrExpectedLiteral
	ErrInvalidArithmeticArity
	ErrExpectedCondition
	ErrInvalidNotCondition
	ErrInvalidComparisonArity
	ErrInvalidAssignment
	ErrImplicitAssignment
	ErrExpectedBoundCall
	ErrUnexpectedBoundCallSyntax
	ErrQualifiedFact
	ErrInvalidSplitArity
	ErrExpectedConditionBody
	ErrExpectedTask
	ErrQualifiedPrimitiveTask
	ErrExpectedBranch
	ErrExpectedDeclaration
	ErrExpectedConstant
	ErrExpectedConstantLiteral
	ErrExpectedParameterVariable
	ErrInvalidAxiomVisibility
	ErrUnexpectedAxiomSyntax
	ErrUnknownDeclaration
	ErrTrailingSource
	ErrExpectedDomain
	ErrLexingFailed
	ErrExpectedIncludePath
	ErrUnterminatedIncludePath
	ErrExpectedIncludeEnd
	ErrMisplacedInclude
)

// Shared diagnostic texts.
const (
	InvalidAssignmentDiagnostic  = "Assignment requires a variable destination and exactly one value expression"
	ImplicitAssignmentDiagnostic = "Implicit call-result binding is no longer supported; use an explicit assignment condition"
	AxiomPrefixInTaskDiagnostic  = "'#' is reserved for axiom calls; use '&' for deferred method calls"
)

// ParserError is the first syntax error found by a parse.
type ParserError struct {
	Code    ParserErrorCode
	Message string
	Range   lexer.Range
}

// HasError reports whether an error was recorded.
func (e ParserError) HasError() bool { return e.Code != ErrNone }

// Include is one (:include "path") directive.
type Include struct {
	Path  string
	Range lexer.Range
}

func isCSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

func isCAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func skipTrivia(text string, p int) int {
	for p < len(text) {
		if isCSpace(text[p]) {
			p++
		} else if strings.HasPrefix(text[p:], "//") {
			end := strings.IndexByte(text[p:], '\n')
			if end < 0 {
				p = len(text)
			} else {
				p += end + 1
			}
		} else {
			break
		}
	}
	return p
}

func readIncludePrefix(text string, p int) (int, bool) {
	if p >= len(text) || text[p] != '(' {
		return 0, false
	}
	p = skipTrivia(text, p+1)
	if p >= len(text) || text[p] != ':' {
		return 0, false
	}
	p = skipTrivia(text, p+1)
	if !strings.HasPrefix(text[p:], "include") {
		return 0, false
	}
	p += len("include")
	if p < len(text) && (isCAlnum(text[p]) || text[p] == '_') {
		return 0, false
	}
	return p, true
}

// SplitDomainFile extracts leading include directives and masks them with
// spaces so the remaining domain text keeps its original coordinates.
func SplitDomainFile(text string) (includes []Include, domainText string, perr ParserError) {
	masked := []byte(text)
	source := NewSourceText(text)
	fail := func(code ParserErrorCode, message string, begin, end int) ([]Include, string, ParserError) {
		return nil, text, ParserError{Code: code, Message: message,
			Range: lexer.Range{Begin: source.Position(begin), End: source.Position(end)}}
	}
	minInt := func(a, b int) int {
		if a < b {
			return a
		}
		return b
	}
	cursor := 0
	for {
		cursor = skipTrivia(text, cursor)
		position, ok := readIncludePrefix(text, cursor)
		if !ok {
			break
		}
		position = skipTrivia(text, position)
		if position >= len(text) || text[position] != '"' {
			return fail(ErrExpectedIncludePath, "Expected quoted path after :include", position, minInt(position+1, len(text)))
		}
		quote := position
		position++
		end := strings.IndexByte(text[position:], '"')
		if end < 0 {
			return fail(ErrUnterminatedIncludePath, "Unterminated :include path", quote, len(text))
		}
		end += position
		path := text[position:end]
		if path == "" {
			return fail(ErrExpectedIncludePath, "Expected non-empty :include path", quote, end+1)
		}
		position = skipTrivia(text, end+1)
		if position >= len(text) || text[position] != ')' {
			return fail(ErrExpectedIncludeEnd, "Expected ')' after :include path", position, minInt(position+1, len(text)))
		}
		position++
		includes = append(includes, Include{Path: path,
			Range: lexer.Range{Begin: source.Position(cursor), End: source.Position(position)}})
		for i := cursor; i < position; i++ {
			if masked[i] != '\n' && masked[i] != '\r' {
				masked[i] = ' '
			}
		}
		cursor = position
	}
	if cursor == len(text) {
		return fail(ErrExpectedDomain, "Missing :domain form", cursor, cursor)
	}
	// Ignore quoted text and comments when checking for misplaced directives.
	for cursor < len(text) {
		cursor = skipTrivia(text, cursor)
		if cursor == len(text) {
			break
		}
		if text[cursor] == '"' {
			end := strings.IndexByte(text[cursor+1:], '"')
			if end < 0 {
				break
			}
			cursor += end + 2
			continue
		}
		if end, ok := readIncludePrefix(text, cursor); ok {
			return fail(ErrMisplacedInclude, ":include directives are only allowed before :domain", cursor, end)
		}
		cursor++
	}
	return includes, string(masked), ParserError{}
}
