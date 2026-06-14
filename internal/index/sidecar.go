package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// sidecarFile is the on-disk JSON structure for the index sidecar.
type sidecarFile struct {
	Version int                     `json:"version"`
	Indexes map[string]*sidecarDef  `json:"indexes"`
}

// sidecarDef is the serialisable form of one index definition.
type sidecarDef struct {
	Columns []string           `json:"columns"`
	Unique  bool               `json:"unique"`
	Entries map[string][]int64 `json:"entries"`
}

// Save serialises the current in-memory index state to the sidecar file using
// an atomic write (temp file followed by rename) to prevent partial-write
// corruption.
func (m *Manager) Save() error {
	m.mu.RLock()
	snap := m.snapshot()
	m.mu.RUnlock()

	sf := sidecarFile{
		Version: 1,
		Indexes: make(map[string]*sidecarDef, len(snap)),
	}
	for name, def := range snap {
		sf.Indexes[name] = &sidecarDef{
			Columns: def.Columns,
			Unique:  def.Unique,
			Entries: def.Entries,
		}
	}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("serialising index %q: %w", m.sidecarPath, err)
	}
	return atomicWrite(m.sidecarPath, data)
}

// Load reads the sidecar file and restores the in-memory index. It returns
// os.ErrNotExist when the sidecar file does not yet exist, or an error when
// the sidecar is missing any index that has been Define()-d on this manager.
// Both conditions signal the caller to rebuild from the data file.
func (m *Manager) Load() error {
	data, err := os.ReadFile(m.sidecarPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return fmt.Errorf("reading index sidecar %q: %w", m.sidecarPath, err)
	}
	var sf sidecarFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return fmt.Errorf("parsing index sidecar %q: %w", m.sidecarPath, err)
	}
	snap := make(map[string]*indexDef, len(sf.Indexes))
	for name, sd := range sf.Indexes {
		snap[name] = &indexDef{
			Columns: sd.Columns,
			Unique:  sd.Unique,
			Entries: sd.Entries,
		}
	}
	// Reject the sidecar when it is missing an index that is currently defined.
	// This happens when the schema gains indexes after the sidecar was written,
	// or when a stale sidecar from a different schema version is present.
	m.mu.RLock()
	for name := range m.defs {
		if _, ok := snap[name]; !ok {
			m.mu.RUnlock()
			return fmt.Errorf("index sidecar %q missing index %q; rebuild required",
				m.sidecarPath, name)
		}
	}
	m.mu.RUnlock()
	m.mu.Lock()
	m.restore(snap)
	m.mu.Unlock()
	return nil
}

// atomicWrite writes data to path by first writing to a sibling temporary
// file and then renaming it into place. os.Rename is atomic on all platforms
// supported by filedb (Linux, macOS, Windows) when src and dst share a
// directory.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".filedb-idx-*")
	if err != nil {
		return fmt.Errorf("creating temp index file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp index file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp index file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("promoting temp index file to %q: %w", path, err)
	}
	return nil
}
