package sqlex

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// tokType identifies the category of a scanned token.
type tokType int

const (
	tokEOF tokType = iota
	tokError

	// Literals
	tokIdent  // unquoted or back-tick-quoted identifier
	tokString // single-quoted string
	tokInt    // integer literal
	tokFloat  // floating-point literal

	// Punctuation
	tokStar      // *
	tokComma     // ,
	tokDot       // .
	tokLParen    // (
	tokRParen    // )
	tokSemicolon // ;

	// Comparison operators
	tokEq  // =
	tokNeq // != or <>
	tokLt  // <
	tokLte // <=
	tokGt  // >
	tokGte // >=

	// Keywords – values must stay above all punctuation tokens.
	tokSelect
	tokDistinct
	tokFrom
	tokWhere
	tokAnd
	tokOr
	tokNot
	tokIn
	tokLike
	tokIs
	tokNull
	tokTrue
	tokFalse
	tokAs
	tokOrder
	tokBy
	tokAsc
	tokDesc
	tokLimit
	tokOffset
	tokGroup
	tokInsert
	tokInto
	tokValues
	tokUpdate
	tokSet
	tokDelete
	tokCount
	tokSum
	tokAvg
	tokMin
	tokMax

	// Arithmetic operators
	tokPlus  // +
	tokMinus // - binary minus; unary minus on digit sequences is handled in lexNumber
	tokSlash // /
	// tokStar already covers *
)

// keywords maps upper-cased SQL keyword strings to their token types.
var keywords = map[string]tokType{
	"SELECT":   tokSelect,
	"DISTINCT": tokDistinct,
	"FROM":     tokFrom,
	"WHERE":    tokWhere,
	"AND":      tokAnd,
	"OR":       tokOr,
	"NOT":      tokNot,
	"IN":       tokIn,
	"LIKE":     tokLike,
	"IS":       tokIs,
	"NULL":     tokNull,
	"TRUE":     tokTrue,
	"FALSE":    tokFalse,
	"AS":       tokAs,
	"ORDER":    tokOrder,
	"BY":       tokBy,
	"ASC":      tokAsc,
	"DESC":     tokDesc,
	"LIMIT":    tokLimit,
	"OFFSET":   tokOffset,
	"GROUP":    tokGroup,
	"INSERT":   tokInsert,
	"INTO":     tokInto,
	"VALUES":   tokValues,
	"UPDATE":   tokUpdate,
	"SET":      tokSet,
	"DELETE":   tokDelete,
	"COUNT":    tokCount,
	"SUM":      tokSum,
	"AVG":      tokAvg,
	"MIN":      tokMin,
	"MAX":      tokMax,
}

// token is a single scanned unit from the SQL input.
type token struct {
	typ tokType
	val string // raw text value, used for identifiers and literals
}

// lexer tokenises a SQL string.
type lexer struct {
	input  []rune
	pos    int
	tokens []token
	err    error
}

// newLexer creates a lexer for the given SQL input string.
func newLexer(input string) *lexer {
	return &lexer{input: []rune(input)}
}

// tokenize scans the entire input and returns the token stream or an error.
func (l *lexer) tokenize() ([]token, error) {
	for l.pos < len(l.input) {
		l.skipWhitespace()
		if l.pos >= len(l.input) {
			break
		}
		if err := l.nextToken(); err != nil {
			return nil, err
		}
	}
	l.tokens = append(l.tokens, token{typ: tokEOF})
	return l.tokens, nil
}

// skipWhitespace advances past spaces, tabs, and newlines.
func (l *lexer) skipWhitespace() {
	for l.pos < len(l.input) && unicode.IsSpace(l.input[l.pos]) {
		l.pos++
	}
}

// peek returns the current rune without advancing.
func (l *lexer) peek() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	return l.input[l.pos]
}

// peekAt returns the rune at pos+offset without advancing.
func (l *lexer) peekAt(offset int) rune {
	if l.pos+offset >= len(l.input) {
		return 0
	}
	return l.input[l.pos+offset]
}

// consume advances pos and returns the current rune.
func (l *lexer) consume() rune {
	r := l.input[l.pos]
	l.pos++
	return r
}

// emit appends a token of the given type with the given string value.
func (l *lexer) emit(typ tokType, val string) {
	l.tokens = append(l.tokens, token{typ: typ, val: val})
}

