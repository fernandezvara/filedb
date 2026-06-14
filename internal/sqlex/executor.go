package sqlex

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fernandezvara/filedb/internal/backend"
	"github.com/fernandezvara/filedb/internal/index"
	"github.com/fernandezvara/filedb/internal/types"
	"github.com/rs/xid"
)

// ExecContext bundles everything the executor needs to run a statement against
// one table.
type ExecContext struct {
	Columns []types.ColumnSpec
	Indexes []types.IndexSpec
	Backend backend.Backend
	Index   *index.Manager
}

// columnMap builds a name-to-spec lookup from the context columns.
func (ctx *ExecContext) columnMap() map[string]types.ColumnSpec {
	m := make(map[string]types.ColumnSpec, len(ctx.Columns))
	for _, c := range ctx.Columns {
		m[c.Name] = c
	}
	return m
}

// ExecSelect executes stmt and returns the result column names and row data.
func ExecSelect(stmt *SelectStmt, ctx ExecContext) ([]string, []map[string]any, error) {
	rows, err := fetchRows(stmt.Where, ctx)
	if err != nil {
		return nil, nil, err
	}

	hasAgg := stmtHasAggregate(stmt)
	if hasAgg || len(stmt.GroupBy) > 0 {
		return execGrouped(stmt, rows, ctx)
	}

	if stmt.Distinct {
		rows = deduplicate(rows, stmt)
	}
	if len(stmt.OrderBy) > 0 {
		sortRows(rows, stmt.OrderBy)
	}
	rows = applyLimitOffset(rows, stmt.Limit, stmt.Offset)

	return projectRows(stmt.Columns, rows, ctx)
}

// ExecInsert executes stmt, writes each value row to the backend, and updates
// all indexes. It returns the number of rows inserted.
func ExecInsert(stmt *InsertStmt, ctx ExecContext) (int64, error) {
	colMap := ctx.columnMap()
	var inserted int64
	for _, valueRow := range stmt.Values {
		row, err := buildInsertRow(stmt.Columns, valueRow, ctx.Columns, colMap)
		if err != nil {
			return inserted, err
		}
		if err := types.ApplyDefaults(row, ctx.Columns); err != nil {
			return inserted, err
		}
		if err := types.ValidateRow(row, ctx.Columns); err != nil {
			return inserted, err
		}
		if err := checkUniqueConstraints(row, ctx); err != nil {
			return inserted, err
		}
		newOffset, allOffsets, err := ctx.Backend.Append(row)
		if err != nil {
			return inserted, fmt.Errorf("insert: %w", err)
		}
		if allOffsets != nil {
			// JSON backend rewrote the file; rebuild the entire index.
			allRows, _, rerr := ctx.Backend.ReadAll()
			if rerr != nil {
				return inserted, fmt.Errorf("insert: re-reading after append: %w", rerr)
			}
			if err := rebuildIndex(ctx, allRows, allOffsets); err != nil {
				return inserted, err
			}
		} else {
			// CSV true append: only add index entries for the new row.
			if err := addIndexEntries(ctx, row, newOffset); err != nil {
				return inserted, err
			}
		}
		if err := ctx.Index.Save(); err != nil {
			return inserted, fmt.Errorf("insert: saving index: %w", err)
		}
		inserted++
	}
	return inserted, nil
}

// ExecUpdate executes stmt, modifying all rows matched by the WHERE clause.
// It returns the number of rows updated.
func ExecUpdate(stmt *UpdateStmt, ctx ExecContext) (int64, error) {
	colMap := ctx.columnMap()
	rows, offsets, err := ctx.Backend.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("update: reading table: %w", err)
	}

	var updated int64
	for i, row := range rows {
		match, err := evalWhere(stmt.Where, row)
		if err != nil {
			return updated, fmt.Errorf("update: evaluating WHERE at row %d: %w", i, err)
		}
		if !match {
			continue
		}
		for _, set := range stmt.Sets {
			spec, ok := colMap[set.Column]
			if !ok {
				return updated, fmt.Errorf("update: unknown column %q", set.Column)
			}
			val, err := evalExpr(set.Value, row)
			if err != nil {
				return updated, fmt.Errorf("update: SET %q: %w", set.Column, err)
			}
			coerced, err := types.Coerce(spec.Type, val)
			if err != nil {
				return updated, fmt.Errorf("update: SET %q: %w", set.Column, err)
			}
			rows[i][set.Column] = coerced
		}
		_ = offsets // offsets are rebuilt after WriteAll
		updated++
	}
	if updated == 0 {
		return 0, nil
	}
	newOffsets, err := ctx.Backend.WriteAll(rows)
	if err != nil {
		return updated, fmt.Errorf("update: writing table: %w", err)
	}
	if err := rebuildIndex(ctx, rows, newOffsets); err != nil {
		return updated, err
	}
	if err := ctx.Index.Save(); err != nil {
		return updated, fmt.Errorf("update: saving index: %w", err)
	}
	return updated, nil
}

