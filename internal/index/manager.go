// Package index manages per-table byte-offset indexes and their sidecar file
// persistence. The Manager holds an in-memory cache of every index defined on
// a table and delegates persistence to sidecar.go.
package index

import (
	"fmt"
	"sync"
)

// Entry maps an index key to the byte offsets of all matching rows in the
// data file.
type Entry map[string][]int64

// indexDef holds the metadata and live entries for one index.
type indexDef struct {
	Columns []string
	Unique  bool
	Entries Entry
}

// Manager maintains the in-memory index cache for a single table and
// coordinates persistence to the companion sidecar file.
type Manager struct {
	mu          sync.RWMutex
	defs        map[string]*indexDef
	sidecarPath string
	cacheOn     bool
	loaded      bool
}

// IndexEntry is a (indexName, key, offset) triple used when rebuilding an
// index from a full scan of the backing file.
type IndexEntry struct {
	IndexName string
	Key       string
	Offset    int64
}

// New creates a Manager whose sidecar file lives at sidecarPath. When cache
// is true the in-memory state persists across queries until a reload.
func New(sidecarPath string, cache bool) *Manager {
	return &Manager{
		defs:        make(map[string]*indexDef),
		sidecarPath: sidecarPath,
		cacheOn:     cache,
	}
}

// Define registers an index by name without populating entries. Call Load or
// Rebuild separately to fill the entry map.
func (m *Manager) Define(name string, columns []string, unique bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defs[name] = &indexDef{
		Columns: columns,
		Unique:  unique,
		Entries: make(Entry),
	}
}

// Add inserts offset into the entry for key in the named index. It returns
// ErrUniqueViolation when the index is unique and key already has an entry.
func (m *Manager) Add(name, key string, offset int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	def, ok := m.defs[name]
	if !ok {
		return fmt.Errorf("index %q not defined", name)
	}
	if def.Unique {
		if existing, exists := def.Entries[key]; exists && len(existing) > 0 {
			return fmt.Errorf("unique violation on index %q for key %q", name, key)
		}
	}
	def.Entries[key] = append(def.Entries[key], offset)
	return nil
}

// Lookup returns the byte offsets associated with key in the named index.
// It returns nil when the key is absent or the index is not defined.
func (m *Manager) Lookup(name, key string) []int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	def, ok := m.defs[name]
	if !ok {
		return nil
	}
	src := def.Entries[key]
	if len(src) == 0 {
		return nil
	}
	out := make([]int64, len(src))
	copy(out, src)
	return out
}

// Rebuild replaces all in-memory entries using the provided list of
// IndexEntry values. It is called after any operation that rewrites the
// backing file (UPDATE, DELETE, or JSON Append).
func (m *Manager) Rebuild(entries []IndexEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, def := range m.defs {
		def.Entries = make(Entry)
	}
	for _, e := range entries {
		def, ok := m.defs[e.IndexName]
		if !ok {
			continue
		}
		if def.Unique {
			if existing := def.Entries[e.Key]; len(existing) > 0 {
				return fmt.Errorf("unique violation on index %q for key %q during rebuild",
					e.IndexName, e.Key)
			}
		}
		def.Entries[e.Key] = append(def.Entries[e.Key], e.Offset)
	}
	m.loaded = true
	return nil
}

// Names returns the names of all defined indexes.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.defs))
	for n := range m.defs {
		names = append(names, n)
	}
	return names
}

// Columns returns the column names covered by the named index, and whether
// the index was found.
func (m *Manager) Columns(name string) ([]string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	def, ok := m.defs[name]
	if !ok {
		return nil, false
	}
	out := make([]string, len(def.Columns))
	copy(out, def.Columns)
	return out, true
}

// IsUnique reports whether the named index enforces uniqueness.
func (m *Manager) IsUnique(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	def, ok := m.defs[name]
	return ok && def.Unique
}

// SidecarPath returns the path to the index sidecar file.
func (m *Manager) SidecarPath() string { return m.sidecarPath }

// CacheEnabled reports whether in-memory caching is active for this manager.
func (m *Manager) CacheEnabled() bool { return m.cacheOn }

// IsLoaded reports whether the manager holds a current in-memory state.
func (m *Manager) IsLoaded() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loaded
}

// snapshot returns a deep copy of all index definitions and entries. It is
// called by Save to produce a stable view for serialisation.
func (m *Manager) snapshot() map[string]*indexDef {
	out := make(map[string]*indexDef, len(m.defs))
	for name, def := range m.defs {
		cols := make([]string, len(def.Columns))
		copy(cols, def.Columns)
		entries := make(Entry, len(def.Entries))
		for k, v := range def.Entries {
			cp := make([]int64, len(v))
			copy(cp, v)
			entries[k] = cp
		}
		out[name] = &indexDef{Columns: cols, Unique: def.Unique, Entries: entries}
	}
	return out
}

// restore replaces the in-memory entry maps from a previously loaded snapshot.
func (m *Manager) restore(snap map[string]*indexDef) {
	for name, def := range snap {
		if target, ok := m.defs[name]; ok {
			target.Entries = def.Entries
		}
	}
	m.loaded = true
}
