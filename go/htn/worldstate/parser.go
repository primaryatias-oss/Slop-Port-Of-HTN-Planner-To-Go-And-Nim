package worldstate

import (
	"fmt"
	"os"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

// World-state grammar (one fact per line):
//
//	<fact>       ::= <identifier> <argument>* <end-of-line>
//	<argument>   ::= '(' <argument>+ ')' | 'true' | 'false' | number | string | identifier-as-symbol

type parser struct {
	tokens   []lexer.Token
	position int
}

func (p *parser) token(i int) *lexer.Token {
	if i < len(p.tokens) {
		return &p.tokens[i]
	}
	return nil
}

func (p *parser) accept(t lexer.TokenType) *lexer.Token {
	tok := p.token(p.position)
	if tok == nil || tok.Type != t {
		return nil
	}
	p.position++
	return tok
}

func (p *parser) parseArgument() (atom.Atom, bool) {
	start := p.position
	if p.accept(lexer.LeftParenthesis) != nil {
		var elems []atom.Atom
		for {
			element, ok := p.parseArgument()
			if !ok {
				break
			}
			elems = append(elems, element)
		}
		if len(elems) == 0 {
			p.position = start
			return atom.Atom{}, false
		}
		if p.accept(lexer.RightParenthesis) == nil {
			p.position = start
			return atom.Atom{}, false
		}
		return atom.NewListOwned(elems), true
	}
	if tok := p.accept(lexer.KeywordTrue); tok != nil {
		return tok.Value, true
	}
	if tok := p.accept(lexer.KeywordFalse); tok != nil {
		return tok.Value, true
	}
	if tok := p.accept(lexer.Number); tok != nil {
		return tok.Value, true
	}
	if tok := p.accept(lexer.String); tok != nil {
		return tok.Value, true
	}
	if tok := p.accept(lexer.Identifier); tok != nil {
		return atom.NewSymbolText(tok.Value.Str()), true
	}
	p.position = start
	return atom.Atom{}, false
}

func (p *parser) parseFact(w *WorldState) bool {
	start := p.position
	identifier := p.accept(lexer.Identifier)
	if identifier == nil {
		p.position = start
		return false
	}
	// World-state facts are line-delimited: bare identifiers are valid symbol
	// arguments, so the line boundary separates consecutive facts.
	line := identifier.Range.Begin.Line
	var args []atom.Atom
	for {
		tok := p.token(p.position)
		if tok == nil || tok.Type == lexer.EndOfFile || tok.Range.Begin.Line != line {
			break
		}
		argument, ok := p.parseArgument()
		if !ok {
			break
		}
		args = append(args, argument)
	}
	if len(args) > MaxFactArguments {
		return false
	}
	w.AddFact(identifier.Value.Str(), args...)
	return true
}

// ParseText parses world-state text into w, appending rows. It returns false
// on lexical or syntax errors; facts parsed before the error are kept.
func ParseText(w *WorldState, text string) bool {
	tokens, ok := lexer.LexWorldState(text)
	if !ok {
		return false
	}
	p := &parser{tokens: tokens}
	for p.accept(lexer.EndOfFile) == nil {
		if !p.parseFact(w) {
			return false
		}
	}
	return true
}

// ParseFile reads and parses a world-state file into w.
func ParseFile(w *WorldState, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("world state [%s] could not be read: %w", path, err)
	}
	if !ParseText(w, string(data)) {
		return fmt.Errorf("world state [%s] could not be parsed", path)
	}
	return nil
}