// ExecDelete executes stmt, removing all rows matched by the WHERE clause.
// It returns the number of rows deleted.
func ExecDelete(stmt *DeleteStmt, ctx ExecContext) (int64, error) {
	rows, _, err := ctx.Backend.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("delete: reading table: %w", err)
	}

	var remaining []backend.Row
	var deleted int64
	for i, row := range rows {
		match, err := evalWhere(stmt.Where, row)
		if err != nil {
			return deleted, fmt.Errorf("delete: evaluating WHERE at row %d: %w", i, err)
		}
		if match {
			deleted++
		} else {
			remaining = append(remaining, row)
		}
	}
	if deleted == 0 {
		return 0, nil
	}
	newOffsets, err := ctx.Backend.WriteAll(remaining)
	if err != nil {
		return deleted, fmt.Errorf("delete: writing table: %w", err)
	}
	if err := rebuildIndex(ctx, remaining, newOffsets); err != nil {
		return deleted, err
	}
	if err := ctx.Index.Save(); err != nil {
		return deleted, fmt.Errorf("delete: saving index: %w", err)
	}
	return deleted, nil
}

// --- internal helpers --------------------------------------------------------

// fetchRows returns all rows that pass the WHERE filter. When the WHERE clause
// references an indexed column with an equality test, the index is used to
// avoid a full scan.
func fetchRows(where Expr, ctx ExecContext) ([]backend.Row, error) {
	if indexName, key, ok := tryIndexLookup(where, ctx); ok {
		offsets := ctx.Index.Lookup(indexName, key)
		rows := make([]backend.Row, 0, len(offsets))
		for _, off := range offsets {
			row, err := ctx.Backend.ReadAt(off)
			if err != nil {
				return nil, fmt.Errorf("index read at %d: %w", off, err)
			}
			// Re-check WHERE; the index may cover multiple columns.
			match, err := evalWhere(where, row)
			if err != nil {
				return nil, err
			}
			if match {
				rows = append(rows, row)
			}
		}
		return rows, nil
	}

	// Full scan.
	all, _, err := ctx.Backend.ReadAll()
	if err != nil {
		return nil, err
	}
	if where == nil {
		return all, nil
	}
	var result []backend.Row
	for _, row := range all {
		match, err := evalWhere(where, row)
		if err != nil {
			return nil, err
		}
		if match {
			result = append(result, row)
		}
	}
	return result, nil
}

// tryIndexLookup inspects where to find a simple col = literal equality on an
// indexed column. It returns (indexName, key, true) on success.
func tryIndexLookup(where Expr, ctx ExecContext) (string, string, bool) {
	bin, ok := where.(*BinExpr)
	if !ok || (bin.Op != "=" && bin.Op != "==") {
		return "", "", false
	}
	ref, ok := bin.Left.(*ColRef)
	if !ok {
		return "", "", false
	}
	lit, ok := bin.Right.(*Lit)
	if !ok {
		return "", "", false
	}
	for _, spec := range ctx.Indexes {
		if len(spec.Columns) == 1 && strings.EqualFold(spec.Columns[0], ref.Name) {
			key := types.IndexKey([]any{lit.Val})
			return spec.Name, key, true
		}
	}
	return "", "", false
}

// evalWhere evaluates a WHERE expression against row. nil where always matches.
func evalWhere(where Expr, row backend.Row) (bool, error) {
	if where == nil {
		return true, nil
	}
	v, err := evalExpr(where, row)
	if err != nil {
		return false, err
	}
	if b, ok := v.(bool); ok {
		return b, nil
	}
	return v != nil, nil
}

