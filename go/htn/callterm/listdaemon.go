package callterm

import "github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"

// BindListCallTerms registers the standard list callterms of AIHtnListDaemon:
// list_add, list_remove_at, list_get, list_size and list_clear. Lists are
// values: every operation returns a new list.
func BindListCallTerms(r *Registry) {
	r.MustBindFunc("list_add", ListAdd)
	r.MustBindFunc("list_remove_at", ListRemoveAt)
	r.MustBindFunc("list_get", ListGet)
	r.MustBindFunc("list_size", ListSize)
	r.MustBindFunc("list_clear", ListClear)
}

// ListAdd returns list with value appended.
func ListAdd(list List, value atom.Atom) atom.Atom {
	result, ok := list.Append(value)
	if !ok {
		return atom.Atom{}
	}
	return result
}

// ListRemoveAt returns list without the element at index.
func ListRemoveAt(list List, index int32) atom.Atom {
	if index < 0 {
		return atom.Atom{}
	}
	result, ok := list.RemoveAt(int(index))
	if !ok {
		return atom.Atom{}
	}
	return result
}

// ListGet returns the element at index (unbound when out of range).
func ListGet(list List, index int32) atom.Atom {
	if index < 0 {
		return atom.Atom{}
	}
	value, _ := list.At(int(index))
	return value
}

// ListSize returns the number of elements.
func ListSize(list List) atom.Atom { return atom.NewInt(int32(list.Len())) }

// ListClear returns an empty list.
func ListClear(List) atom.Atom { return atom.EmptyList() }
