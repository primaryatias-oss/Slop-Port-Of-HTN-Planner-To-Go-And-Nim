// Package lexer contains the token model and the two lexers of the HTN
// languages: the domain lexer used by the compiler and the world-state lexer
// used by the fact database parser. Both share the identifier, number, string
// and comment rules of the original HTNLexerBase.
package lexer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
)

// Position is a source location. Line and Column are 1-based; Offset is a
// 0-based byte offset.
type Position struct {
	Offset int
	Line   int
	Column int
}

// Range is a half-open source range.
type Range struct {
	Begin Position
	End   Position
}

// DefaultPosition is the default HTNSourcePosition value.
var DefaultPosition = Position{Offset: 0, Line: 1, Column: 1}

// DefaultRange is the default HTNSourceRange value.
var DefaultRange = Range{Begin: DefaultPosition, End: DefaultPosition}

// TokenType enumerates token kinds in the original declaration order.
type TokenType uint8

const (
	Colon TokenType = iota
	LeftParenthesis
	RightParenthesis
	ExclamationMark
	QuestionMark
	Hash
	Ampersand
	At

	Assign
	EqualEqual
	NotEqual
	Less
	LessEqual
	Greater
	GreaterEqual

	Plus
	Minus
	Increment
	Decrement
	Multiply
	Divide
	Modulo

	KeywordDomain
	KeywordTopLevelDomain
	KeywordBase
	KeywordOverrides
	KeywordMethod
	KeywordTopLevelMethod
	KeywordAxiom
	KeywordConstants
	KeywordAnd
	KeywordOr
	KeywordAlt
	KeywordNot
	KeywordCall
	KeywordTrue
	KeywordFalse

	Identifier
	Number
	String

	EndOfFile
)

var tokenTypeNames = [...]string{
	"COLON", "LEFT_PARENTHESIS", "RIGHT_PARENTHESIS", "EXCLAMATION_MARK", "QUESTION_MARK", "HASH", "AMPERSAND", "AT",
	"ASSIGN", "EQUAL_EQUAL", "NOT_EQUAL", "LESS", "LESS_EQUAL", "GREATER", "GREATER_EQUAL",
	"PLUS", "MINUS", "INCREMENT", "DECREMENT", "MULTIPLY", "DIVIDE", "MODULO",
	"HTN_DOMAIN", "HTN_TOP_LEVEL_DOMAIN", "HTN_BASE", "HTN_OVERRIDES", "HTN_METHOD", "HTN_TOP_LEVEL_METHOD",
	"HTN_AXIOM", "HTN_CONSTANTS", "AND", "OR", "ALT", "NOT", "CALL", "TRUE", "FALSE",
	"IDENTIFIER", "NUMBER", "STRING", "END_OF_FILE",
}

// String returns the original enumerator name.
func (t TokenType) String() string {
	if int(t) < len(tokenTypeNames) {
		return tokenTypeNames[t]
	}
	return "INVALID"
}

// Token is one lexical unit with its value and source range.
type Token struct {
	Type   TokenType
	Value  atom.Atom
	Range  Range
	Lexeme string
}

// IsValidCharacter reports whether lexing may continue at c.
func IsValidCharacter(c byte) bool { return c != 0 }

// IsLetter matches the original rule, including its quirk that every ASCII
// character at or above '_' (which also covers '`', '{', '|', '}' and '~') is
// treated as a letter. Non-ASCII bytes are negative chars in the original and
// therefore never letters.
func IsLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '_' && c < 0x80)
}

// IsDigit reports whether c is an ASCII digit.
func IsDigit(c byte) bool { return c >= '0' && c <= '9' }

// IsAlphanumeric reports whether c is a letter or digit.
func IsAlphanumeric(c byte) bool { return IsLetter(c) || IsDigit(c) }

// SpecialCharacterEscape renders tabs and newlines as escape sequences.
func SpecialCharacterEscape(c byte) string {
	switch c {
	case '\t':
		return "\\t"
	case '\n':
		return "\\n"
	}
	return string([]byte{c})
}

// Context is the mutable lexing state shared by both lexers.
type Context struct {
	text          string
	tokens        []Token
	position      int
	row           int
	column        int
	tokenStart    int
	tokenStartRow int
	tokenStartCol int
	lastError     string
	lastErrorAt   Range
}

// NewContext creates a lexing context for text.
func NewContext(text string) *Context { return &Context{text: text} }

// Tokens returns the tokens produced so far.
func (c *Context) Tokens() []Token { return c.tokens }

// LastError returns the most recent lexical error message and range.
func (c *Context) LastError() (string, Range) { return c.lastError, c.lastErrorAt }

func (c *Context) setLastError(message string, r Range) {
	c.lastError = message
	c.lastErrorAt = r
}

// Character returns the character at the current position plus offset, or 0
// past the end of the text.
func (c *Context) Character(offset int) byte {
	p := c.position + offset
	if p < len(c.text) {
		return c.text[p]
	}
	return 0
}

