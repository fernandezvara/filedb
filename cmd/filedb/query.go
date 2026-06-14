package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/fernandezvara/filedb"
	"github.com/fernandezvara/filedb/cmd/filedb/format"
)

// runQuery executes sql against db and writes the result to w in the given
// output format. It detects whether the statement is a SELECT (using Query) or
// a write statement (using Exec) by inspecting the first keyword.
func runQuery(db *filedb.DB, sql string, fmt_ format.Format, w io.Writer) error {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return nil
	}

	upper := strings.ToUpper(sql)
	if strings.HasPrefix(upper, "SELECT") {
		rows, err := db.Query(sql)
		if err != nil {
			return err
		}
		defer rows.Close()

		cols := rows.Columns()
		var data []map[string]any
		for rows.Next() {
			dest := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range dest {
				ptrs[i] = &dest[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			row := make(map[string]any, len(cols))
			for i, col := range cols {
				row[col] = dest[i]
			}
			data = append(data, row)
		}
		return format.Write(w, fmt_, cols, data)
	}

	result, err := db.Exec(sql)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%d row(s) affected\n", result.RowsAffected)
	return nil
}