// nextToken reads the next token from the current position.
func (l *lexer) nextToken() error {
	ch := l.peek()
	switch {
	case ch == '*':
		l.consume()
		l.emit(tokStar, "*")
	case ch == ',':
		l.consume()
		l.emit(tokComma, ",")
	case ch == '.':
		l.consume()
		l.emit(tokDot, ".")
	case ch == '(':
		l.consume()
		l.emit(tokLParen, "(")
	case ch == ')':
		l.consume()
		l.emit(tokRParen, ")")
	case ch == ';':
		l.consume()
		l.emit(tokSemicolon, ";")
	case ch == '=':
		l.consume()
		l.emit(tokEq, "=")
	case ch == '!' && l.peekAt(1) == '=':
		l.consume(); l.consume()
		l.emit(tokNeq, "!=")
	case ch == '<' && l.peekAt(1) == '>':
		l.consume(); l.consume()
		l.emit(tokNeq, "<>")
	case ch == '<' && l.peekAt(1) == '=':
		l.consume(); l.consume()
		l.emit(tokLte, "<=")
	case ch == '<':
		l.consume()
		l.emit(tokLt, "<")
	case ch == '>' && l.peekAt(1) == '=':
		l.consume(); l.consume()
		l.emit(tokGte, ">=")
	case ch == '>':
		l.consume()
		l.emit(tokGt, ">")
	case ch == '\'':
		return l.lexString()
	case ch == '`':
		return l.lexBacktickIdent()
	case ch == '+':
		l.consume()
		l.emit(tokPlus, "+")
	case ch == '/':
		l.consume()
		l.emit(tokSlash, "/")
	case unicode.IsDigit(ch) || (ch == '-' && unicode.IsDigit(l.peekAt(1))):
		return l.lexNumber()
	case ch == '-':
		// Reached only when '-' is not immediately followed by a digit.
		l.consume()
		l.emit(tokMinus, "-")
	case unicode.IsLetter(ch) || ch == '_':
		l.lexWord()
	default:
		return fmt.Errorf("unexpected character %q at position %d", string(ch), l.pos)
	}
	return nil
}

// lexString reads a single-quoted string literal, handling '' as an escaped
// single quote.
func (l *lexer) lexString() error {
	l.consume() // opening '
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.consume()
		if ch == '\'' {
			if l.peek() == '\'' {
				// Escaped single quote inside string.
				l.consume()
				sb.WriteRune('\'')
				continue
			}
			l.emit(tokString, sb.String())
			return nil
		}
		sb.WriteRune(ch)
	}
	return fmt.Errorf("unterminated string literal")
}

// lexBacktickIdent reads a back-tick-quoted identifier.
func (l *lexer) lexBacktickIdent() error {
	l.consume() // opening `
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.consume()
		if ch == '`' {
			l.emit(tokIdent, sb.String())
			return nil
		}
		sb.WriteRune(ch)
	}
	return fmt.Errorf("unterminated back-tick identifier")
}

// lexNumber reads an integer or floating-point literal, including an optional
// leading minus sign.
func (l *lexer) lexNumber() error {
	var sb strings.Builder
	if l.peek() == '-' {
		sb.WriteRune(l.consume())
	}
	for l.pos < len(l.input) && unicode.IsDigit(l.peek()) {
		sb.WriteRune(l.consume())
	}
	isFloat := false
	if l.peek() == '.' && unicode.IsDigit(l.peekAt(1)) {
		isFloat = true
		sb.WriteRune(l.consume()) // '.'
		for l.pos < len(l.input) && unicode.IsDigit(l.peek()) {
			sb.WriteRune(l.consume())
		}
	}
	raw := sb.String()
	if isFloat {
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return fmt.Errorf("invalid float %q", raw)
		}
		l.emit(tokFloat, raw)
	} else {
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			return fmt.Errorf("invalid integer %q", raw)
		}
		l.emit(tokInt, raw)
	}
	return nil
}

// lexWord reads an unquoted identifier or keyword.
func (l *lexer) lexWord() {
	var sb strings.Builder
	for l.pos < len(l.input) && (unicode.IsLetter(l.peek()) || unicode.IsDigit(l.peek()) || l.peek() == '_') {
		sb.WriteRune(l.consume())
	}
	word := sb.String()
	upper := strings.ToUpper(word)
	if tt, ok := keywords[upper]; ok {
		l.emit(tt, word)
	} else {
		l.emit(tokIdent, word)
	}
}