// evalExpr evaluates an expression node against a row, returning a typed Go value.
func evalExpr(expr Expr, row backend.Row) (any, error) {
	switch e := expr.(type) {
	case *Lit:
		return e.Val, nil
	case *ColRef:
		v, ok := row[e.Name]
		if !ok {
			// Try case-insensitive match.
			lower := strings.ToLower(e.Name)
			for k, val := range row {
				if strings.ToLower(k) == lower {
					return val, nil
				}
			}
			return nil, nil
		}
		return v, nil
	case *BinExpr:
		return evalBinary(e, row)
	case *UnaryExpr:
		v, err := evalExpr(e.Expr, row)
		if err != nil {
			return nil, err
		}
		if e.Op == "NOT" {
			b, _ := v.(bool)
			return !b, nil
		}
		return nil, fmt.Errorf("unknown unary op %q", e.Op)
	case *LikeExpr:
		return evalLike(e, row)
	case *InExpr:
		return evalIn(e, row)
	case *IsNullExpr:
		v, err := evalExpr(e.Expr, row)
		if err != nil {
			return nil, err
		}
		isNull := v == nil
		if e.Not {
			return !isNull, nil
		}
		return isNull, nil
	case *FuncExpr:
		// Aggregate functions are handled at a higher level; single-row
		// evaluation of aggregates returns nil.
		return nil, nil
	default:
		return nil, fmt.Errorf("unhandled expression type %T", expr)
	}
}

// evalBinary evaluates a binary expression including boolean AND/OR and
// comparison operators.
func evalBinary(e *BinExpr, row backend.Row) (any, error) {
	switch e.Op {
	case "AND":
		left, err := evalExpr(e.Left, row)
		if err != nil {
			return nil, err
		}
		if lb, _ := left.(bool); !lb {
			return false, nil // short-circuit
		}
		return evalExpr(e.Right, row)
	case "OR":
		left, err := evalExpr(e.Left, row)
		if err != nil {
			return nil, err
		}
		if lb, _ := left.(bool); lb {
			return true, nil // short-circuit
		}
		return evalExpr(e.Right, row)
	}

	left, err := evalExpr(e.Left, row)
	if err != nil {
		return nil, err
	}
	right, err := evalExpr(e.Right, row)
	if err != nil {
		return nil, err
	}
	// Arithmetic operators produce numeric values; comparison operators produce bool.
	switch e.Op {
	case "+":
		return arithmeticOp(left, right, func(a, b float64) float64 { return a + b })
	case "-":
		return arithmeticOp(left, right, func(a, b float64) float64 { return a - b })
	case "*":
		return arithmeticOp(left, right, func(a, b float64) float64 { return a * b })
	case "/":
		if toFloat(right) == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return arithmeticOp(left, right, func(a, b float64) float64 { return a / b })
	}
	cmp := types.Compare(left, right)
	switch e.Op {
	case "=", "==":
		return cmp == 0, nil
	case "!=", "<>":
		return cmp != 0, nil
	case "<":
		return cmp < 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case ">=":
		return cmp >= 0, nil
	default:
		return nil, fmt.Errorf("unknown binary operator %q", e.Op)
	}
}

// evalLike evaluates a LIKE expression using % and _ wildcards.
func evalLike(e *LikeExpr, row backend.Row) (any, error) {
	val, err := evalExpr(e.Expr, row)
	if err != nil {
		return nil, err
	}
	pat, err := evalExpr(e.Pattern, row)
	if err != nil {
		return nil, err
	}
	s := fmt.Sprintf("%v", val)
	p := fmt.Sprintf("%v", pat)
	matched := likeMatch(p, s)
	if e.Not {
		return !matched, nil
	}
	return matched, nil
}

// likeMatch implements SQL LIKE semantics: % matches any substring, _ matches
// exactly one character. Matching is case-sensitive.
func likeMatch(pattern, s string) bool {
	if pattern == "%" {
		return true
	}
	if len(pattern) == 0 {
		return len(s) == 0
	}
	if pattern[0] == '%' {
		rest := pattern[1:]
		for i := 0; i <= len(s); i++ {
			if likeMatch(rest, s[i:]) {
				return true
			}
		}
		return false
	}
	if len(s) == 0 {
		return false
	}
	if pattern[0] == '_' || rune(pattern[0]) == rune(s[0]) {
		return likeMatch(pattern[1:], s[1:])
	}
	return false
}

