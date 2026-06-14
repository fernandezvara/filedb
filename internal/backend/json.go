package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/fernandezvara/filedb/internal/types"
)

// jsonBackend implements Backend for JSON files whose content is a single
// top-level array of objects: [{...}, {...}, ...]. The _id column, when
// auto-generated, is stored as a field in each object.
//
// Because any mutation requires rewriting the JSON array, Append also performs
// a full rewrite and returns the new offsets for all rows. The registry uses
// this signal to rebuild the entire index after each write.
type jsonBackend struct {
	path   string
	schema []types.ColumnSpec
}

// NewJSON creates a JSON backend for the file at path. When createIfNotExists
// is true and the file does not already exist, it is initialised with an
// empty JSON array.
func NewJSON(path string, schema []types.ColumnSpec, createIfNotExists bool) (Backend, error) {
	b := &jsonBackend{path: path, schema: schema}
	if createIfNotExists {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte("[]\n"), 0o644); err != nil {
				return nil, fmt.Errorf("json: creating %q: %w", path, err)
			}
		}
	}
	return b, nil
}

// ReadAll reads the JSON array from disk and returns all rows together with
// the byte offset of the opening '{' of each object within the file.
func (b *jsonBackend) ReadAll() ([]Row, []int64, error) {
	data, err := os.ReadFile(b.path)
	if err != nil {
		return nil, nil, fmt.Errorf("json: reading %q: %w", b.path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, nil
	}

	offsets := findObjectOffsets(data)
	var rawObjs []json.RawMessage
	if err := json.Unmarshal(data, &rawObjs); err != nil {
		return nil, nil, fmt.Errorf("json: parsing array in %q: %w", b.path, err)
	}
	if len(rawObjs) != len(offsets) {
		// Mismatch means the offset scanner and the JSON parser disagree;
		// fall back to post-parse offset scanning which is always correct.
		offsets = recomputeOffsets(data, rawObjs)
	}

	rows := make([]Row, len(rawObjs))
	for i, raw := range rawObjs {
		row, err := b.unmarshalObject(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("json: object %d in %q: %w", i, b.path, err)
		}
		rows[i] = row
	}
	return rows, offsets, nil
}

// ReadAt seeks to offset and decodes the JSON object that starts there.
func (b *jsonBackend) ReadAt(offset int64) (Row, error) {
	f, err := os.Open(b.path)
	if err != nil {
		return nil, fmt.Errorf("json: opening %q: %w", b.path, err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("json: seeking in %q: %w", b.path, err)
	}
	dec := json.NewDecoder(f)
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("json: decoding object at offset %d in %q: %w", offset, b.path, err)
	}
	return b.unmarshalObject(raw)
}

// Append adds row to the data set, rewrites the file atomically, and returns
// the offset of the new row together with the new offsets of every row. The
// non-nil allOffsets return signals to the caller that all index entries must
// be rebuilt.
func (b *jsonBackend) Append(row Row) (int64, []int64, error) {
	rows, _, err := b.ReadAll()
	if err != nil {
		return 0, nil, err
	}
	rows = append(rows, row)
	allOffsets, err := b.WriteAll(rows)
	if err != nil {
		return 0, nil, err
	}
	newOffset := allOffsets[len(allOffsets)-1]
	return newOffset, allOffsets, nil
}

// WriteAll atomically replaces the file with a formatted JSON array of rows
// and returns the byte offset of each object's opening '{' in the written
// file.
func (b *jsonBackend) WriteAll(rows []Row) ([]int64, error) {
	if len(rows) == 0 {
		if err := atomicWrite(b.path, []byte("[]\n")); err != nil {
			return nil, err
		}
		return nil, nil
	}

	var buf bytes.Buffer
	buf.WriteString("[\n")

	offsets := make([]int64, len(rows))
	for i, row := range rows {
		marshalled, err := b.marshalObject(row)
		if err != nil {
			return nil, fmt.Errorf("json: marshalling row %d: %w", i, err)
		}
		if i > 0 {
			buf.WriteString(",\n")
		}
		buf.WriteString("  ")
		offsets[i] = int64(buf.Len()) // position of the opening '{'
		buf.Write(marshalled)
	}
	buf.WriteString("\n]\n")

	if err := atomicWrite(b.path, buf.Bytes()); err != nil {
		return nil, err
	}
	return offsets, nil
}

// Close is a no-op for the JSON backend.
func (b *jsonBackend) Close() error { return nil }

// unmarshalObject decodes a raw JSON object into a typed Row using the backend
// schema for type coercion.
func (b *jsonBackend) unmarshalObject(raw json.RawMessage) (Row, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	row := make(Row, len(b.schema))
	for _, col := range b.schema {
		rawVal, exists := m[col.Name]
		if !exists {
			row[col.Name] = nil
			continue
		}
		var v any
		if err := json.Unmarshal(rawVal, &v); err != nil {
			return nil, fmt.Errorf("column %q: %w", col.Name, err)
		}
		coerced, err := types.Coerce(col.Type, v)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", col.Name, err)
		}
		row[col.Name] = coerced
	}
	return row, nil
}

// marshalObject converts a typed Row to a compact JSON object containing only
// the columns declared in the schema.
func (b *jsonBackend) marshalObject(row Row) ([]byte, error) {
	m := make(map[string]any, len(b.schema))
	for _, col := range b.schema {
		m[col.Name] = types.Format(col.Type, row[col.Name])
	}
	return json.Marshal(m)
}

// findObjectOffsets scans data (a JSON array) and returns the byte position
// of the opening '{' of each top-level object. It handles strings and nested
// braces/brackets correctly.
func findObjectOffsets(data []byte) []int64 {
	var offsets []int64
	depth := 0
	arrayDepth := 0
	inString := false
	escaped := false

	for i := 0; i < len(data); i++ {
		b := data[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '[':
			arrayDepth++
		case ']':
			arrayDepth--
		case '{':
			if arrayDepth == 1 && depth == 0 {
				offsets = append(offsets, int64(i))
			}
			depth++
		case '}':
			depth--
		}
	}
	return offsets
}

// recomputeOffsets re-derives per-object offsets by marshalling each raw
// object and scanning the original data for its starting position. This is
// the fallback path when findObjectOffsets returns a count that differs from
// the parsed object count.
func recomputeOffsets(data []byte, raws []json.RawMessage) []int64 {
	offsets := make([]int64, len(raws))
	search := int64(0)
	for i, raw := range raws {
		// Find the opening '{' of this object.
		idx := bytes.Index(data[search:], []byte("{"))
		if idx < 0 {
			break
		}
		offsets[i] = search + int64(idx)
		search = offsets[i] + int64(len(raw))
	}
	return offsets
}
