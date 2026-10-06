package lsp

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Kind is the type of a JSON value.
type Kind uint8

// JSON value kinds. Integers (numbers without fraction or exponent that fit
// in int64) are kept apart from other numbers, as in the original HTNLspJson.
const (
	KindNull Kind = iota
	KindBool
	KindInteger
	KindNumber
	KindString
	KindArray
	KindObject
)

// JSON is the language server's JSON value (port of HTNLspJson). Objects
// keep the first occurrence of a duplicated key and serialize their keys in
// byte order, like the original std::map.
type JSON struct {
	kind    Kind
	boolean bool
	integer int64
	number  float64
	text    string
	array   []JSON
	object  map[string]JSON
}

// Null returns the JSON null value.
func Null() JSON { return JSON{} }

// Bool returns a JSON boolean.
func Bool(value bool) JSON { return JSON{kind: KindBool, boolean: value} }

// Integer returns a JSON integer.
func Integer(value int64) JSON { return JSON{kind: KindInteger, integer: value} }

// String returns a JSON string.
func String(value string) JSON { return JSON{kind: KindString, text: value} }

// Array returns a JSON array.
func Array(values ...JSON) JSON {
	if values == nil {
		values = []JSON{}
	}
	return JSON{kind: KindArray, array: values}
}

// Field is one member of an object literal.
type Field struct {
	Key   string
	Value JSON
}

// Object returns a JSON object.
func Object(fields ...Field) JSON {
	object := make(map[string]JSON, len(fields))
	for _, field := range fields {
		if _, exists := object[field.Key]; !exists {
			object[field.Key] = field.Value
		}
	}
	return JSON{kind: KindObject, object: object}
}

// Kind returns the value's kind.
func (j JSON) Kind() Kind { return j.kind }

// IsString reports whether the value is a string.
func (j JSON) IsString() bool { return j.kind == KindString }

// IsInteger reports whether the value is an integer.
func (j JSON) IsInteger() bool { return j.kind == KindInteger }

// IsArray reports whether the value is an array.
func (j JSON) IsArray() bool { return j.kind == KindArray }

// AsString returns the string value or "".
func (j JSON) AsString() string { return j.text }

// AsInteger returns the integer value or 0.
func (j JSON) AsInteger() int64 { return j.integer }

// AsArray returns the array elements or nil.
func (j JSON) AsArray() []JSON { return j.array }

// Find returns the member named key of an object.
func (j JSON) Find(key string) (JSON, bool) {
	if j.kind != KindObject {
		return JSON{}, false
	}
	value, ok := j.object[key]
	return value, ok
}

// FindNested follows a chain of object members.
func (j JSON) FindNested(keys ...string) (JSON, bool) {
	current := j
	for _, key := range keys {
		next, ok := current.Find(key)
		if !ok {
			return JSON{}, false
		}
		current = next
	}
	return current, true
}

// Set adds or replaces an object member.
func (j *JSON) Set(key string, value JSON) {
	if j.kind != KindObject {
		*j = Object()
	}
	j.object[key] = value
}

// Append adds an element to an array.
func (j *JSON) Append(value JSON) {
	if j.kind != KindArray {
		*j = Array()
	}
	j.array = append(j.array, value)
}

// ParseJSON parses a complete JSON document. On failure it returns the
// original parser's error message.
func ParseJSON(text string) (JSON, string, bool) {
	p := jsonParser{text: text}
	p.skipWhitespace()
	value, ok := p.parseValue()
	if !ok {
		return JSON{}, p.err, false
	}
	p.skipWhitespace()
	if p.offset != len(p.text) {
		return JSON{}, "Unexpected trailing JSON content", false
	}
	return value, "", true
}

type jsonParser struct {
	text   string
	offset int
	err    string
}

func (p *jsonParser) fail(message string) (JSON, bool) {
	p.err = message
	return JSON{}, false
}

// isSpace is std::isspace in the "C" locale.
func isSpace(c byte) bool { return c == ' ' || (c >= '\t' && c <= '\r') }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *jsonParser) skipWhitespace() {
	for p.offset < len(p.text) && isSpace(p.text[p.offset]) {
		p.offset++
	}
}

func (p *jsonParser) consume(c byte) bool {
	if p.offset < len(p.text) && p.text[p.offset] == c {
		p.offset++
		return true
	}
	return false
}

func (p *jsonParser) consumeLiteral(literal string) bool {
	if !strings.HasPrefix(p.text[p.offset:], literal) {
		return false
	}
	p.offset += len(literal)
	return true
}

func (p *jsonParser) parseValue() (JSON, bool) {
	p.skipWhitespace()
	if p.offset >= len(p.text) {
		return p.fail("Unexpected end of JSON input")
	}
	switch c := p.text[p.offset]; {
	case c == '{':
		return p.parseObject()
	case c == '[':
		return p.parseArray()
	case c == '"':
		text, ok := p.parseString()
		if !ok {
			return JSON{}, false
		}
		return String(text), true
	case c == 't' && p.consumeLiteral("true"):
		return Bool(true), true
	case c == 'f' && p.consumeLiteral("false"):
		return Bool(false), true
	case c == 'n' && p.consumeLiteral("null"):
		return Null(), true
	case c == '-' || isDigit(c):
		return p.parseNumber()
	}
	return p.fail("Unexpected JSON token")
}

