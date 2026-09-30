// Package atom implements the HTN value model: the tagged Atom value, interned
// symbols and immutable lists.
//
// It is the Go counterpart of HTNAtom/HtnSymbol/HTNAtomList in the original C++
// framework. The C representation owns strings and linked lists and deep-copies
// them on assignment; this port gives the same value semantics by making list
// payloads immutable, so copying an Atom is always O(1) and never aliases
// mutable state.
package atom

import (
	"math"
	"strconv"
	"strings"
)

// Kind identifies the runtime type stored in an Atom. The numeric values match
// the original HTNAtomType enumeration.
type Kind uint8

const (
	KindUnbound Kind = iota
	KindBool
	KindInt
	KindFloat
	KindString
	KindSymbol
	KindList
)

// String returns a readable name for the kind.
func (k Kind) String() string {
	switch k {
	case KindUnbound:
		return "unbound"
	case KindBool:
		return "bool"
	case KindInt:
		return "int32"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindSymbol:
		return "symbol"
	case KindList:
		return "list"
	}
	return "invalid"
}

// Atom is an HTN value: unbound, bool, int32, float32, string, symbol or list.
// The zero value is an unbound atom.
type Atom struct {
	kind Kind
	num  uint32 // bool (0/1), int32 bits or float32 bits
	str  string
	sym  *Symbol
	list *listData
}

type listData struct {
	elems []Atom
}

// Unbound returns an unbound atom.
func Unbound() Atom { return Atom{} }

// NewBool returns a bool atom.
func NewBool(v bool) Atom {
	if v {
		return Atom{kind: KindBool, num: 1}
	}
	return Atom{kind: KindBool}
}

// NewInt returns an int32 atom.
func NewInt(v int32) Atom { return Atom{kind: KindInt, num: uint32(v)} }

// NewFloat returns a float32 atom.
func NewFloat(v float32) Atom { return Atom{kind: KindFloat, num: math.Float32bits(v)} }

// NewString returns a string atom.
func NewString(v string) Atom { return Atom{kind: KindString, str: v} }

// NewSymbol returns a symbol atom. A nil symbol produces an unbound atom.
func NewSymbol(s *Symbol) Atom {
	if s == nil {
		return Atom{}
	}
	return Atom{kind: KindSymbol, sym: s}
}

// NewSymbolText interns text and returns the symbol atom.
func NewSymbolText(text string) Atom { return NewSymbol(Intern(text)) }

// NewList returns a list atom holding a copy of elems.
func NewList(elems ...Atom) Atom {
	copied := make([]Atom, len(elems))
	copy(copied, elems)
	return Atom{kind: KindList, list: &listData{elems: copied}}
}

// NewListOwned returns a list atom that takes ownership of elems. The caller
// must not modify elems afterwards.
func NewListOwned(elems []Atom) Atom {
	return Atom{kind: KindList, list: &listData{elems: elems}}
}

// EmptyList returns an empty list atom (which is bound).
func EmptyList() Atom { return Atom{kind: KindList, list: &listData{}} }

// Kind returns the atom's kind.
func (a Atom) Kind() Kind { return a.kind }

// IsBound reports whether the atom holds a value.
func (a Atom) IsBound() bool { return a.kind != KindUnbound }

// Is reports whether the atom has kind k.
func (a Atom) Is(k Kind) bool { return a.kind == k }

// Bool returns the bool payload (false for other kinds).
func (a Atom) Bool() bool { return a.kind == KindBool && a.num != 0 }

// Int returns the int32 payload (0 for other kinds).
func (a Atom) Int() int32 {
	if a.kind != KindInt {
		return 0
	}
	return int32(a.num)
}

// Float returns the float32 payload (0 for other kinds).
func (a Atom) Float() float32 {
	if a.kind != KindFloat {
		return 0
	}
	return math.Float32frombits(a.num)
}

// Str returns the string payload ("" for other kinds).
func (a Atom) Str() string {
	if a.kind != KindString {
		return ""
	}
	return a.str
}

// Symbol returns the symbol payload (nil for other kinds).
func (a Atom) Symbol() *Symbol {
	if a.kind != KindSymbol {
		return nil
	}
	return a.sym
}

// Len returns the number of list elements, or -1 when the atom is not a list
// (mirroring HTNAtom_GetListSize).
func (a Atom) Len() int {
	if a.kind != KindList {
		return -1
	}
	return len(a.list.elems)
}

// IsListEmpty reports true for non-lists and for empty lists.
func (a Atom) IsListEmpty() bool { return a.kind != KindList || len(a.list.elems) == 0 }

