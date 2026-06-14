// Package registry maintains the set of tables registered with a DB instance.
// Each entry bundles a table definition, its file backend, its index manager,
// and a per-table read/write mutex so concurrent queries can proceed
// independently.
package registry

import (
	"fmt"
	"sync"

	"github.com/fernandezvara/filedb/internal/backend"
	"github.com/fernandezvara/filedb/internal/index"
	"github.com/fernandezvara/filedb/internal/types"
)

// Entry is the live state for one registered table.
type Entry struct {
	Columns  []types.ColumnSpec
	Indexes  []types.IndexSpec
	Backend  backend.Backend
	IdxMgr   *index.Manager
	FilePath string
	Format   string
	mu       sync.RWMutex
}

// RLock acquires a read lock on the entry for a SELECT operation.
func (e *Entry) RLock() { e.mu.RLock() }

// RUnlock releases the read lock.
func (e *Entry) RUnlock() { e.mu.RUnlock() }

// Lock acquires an exclusive write lock for INSERT, UPDATE, or DELETE.
func (e *Entry) Lock() { e.mu.Lock() }

// Unlock releases the exclusive write lock.
func (e *Entry) Unlock() { e.mu.Unlock() }

// Registry is a thread-safe map from table name to Entry.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

// New creates an empty Registry.
func New() *Registry {
	return &Registry{entries: make(map[string]*Entry)}
}

// Add registers a new table entry under name. It returns an error if the name
// is already in use.
func (r *Registry) Add(name string, e *Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("registry: table %q already registered", name)
	}
	r.entries[name] = e
	return nil
}

// Remove unregisters the named table and returns its entry so the caller can
// close the backend. It returns an error when the name is not found.
func (r *Registry) Remove(name string) (*Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		return nil, fmt.Errorf("registry: table %q not found", name)
	}
	delete(r.entries, name)
	return e, nil
}

// Get returns the entry for name without acquiring a lock on the entry itself.
// Callers must call e.RLock / e.Lock before using the entry's backend or
// index.
func (r *Registry) Get(name string) (*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return nil, fmt.Errorf("registry: table %q not found", name)
	}
	return e, nil
}

// List returns the names of all registered tables in arbitrary order.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for n := range r.entries {
		names = append(names, n)
	}
	return names
}

// Close calls Close on every registered backend and returns the first error
// encountered.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for _, e := range r.entries {
		if err := e.Backend.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
