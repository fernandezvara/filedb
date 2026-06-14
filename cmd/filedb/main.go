// Command filedb is a command-line interface to a filedb datastore. It
// supports two modes of operation:
//
//	filedb repl   – interactive SQL shell
//	filedb query  – execute a single SQL statement and exit
//
// Configuration is supplied via the --config flag or the FILEDB_CONFIG
// environment variable. The output format is controlled by --format.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/fernandezvara/filedb"
	"github.com/fernandezvara/filedb/cmd/filedb/format"
)

// Version is set by build flags (ldflags) and defaults to "dev"
var Version = "dev"

func main() {
	fs := flag.NewFlagSet("filedb", flag.ExitOnError)

	configPath := fs.String("config", "", "path to YAML config file (overrides FILEDB_CONFIG)")
	fmtStr := fs.String("format", "table", "output format: table, json, csv")
	fs.Usage = usage

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	args := fs.Args()

	// Handle version command early since it doesn't need a config
	if len(args) > 0 && args[0] == "version" {
		fmt.Println(Version)
		return
	}

	// Resolve config path: flag > env var.
	cfg := *configPath
	if cfg == "" {
		cfg = os.Getenv("FILEDB_CONFIG")
	}
	if cfg == "" {
		fmt.Fprintln(os.Stderr, "filedb: no config file specified (use --config or FILEDB_CONFIG)")
		os.Exit(1)
	}

	outFmt := format.Format(*fmtStr)
	switch outFmt {
	case format.Table, format.JSON, format.CSV:
	default:
		fmt.Fprintf(os.Stderr, "filedb: unknown format %q; use table, json, or csv\n", *fmtStr)
		os.Exit(1)
	}

	db, err := filedb.Open(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "filedb: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if len(args) == 0 {
		// Default to REPL when no subcommand is given.
		runREPL(db, outFmt)
		return
	}

	switch args[0] {
	case "repl":
		runREPL(db, outFmt)

	case "query":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "filedb: query subcommand requires a SQL string argument")
			os.Exit(1)
		}
		sql := args[1]
		if err := runQuery(db, sql, outFmt, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "filedb: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "filedb: unknown subcommand %q\n", args[0])
		usage()
		os.Exit(1)
	}
}

// usage prints the command-line help text.
func usage() {
	fmt.Fprintln(os.Stderr, `Usage: filedb [flags] <subcommand> [args]

Flags:
  --config   path to YAML configuration file (or set FILEDB_CONFIG)
  --format   output format: table (default), json, csv

Subcommands:
  version           print the version number
  repl              start an interactive SQL shell
  query <sql>       execute a single SQL statement and print the result

Examples:
  filedb version
  filedb --config db.yaml repl
  filedb --config db.yaml --format json query "SELECT * FROM users LIMIT 5"
  FILEDB_CONFIG=db.yaml filedb query "DELETE FROM logs WHERE level = 'debug'"`)
}