// Advance moves one character forward while maintaining line/column state.
func (c *Context) Advance(newLine bool) {
	if c.position >= len(c.text) {
		return
	}
	c.position++
	if newLine {
		c.row++
		c.column = 0
	} else {
		c.column++
	}
}

func (c *Context) markTokenStart() {
	c.tokenStart = c.position
	c.tokenStartRow = c.row
	c.tokenStartCol = c.column
}

func (c *Context) currentPosition() Position {
	return Position{Offset: c.position, Line: c.row + 1, Column: c.column + 1}
}

func (c *Context) addToken(value atom.Atom, t TokenType, lexeme string) {
	var r Range
	r.Begin = Position{Offset: c.tokenStart, Line: c.tokenStartRow + 1, Column: c.tokenStartCol + 1}
	end := c.position
	if end < c.tokenStart+1 {
		end = c.tokenStart + 1
	}
	r.End = Position{Offset: end, Line: c.row + 1, Column: c.column + 1}
	if r.End.Offset <= r.Begin.Offset {
		r.End = Position{Offset: r.Begin.Offset + 1, Line: r.Begin.Line, Column: r.Begin.Column + 1}
	}
	if t == EndOfFile {
		r.End = r.Begin
	}
	c.tokens = append(c.tokens, Token{Type: t, Value: value, Range: r, Lexeme: lexeme})
}

func (c *Context) lexIdentifier(keywords map[string]TokenType) {
	start := c.position
	c.Advance(false)
	for IsAlphanumeric(c.Character(0)) {
		c.Advance(false)
	}
	lexeme := c.text[start:c.position]
	t, isKeyword := keywords[lexeme]
	if !isKeyword {
		t = Identifier
	}
	switch t {
	case KeywordTrue:
		c.addToken(atom.NewBool(true), t, lexeme)
	case KeywordFalse:
		c.addToken(atom.NewBool(false), t, lexeme)
	default:
		c.addToken(atom.NewString(lexeme), t, lexeme)
	}
}

func (c *Context) lexNumber() bool {
	start := c.position
	begin := Position{Offset: start, Line: c.row + 1, Column: c.column + 1}
	c.Advance(false)
	for IsDigit(c.Character(0)) {
		c.Advance(false)
	}
	isFloat := c.Character(0) == '.' && IsDigit(c.Character(1))
	if isFloat {
		c.Advance(false)
		for IsDigit(c.Character(0)) {
			c.Advance(false)
		}
	}
	lexeme := c.text[start:c.position]
	var value atom.Atom
	ok := true
	if isFloat {
		// std::from_chars reports result_out_of_range for overflow and for
		// non-zero literals that underflow to zero.
		f, err := strconv.ParseFloat(lexeme, 32)
		if err != nil || (f == 0 && strings.ContainsAny(lexeme, "123456789")) {
			ok = false
		} else {
			value = atom.NewFloat(float32(f))
		}
	} else {
		i, err := strconv.ParseInt(lexeme, 10, 32)
		if err != nil {
			ok = false
		} else {
			value = atom.NewInt(int32(i))
		}
	}
	if !ok {
		c.setLastError("Number out of bounds", Range{Begin: begin, End: c.currentPosition()})
		return false
	}
	c.addToken(value, Number, lexeme)
	return true
}

func (c *Context) lexString() bool {
	start := c.position
	c.Advance(false)
	for ch := c.Character(0); ch != '"' && c.position < len(c.text); ch = c.Character(0) {
		c.Advance(ch == '\n')
	}
	if c.Character(0) != '"' {
		p := c.currentPosition()
		c.setLastError("Character '\"' could not be found at end of the string", Range{Begin: p, End: p})
		return false
	}
	end := c.position
	c.Advance(false)
	lexeme := c.text[start+1 : end]
	c.addToken(atom.NewString(lexeme), String, lexeme)
	return true
}

func (c *Context) lexComment() {
	for ch := c.Character(0); ch != '\n' && c.position < len(c.text); ch = c.Character(0) {
		c.Advance(false)
	}
	if c.Character(0) == '\n' {
		c.Advance(true)
	}
}

var worldStateKeywords = map[string]TokenType{
	"call": KeywordCall, "true": KeywordTrue, "false": KeywordFalse,
}

var domainKeywords = map[string]TokenType{
	"domain": KeywordDomain, "top_level_domain": KeywordTopLevelDomain,
	"base": KeywordBase, "overrides": KeywordOverrides,
	"method": KeywordMethod, "top_level_method": KeywordTopLevelMethod,
	"axiom": KeywordAxiom, "constants": KeywordConstants,
	"and": KeywordAnd, "or": KeywordOr, "alt": KeywordAlt, "not": KeywordNot,
	"call": KeywordCall, "true": KeywordTrue, "false": KeywordFalse,
}