func (p *jsonParser) parseObject() (JSON, bool) {
	p.offset++
	result := Object()
	p.skipWhitespace()
	if p.consume('}') {
		return result, true
	}
	for p.offset < len(p.text) {
		key, ok := p.parseString()
		if !ok {
			return JSON{}, false
		}
		p.skipWhitespace()
		if !p.consume(':') {
			return p.fail("Expected ':' in JSON object")
		}
		value, ok := p.parseValue()
		if !ok {
			return JSON{}, false
		}
		if _, exists := result.object[key]; !exists {
			result.object[key] = value
		}
		p.skipWhitespace()
		if p.consume('}') {
			return result, true
		}
		if !p.consume(',') {
			return p.fail("Expected ',' or '}' in JSON object")
		}
		p.skipWhitespace()
	}
	return p.fail("Unterminated JSON object")
}

func (p *jsonParser) parseArray() (JSON, bool) {
	p.offset++
	result := Array()
	p.skipWhitespace()
	if p.consume(']') {
		return result, true
	}
	for p.offset < len(p.text) {
		value, ok := p.parseValue()
		if !ok {
			return JSON{}, false
		}
		result.array = append(result.array, value)
		p.skipWhitespace()
		if p.consume(']') {
			return result, true
		}
		if !p.consume(',') {
			return p.fail("Expected ',' or ']' in JSON array")
		}
		p.skipWhitespace()
	}
	return p.fail("Unterminated JSON array")
}

func (p *jsonParser) parseNumber() (JSON, bool) {
	begin := p.offset
	if p.text[p.offset] == '-' {
		p.offset++
	}
	for p.offset < len(p.text) && isDigit(p.text[p.offset]) {
		p.offset++
	}
	floating := false
	if p.offset < len(p.text) && p.text[p.offset] == '.' {
		floating = true
		p.offset++
		for p.offset < len(p.text) && isDigit(p.text[p.offset]) {
			p.offset++
		}
	}
	if p.offset < len(p.text) && (p.text[p.offset] == 'e' || p.text[p.offset] == 'E') {
		floating = true
		p.offset++
		if p.offset < len(p.text) && (p.text[p.offset] == '+' || p.text[p.offset] == '-') {
			p.offset++
		}
		for p.offset < len(p.text) && isDigit(p.text[p.offset]) {
			p.offset++
		}
	}
	number := p.text[begin:p.offset]
	if floating {
		// std::from_chars rejects out-of-range values, including nonzero
		// values that underflow to zero.
		value, err := strconv.ParseFloat(number, 64)
		if err == nil && !math.IsInf(value, 0) && !math.IsNaN(value) &&
			(value != 0 || !hasNonzeroMantissa(number)) {
			return JSON{kind: KindNumber, number: value}, true
		}
	} else if value, err := strconv.ParseInt(number, 10, 64); err == nil {
		return Integer(value), true
	}
	return p.fail("Invalid JSON number")
}

// hasNonzeroMantissa reports whether a number literal has a nonzero digit
// before its exponent.
func hasNonzeroMantissa(number string) bool {
	for i := 0; i < len(number); i++ {
		switch c := number[i]; {
		case c == 'e' || c == 'E':
			return false
		case c >= '1' && c <= '9':
			return true
		}
	}
	return false
}

func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func (p *jsonParser) parseString() (string, bool) {
	p.skipWhitespace()
	if !p.consume('"') {
		p.err = "Expected JSON string"
		return "", false
	}
	var b strings.Builder
	for p.offset < len(p.text) {
		c := p.text[p.offset]
		p.offset++
		if c == '"' {
			return b.String(), true
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if p.offset >= len(p.text) {
			p.err = "Invalid JSON escape"
			return "", false
		}
		escape := p.text[p.offset]
		p.offset++
		switch escape {
		case '"', '\\', '/':
			b.WriteByte(escape)
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			if p.offset+4 > len(p.text) {
				p.err = "Invalid JSON unicode escape"
				return "", false
			}
			code := 0
			for i := 0; i < 4; i++ {
				digit := hexValue(p.text[p.offset])
				p.offset++
				if digit < 0 {
					p.err = "Invalid JSON unicode escape"
					return "", false
				}
				code = code<<4 | digit
			}
			// Each escape is encoded on its own (surrogates are not paired).
			switch {
			case code <= 0x7F:
				b.WriteByte(byte(code))
			case code <= 0x7FF:
				b.WriteByte(byte(0xC0 | code>>6))
				b.WriteByte(byte(0x80 | code&0x3F))
			default:
				b.WriteByte(byte(0xE0 | code>>12))
				b.WriteByte(byte(0x80 | (code>>6)&0x3F))
				b.WriteByte(byte(0x80 | code&0x3F))
			}
		default:
			p.err = "Unsupported JSON escape"
			return "", false
		}
	}
	p.err = "Unterminated JSON string"
	return "", false
}

// Serialize writes the value in the original's compact form.
func (j JSON) Serialize() string {
	var b strings.Builder
	j.serialize(&b)
	return b.String()
}

func (j JSON) serialize(b *strings.Builder) {
	switch j.kind {
	case KindNull:
		b.WriteString("null")
	case KindBool:
		if j.boolean {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case KindInteger:
		b.WriteString(strconv.FormatInt(j.integer, 10))
	case KindNumber:
		// std::ostream with setprecision(17): %.17g.
		b.WriteString(strconv.FormatFloat(j.number, 'g', 17, 64))
	case KindString:
		escapeJSONString(b, j.text)
	case KindArray:
		b.WriteByte('[')
		for i, value := range j.array {
			if i > 0 {
				b.WriteByte(',')
			}
			value.serialize(b)
		}
		b.WriteByte(']')
	case KindObject:
		keys := make([]string, 0, len(j.object))
		for key := range j.object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			escapeJSONString(b, key)
			b.WriteByte(':')
			j.object[key].serialize(b)
		}
		b.WriteByte('}')
	}
}

func escapeJSONString(b *strings.Builder, text string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&15])
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}
