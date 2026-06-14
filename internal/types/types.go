// Package types provides value parsing, formatting, and coercion for the
// filedb column type system. It is used by every internal package that
// handles typed row data.
package types

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Supported DataType string constants. These match the public package values
// and are redeclared here to avoid an import cycle.
const (
	TypeString   = "string"
	TypeInt      = "int"
	TypeFloat    = "float"
	TypeBool     = "bool"
	TypeDate     = "date"
	TypeDatetime = "datetime"
	TypeXID      = "xid" // internal auto-generated primary key type

	DateLayout     = "2006-01-02"
	DatetimeLayout = time.RFC3339

	// AutoIDColumn is the name of the column automatically added to tables
	// that declare no primary key.
	AutoIDColumn = "_id"

	// compositeSep separates field values in composite index keys. Using the
	// null byte prevents collision with normal string values.
	compositeSep = "\x00"
)

// ColumnSpec is the minimal column description shared by internal packages.
type ColumnSpec struct {
	Name       string
	Type       string
	Nullable   bool
	Default    any
	PrimaryKey bool
}

// IndexSpec is the minimal index description shared by internal packages.
type IndexSpec struct {
	Name    string
	Columns []string
	Unique  bool
}

// Parse converts a raw string (as read from a file) to the canonical Go type
// for the given column type. An empty string for non-string types returns nil,
// representing a null value.
func Parse(colType, raw string) (any, error) {
	if raw == "" && colType != TypeString && colType != TypeXID {
		return nil, nil
	}
	switch colType {
	case TypeString, TypeXID:
		return raw, nil
	case TypeInt:
		v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot parse %q as int: %w", raw, err)
		}
		return v, nil
	case TypeFloat:
		v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, fmt.Errorf("cannot parse %q as float: %w", raw, err)
		}
		return v, nil
	case TypeBool:
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "1", "yes":
			return true, nil
		case "false", "0", "no", "":
			return false, nil
		default:
			return nil, fmt.Errorf("cannot parse %q as bool", raw)
		}
	case TypeDate:
		t, err := time.Parse(DateLayout, strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("cannot parse %q as date (YYYY-MM-DD): %w", raw, err)
		}
		return t, nil
	case TypeDatetime:
		t, err := time.Parse(DatetimeLayout, strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("cannot parse %q as datetime (RFC3339): %w", raw, err)
		}
		return t, nil
	default:
		return nil, fmt.Errorf("unknown column type %q", colType)
	}
}

// Format converts a typed Go value to its canonical string representation
// for storage in a file.
func Format(colType string, v any) string {
	if v == nil {
		return ""
	}
	switch colType {
	case TypeDate:
		if t, ok := v.(time.Time); ok {
			return t.Format(DateLayout)
		}
	case TypeDatetime:
		if t, ok := v.(time.Time); ok {
			return t.Format(DatetimeLayout)
		}
	case TypeBool:
		if b, ok := v.(bool); ok {
			if b {
				return "true"
			}
			return "false"
		}
	case TypeInt:
		switch n := v.(type) {
		case int64:
			return strconv.FormatInt(n, 10)
		case float64:
			return strconv.FormatInt(int64(n), 10)
		}
	case TypeFloat:
		if f, ok := v.(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	return fmt.Sprintf("%v", v)
}

// Coerce converts an arbitrary Go value to the canonical Go type for colType.
// String inputs are handled by Parse. Numeric types are widened or narrowed
// between int64 and float64 as needed.
func Coerce(colType string, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		return Parse(colType, s)
	}
	switch colType {
	case TypeString, TypeXID:
		return fmt.Sprintf("%v", v), nil
	case TypeInt:
		switch n := v.(type) {
		case int64:
			return n, nil
		case int:
			return int64(n), nil
		case float64:
			return int64(n), nil
		default:
			return nil, fmt.Errorf("cannot coerce %T to int", v)
		}
	case TypeFloat:
		switch n := v.(type) {
		case float64:
			return n, nil
		case int64:
			return float64(n), nil
		case int:
			return float64(n), nil
		default:
			return nil, fmt.Errorf("cannot coerce %T to float", v)
		}
	case TypeBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("cannot coerce %T to bool", v)
	case TypeDate, TypeDatetime:
		if t, ok := v.(time.Time); ok {
			return t, nil
		}
		return nil, fmt.Errorf("cannot coerce %T to %s", v, colType)
	default:
		return nil, fmt.Errorf("unknown column type %q", colType)
	}
}

// IndexKey builds a canonical composite key string from an ordered list of
// column values. The null-byte separator prevents collisions between values
// that contain the separator character.
func IndexKey(values []any) string {
	parts := make([]string, len(values))
	for i, v := range values {
		if v == nil {
			parts[i] = "\x00nil\x00"
		} else {
			parts[i] = fmt.Sprintf("%v", v)
		}
	}
	return strings.Join(parts, compositeSep)
}

// Compare returns -1, 0, or 1 comparing a to b. Values of different types
// are compared by their string representation. nil is always less than
// non-nil.
func Compare(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch av := a.(type) {
	case int64:
		if bv, ok := b.(int64); ok {
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		}
		if bv, ok := b.(float64); ok {
			af := float64(av)
			if af < bv {
				return -1
			}
			if af > bv {
				return 1
			}
			return 0
		}
	case float64:
		var bv float64
		switch bt := b.(type) {
		case float64:
			bv = bt
		case int64:
			bv = float64(bt)
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
		return 0
	case time.Time:
		if bv, ok := b.(time.Time); ok {
			if av.Before(bv) {
				return -1
			}
			if av.After(bv) {
				return 1
			}
			return 0
		}
	case bool:
		if bv, ok := b.(bool); ok {
			if !av && bv {
				return -1
			}
			if av && !bv {
				return 1
			}
			return 0
		}
	}
	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	return strings.Compare(as, bs)
}

// ValidateRow checks that every non-nullable column without a default has a
// non-nil value in row. It returns the first violation found.
func ValidateRow(row map[string]any, cols []ColumnSpec) error {
	for _, c := range cols {
		v, exists := row[c.Name]
		if !exists || v == nil {
			if !c.Nullable && c.Default == nil {
				return fmt.Errorf("column %q: null value not allowed", c.Name)
			}
		}
	}
	return nil
}

// ApplyDefaults fills in missing or nil column values in row using the default
// values declared in cols.
func ApplyDefaults(row map[string]any, cols []ColumnSpec) error {
	for _, c := range cols {
		v, exists := row[c.Name]
		if (!exists || v == nil) && c.Default != nil {
			coerced, err := Coerce(c.Type, c.Default)
			if err != nil {
				return fmt.Errorf("column %q default: %w", c.Name, err)
			}
			row[c.Name] = coerced
		}
	}
	return nil
}