// LexWorldState tokenizes world-state text. It returns false when any
// lexical error occurred; tokens up to the end of text are still produced.
func LexWorldState(text string) ([]Token, bool) {
	c := NewContext(text)
	result := true
	for ch := c.Character(0); IsValidCharacter(ch); ch = c.Character(0) {
		c.markTokenStart()
		switch ch {
		case '(':
			c.addToken(atom.Atom{}, LeftParenthesis, "(")
			c.Advance(false)
		case ')':
			c.addToken(atom.Atom{}, RightParenthesis, ")")
			c.Advance(false)
		case '/':
			if c.Character(1) == '/' {
				c.lexComment()
				break
			}
			result = false
			c.Advance(false)
		case '"':
			result = c.lexString() && result
		case '\r', ' ':
			c.Advance(false)
		case '\n':
			c.Advance(true)
		default:
			if IsDigit(ch) {
				result = c.lexNumber() && result
				break
			}
			if IsLetter(ch) {
				c.lexIdentifier(worldStateKeywords)
				break
			}
			result = false
			c.Advance(false)
		}
	}
	c.markTokenStart()
	c.addToken(atom.Atom{}, EndOfFile, "")
	return c.tokens, result
}

// DomainLexResult holds the output of LexDomain.
type DomainLexResult struct {
	Tokens     []Token
	OK         bool
	Error      string
	ErrorRange Range
}

// LexDomain tokenizes domain text with the compiler lexer rules.
func LexDomain(text string) DomainLexResult {
	c := NewContext(text)
	result := true
	for ch := c.Character(0); IsValidCharacter(ch); ch = c.Character(0) {
		c.markTokenStart()
		switch ch {
		case ':':
			c.addToken(atom.Atom{}, Colon, ":")
			c.Advance(false)
		case '(':
			c.addToken(atom.Atom{}, LeftParenthesis, "(")
			c.Advance(false)
		case ')':
			c.addToken(atom.Atom{}, RightParenthesis, ")")
			c.Advance(false)
		case '!':
			if c.Character(1) == '=' {
				c.addToken(atom.Atom{}, NotEqual, "!=")
				c.Advance(false)
				c.Advance(false)
			} else {
				c.addToken(atom.Atom{}, ExclamationMark, "!")
				c.Advance(false)
			}
		case '=':
			if c.Character(1) == '=' {
				c.addToken(atom.Atom{}, EqualEqual, "==")
				c.Advance(false)
				c.Advance(false)
				break
			}
			c.addToken(atom.Atom{}, Assign, "=")
			c.Advance(false)
		case '<':
			if c.Character(1) == '=' {
				c.addToken(atom.Atom{}, LessEqual, "<=")
				c.Advance(false)
				c.Advance(false)
			} else {
				c.addToken(atom.Atom{}, Less, "<")
				c.Advance(false)
			}
		case '>':
			if c.Character(1) == '=' {
				c.addToken(atom.Atom{}, GreaterEqual, ">=")
				c.Advance(false)
				c.Advance(false)
			} else {
				c.addToken(atom.Atom{}, Greater, ">")
				c.Advance(false)
			}
		case '+':
			if c.Character(1) == '+' {
				c.addToken(atom.Atom{}, Increment, "++")
				c.Advance(false)
				c.Advance(false)
			} else {
				c.addToken(atom.Atom{}, Plus, "+")
				c.Advance(false)
			}
		case '-':
			if c.Character(1) == '-' {
				c.addToken(atom.Atom{}, Decrement, "--")
				c.Advance(false)
				c.Advance(false)
			} else {
				c.addToken(atom.Atom{}, Minus, "-")
				c.Advance(false)
			}
		case '*':
			c.addToken(atom.Atom{}, Multiply, "*")
			c.Advance(false)
		case '%':
			c.addToken(atom.Atom{}, Modulo, "%")
			c.Advance(false)
		case '?':
			c.addToken(atom.Atom{}, QuestionMark, "?")
			c.Advance(false)
		case '#':
			c.addToken(atom.Atom{}, Hash, "#")
			c.Advance(false)
		case '&':
			c.addToken(atom.Atom{}, Ampersand, "&")
			c.Advance(false)
		case '@':
			c.addToken(atom.Atom{}, At, "@")
			c.Advance(false)
		case '/':
			if c.Character(1) == '/' {
				c.lexComment()
				break
			}
			c.addToken(atom.Atom{}, Divide, "/")
			c.Advance(false)
		case '"':
			result = c.lexString() && result
		case '\r', ' ', '\t':
			c.Advance(false)
		case '\n':
			c.Advance(true)
		default:
			if IsDigit(ch) {
				result = c.lexNumber() && result
				break
			}
			if IsLetter(ch) {
				c.lexIdentifier(domainKeywords)
				break
			}
			p := c.currentPosition()
			c.setLastError(fmt.Sprintf("Character [%s] not recognized", SpecialCharacterEscape(ch)),
				Range{Begin: p, End: Position{Offset: p.Offset + 1, Line: p.Line, Column: p.Column + 1}})
			result = false
			c.Advance(false)
		}
	}
	c.markTokenStart()
	c.addToken(atom.Atom{}, EndOfFile, "")
	msg, r := c.LastError()
	return DomainLexResult{Tokens: c.tokens, OK: result, Error: msg, ErrorRange: r}
}
