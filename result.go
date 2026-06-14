package filedb

// Result carries metadata about a completed write operation (INSERT, UPDATE,
// or DELETE).
type Result struct {
	// RowsAffected is the number of rows inserted, modified, or removed by
	// the statement.
	RowsAffected int64
}