// evalIn evaluates an IN expression.
func evalIn(e *InExpr, row backend.Row) (any, error) {
	val, err := evalExpr(e.Expr, row)
	if err != nil {
		return nil, err
	}
	for _, item := range e.List {
		cmp, err := evalExpr(item, row)
		if err != nil {
			return nil, err
		}
		if types.Compare(val, cmp) == 0 {
			if e.Not {
				return false, nil
			}
			return true, nil
		}
	}
	if e.Not {
		return true, nil
	}
	return false, nil
}

// buildInsertRow constructs a typed Row from an explicit column list (or
// schema order when cols is empty) and the supplied value expressions.
func buildInsertRow(cols []string, values []Expr, schema []types.ColumnSpec, colMap map[string]types.ColumnSpec) (backend.Row, error) {
	row := make(backend.Row, len(schema))

	// Determine the mapping from value position to column name.
	targets := cols
	if len(targets) == 0 {
		targets = make([]string, len(schema))
		for i, c := range schema {
			targets[i] = c.Name
		}
	}
	if len(values) != len(targets) {
		return nil, fmt.Errorf("column count %d does not match value count %d",
			len(targets), len(values))
	}

	// Auto-generate _id when the column is not explicitly provided.
	hasID := false
	for _, col := range targets {
		if col == types.AutoIDColumn {
			hasID = true
			break
		}
	}
	if !hasID {
		for _, c := range schema {
			if c.Name == types.AutoIDColumn {
				row[types.AutoIDColumn] = xid.New().String()
				break
			}
		}
	}

	for i, col := range targets {
		spec, ok := colMap[col]
		if !ok {
			return nil, fmt.Errorf("unknown column %q", col)
		}
		val, err := evalExpr(values[i], nil)
		if err != nil {
			return nil, fmt.Errorf("value for column %q: %w", col, err)
		}
		coerced, err := types.Coerce(spec.Type, val)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", col, err)
		}
		row[col] = coerced
	}
	return row, nil
}

// checkUniqueConstraints verifies that inserting row would not violate any
// unique index in ctx.
func checkUniqueConstraints(row backend.Row, ctx ExecContext) error {
	for _, spec := range ctx.Indexes {
		if !spec.Unique {
			continue
		}
		vals := make([]any, len(spec.Columns))
		for i, col := range spec.Columns {
			vals[i] = row[col]
		}
		key := types.IndexKey(vals)
		if existing := ctx.Index.Lookup(spec.Name, key); len(existing) > 0 {
			return fmt.Errorf("unique violation on index %q", spec.Name)
		}
	}
	return nil
}

// addIndexEntries appends one index entry per index for row at offset.
func addIndexEntries(ctx ExecContext, row backend.Row, offset int64) error {
	for _, spec := range ctx.Indexes {
		vals := make([]any, len(spec.Columns))
		for i, col := range spec.Columns {
			vals[i] = row[col]
		}
		key := types.IndexKey(vals)
		if err := ctx.Index.Add(spec.Name, key, offset); err != nil {
			return fmt.Errorf("index %q: %w", spec.Name, err)
		}
	}
	return nil
}

// rebuildIndex clears and repopulates all index entries from rows and their
// corresponding offsets.
func rebuildIndex(ctx ExecContext, rows []backend.Row, offsets []int64) error {
	entries := make([]index.IndexEntry, 0, len(rows)*len(ctx.Indexes))
	for i, row := range rows {
		if i >= len(offsets) {
			break
		}
		for _, spec := range ctx.Indexes {
			vals := make([]any, len(spec.Columns))
			for j, col := range spec.Columns {
				vals[j] = row[col]
			}
			entries = append(entries, index.IndexEntry{
				IndexName: spec.Name,
				Key:       types.IndexKey(vals),
				Offset:    offsets[i],
			})
		}
	}
	return ctx.Index.Rebuild(entries)
}

