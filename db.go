package filedb

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fernandezvara/filedb/internal/backend"
	"github.com/fernandezvara/filedb/internal/index"
	"github.com/fernandezvara/filedb/internal/registry"
	"github.com/fernandezvara/filedb/internal/sqlex"
	"github.com/fernandezvara/filedb/internal/types"
)

// DB is the main entry point for filedb. It manages a set of registered tables
// and dispatches SQL queries to the appropriate table backend. A DB must be
// created with Open or New and closed with Close when no longer needed.
//
// DB is safe for concurrent use; individual table operations hold per-table
// read or write locks.
type DB struct {
	reg    *registry.Registry
	logger *slog.Logger
	opts   options
}

// Open reads a YAML configuration file from path, applies the given options,
// and returns a ready DB with all configured tables registered.
//
// Relative table file paths in the configuration are resolved against the
// directory that contains the config file, not the process working directory.
// This lets operators keep the config and data files together regardless of
// where the binary is invoked from.
func Open(path string, opts ...Option) (*DB, error) {
	cfg, err := loadConfig(path)
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Dir(path)
	for i := range cfg.Tables {
		if !filepath.IsAbs(cfg.Tables[i].File) {
			cfg.Tables[i].File = filepath.Join(baseDir, cfg.Tables[i].File)
		}
	}
	return New(cfg, opts...)
}

// New creates a DB from the provided programmatic Config and options.
func New(cfg Config, opts ...Option) (*DB, error) {
	o := applyOptions(opts)
	db := &DB{
		reg:    registry.New(),
		logger: o.logger,
		opts:   o,
	}
	for _, td := range cfg.Tables {
		if err := db.AddTable(td); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

// AddTable validates def and registers a new table backed by the file
// described in def. If the backing file does not exist it is created. If a
// table with the same name is already registered ErrTableExists is returned.
func (db *DB) AddTable(def TableDef) error {
	if def.Name == "" {
		return fmt.Errorf("%w: table name is required", ErrInvalidSchema)
	}
	if def.File == "" {
		return fmt.Errorf("%w: file path is required for table %q", ErrInvalidSchema, def.Name)
	}
	if def.Format != CSV && def.Format != JSON {
		return fmt.Errorf("%w: format must be csv or json for table %q", ErrInvalidSchema, def.Name)
	}

	cols, idxSpecs, err := buildColumnSpecs(def)
	if err != nil {
		return err
	}

	useCache := !db.opts.disableCache && !def.DisableCache
	sidecar := sidecarPath(def.File)
	idxMgr := index.New(sidecar, useCache)
	for _, is := range idxSpecs {
		idxMgr.Define(is.Name, is.Columns, is.Unique)
	}

	var b backend.Backend
	switch def.Format {
	case CSV:
		b, err = backend.NewCSV(def.File, cols, true)
	case JSON:
		b, err = backend.NewJSON(def.File, cols, true)
	}
	if err != nil {
		return fmt.Errorf("filedb: opening table %q: %w", def.Name, err)
	}

	if err := db.ensureIndex(idxMgr, b, cols, idxSpecs); err != nil {
		b.Close()
		return fmt.Errorf("filedb: building index for table %q: %w", def.Name, err)
	}

	entry := &registry.Entry{
		Columns:  cols,
		Indexes:  idxSpecs,
		Backend:  b,
		IdxMgr:   idxMgr,
		FilePath: def.File,
		Format:   string(def.Format),
	}
	if err := db.reg.Add(def.Name, entry); err != nil {
		b.Close()
		return ErrTableExists
	}
	db.log("table registered", "table", def.Name, "file", def.File)
	return nil
}

// RemoveTable unregisters the named table. When deleteFile is true the backing
// data file and its index sidecar are also removed from disk.
func (db *DB) RemoveTable(name string, deleteFile bool) error {
	entry, err := db.reg.Remove(name)
	if err != nil {
		return ErrTableNotFound
	}
	entry.Lock()
	defer entry.Unlock()
	if err := entry.Backend.Close(); err != nil {
		db.log("error closing backend", "table", name, "error", err)
	}
	if deleteFile {
		os.Remove(entry.FilePath)
		os.Remove(entry.IdxMgr.SidecarPath())
	}
	db.log("table removed", "table", name, "deleteFile", deleteFile)
	return nil
}

// ReloadTable forces the in-memory index for name to be rebuilt from the
// backing data file. Use this after the backing file has been modified by an
// external process.
func (db *DB) ReloadTable(name string) error {
	entry, err := db.reg.Get(name)
	if err != nil {
		return ErrTableNotFound
	}
	entry.Lock()
	defer entry.Unlock()
	return db.ensureIndex(entry.IdxMgr, entry.Backend, entry.Columns, entry.Indexes)
}

// Tables returns the names of all currently registered tables.
func (db *DB) Tables() []string {
	return db.reg.List()
}

// Query executes a SELECT statement and returns a Rows cursor. The caller must
// call rows.Close() when done. For non-SELECT statements use Exec.
func (db *DB) Query(sql string) (*Rows, error) {
	stmt, err := sqlex.Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSQL, err)
	}
	sel, ok := stmt.(*sqlex.SelectStmt)
	if !ok {
		return nil, fmt.Errorf("%w: Query only accepts SELECT statements; use Exec for writes", ErrInvalidSQL)
	}

	entry, err := db.reg.Get(sel.TableName())
	if err != nil {
		return nil, ErrTableNotFound
	}
	entry.RLock()
	defer entry.RUnlock()

	ctx := sqlex.ExecContext{
		Columns: entry.Columns,
		Indexes: entry.Indexes,
		Backend: entry.Backend,
		Index:   entry.IdxMgr,
	}
	cols, data, err := sqlex.ExecSelect(sel, ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSQL, err)
	}
	return newRows(cols, data), nil
}

