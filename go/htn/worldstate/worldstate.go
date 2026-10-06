// Package worldstate implements the HTN fact database (HTNWorldState): facts
// are grouped by interned symbol and arity, rows are appended in insertion
// order and duplicate rows are preserved.
package worldstate

import (
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
)

// MaxFactArguments is the maximum number of arguments of one fact.
const MaxFactArguments = 10

// FactArgumentsSize counts the arity tables of a fact (0..MaxFactArguments).
const FactArgumentsSize = MaxFactArguments + 1

// Table holds every row of one fact/arity pair.
type Table struct {
	rows [][]atom.Atom
}

// RowCount returns the number of rows.
func (t *Table) RowCount() int { return len(t.rows) }

// Row returns row i. The slice must be treated as read-only.
func (t *Table) Row(i int) []atom.Atom { return t.rows[i] }

// Rows returns every row. The slices must be treated as read-only.
func (t *Table) Rows() [][]atom.Atom { return t.rows }

func (t *Table) add(args []atom.Atom) {
	row := make([]atom.Atom, len(args))
	copy(row, args)
	t.rows = append(t.rows, row)
}

// RemoveRow deletes row i, preserving the order of the remaining rows.
func (t *Table) RemoveRow(i int) bool {
	if i < 0 || i >= len(t.rows) {
		return false
	}
	copy(t.rows[i:], t.rows[i+1:])
	t.rows[len(t.rows)-1] = nil
	t.rows = t.rows[:len(t.rows)-1]
	return true
}

// Clear removes every row.
func (t *Table) Clear() {
	for i := range t.rows {
		t.rows[i] = nil
	}
	t.rows = t.rows[:0]
}

// Contains reports whether any row equals args.
func (t *Table) Contains(args []atom.Atom) bool {
	for _, row := range t.rows {
		match := true
		for i := range args {
			if !atom.Equal(row[i], args[i]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Tables are the arity tables of one fact symbol.
type Tables [FactArgumentsSize]Table

// EmptyTables is shared by generated planners for facts that are absent from a
// world state: every arity has zero rows.
var EmptyTables = &Tables{}

// WorldState is the fact database used by generated planners.
type WorldState struct {
	facts      map[*atom.Symbol]*Tables
	order      []*atom.Symbol
	generation uint64
	registry   *FactRegistry
}

// New creates an empty world state.
func New() *WorldState {
	return &WorldState{facts: make(map[*atom.Symbol]*Tables), generation: 1}
}

// SetFactRegistry associates the world state with the fact slots of one domain.
// The registry is borrowed.
func (w *WorldState) SetFactRegistry(r *FactRegistry) { w.registry = r }

// FactRegistry returns the associated registry (possibly nil).
func (w *WorldState) FactRegistry() *FactRegistry { return w.registry }

// FindFactSlot resolves a symbol through the associated registry.
func (w *WorldState) FindFactSlot(fact *atom.Symbol) FactSlot {
	if w.registry == nil {
		return InvalidFactSlot
	}
	return w.registry.FindSlot(fact)
}

// Generation changes whenever a new fact entry is created. Generated execution
// storage uses it to refresh cached fact tables.
func (w *WorldState) Generation() uint64 { return w.generation }

func (w *WorldState) findOrCreate(fact *atom.Symbol) *Tables {
	if tables := w.facts[fact]; tables != nil {
		return tables
	}
	tables := &Tables{}
	w.facts[fact] = tables
	w.order = append(w.order, fact)
	w.generation++
	if w.generation == 0 {
		w.generation = 1
	}
	return tables
}

// AddFact appends one row for the named fact (the arity is len(args)). It does
// not consult the fact registry, like HTNWorldState::AddFact.
func (w *WorldState) AddFact(fact string, args ...atom.Atom) bool {
	return w.AddFactSymbol(atom.Intern(fact), args...)
}

// AddFactSymbol appends one row for the fact symbol.
func (w *WorldState) AddFactSymbol(fact *atom.Symbol, args ...atom.Atom) bool {
	if fact == nil || len(args) > MaxFactArguments {
		return false
	}
	w.findOrCreate(fact)[len(args)].add(args)
	return true
}

// WriteFact appends one row after checking that the fact belongs to the
// associated registry and that every argument is bound. Failure leaves the
// world state unchanged.
func (w *WorldState) WriteFact(fact *atom.Symbol, args ...atom.Atom) bool {
	if fact == nil || w.FindFactSlot(fact) == InvalidFactSlot || len(args) > MaxFactArguments {
		return false
	}
	for _, arg := range args {
		if !arg.IsBound() {
			return false
		}
	}
	w.findOrCreate(fact)[len(args)].add(args)
	return true
}

// ClearFact removes every row of one registered fact/arity.
func (w *WorldState) ClearFact(fact *atom.Symbol, arity int) bool {
	if fact == nil || w.FindFactSlot(fact) == InvalidFactSlot || arity < 0 || arity >= FactArgumentsSize {
		return false
	}
	if tables := w.facts[fact]; tables != nil {
		tables[arity].Clear()
	}
	return true
}

// RemoveFact removes one row. Removing an absent fact is a no-op.
func (w *WorldState) RemoveFact(fact string, arity int, index int) {
	tables := w.facts[atom.Intern(fact)]
	if tables == nil || arity < 0 || arity >= FactArgumentsSize {
		return
	}
	tables[arity].RemoveRow(index)
}

// RemoveAllFacts clears every row while keeping the fact tables allocated.
func (w *WorldState) RemoveAllFacts() {
	for _, tables := range w.facts {
		for i := range tables {
			tables[i].Clear()
		}
	}
}

// FindTables returns the arity tables of a fact, or nil when absent.
func (w *WorldState) FindTables(fact *atom.Symbol) *Tables {
	return w.facts[fact]
}

// ResolveGeneratedTables returns the fact's tables, or the shared empty tables
// when the fact does not exist (HTNWorldState_ResolveGeneratedFactTables).
func (w *WorldState) ResolveGeneratedTables(fact *atom.Symbol) *Tables {
	if tables := w.facts[fact]; tables != nil {
		return tables
	}
	return EmptyTables
}

// FindTable returns the table of one fact/arity pair, or nil when absent.
func (w *WorldState) FindTable(fact string, arity int) *Table {
	tables := w.facts[atom.Intern(fact)]
	if tables == nil || arity < 0 || arity >= FactArgumentsSize {
		return nil
	}
	return &tables[arity]
}

// RowCount returns the number of rows of a fact/arity pair.
func (w *WorldState) RowCount(fact string, arity int) int {
	if table := w.FindTable(fact, arity); table != nil {
		return table.RowCount()
	}
	return 0
}

// ContainsFact reports whether the exact row exists.
func (w *WorldState) ContainsFact(fact string, args ...atom.Atom) bool {
	table := w.FindTable(fact, len(args))
	return table != nil && table.Contains(args)
}

// Facts returns the fact symbols in creation order.
func (w *WorldState) Facts() []*atom.Symbol { return w.order }

// TableCount returns the number of non-empty arity tables of a fact.
func (w *WorldState) TableCount(fact string) int {
	tables := w.facts[atom.Intern(fact)]
	if tables == nil {
		return 0
	}
	count := 0
	for i := range tables {
		if tables[i].RowCount() > 0 {
			count++
		}
	}
	return count
}