// projectRows applies the SELECT column list to the row set and returns the
// result column names and projected rows.
func projectRows(cols []ResultCol, rows []backend.Row, ctx ExecContext) ([]string, []map[string]any, error) {
	// Determine the output column names.
	var names []string
	for _, rc := range cols {
		if rc.Star {
			for _, c := range ctx.Columns {
				names = append(names, c.Name)
			}
			continue
		}
		name := colName(rc)
		names = append(names, name)
	}

	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out := make(map[string]any, len(names))
		ni := 0
		for _, rc := range cols {
			if rc.Star {
				for _, c := range ctx.Columns {
					out[c.Name] = row[c.Name]
					ni++
				}
				continue
			}
			val, err := evalExpr(rc.Expr, row)
			if err != nil {
				return nil, nil, err
			}
			out[names[ni]] = val
			ni++
		}
		result = append(result, out)
	}
	return names, result, nil
}

// colName derives a display name for a result column.
func colName(rc ResultCol) string {
	if rc.Alias != "" {
		return rc.Alias
	}
	switch e := rc.Expr.(type) {
	case *ColRef:
		return e.Name
	case *FuncExpr:
		if e.Star {
			return e.Name + "(*)"
		}
		if len(e.Args) > 0 {
			if ref, ok := e.Args[0].(*ColRef); ok {
				return e.Name + "(" + ref.Name + ")"
			}
		}
		return e.Name
	}
	return "expr"
}

// stmtHasAggregate reports whether any result column contains an aggregate
// function.
func stmtHasAggregate(stmt *SelectStmt) bool {
	for _, rc := range stmt.Columns {
		if rc.Star {
			continue
		}
		if hasAggFunc(rc.Expr) {
			return true
		}
	}
	return false
}

// hasAggFunc recursively checks whether expr contains an aggregate function.
func hasAggFunc(expr Expr) bool {
	switch e := expr.(type) {
	case *FuncExpr:
		switch e.Name {
		case "COUNT", "SUM", "AVG", "MIN", "MAX":
			return true
		}
	case *BinExpr:
		return hasAggFunc(e.Left) || hasAggFunc(e.Right)
	}
	return false
}

// execGrouped handles SELECT with GROUP BY or aggregate functions.
func execGrouped(stmt *SelectStmt, rows []backend.Row, ctx ExecContext) ([]string, []map[string]any, error) {
	// Group rows by the GROUP BY key.
	type group struct {
		key  string
		rows []backend.Row
	}
	var groups []*group
	groupIndex := map[string]int{}

	for _, row := range rows {
		key := groupKey(stmt.GroupBy, row)
		idx, ok := groupIndex[key]
		if !ok {
			idx = len(groups)
			groups = append(groups, &group{key: key})
			groupIndex[key] = idx
		}
		groups[idx].rows = append(groups[idx].rows, row)
	}
	if len(groups) == 0 && len(stmt.GroupBy) == 0 {
		// Aggregate over all rows as a single group.
		groups = []*group{{rows: rows}}
	}

	// Determine output column names.
	var names []string
	for _, rc := range stmt.Columns {
		names = append(names, colName(rc))
	}

	var result []map[string]any
	for _, g := range groups {
		out := make(map[string]any, len(names))
		for i, rc := range stmt.Columns {
			if rc.Star {
				continue
			}
			val, err := evalAggregate(rc.Expr, g.rows)
			if err != nil {
				return nil, nil, err
			}
			out[names[i]] = val
		}
		result = append(result, out)
	}
	if len(stmt.OrderBy) > 0 {
		sortRows(result, stmt.OrderBy)
	}
	result = applyLimitOffset(result, stmt.Limit, stmt.Offset)
	return names, result, nil
}

// groupKey builds a string key from the GROUP BY column values of row.
func groupKey(groupBy []string, row backend.Row) string {
	vals := make([]any, len(groupBy))
	for i, col := range groupBy {
		vals[i] = row[col]
	}
	return types.IndexKey(vals)
}

// evalAggregate evaluates an expression that may be an aggregate function
// over a group of rows.
func evalAggregate(expr Expr, rows []backend.Row) (any, error) {
	fn, ok := expr.(*FuncExpr)
	if !ok {
		// Non-aggregate: take value from first row.
		if len(rows) == 0 {
			return nil, nil
		}
		return evalExpr(expr, rows[0])
	}
	switch fn.Name {
	case "COUNT":
		return int64(len(rows)), nil
	case "SUM":
		return aggSum(fn, rows)
	case "AVG":
		return aggAvg(fn, rows)
	case "MIN":
		return aggMin(fn, rows)
	case "MAX":
		return aggMax(fn, rows)
	default:
		return nil, fmt.Errorf("unknown aggregate function %q", fn.Name)
	}
}

