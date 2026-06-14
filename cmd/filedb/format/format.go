// Package format provides output formatters for filedb query results.
// The three supported modes are: an ASCII table, JSON (array of objects),
// and CSV (header row + data rows).
package format

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// Format identifies the output style.
type Format string

const (
	Table Format = "table"
	JSON  Format = "json"
	CSV   Format = "csv"
)

// Write renders columns and rows to w in the requested format. columns must
// be the ordered list of column names; rows must be a slice of maps using
// those same names as keys.
func Write(w io.Writer, f Format, columns []string, rows []map[string]any) error {
	switch f {
	case JSON:
		return writeJSON(w, columns, rows)
	case CSV:
		return writeCSV(w, columns, rows)
	default:
		return writeTable(w, columns, rows)
	}
}

// writeTable formats the result as an ASCII table using tab-separated columns
// with a header separator line.
func writeTable(w io.Writer, columns []string, rows []map[string]any) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Header row.
	fmt.Fprintln(tw, strings.Join(columns, "\t"))

	// Separator line: one dash sequence per column width.
	seps := make([]string, len(columns))
	for i, col := range columns {
		seps[i] = strings.Repeat("-", len(col))
	}
	fmt.Fprintln(tw, strings.Join(seps, "\t"))

	// Data rows.
	for _, row := range rows {
		vals := make([]string, len(columns))
		for i, col := range columns {
			vals[i] = cellString(row[col])
		}
		fmt.Fprintln(tw, strings.Join(vals, "\t"))
	}
	return tw.Flush()
}

// writeJSON renders rows as a JSON array of objects. Each object contains only
// the keys listed in columns, in the order supplied. Float values are rounded
// to 10 significant figures before encoding to remove floating-point noise
// from arithmetic (e.g. salary * 1.1 producing 126500.00000000001).
func writeJSON(w io.Writer, columns []string, rows []map[string]any) error {
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		obj := make(map[string]any, len(columns))
		for _, col := range columns {
			obj[col] = cleanValue(row[col])
		}
		out[i] = obj
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// cleanValue normalises a row value for JSON output: float64 values are
// rounded to 10 significant figures to suppress floating-point arithmetic
// noise, and time.Time values are formatted as strings.
func cleanValue(v any) any {
	switch t := v.(type) {
	case float64:
		s := strconv.FormatFloat(t, 'g', 10, 64)
		cleaned, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return cleaned
		}
	case time.Time:
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
			return t.Format("2006-01-02")
		}
		return t.Format(time.RFC3339)
	}
	return v
}

// writeCSV renders rows as comma-separated values with a header row.
func writeCSV(w io.Writer, columns []string, rows []map[string]any) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(columns); err != nil {
		return err
	}
	for _, row := range rows {
		fields := make([]string, len(columns))
		for i, col := range columns {
			fields[i] = cellString(row[col])
		}
		if err := cw.Write(fields); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// cellString converts any value to its display string for table and CSV output.
// Floats are formatted with up to 10 significant figures to avoid floating-point
// noise (e.g. 126500.00000000001 displays as 126500).
// time.Time values at midnight UTC are assumed to be date-only columns and are
// formatted as YYYY-MM-DD; others use RFC3339.
func cellString(v any) string {
	if v == nil {
		return "NULL"
	}
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'g', 10, 64)
	case time.Time:
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
			return t.Format("2006-01-02")
		}
		return t.Format(time.RFC3339)
	}
	return fmt.Sprintf("%v", v)
}
