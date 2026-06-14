package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fernandezvara/filedb"
	"github.com/fernandezvara/filedb/cmd/filedb/format"
)

// runREPL starts an interactive SQL prompt that reads statements from stdin,
// executes them against db, and writes output in fmt_ to stdout. It exits
// cleanly when the input stream is closed or when the user types "exit" or
// "quit".
func runREPL(db *filedb.DB, fmt_ format.Format) {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Fprintln(os.Stdout, "filedb interactive shell. Type 'exit' or 'quit' to leave.")
	fmt.Fprint(os.Stdout, "> ")

	var buf strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Handle exit commands.
		if lower := strings.ToLower(line); lower == "exit" || lower == "quit" {
			fmt.Fprintln(os.Stdout, "bye")
			return
		}

		// Accumulate lines until a semicolon terminates the statement.
		if buf.Len() > 0 {
			buf.WriteString(" ")
		}
		buf.WriteString(line)

		// A statement is complete when it ends with ';' (after trimming) or
		// is a non-empty single-line input without continuation.
		stmt := buf.String()
		if !strings.HasSuffix(stmt, ";") && !isSingleLineStatement(stmt) {
			fmt.Fprint(os.Stdout, "  ")
			continue
		}

		if err := runQuery(db, stmt, fmt_, os.Stdout); err != nil {
			fmt.Fprintf(os.Stdout, "error: %v\n", err)
		}
		buf.Reset()
		fmt.Fprint(os.Stdout, "> ")
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "filedb: read error: %v\n", err)
	}
}

// isSingleLineStatement reports whether stmt appears to be a complete SQL
// statement on a single line, i.e. it contains a SQL verb and does not look
// like the beginning of a multi-line query.
func isSingleLineStatement(stmt string) bool {
	upper := strings.ToUpper(strings.TrimSpace(stmt))
	for _, verb := range []string{"SELECT ", "INSERT ", "UPDATE ", "DELETE "} {
		if strings.HasPrefix(upper, verb) {
			return true
		}
	}
	return false
}
