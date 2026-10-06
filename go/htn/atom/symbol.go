package atom

import "sync"

// Symbol is an interned HTN symbol. Interned symbols are compared by pointer
// identity and live for the lifetime of the process.
type Symbol struct {
	text string
	hash uint64
	id   uint64
}

// String returns the symbol text.
func (s *Symbol) String() string {
	if s == nil {
		return ""
	}
	return s.text
}

// Text returns the symbol text.
func (s *Symbol) Text() string { return s.String() }

// Hash returns the stable 64-bit FNV-1a hash of the symbol text.
func (s *Symbol) Hash() uint64 { return s.hash }

// ID returns the collision-free process-unique symbol identifier.
func (s *Symbol) ID() uint64 { return s.id }

var symbolTable = struct {
	sync.RWMutex
	symbols map[string]*Symbol
	nextID  uint64
}{symbols: make(map[string]*Symbol), nextID: 1}

func hashSymbolText(text string) uint64 {
	hash := uint64(14695981039346656037)
	for i := 0; i < len(text); i++ {
		hash ^= uint64(text[i])
		hash *= 1099511628211
	}
	return hash
}

// Intern returns the unique symbol for text, creating it when necessary.
// Intern is safe for concurrent use.
func Intern(text string) *Symbol {
	symbolTable.RLock()
	existing := symbolTable.symbols[text]
	symbolTable.RUnlock()
	if existing != nil {
		return existing
	}
	symbolTable.Lock()
	defer symbolTable.Unlock()
	if existing = symbolTable.symbols[text]; existing != nil {
		return existing
	}
	symbol := &Symbol{text: text, hash: hashSymbolText(text), id: symbolTable.nextID}
	symbolTable.nextID++
	symbolTable.symbols[text] = symbol
	return symbol
}
