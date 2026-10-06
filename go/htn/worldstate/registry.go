package worldstate

import "github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"

// FactSlot is a compact domain-specific fact index.
type FactSlot = uint32

// InvalidFactSlot marks a symbol that the domain does not reference.
const InvalidFactSlot FactSlot = 0xFFFFFFFF

// FactRegistry maps the fact symbols referenced by one domain to compact
// slots. Configure it before planning; afterwards it is read-only and may be
// shared by concurrent planners.
type FactRegistry struct {
	slots   map[*atom.Symbol]FactSlot
	symbols []*atom.Symbol
}

// NewFactRegistry creates an empty registry.
func NewFactRegistry() *FactRegistry {
	return &FactRegistry{slots: make(map[*atom.Symbol]FactSlot)}
}

// Reset removes every registration.
func (r *FactRegistry) Reset() {
	r.slots = make(map[*atom.Symbol]FactSlot)
	r.symbols = nil
}

// Register returns the slot of symbol, registering it when needed.
func (r *FactRegistry) Register(symbol *atom.Symbol) FactSlot {
	if symbol == nil {
		return InvalidFactSlot
	}
	if slot, ok := r.slots[symbol]; ok {
		return slot
	}
	slot := FactSlot(len(r.symbols))
	r.slots[symbol] = slot
	r.symbols = append(r.symbols, symbol)
	return slot
}

// FindSlot returns the slot of symbol or InvalidFactSlot.
func (r *FactRegistry) FindSlot(symbol *atom.Symbol) FactSlot {
	if r == nil || symbol == nil {
		return InvalidFactSlot
	}
	if slot, ok := r.slots[symbol]; ok {
		return slot
	}
	return InvalidFactSlot
}

// Symbol returns the symbol registered at slot, or nil.
func (r *FactRegistry) Symbol(slot FactSlot) *atom.Symbol {
	if int(slot) < len(r.symbols) {
		return r.symbols[slot]
	}
	return nil
}

// SlotCount returns the number of registered facts.
func (r *FactRegistry) SlotCount() int { return len(r.symbols) }
