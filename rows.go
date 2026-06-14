package filedb

import "fmt"

// Rows is a forward-only cursor over the result set produced by a SELECT
// query. The usage pattern mirrors database/sql.Rows:
//
//	rows, err := db.Query("SELECT id, name FROM users")
//	if err != nil { ... }
//	defer rows.Close()
//	for rows.Next() {
//	    var id, name string
//	    if err := rows.Scan(&id, &name); err != nil { ... }
//	}
type Rows struct {
	columns []string
	data    []map[string]any
	pos     int
	current map[string]any
	closed  bool
}

// newRows constructs a Rows cursor from an ordered column list and a slice of
// row maps. It is used internally by the query executor.
func newRows(columns []string, data []map[string]any) *Rows {
	return &Rows{columns: columns, data: data, pos: -1}
}

// Next advances the cursor to the next row and returns true when a row is
// available to be read with Scan. It returns false when the result set is
// exhausted or after Close has been called.
func (r *Rows) Next() bool {
	if r.closed || r.pos+1 >= len(r.data) {
		return false
	}
	r.pos++
	r.current = r.data[r.pos]
	return true
}

// Columns returns the ordered list of column names in the result set.
// The slice is a copy; callers may modify it without affecting the cursor.
func (r *Rows) Columns() []string {
	out := make([]string, len(r.columns))
	copy(out, r.columns)
	return out
}

// Scan copies the values of the current row into dest, which must be a list
// of non-nil pointers matching the column count returned by Columns. Supported
// destination types are *any, *string, *int64, *float64, and *bool.
func (r *Rows) Scan(dest ...any) error {
	if r.current == nil {
		return fmt.Errorf("filedb: Scan called before Next or after exhaustion")
	}
	if len(dest) != len(r.columns) {
		return fmt.Errorf("filedb: Scan expects %d destinations, got %d",
			len(r.columns), len(dest))
	}
	for i, col := range r.columns {
		if err := scanValue(dest[i], r.current[col]); err != nil {
			return fmt.Errorf("filedb: Scan column %q: %w", col, err)
		}
	}
	return nil
}

// Close marks the cursor as exhausted. Subsequent calls to Next return false.
// Close is idempotent.
func (r *Rows) Close() error {
	r.closed = true
	r.current = nil
	return nil
}

// scanValue copies src into the pointer dest with basic type widening.
func scanValue(dest, src any) error {
	switch d := dest.(type) {
	case *any:
		*d = src
	case *string:
		if src == nil {
			*d = ""
		} else {
			*d = fmt.Sprintf("%v", src)
		}
	case *int64:
		switch v := src.(type) {
		case int64:
			*d = v
		case float64:
			*d = int64(v)
		case nil:
			*d = 0
		default:
			return fmt.Errorf("cannot assign %T to *int64", src)
		}
	case *float64:
		switch v := src.(type) {
		case float64:
			*d = v
		case int64:
			*d = float64(v)
		case nil:
			*d = 0
		default:
			return fmt.Errorf("cannot assign %T to *float64", src)
		}
	case *bool:
		switch v := src.(type) {
		case bool:
			*d = v
		case nil:
			*d = false
		default:
			return fmt.Errorf("cannot assign %T to *bool", src)
		}
	case nil:
		return fmt.Errorf("nil destination pointer")
	default:
		return fmt.Errorf("unsupported destination type %T", dest)
	}
	return nil
}
