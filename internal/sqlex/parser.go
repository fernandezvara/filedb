package sqlex

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse tokenises and parses sql, returning the top-level Statement AST node
// or an error wrapping the parse failure.
func Parse(sql string) (Statement, error) {
	l := newLexer(sql)
	tokens, err := l.tokenize()
	if err != nil {
		return nil, fmt.Errorf("lex: %w", err)
	}
	p := &parser{tokens: tokens}
	stmt, err := p.parseStatement()
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return stmt, nil
}

// parser holds the token stream and a current position pointer.
type parser struct {
	tokens []token
	pos    int
}

// peek returns the current token without consuming it.
func (p *parser) peek() token { return p.tokens[p.pos] }

// peekType returns the type of the current token.
func (p *parser) peekType() tokType { return p.tokens[p.pos].typ }

// consume advances and returns the previous token.
func (p *parser) consume() token {
	t := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

// expect consumes a token of the expected type or returns an error.
func (p *parser) expect(tt tokType) (token, error) {
	t := p.consume()
	if t.typ != tt {
		return t, fmt.Errorf("expected token type %d, got %q (%d)", tt, t.val, t.typ)
	}
	return t, nil
}

// expectIdent consumes an identifier token or returns an error.
func (p *parser) expectIdent() (string, error) {
	t := p.consume()
	if t.typ != tokIdent {
		return "", fmt.Errorf("expected identifier, got %q", t.val)
	}
	return t.val, nil
}

// match consumes the current token and returns true if its type matches tt.
func (p *parser) match(tt tokType) bool {
	if p.peekType() == tt {
		p.consume()
		return true
	}
	return false
}

// parseStatement dispatches to the correct statement parser.
func (p *parser) parseStatement() (Statement, error) {
	switch p.peekType() {
	case tokSelect:
		return p.parseSelect()
	case tokInsert:
		return p.parseInsert()
	case tokUpdate:
		return p.parseUpdate()
	case tokDelete:
		return p.parseDelete()
	default:
		return nil, fmt.Errorf("expected SELECT, INSERT, UPDATE or DELETE, got %q", p.peek().val)
	}
}

// parseSelect parses a SELECT statement.
func (p *parser) parseSelect() (*SelectStmt, error) {
	p.consume() // SELECT
	stmt := &SelectStmt{}
	stmt.Distinct = p.match(tokDistinct)

	cols, err := p.parseResultCols()
	if err != nil {
		return nil, err
	}
	stmt.Columns = cols

	if _, err := p.expect(tokFrom); err != nil {
		return nil, err
	}
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt.From = name

	if p.peekType() == tokWhere {
		p.consume()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = expr
	}
	if p.peekType() == tokGroup {
		p.consume() // GROUP
		if _, err := p.expect(tokBy); err != nil {
			return nil, err
		}
		cols, err := p.parseIdentList()
		if err != nil {
			return nil, err
		}
		stmt.GroupBy = cols
	}
	if p.peekType() == tokOrder {
		p.consume() // ORDER
		if _, err := p.expect(tokBy); err != nil {
			return nil, err
		}
		ob, err := p.parseOrderBy()
		if err != nil {
			return nil, err
		}
		stmt.OrderBy = ob
	}
	if p.peekType() == tokLimit {
		p.consume()
		n, err := p.parseInt64()
		if err != nil {
			return nil, fmt.Errorf("LIMIT: %w", err)
		}
		stmt.Limit = &n
	}
	if p.peekType() == tokOffset {
		p.consume()
		n, err := p.parseInt64()
		if err != nil {
			return nil, fmt.Errorf("OFFSET: %w", err)
		}
		stmt.Offset = &n
	}
	p.match(tokSemicolon)
	return stmt, nil
}

// parseResultCols parses the SELECT column list: * or expr [AS alias], ...
func (p *parser) parseResultCols() ([]ResultCol, error) {
	var cols []ResultCol
	for {
		if p.peekType() == tokStar {
			p.consume()
			cols = append(cols, ResultCol{Star: true})
		} else {
			expr, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			rc := ResultCol{Expr: expr}
			if p.peekType() == tokAs {
				p.consume()
				alias, err := p.expectIdent()
				if err != nil {
					return nil, err
				}
				rc.Alias = alias
			}
			cols = append(cols, rc)
		}
		if !p.match(tokComma) {
			break
		}
	}
	return cols, nil
}

// parseInsert parses an INSERT INTO statement.
func (p *parser) parseInsert() (*InsertStmt, error) {
	p.consume() // INSERT
	if _, err := p.expect(tokInto); err != nil {
		return nil, err
	}
	table, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt := &InsertStmt{Table: table}

	// Optional explicit column list.
	if p.peekType() == tokLParen {
		p.consume()
		cols, err := p.parseIdentList()
		if err != nil {
			return nil, err
		}
		stmt.Columns = cols
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(tokValues); err != nil {
		return nil, err
	}
	// One or more value rows.
	for {
		if _, err := p.expect(tokLParen); err != nil {
			return nil, err
		}
		row, err := p.parseExprList()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		stmt.Values = append(stmt.Values, row)
		if !p.match(tokComma) {
			break
		}
	}
	p.match(tokSemicolon)
	return stmt, nil
}

// parseUpdate parses an UPDATE statement.
func (p *parser) parseUpdate() (*UpdateStmt, error) {
	p.consume() // UPDATE
	table, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt := &UpdateStmt{Table: table}
	if _, err := p.expect(tokSet); err != nil {
		return nil, err
	}
	for {
		col, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokEq); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Sets = append(stmt.Sets, SetClause{Column: col, Value: val})
		if !p.match(tokComma) {
			break
		}
	}
	if p.peekType() == tokWhere {
		p.consume()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = expr
	}
	p.match(tokSemicolon)
	return stmt, nil
}

// parseDelete parses a DELETE FROM statement.
func (p *parser) parseDelete() (*DeleteStmt, error) {
	p.consume() // DELETE
	if _, err := p.expect(tokFrom); err != nil {
		return nil, err
	}
	table, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt := &DeleteStmt{Table: table}
	if p.peekType() == tokWhere {
		p.consume()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = expr
	}
	p.match(tokSemicolon)
	return stmt, nil
}

// parseExpr parses a boolean expression (top level: OR).
func (p *parser) parseExpr() (Expr, error) { return p.parseOr() }

// parseOr handles: andExpr (OR andExpr)*
func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peekType() == tokOr {
		p.consume()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinExpr{Left: left, Op: "OR", Right: right}
	}
	return left, nil
}