// aggSum computes the SUM of the first argument expression over rows.
func aggSum(fn *FuncExpr, rows []backend.Row) (any, error) {
	if len(fn.Args) == 0 {
		return nil, fmt.Errorf("SUM requires one argument")
	}
	var sum float64
	for _, row := range rows {
		v, err := evalExpr(fn.Args[0], row)
		if err != nil {
			return nil, err
		}
		sum += toFloat(v)
	}
	return sum, nil
}

// aggAvg computes the AVG of the first argument expression over rows.
func aggAvg(fn *FuncExpr, rows []backend.Row) (any, error) {
	if len(fn.Args) == 0 || len(rows) == 0 {
		return nil, nil
	}
	s, err := aggSum(fn, rows)
	if err != nil {
		return nil, err
	}
	return s.(float64) / float64(len(rows)), nil
}

// aggMin finds the minimum value of the first argument over rows.
func aggMin(fn *FuncExpr, rows []backend.Row) (any, error) {
	if len(fn.Args) == 0 || len(rows) == 0 {
		return nil, nil
	}
	min, err := evalExpr(fn.Args[0], rows[0])
	if err != nil {
		return nil, err
	}
	for _, row := range rows[1:] {
		v, err := evalExpr(fn.Args[0], row)
		if err != nil {
			return nil, err
		}
		if types.Compare(v, min) < 0 {
			min = v
		}
	}
	return min, nil
}

// aggMax finds the maximum value of the first argument over rows.
func aggMax(fn *FuncExpr, rows []backend.Row) (any, error) {
	if len(fn.Args) == 0 || len(rows) == 0 {
		return nil, nil
	}
	max, err := evalExpr(fn.Args[0], rows[0])
	if err != nil {
		return nil, err
	}
	for _, row := range rows[1:] {
		v, err := evalExpr(fn.Args[0], row)
		if err != nil {
			return nil, err
		}
		if types.Compare(v, max) > 0 {
			max = v
		}
	}
	return max, nil
}

// arithmeticOp applies op to left and right after widening both to float64.
// When both inputs are int64 and the result is a whole number, int64 is returned
// so values stay compatible with integer column types.
func arithmeticOp(left, right any, op func(float64, float64) float64) (any, error) {
	a := toFloat(left)
	b := toFloat(right)
	result := op(a, b)
	_, li := left.(int64)
	_, ri := right.(int64)
	if li && ri && result == float64(int64(result)) {
		return int64(result), nil
	}
	return result, nil
}

// toFloat converts a numeric any value to float64; non-numeric values return 0.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	}
	return 0
}

// sortRows sorts rows in-place according to the ORDER BY clauses.
func sortRows(rows []map[string]any, orderBy []OrderByClause) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, ob := range orderBy {
			vi, _ := evalExpr(ob.Expr, rows[i])
			vj, _ := evalExpr(ob.Expr, rows[j])
			cmp := types.Compare(vi, vj)
			if cmp == 0 {
				continue
			}
			if ob.Desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
}

// applyLimitOffset slices rows to honour LIMIT and OFFSET clauses.
func applyLimitOffset(rows []map[string]any, limit, offset *int64) []map[string]any {
	start := 0
	if offset != nil && *offset > 0 {
		start = int(*offset)
		if start > len(rows) {
			return nil
		}
	}
	rows = rows[start:]
	if limit != nil && int(*limit) < len(rows) {
		rows = rows[:int(*limit)]
	}
	return rows
}

// deduplicate removes rows with identical projected values when SELECT DISTINCT
// is used.
func deduplicate(rows []backend.Row, stmt *SelectStmt) []backend.Row {
	seen := map[string]struct{}{}
	var out []backend.Row
	for _, row := range rows {
		vals := make([]any, 0, len(stmt.Columns))
		for _, rc := range stmt.Columns {
			if rc.Star {
				for _, v := range row {
					vals = append(vals, v)
				}
			} else {
				v, _ := evalExpr(rc.Expr, row)
				vals = append(vals, v)
			}
		}
		key := types.IndexKey(vals)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			out = append(out, row)
		}
	}
	return out
}
