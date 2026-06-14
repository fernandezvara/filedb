// Package backend defines the file-backend interface and shared utilities used
// by the CSV and JSON implementations.
package backend

import (
	"fmt"
	"os"
	"path/filepath"
)

// Row is an unordered map of column name to typed value representing one
// record. Values are typed Go values: string, int64, float64, bool, time.Time,
// or nil. Row is a type alias so it can be used without a conversion across
// packages that import backend.
type Row = map[string]any

// Backend is the interface that CSV and JSON file implementations must satisfy.
// Every write method must flush to disk before returning.
type Backend interface {
	// ReadAll returns all rows in the file together with the byte offset at
	// which each row begins. The offset slice and row slice share the same
	// index: offsets[i] is the file position of rows[i].
	ReadAll() (rows []Row, offsets []int64, err error)

	// ReadAt reads and returns the single row whose data starts at the given
	// byte offset in the file.
	ReadAt(offset int64) (Row, error)

	// Append adds row to the end of the file. For CSV the file is extended in
	// place and only the new row's offset is returned; for JSON the file is
	// fully rewritten and allOffsets contains the new positions of every row
	// in insertion order (including the appended row as the last element).
	// Callers must check whether allOffsets is nil to determine whether a
	// full index rebuild is required.
	Append(row Row) (newOffset int64, allOffsets []int64, err error)

	// WriteAll atomically replaces the entire file content with rows and
	// returns the byte offset of each row in the written file.
	WriteAll(rows []Row) (offsets []int64, err error)

	// Close releases any resources held by the backend.
	Close() error
}

// atomicWrite writes data to path by first writing to a sibling temp file and
// then renaming it into place. This prevents partial writes from corrupting an
// existing data file.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".filedb-data-*")
	if err != nil {
		return fmt.Errorf("creating temp data file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp data file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp data file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("promoting temp data file to %q: %w", path, err)
	}
	return nil
}
