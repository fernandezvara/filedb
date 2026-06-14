// Package sqlex provides a self-contained SQL parser and executor for the
// filedb SQL subset. It supports SELECT, INSERT, UPDATE, and DELETE statements
// against single tables with WHERE, ORDER BY, GROUP BY, LIMIT, and OFFSET
// clauses, plus aggregate functions COUNT, SUM, AVG, MIN, and MAX.
package sqlex

// Statement is the top-level interface for a parsed SQL statement.
type Statement interface {
	stmtNode()
	// TableName returns the name of the table targeted by the statement.
	TableName() string
}

// SelectStmt represents a parsed SELECT statement.
type SelectStmt struct {
	Distinct bool
	Columns  []ResultCol
	From     string
	Where    Expr
	GroupBy  []string
	OrderBy  []OrderByClause
	Limit    *int64
	Offset   *int64
}

func (*SelectStmt) stmtNode()         {}
func (s *SelectStmt) TableName() string { return s.From }

// InsertStmt represents a parsed INSERT INTO statement.
type InsertStmt struct {
	Table   string
	Columns []string    // explicit column list; empty means schema order
	Values  [][]Expr    // one inner slice per row; multi-row inserts supported
}

func (*InsertStmt) stmtNode()          {}
func (s *InsertStmt) TableName() string { return s.Table }

// UpdateStmt represents a parsed UPDATE statement.
type UpdateStmt struct {
	Table string
	Sets  []SetClause
	Where Expr
}

func (*UpdateStmt) stmtNode()          {}
func (s *UpdateStmt) TableName() string { return s.Table }

// DeleteStmt represents a parsed DELETE FROM statement.
type DeleteStmt struct {
	Table string
	Where Expr
}

func (*DeleteStmt) stmtNode()          {}
func (s *DeleteStmt) TableName() string { return s.Table }

// SetClause is a single col = expr assignment inside an UPDATE SET list.
type SetClause struct {
	Column string
	Value  Expr
}

// OrderByClause specifies one column in an ORDER BY clause.
type OrderByClause struct {
	Expr Expr
	Desc bool
}

// ResultCol is one element of the SELECT column list.
type ResultCol struct {
	Star  bool   // SELECT *
	Expr  Expr   // column reference or function call
	Alias string // AS alias, may be empty
}

// Expr is the interface implemented by all expression nodes.
type Expr interface {
	exprNode()
}

// ColRef is a column name reference, optionally qualified as table.column.
type ColRef struct {
	Name string
}

func (*ColRef) exprNode() {}

// Lit is a literal value: string, int64, float64, bool, or nil (NULL).
type Lit struct {
	Val any
}

func (*Lit) exprNode() {}

// BinExpr is a binary expression: left Op right.
// Op is one of: =, !=, <>, <, <=, >, >=, AND, OR.
type BinExpr struct {
	Left  Expr
	Op    string
	Right Expr
}

func (*BinExpr) exprNode() {}

// UnaryExpr is NOT expr.
type UnaryExpr struct {
	Op   string // "NOT"
	Expr Expr
}

func (*UnaryExpr) exprNode() {}

// LikeExpr is: expr LIKE pattern [/ NOT LIKE].
type LikeExpr struct {
	Expr    Expr
	Pattern Expr
	Not     bool
}

func (*LikeExpr) exprNode() {}

// InExpr is: expr IN (list) [/ NOT IN].
type InExpr struct {
	Expr Expr
	List []Expr
	Not  bool
}

func (*InExpr) exprNode() {}

// IsNullExpr is: expr IS [NOT] NULL.
type IsNullExpr struct {
	Expr Expr
	Not  bool // IS NOT NULL
}

func (*IsNullExpr) exprNode() {}

// FuncExpr is a function call: name(args) or COUNT(*).
type FuncExpr struct {
	Name string // upper-cased: COUNT, SUM, AVG, MIN, MAX
	Args []Expr
	Star bool // true for COUNT(*)
}

func (*FuncExpr) exprNode() {}
