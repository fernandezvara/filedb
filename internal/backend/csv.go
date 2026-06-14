package backend

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fernandezvara/filedb/internal/types"
)

// csvBackend implements Backend for comma-separated value files. The first row
// of the file is always the header and contains the column names in schema
// order. Each subsequent row is one data record. The _id column, when
// auto-generated, appears as the first column in the header.
type csvBackend struct {
	path   string
	schema []types.ColumnSpec
}

// NewCSV creates a CSV backend for the file at path. When createIfNotExists
// is true and the file does not already exist, it is created with a header
// row derived from schema.
func NewCSV(path string, schema []types.ColumnSpec, createIfNotExists bool) (Backend, error) {
	b := &csvBackend{path: path, schema: schema}
	if createIfNotExists {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := b.createFile(); err != nil {
				return nil, err
			}
		}
	}
	return b, nil
}

// createFile writes an empty CSV file containing only the header row.
func (b *csvBackend) createFile() error {
	f, err := os.Create(b.path)
	if err != nil {
		return fmt.Errorf("csv: creating %q: %w", b.path, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write(b.headerFields()); err != nil {
		return fmt.Errorf("csv: writing header to %q: %w", b.path, err)
	}
	w.Flush()
	return w.Error()
}

// headerFields returns the ordered column names for use as the CSV header.
func (b *csvBackend) headerFields() []string {
	names := make([]string, len(b.schema))
	for i, c := range b.schema {
		names[i] = c.Name
	}
	return names
}

// ReadAll opens the file, skips the header, and returns all data rows along
// with the byte offset of the start of each row within the file.
func (b *csvBackend) ReadAll() ([]Row, []int64, error) {
	f, err := os.Open(b.path)
	if err != nil {
		return nil, nil, fmt.Errorf("csv: opening %q: %w", b.path, err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	// Read and discard the header line; track its byte length as the
	// initial offset for the first data row.
	headerLine, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, nil, fmt.Errorf("csv: reading header of %q: %w", b.path, err)
	}
	offset := int64(len(headerLine))

	var rows []Row
	var offsets []int64
	for {
		rowOffset := offset
		line, readErr := br.ReadString('\n')
		offset += int64(len(line))
		if len(line) == 0 {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if readErr == io.EOF {
				break
			}
			continue
		}
		row, err := b.parseLine(line)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, row)
		offsets = append(offsets, rowOffset)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, nil, fmt.Errorf("csv: reading %q: %w", b.path, readErr)
		}
	}
	return rows, offsets, nil
}

// ReadAt seeks to offset and parses the single CSV row starting at that
// position.
func (b *csvBackend) ReadAt(offset int64) (Row, error) {
	f, err := os.Open(b.path)
	if err != nil {
		return nil, fmt.Errorf("csv: opening %q: %w", b.path, err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("csv: seeking in %q: %w", b.path, err)
	}
	br := bufio.NewReader(f)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("csv: reading at offset %d in %q: %w", offset, b.path, err)
	}
	return b.parseLine(strings.TrimRight(line, "\r\n"))
}

// Append writes row as a new CSV line at the end of the file. Because CSV
// supports true append, existing row offsets are not affected and allOffsets
// is returned as nil.
func (b *csvBackend) Append(row Row) (int64, []int64, error) {
	f, err := os.OpenFile(b.path, os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return 0, nil, fmt.Errorf("csv: opening %q for append: %w", b.path, err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return 0, nil, fmt.Errorf("csv: stat %q: %w", b.path, err)
	}
	newOffset := stat.Size()

	w := csv.NewWriter(f)
	if err := w.Write(b.rowToFields(row)); err != nil {
		return 0, nil, fmt.Errorf("csv: encoding row in %q: %w", b.path, err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return 0, nil, fmt.Errorf("csv: flushing row to %q: %w", b.path, err)
	}
	return newOffset, nil, nil // nil allOffsets signals no full rebuild needed
}

// WriteAll atomically replaces the entire file with header + rows, returning
// the byte offset of each row in the new file.
func (b *csvBackend) WriteAll(rows []Row) ([]int64, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if err := w.Write(b.headerFields()); err != nil {
		return nil, fmt.Errorf("csv: encoding header: %w", err)
	}
	w.Flush()

	offsets := make([]int64, len(rows))
	for i, row := range rows {
		offsets[i] = int64(buf.Len())
		if err := w.Write(b.rowToFields(row)); err != nil {
			return nil, fmt.Errorf("csv: encoding row %d: %w", i, err)
		}
		w.Flush()
	}
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("csv: write error: %w", err)
	}
	if err := atomicWrite(b.path, buf.Bytes()); err != nil {
		return nil, err
	}
	return offsets, nil
}

// Close is a no-op for the CSV backend; files are opened and closed per
// operation to remain concurrency-safe.
func (b *csvBackend) Close() error { return nil }

// parseLine parses a single CSV record line into a typed Row using the
// backend schema for type coercion.
func (b *csvBackend) parseLine(line string) (Row, error) {
	r := csv.NewReader(strings.NewReader(line))
	fields, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("csv: parsing line %q: %w", line, err)
	}
	row := make(Row, len(b.schema))
	for i, col := range b.schema {
		raw := ""
		if i < len(fields) {
			raw = fields[i]
		}
		v, err := types.Parse(col.Type, raw)
		if err != nil {
			return nil, fmt.Errorf("csv: column %q: %w", col.Name, err)
		}
		row[col.Name] = v
	}
	return row, nil
}

// rowToFields converts a typed Row into an ordered slice of string values
// matching the schema column order.
func (b *csvBackend) rowToFields(row Row) []string {
	fields := make([]string, len(b.schema))
	for i, col := range b.schema {
		fields[i] = types.Format(col.Type, row[col.Name])
	}
	return fields
}