// parseAnd handles: notExpr (AND notExpr)*
func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peekType() == tokAnd {
		p.consume()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinExpr{Left: left, Op: "AND", Right: right}
	}
	return left, nil
}

// parseNot handles: NOT notExpr | compareExpr
func (p *parser) parseNot() (Expr, error) {
	if p.peekType() == tokNot {
		p.consume()
		expr, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "NOT", Expr: expr}, nil
	}
	return p.parseCompare()
}

// parseAdd handles: mulExpr ((+ | -) mulExpr)*
func (p *parser) parseAdd() (Expr, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.peekType() == tokPlus || p.peekType() == tokMinus {
		op := p.consume().val
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &BinExpr{Left: left, Op: op, Right: right}
	}
	return left, nil
}

// parseMul handles: primary ((* | /) primary)*
func (p *parser) parseMul() (Expr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.peekType() == tokStar || p.peekType() == tokSlash {
		op := p.consume().val
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &BinExpr{Left: left, Op: op, Right: right}
	}
	return left, nil
}

// parseCompare handles: addExpr [op addExpr | LIKE str | IN (...) | IS [NOT] NULL]
func (p *parser) parseCompare() (Expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}

	switch p.peekType() {
	case tokEq, tokNeq, tokLt, tokLte, tokGt, tokGte:
		op := p.consume().val
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &BinExpr{Left: left, Op: strings.ToUpper(op), Right: right}, nil

	case tokLike:
		p.consume()
		pattern, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &LikeExpr{Expr: left, Pattern: pattern}, nil

	case tokNot:
		// NOT LIKE or NOT IN
		p.consume()
		switch p.peekType() {
		case tokLike:
			p.consume()
			pattern, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			return &LikeExpr{Expr: left, Pattern: pattern, Not: true}, nil
		case tokIn:
			p.consume()
			list, err := p.parseInList()
			if err != nil {
				return nil, err
			}
			return &InExpr{Expr: left, List: list, Not: true}, nil
		default:
			return nil, fmt.Errorf("expected LIKE or IN after NOT")
		}

	case tokIn:
		p.consume()
		list, err := p.parseInList()
		if err != nil {
			return nil, err
		}
		return &InExpr{Expr: left, List: list}, nil

	case tokIs:
		p.consume()
		not := p.match(tokNot)
		if _, err := p.expect(tokNull); err != nil {
			return nil, fmt.Errorf("expected NULL after IS [NOT]")
		}
		return &IsNullExpr{Expr: left, Not: not}, nil
	}
	return left, nil
}