// At returns the list element at index i. It returns (Atom{}, false) when the
// atom is not a list or the index is out of range.
func (a Atom) At(i int) (Atom, bool) {
	if a.kind != KindList || i < 0 || i >= len(a.list.elems) {
		return Atom{}, false
	}
	return a.list.elems[i], true
}

// Elements returns the list elements. The returned slice must be treated as
// read-only. It returns nil for non-list atoms.
func (a Atom) Elements() []Atom {
	if a.kind != KindList {
		return nil
	}
	return a.list.elems
}

// Append returns a new list with v appended. A non-list, unbound receiver is
// treated as an empty list (matching HTNAtom_PushBackListElement); any other
// non-list receiver returns (receiver, false).
func (a Atom) Append(v Atom) (Atom, bool) {
	switch a.kind {
	case KindUnbound:
		return NewList(v), true
	case KindList:
		elems := make([]Atom, len(a.list.elems)+1)
		copy(elems, a.list.elems)
		elems[len(elems)-1] = v
		return NewListOwned(elems), true
	}
	return a, false
}

// RemoveAt returns a new list without the element at index i.
func (a Atom) RemoveAt(i int) (Atom, bool) {
	if a.kind != KindList || i < 0 || i >= len(a.list.elems) {
		return a, false
	}
	elems := make([]Atom, 0, len(a.list.elems)-1)
	elems = append(elems, a.list.elems[:i]...)
	elems = append(elems, a.list.elems[i+1:]...)
	return NewListOwned(elems), true
}

// SplitDirection selects which element SplitList extracts.
type SplitDirection uint8

const (
	// SplitFront extracts the first element; the remainder holds the rest.
	SplitFront SplitDirection = iota
	// SplitBack extracts the last element; the remainder holds the rest.
	SplitBack
)

// SplitList splits a non-empty list into an element and the remaining list
// (HTNAtomList_Split).
func (a Atom) SplitList(direction SplitDirection) (element Atom, remainder Atom, ok bool) {
	if a.kind != KindList || len(a.list.elems) == 0 {
		return Atom{}, Atom{}, false
	}
	elems := a.list.elems
	if direction == SplitFront {
		rest := make([]Atom, len(elems)-1)
		copy(rest, elems[1:])
		return elems[0], NewListOwned(rest), true
	}
	rest := make([]Atom, len(elems)-1)
	copy(rest, elems[:len(elems)-1])
	return elems[len(elems)-1], NewListOwned(rest), true
}

// Equal implements HTNAtom_Equals: kinds must match and payloads compare by
// value (lists element-wise, floats with IEEE equality).
func Equal(a, b Atom) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case KindUnbound:
		return true
	case KindBool, KindInt:
		return a.num == b.num
	case KindFloat:
		return math.Float32frombits(a.num) == math.Float32frombits(b.num)
	case KindString:
		return a.str == b.str
	case KindSymbol:
		return a.sym == b.sym
	case KindList:
		if a.list == b.list {
			return true
		}
		if len(a.list.elems) != len(b.list.elems) {
			return false
		}
		for i := range a.list.elems {
			if !Equal(a.list.elems[i], b.list.elems[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// Equal reports whether a equals b (see the package-level Equal).
func (a Atom) Equal(b Atom) bool { return Equal(a, b) }

// ToString formats the atom as HTNAtomToString does: floats use one decimal
// place, bools print true/false, strings are optionally double-quoted and lists
// are space separated inside parentheses. Unbound atoms print as "".
func ToString(a Atom, quoteStrings bool) string {
	var b strings.Builder
	appendString(&b, a, quoteStrings)
	return b.String()
}

// String formats the atom with double-quoted strings.
func (a Atom) String() string { return ToString(a, true) }

// Text formats the atom without quoting strings.
func (a Atom) Text() string { return ToString(a, false) }

func appendString(b *strings.Builder, a Atom, quote bool) {
	switch a.kind {
	case KindBool:
		if a.num != 0 {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case KindInt:
		b.WriteString(strconv.FormatInt(int64(int32(a.num)), 10))
	case KindFloat:
		b.WriteString(FormatFloat(math.Float32frombits(a.num)))
	case KindString:
		if quote {
			b.WriteByte('"')
			b.WriteString(a.str)
			b.WriteByte('"')
		} else {
			b.WriteString(a.str)
		}
	case KindSymbol:
		if a.sym != nil {
			b.WriteString(a.sym.text)
		}
	case KindList:
		b.WriteByte('(')
		for i, e := range a.list.elems {
			if i > 0 {
				b.WriteByte(' ')
			}
			appendString(b, e, quote)
		}
		b.WriteByte(')')
	}
}

// FormatFloat renders a float32 like std::fixed with precision 1 on glibc.
func FormatFloat(v float32) string {
	return strconv.FormatFloat(float64(v), 'f', 1, 64)
}