// Exec executes an INSERT, UPDATE, or DELETE statement and returns a Result
// with the number of affected rows. For SELECT statements use Query.
func (db *DB) Exec(sql string) (Result, error) {
	stmt, err := sqlex.Parse(sql)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidSQL, err)
	}

	entry, err := db.reg.Get(stmt.TableName())
	if err != nil {
		return Result{}, ErrTableNotFound
	}
	entry.Lock()
	defer entry.Unlock()

	ctx := sqlex.ExecContext{
		Columns: entry.Columns,
		Indexes: entry.Indexes,
		Backend: entry.Backend,
		Index:   entry.IdxMgr,
	}

	var affected int64
	switch s := stmt.(type) {
	case *sqlex.InsertStmt:
		affected, err = sqlex.ExecInsert(s, ctx)
	case *sqlex.UpdateStmt:
		affected, err = sqlex.ExecUpdate(s, ctx)
	case *sqlex.DeleteStmt:
		affected, err = sqlex.ExecDelete(s, ctx)
	default:
		return Result{}, fmt.Errorf("%w: use Query for SELECT statements", ErrInvalidSQL)
	}
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidSQL, err)
	}
	db.log("exec complete", "sql", sql, "affected", affected)
	return Result{RowsAffected: affected}, nil
}

// Close closes all table backends and releases resources. The DB must not be
// used after Close returns.
func (db *DB) Close() error {
	return db.reg.Close()
}

// --- internal helpers --------------------------------------------------------

// buildColumnSpecs converts a public TableDef into the internal column and
// index specification slices. It injects a hidden _id column when no column
// carries the PrimaryKey flag.
func buildColumnSpecs(def TableDef) ([]types.ColumnSpec, []types.IndexSpec, error) {
	hasPK := false
	for _, c := range def.Columns {
		if c.PrimaryKey {
			hasPK = true
			break
		}
	}

	var cols []types.ColumnSpec
	if !hasPK {
		cols = append(cols, types.ColumnSpec{
			Name:       types.AutoIDColumn,
			Type:       types.TypeXID,
			Nullable:   false,
			PrimaryKey: true,
		})
	}
	for _, c := range def.Columns {
		cols = append(cols, types.ColumnSpec{
			Name:       c.Name,
			Type:       string(c.Type),
			Nullable:   c.Nullable,
			Default:    c.Default,
			PrimaryKey: c.PrimaryKey,
		})
	}

	// Validate column names are unique.
	seen := map[string]struct{}{}
	for _, c := range cols {
		if _, dup := seen[c.Name]; dup {
			return nil, nil, fmt.Errorf("%w: duplicate column name %q in table %q",
				ErrInvalidSchema, c.Name, def.Name)
		}
		seen[c.Name] = struct{}{}
	}

	// Build index specs. Add implicit primary-key index.
	var idxSpecs []types.IndexSpec
	for _, c := range cols {
		if c.PrimaryKey {
			idxSpecs = append(idxSpecs, types.IndexSpec{
				Name:    "_pk",
				Columns: []string{c.Name},
				Unique:  true,
			})
			break
		}
	}
	for _, ix := range def.Indexes {
		if ix.Name == "" {
			return nil, nil, fmt.Errorf("%w: index name is required in table %q",
				ErrInvalidSchema, def.Name)
		}
		if len(ix.Columns) == 0 {
			return nil, nil, fmt.Errorf("%w: index %q in table %q must cover at least one column",
				ErrInvalidSchema, ix.Name, def.Name)
		}
		idxSpecs = append(idxSpecs, types.IndexSpec{
			Name:    ix.Name,
			Columns: ix.Columns,
			Unique:  ix.Unique,
		})
	}
	return cols, idxSpecs, nil
}

// ensureIndex loads the sidecar index for idxMgr. When the sidecar is absent
// or the cache is disabled it performs a full rebuild from b.
func (db *DB) ensureIndex(idxMgr *index.Manager, b backend.Backend, cols []types.ColumnSpec, idxSpecs []types.IndexSpec) error {
	if idxMgr.CacheEnabled() && !idxMgr.IsLoaded() {
		if err := idxMgr.Load(); err == nil {
			return nil
		}
		// Sidecar missing or corrupt; fall through to rebuild.
	}
	rows, offsets, err := b.ReadAll()
	if err != nil {
		return err
	}
	entries := buildIndexEntries(rows, offsets, idxSpecs)
	if err := idxMgr.Rebuild(entries); err != nil {
		return err
	}
	return idxMgr.Save()
}

// buildIndexEntries converts rows+offsets into the flat IndexEntry list used
// by index.Manager.Rebuild.
func buildIndexEntries(rows []backend.Row, offsets []int64, idxSpecs []types.IndexSpec) []index.IndexEntry {
	entries := make([]index.IndexEntry, 0, len(rows)*len(idxSpecs))
	for i, row := range rows {
		if i >= len(offsets) {
			break
		}
		for _, spec := range idxSpecs {
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
	return entries
}

// sidecarPath returns the path of the index sidecar file for a given data file.
func sidecarPath(dataFile string) string {
	ext := filepath.Ext(dataFile)
	base := strings.TrimSuffix(dataFile, ext)
	return base + ".idx"
}

// log emits a structured log message when a logger has been configured.
func (db *DB) log(msg string, args ...any) {
	if db.logger != nil {
		db.logger.Info(msg, args...)
	}
}
