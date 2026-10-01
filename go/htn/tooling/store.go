package tooling

import (
	"os"
)

// Document is one open editor buffer.
type Document struct {
	FilePath string
	Text     string
	Version  uint64
}

type cacheEntry struct {
	model           Model
	generation      uint64
	documentVersion uint64
	hasModel        bool
}

// Store keeps open editor buffers and their cached semantic models. Open
// buffers win over the file system when domains are loaded; the store never
// writes buffers to disk. It is not safe for concurrent use.
type Store struct {
	documents  map[string]*Document
	cache      map[string]*cacheEntry
	generation uint64
}

// NewStore creates an empty document store.
func NewStore() *Store {
	return &Store{documents: map[string]*Document{}, cache: map[string]*cacheEntry{}, generation: 1}
}

func pathKey(path string) string { return canonicalPath(path) }

func (s *Store) invalidate() {
	s.generation++
	if s.generation == 0 {
		s.generation = 1
		s.cache = map[string]*cacheEntry{}
	}
}

// Open opens (or replaces) a document.
func (s *Store) Open(path, text string, version uint64) {
	s.documents[pathKey(path)] = &Document{FilePath: path, Text: text, Version: version}
	s.invalidate()
}

// Update replaces the text of an open document. It returns false when the
// document is not open.
func (s *Store) Update(path, text string, version uint64) bool {
	document := s.documents[pathKey(path)]
	if document == nil {
		return false
	}
	document.Text = text
	document.Version = version
	s.invalidate()
	return true
}

// Close closes a document. It returns false when the document is not open.
func (s *Store) Close(path string) bool {
	key := pathKey(path)
	if s.documents[key] == nil {
		return false
	}
	delete(s.documents, key)
	delete(s.cache, key)
	s.invalidate()
	return true
}

// Clear closes every document.
func (s *Store) Clear() {
	if len(s.documents) == 0 && len(s.cache) == 0 {
		return
	}
	s.documents = map[string]*Document{}
	s.cache = map[string]*cacheEntry{}
	s.invalidate()
}

// IsOpen reports whether a document is open.
func (s *Store) IsOpen(path string) bool { return s.documents[pathKey(path)] != nil }

// Document returns an open document or nil.
func (s *Store) Document(path string) *Document { return s.documents[pathKey(path)] }

// Generation changes whenever any open document changes.
func (s *Store) Generation() uint64 { return s.generation }

// Read implements compiler.SourceProvider: open buffers win, other files are
// read from disk.
func (s *Store) Read(path string) (string, bool) {
	if document := s.documents[pathKey(path)]; document != nil {
		return document.Text, true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// Model returns the cached semantic model of an open document. A change to
// any open document invalidates every cached model because includes make
// documents depend on each other's unsaved text.
func (s *Store) Model(path string) *Model {
	key := pathKey(path)
	document := s.documents[key]
	if document == nil {
		return nil
	}
	entry := s.cache[key]
	if entry == nil {
		entry = &cacheEntry{}
		s.cache[key] = entry
	}
	if !entry.hasModel || entry.generation != s.generation || entry.documentVersion != document.Version {
		entry.model.Analyze(document.FilePath, document.Text, s.Read)
		entry.generation = s.generation
		entry.documentVersion = document.Version
		entry.hasModel = true
	}
	return &entry.model
}