// parsePrimary parses a literal, identifier, function call, or parenthesised
// expression.
func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.typ {
	case tokString:
		p.consume()
		return &Lit{Val: t.val}, nil
	case tokInt:
		p.consume()
		n, _ := strconv.ParseInt(t.val, 10, 64)
		return &Lit{Val: n}, nil
	case tokFloat:
		p.consume()
		f, _ := strconv.ParseFloat(t.val, 64)
		return &Lit{Val: f}, nil
	case tokTrue:
		p.consume()
		return &Lit{Val: true}, nil
	case tokFalse:
		p.consume()
		return &Lit{Val: false}, nil
	case tokNull:
		p.consume()
		return &Lit{Val: nil}, nil
	case tokLParen:
		p.consume()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return expr, nil
	case tokCount, tokSum, tokAvg, tokMin, tokMax:
		return p.parseFunc()
	case tokIdent:
		return p.parseIdentOrFunc()
	default:
		return nil, fmt.Errorf("unexpected token %q in expression", t.val)
	}
}

// parseIdentOrFunc reads an identifier and checks whether it is followed by
// a '(' to distinguish a column reference from a function call.
func (p *parser) parseIdentOrFunc() (Expr, error) {
	name := p.consume().val
	if p.peekType() != tokLParen {
		return &ColRef{Name: name}, nil
	}
	// Function call with an arbitrary name.
	p.consume() // (
	if p.peekType() == tokRParen {
		p.consume()
		return &FuncExpr{Name: strings.ToUpper(name)}, nil
	}
	if p.peekType() == tokStar {
		p.consume()
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return &FuncExpr{Name: strings.ToUpper(name), Star: true}, nil
	}
	args, err := p.parseExprList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokRParen); err != nil {
		return nil, err
	}
	return &FuncExpr{Name: strings.ToUpper(name), Args: args}, nil
}

// parseFunc parses a built-in aggregate function call (COUNT, SUM, AVG, MIN, MAX).
func (p *parser) parseFunc() (Expr, error) {
	name := strings.ToUpper(p.consume().val)
	if _, err := p.expect(tokLParen); err != nil {
		return nil, err
	}
	if name == "COUNT" && p.peekType() == tokStar {
		p.consume()
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return &FuncExpr{Name: name, Star: true}, nil
	}
	args, err := p.parseExprList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokRParen); err != nil {
		return nil, err
	}
	return &FuncExpr{Name: name, Args: args}, nil
}

// parseExprList parses a comma-separated list of expressions.
func (p *parser) parseExprList() ([]Expr, error) {
	var exprs []Expr
	for {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
		if !p.match(tokComma) {
			break
		}
	}
	return exprs, nil
}

// parseIdentList parses a comma-separated list of identifiers.
func (p *parser) parseIdentList() ([]string, error) {
	var names []string
	for {
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		names = append(names, name)
		if !p.match(tokComma) {
			break
		}
	}
	return names, nil
}

// parseOrderBy parses: expr [ASC|DESC] (, expr [ASC|DESC])*
func (p *parser) parseOrderBy() ([]OrderByClause, error) {
	var clauses []OrderByClause
	for {
		expr, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		desc := false
		if p.peekType() == tokDesc {
			p.consume()
			desc = true
		} else {
			p.match(tokAsc)
		}
		clauses = append(clauses, OrderByClause{Expr: expr, Desc: desc})
		if !p.match(tokComma) {
			break
		}
	}
	return clauses, nil
}

// parseInList parses the parenthesised value list for an IN expression.
func (p *parser) parseInList() ([]Expr, error) {
	if _, err := p.expect(tokLParen); err != nil {
		return nil, err
	}
	list, err := p.parseExprList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokRParen); err != nil {
		return nil, err
	}
	return list, nil
}

// parseInt64 reads an integer or negative-integer literal token.
func (p *parser) parseInt64() (int64, error) {
	t := p.consume()
	if t.typ != tokInt {
		return 0, fmt.Errorf("expected integer, got %q", t.val)
	}
	return strconv.ParseInt(t.val, 10, 64)
}
