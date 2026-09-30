package compiler

import (
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

type form struct {
	tokenType lexer.TokenType
	value     atom.Atom
	rng       lexer.Range
	isList    bool
	items     []form
}

func emptyForm() form { return form{tokenType: lexer.EndOfFile, rng: lexer.DefaultRange} }

type syntaxParser struct {
	errorRange lexer.Range
	err        ParserError
	fileIndex  uint32
}

func (p *syntaxParser) invalid(code ParserErrorCode, message string) {
	if !p.err.HasError() {
		p.err = ParserError{Code: code, Message: message, Range: p.errorRange}
	}
}

func (p *syntaxParser) failed() bool { return p.err.HasError() }

func (p *syntaxParser) readForm(tokens []lexer.Token, position *int) form {
	if *position >= len(tokens) {
		p.invalid(ErrTokenOutOfBounds, "Unexpected end of compiler syntax")
		return emptyForm()
	}
	token := tokens[*position]
	*position++
	p.errorRange = token.Range
	result := form{tokenType: token.Type, value: token.Value, rng: token.Range}
	if result.tokenType == lexer.RightParenthesis || result.tokenType == lexer.EndOfFile {
		p.invalid(ErrUnexpectedToken, "Unexpected compiler syntax token")
		return emptyForm()
	}
	if result.tokenType != lexer.LeftParenthesis {
		return result
	}
	result.isList = true
	for *position < len(tokens) && tokens[*position].Type != lexer.RightParenthesis {
		if tokens[*position].Type == lexer.EndOfFile {
			p.invalid(ErrUnclosedList, "Unclosed compiler syntax list")
			return emptyForm()
		}
		result.items = append(result.items, p.readForm(tokens, position))
		if p.failed() {
			return emptyForm()
		}
	}
	if *position >= len(tokens) {
		p.invalid(ErrUnclosedList, "Unclosed compiler syntax list")
		return emptyForm()
	}
	result.rng.End = tokens[*position].Range.End
	*position++
	return result
}

var emptyFormValue = emptyForm()

func (p *syntaxParser) at(items []form, index int) *form {
	if index >= len(items) {
		p.invalid(ErrIncompleteSyntax, "Incomplete compiler syntax")
		return &emptyFormValue
	}
	return &items[index]
}

func is(f *form, t lexer.TokenType) bool { return !f.isList && f.tokenType == t }

func (p *syntaxParser) name(f *form) string {
	if !is(f, lexer.Identifier) {
		p.invalid(ErrExpectedIdentifier, "Expected compiler identifier")
		return ""
	}
	return f.value.Str()
}

func (p *syntaxParser) source(n *Node, r lexer.Range) {
	n.Range = r
	n.FileIndex = p.fileIndex
}

func (p *syntaxParser) identifier(f *form, name string) *Value {
	result := &Value{Kind: ValueIdentifier}
	if name == "" {
		name = p.name(f)
	}
	result.Atom = atom.NewString(name)
	p.source(&result.Node, f.rng)
	return result
}

func (p *syntaxParser) literal(f *form) atom.Atom {
	if f.isList {
		if len(f.items) == 0 {
			p.invalid(ErrEmptyLiteralList, "Empty literal list")
			return atom.NewString("")
		}
		elems := make([]atom.Atom, 0, len(f.items))
		for i := range f.items {
			element := p.literal(&f.items[i])
			if p.failed() {
				return atom.NewString("")
			}
			elems = append(elems, element)
		}
		return atom.NewListOwned(elems)
	}
	if is(f, lexer.Identifier) {
		return atom.NewSymbolText(p.name(f))
	}
	if !is(f, lexer.KeywordTrue) && !is(f, lexer.KeywordFalse) && !is(f, lexer.Number) && !is(f, lexer.String) {
		p.invalid(ErrExpectedLiteral, "Expected compiler literal")
		return atom.NewString("")
	}
	return f.value
}

func arithmeticOperatorOf(f *form) (ArithmeticOperator, bool) {
	switch {
	case is(f, lexer.Plus):
		return OpAdd, true
	case is(f, lexer.Minus):
		return OpSubtract, true
	case is(f, lexer.Increment):
		return OpIncrement, true
	case is(f, lexer.Decrement):
		return OpDecrement, true
	case is(f, lexer.Multiply):
		return OpMultiply, true
	case is(f, lexer.Divide):
		return OpDivide, true
	case is(f, lexer.Modulo):
		return OpModulo, true
	}
	return OpAdd, false
}

func (p *syntaxParser) argument(items []form, index *int) *Value {
	head := p.at(items, *index)
	*index++
	result := &Value{}
	p.source(&result.Node, head.rng)
	if is(head, lexer.QuestionMark) || is(head, lexer.At) {
		id := p.at(items, *index)
		*index++
		if p.failed() {
			return nil
		}
		if is(head, lexer.QuestionMark) {
			result.Kind = ValueVariable
		} else {
			result.Kind = ValueConstant
		}
		result.Atom = atom.NewString(p.name(id))
		result.Range.End = id.rng.End
	} else if op, ok := arithmeticOperatorHead(head); ok {
		result.Kind = ValueArithmetic
		result.ArithmeticOp = op
		result.Atom = atom.Atom{}
		result.Range = head.rng
		for i := 1; i < len(head.items); {
			result.ArithmeticOperands = append(result.ArithmeticOperands, p.argument(head.items, &i))
			if p.failed() {
				return nil
			}
		}
		count := len(result.ArithmeticOperands)
		var valid bool
		switch op {
		case OpIncrement, OpDecrement:
			valid = count == 1
		case OpSubtract:
			valid = count >= 1
		case OpModulo:
			valid = count == 2
		default:
			valid = count >= 2
		}
		if !valid {
			p.invalid(ErrInvalidArithmeticArity, "Invalid arithmetic expression arity")
			return nil
		}
	} else if head.isList && len(head.items) > 0 && is(&head.items[0], lexer.KeywordCall) {
		result.Kind = ValueCall
		result.CallID = p.identifier(p.at(head.items, 1), "")
		if p.failed() {
			return nil
		}
		result.Atom = result.CallID.Atom
		for i := 2; i < len(head.items); {
			result.CallArguments = append(result.CallArguments, p.argument(head.items, &i))
			if p.failed() {
				return nil
			}
		}
	} else {
		result.Kind = ValueLiteral
		result.Atom = p.literal(head)
	}
	if p.failed() {
		return nil
	}
	return result
}

func arithmeticOperatorHead(head *form) (ArithmeticOperator, bool) {
	if !head.isList || len(head.items) == 0 {
		return OpAdd, false
	}
	return arithmeticOperatorOf(&head.items[0])
}

func (p *syntaxParser) qualifiedIdentifier(items []form, start int) (*Value, int) {
	first := p.at(items, start)
	id := p.name(first)
	if p.failed() {
		return nil, start
	}
	next := start + 1
	if next+2 < len(items) && is(&items[next], lexer.Colon) && is(&items[next+1], lexer.Colon) {
		id += "::" + p.name(&items[next+2])
		if p.failed() {
			return nil, start
		}
		next += 3
	}
	return p.identifier(first, id), next
}

func (p *syntaxParser) condition(f *form) *Condition {
	p.errorRange = f.rng
	if !f.isList || len(f.items) == 0 {
		p.invalid(ErrExpectedCondition, "Expected compiler condition")
		return nil
	}
	items := f.items
	result := &Condition{}
	p.source(&result.Node, f.rng)
	first := &items[0]
	if is(first, lexer.KeywordAnd) || is(first, lexer.KeywordOr) || is(first, lexer.KeywordAlt) {
		switch {
		case is(first, lexer.KeywordAnd):
			result.Kind = CondAnd
		case is(first, lexer.KeywordOr):
			result.Kind = CondOr
		default:
			result.Kind = CondAlt
		}
		result.Range.Begin = first.rng.Begin
		if len(items) > 1 {
			result.Range.End = items[len(items)-1].rng.End
		} else {
			result.Range.End = first.rng.End
		}
		for i := 1; i < len(items); i++ {
			result.Children = append(result.Children, p.condition(&items[i]))
			if p.failed() {
				return nil
			}
		}
		return result
	}
	if is(first, lexer.KeywordNot) {
		if len(items) != 2 {
			p.invalid(ErrInvalidNotCondition, "Invalid not condition")
			return nil
		}
		result.Kind = CondNot
		result.Children = append(result.Children, p.condition(p.at(items, 1)))
		result.Range.End = items[len(items)-1].rng.End
		return result
	}
	firstType := first.tokenType
	if firstType == lexer.EqualEqual || firstType == lexer.NotEqual || firstType == lexer.Less ||
		firstType == lexer.LessEqual || firstType == lexer.Greater || firstType == lexer.GreaterEqual {
		result.Kind = CondComparison
		switch firstType {
		case lexer.EqualEqual:
			result.Operator = 0
		case lexer.NotEqual:
			result.Operator = 1
		case lexer.Less:
			result.Operator = 2
		case lexer.LessEqual:
			result.Operator = 3
		case lexer.Greater:
			result.Operator = 4
		default:
			result.Operator = 5
		}
		result.Range.Begin = first.rng.Begin
		result.Range.End = items[len(items)-1].rng.End
		for i := 1; i < len(items); {
			result.Arguments = append(result.Arguments, p.argument(items, &i))
			if p.failed() {
				return nil
			}
		}
		if len(result.Arguments) != 2 {
			p.invalid(ErrInvalidComparisonArity, "Comparison requires two arguments")
			return nil
		}
		return result
	}
	index := 0
	if is(&items[index], lexer.QuestionMark) {
		p.invalid(ErrImplicitAssignment, ImplicitAssignmentDiagnostic)
		return nil
	} else if is(&items[index], lexer.Assign) {
		result.Kind = CondAssignment
		index++
		if index >= len(items) || !is(&items[index], lexer.QuestionMark) {
			p.invalid(ErrInvalidAssignment, InvalidAssignmentDiagnostic)
			return nil
		}
		result.Output = p.argument(items, &index)
		if p.failed() {
			return nil
		}
		if index >= len(items) {
			p.invalid(ErrInvalidAssignment, InvalidAssignmentDiagnostic)
			return nil
		}
		result.Arguments = append(result.Arguments, p.argument(items, &index))
		if index != len(items) {
			p.invalid(ErrInvalidAssignment, InvalidAssignmentDiagnostic)
		}
	} else if is(&items[index], lexer.KeywordCall) {
		result.Kind = CondCall
		result.ID = p.identifier(p.at(items, 1), "")
		for i := 2; i < len(items); {
			result.Arguments = append(result.Arguments, p.argument(items, &i))
			if p.failed() {
				return nil
			}
		}
	} else {
		isAxiom := is(&items[index], lexer.Hash)
		if isAxiom {
			index++
		}
		id, next := p.qualifiedIdentifier(items, index)
		if p.failed() {
			return nil
		}
		result.ID = id
		index = next
		if !isAxiom && strings.Contains(ValueText(result.ID), "::") {
			p.invalid(ErrQualifiedFact, "Fact condition cannot be qualified")
		}
		for index < len(items) {
			result.Arguments = append(result.Arguments, p.argument(items, &index))
			if p.failed() {
				return nil
			}
		}
		idName := ValueText(result.ID)
		if !isAxiom && (idName == "split_list" || idName == "split_list_front" || idName == "split_list_back") {
			if len(result.Arguments) != 3 {
				p.invalid(ErrInvalidSplitArity, "split_list requires three arguments")
			}
			result.Kind = CondSplit
			switch idName {
			case "split_list_back":
				result.Operator = 2
			case "split_list_front":
				result.Operator = 1
			default:
				result.Operator = 0
			}
			if result.Operator == 2 && len(result.Arguments) == 3 {
				result.Arguments[1], result.Arguments[2] = result.Arguments[2], result.Arguments[1]
			}
		} else if isAxiom {
			result.Kind = CondAxiom
		} else {
			result.Kind = CondFact
		}
	}
	result.Range.End = items[len(items)-1].rng.End
	return result
}

func (p *syntaxParser) body(f *form) *Condition {
	p.errorRange = f.rng
	if !f.isList {
		p.invalid(ErrExpectedConditionBody, "Expected compiler condition body")
		return nil
	}
	if len(f.items) == 0 {
		return nil
	}
	first := &f.items[0]
	if is(first, lexer.KeywordAnd) || is(first, lexer.KeywordOr) || is(first, lexer.KeywordAlt) {
		return p.condition(f)
	}
	p.invalid(ErrExpectedConditionBody, "Expected and, or or alt condition body")
	return nil
}

func (p *syntaxParser) task(f *form) *Task {
	p.errorRange = f.rng
	if !f.isList || len(f.items) == 0 {
		p.invalid(ErrExpectedTask, "Expected compiler task")
		return nil
	}
	result := &Task{}
	p.source(&result.Node, f.rng)
	index := 0
	switch {
	case is(&f.items[index], lexer.ExclamationMark):
		result.Kind = TaskPrimitive
		index++
	case is(&f.items[index], lexer.Ampersand):
		result.Kind = TaskDeferred
		index++
	case is(&f.items[index], lexer.Hash):
		p.invalid(ErrAxiomPrefixInTaskList, AxiomPrefixInTaskDiagnostic)
		return nil
	default:
		result.Kind = TaskCompound
	}
	id, next := p.qualifiedIdentifier(f.items, index)
	if p.failed() {
		return nil
	}
	if result.Kind == TaskPrimitive && strings.Contains(ValueText(id), "::") {
		p.invalid(ErrQualifiedPrimitiveTask, "Primitive task cannot be qualified")
	}
	result.ID = id
	for index = next; index < len(f.items); {
		result.Arguments = append(result.Arguments, p.argument(f.items, &index))
		if p.failed() {
			return nil
		}
	}
	return result
}

func (p *syntaxParser) branch(f *form) *Branch {
	p.errorRange = f.rng
	if !f.isList || len(f.items) != 3 || !f.items[1].isList || !f.items[2].isList {
		p.invalid(ErrExpectedBranch, "Expected compiler branch")
		return nil
	}
	result := &Branch{}
	p.source(&result.Node, f.rng)
	result.ID = p.name(&f.items[0])
	result.Precondition = p.body(&f.items[1])
	if p.failed() {
		return nil
	}
	for i := range f.items[2].items {
		result.Tasks = append(result.Tasks, p.task(&f.items[2].items[i]))
		if p.failed() {
			return nil
		}
	}
	return result
}

func (p *syntaxParser) declaration(f *form, domain *Domain) bool {
	p.errorRange = f.rng
	if !f.isList || len(f.items) < 2 || !is(&f.items[0], lexer.Colon) {
		p.invalid(ErrExpectedDeclaration, "Expected compiler declaration")
		return false
	}
	items := f.items
	switch {
	case is(&items[1], lexer.KeywordConstants):
		group := &ConstantGroup{}
		p.source(&group.Node, f.rng)
		index := 2
		group.ID = "unnamed"
		if index < len(items) && is(&items[index], lexer.Identifier) {
			group.ID = p.name(&items[index])
			index++
		}
		if index < len(items) && is(&items[index], lexer.KeywordBase) {
			group.IsBase = true
			index++
		} else if index < len(items) && is(&items[index], lexer.KeywordOverrides) {
			group.OverridesDomain = p.name(p.at(items, index+1))
			if p.failed() {
				return false
			}
			index += 2
		}
		for ; index < len(items); index++ {
			entry := &items[index]
			if !entry.isList || len(entry.items) != 2 {
				p.invalid(ErrExpectedConstant, "Expected compiler constant")
				return false
			}
			constant := &Constant{}
			p.source(&constant.Node, entry.rng)
			constant.ID = p.name(p.at(entry.items, 0))
			if p.failed() {
				return false
			}
			valueIndex := 1
			constant.Value = p.argument(entry.items, &valueIndex)
			if p.failed() {
				return false
			}
			if constant.Value.Kind != ValueLiteral || valueIndex != len(entry.items) {
				p.invalid(ErrExpectedConstantLiteral, "Expected constant literal")
				return false
			}
			group.Constants = append(group.Constants, constant)
		}
		domain.ConstantGroups = append(domain.ConstantGroups, group)
	case is(&items[1], lexer.KeywordAxiom) || is(&items[1], lexer.KeywordMethod):
		isAxiom := is(&items[1], lexer.KeywordAxiom)
		signature := p.at(items, 2)
		id := p.name(p.at(signature.items, 0))
		if p.failed() {
			return false
		}
		var parameters []*Value
		for i := 1; i < len(signature.items); {
			parameter := p.argument(signature.items, &i)
			if p.failed() {
				return false
			}
			if parameter.Kind != ValueVariable {
				p.invalid(ErrExpectedParameterVariable, "Expected parameter variable")
				return false
			}
			parameters = append(parameters, parameter)
		}
		index := 3
		topLevel, isBase, overrides := false, false, ""
		if index < len(items) && is(&items[index], lexer.KeywordTopLevelMethod) {
			topLevel = true
			index++
		} else if index < len(items) && is(&items[index], lexer.KeywordBase) {
			isBase = true
			index++
		} else if index < len(items) && is(&items[index], lexer.KeywordOverrides) {
			overrides = p.name(p.at(items, index+1))
			if p.failed() {
				return false
			}
			index += 2
		}
		if isAxiom {
			if topLevel {
				p.invalid(ErrInvalidAxiomVisibility, "Axiom cannot be a top_level_method")
				return false
			}
			axiom := &Axiom{ID: id, IsBase: isBase, OverridesDomain: overrides, Parameters: parameters}
			p.source(&axiom.Node, f.rng)
			axiom.Body = p.body(p.at(items, index))
			if p.failed() {
				return false
			}
			if index+1 != len(items) {
				p.invalid(ErrUnexpectedAxiomSyntax, "Unexpected axiom syntax")
				return false
			}
			domain.Axioms = append(domain.Axioms, axiom)
		} else {
			method := &Method{ID: id, Parameters: parameters, TopLevel: topLevel, IsBase: isBase, OverridesDomain: overrides}
			p.source(&method.Node, f.rng)
			for ; index < len(items); index++ {
				method.Branches = append(method.Branches, p.branch(&items[index]))
				if p.failed() {
					return false
				}
			}
			domain.Methods = append(domain.Methods, method)
		}
	default:
		p.invalid(ErrUnknownDeclaration, "Unknown compiler declaration")
		return false
	}
	return true
}

// ParseResult is the outcome of ParseDomainSyntax.
type ParseResult struct {
	Domain     *Domain
	OK         bool
	Error      string
	ErrorRange lexer.Range
	ParseError ParserError
}

// ParseDomainSyntax parses domain source text (with include directives
// already masked) into compiler syntax. Recoverable declaration errors are
// reported to diagnostics (when non-nil) and parsing continues with the next
// declaration; the returned domain is nil on any error.
func ParseDomainSyntax(source string, fileIndex uint32, diagnostics *DiagnosticSink, filePath string) ParseResult {
	p := &syntaxParser{fileIndex: fileIndex, errorRange: lexer.DefaultRange}
	fail := func() ParseResult {
		return ParseResult{OK: false, Error: p.err.Message, ErrorRange: p.err.Range, ParseError: p.err}
	}
	lexed := lexer.LexDomain(source)
	if !lexed.OK {
		message := lexed.Error
		if message == "" {
			message = "Compiler source lexing failed"
		}
		p.errorRange = lexed.ErrorRange
		p.err = ParserError{Code: ErrLexingFailed, Message: message, Range: lexed.ErrorRange}
		if diagnostics != nil {
			diagnostics.Error(filePath, message, RecoveryFatal, lexed.ErrorRange)
		}
		return fail()
	}
	tokens := lexed.Tokens
	position := 0
	root := p.readForm(tokens, &position)
	if p.failed() {
		return fail()
	}
	if position >= len(tokens) || tokens[position].Type != lexer.EndOfFile {
		if position < len(tokens) {
			p.errorRange = tokens[position].Range
		}
		p.invalid(ErrTrailingSource, "Unexpected source after compiler domain")
		return fail()
	}
	if !root.isList || len(root.items) < 3 || !is(&root.items[0], lexer.Colon) || !is(&root.items[1], lexer.KeywordDomain) {
		p.invalid(ErrExpectedDomain, "Expected compiler domain")
		return fail()
	}
	domain := &Domain{}
	domain.ID = p.name(&root.items[2])
	if p.failed() {
		return fail()
	}
	domain.Range = root.rng
	domain.FileIndex = fileIndex
	index := 3
	if index < len(root.items) && is(&root.items[index], lexer.KeywordTopLevelDomain) {
		domain.IsTopLevel = true
		index++
	} else if index < len(root.items) && is(&root.items[index], lexer.KeywordBase) {
		domain.IsBase = true
		index++
	}
	result := ParseResult{OK: true}
	for ; index < len(root.items); index++ {
		declaration := &root.items[index]
		p.errorRange = declaration.rng
		if !declaration.isList || len(declaration.items) < 2 {
			p.invalid(ErrExpectedDeclaration, "Expected compiler declaration")
		} else {
			p.declaration(declaration, domain)
		}
		if p.failed() {
			if result.OK {
				result.Error = p.err.Message
				result.ParseError = p.err
				result.ErrorRange = p.err.Range
			}
			if diagnostics != nil {
				diagnostics.Error(filePath, p.err.Message, RecoveryRecoverable, p.err.Range)
			}
			result.OK = false
			p.err = ParserError{}
		}
	}
	if result.OK {
		result.Domain = domain
	}
	return result
}
