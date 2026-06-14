# filedb

filedb is a SQL-queryable, file-backed local datastore that maps CSV and JSON files to named tables. It is designed to be embedded in Go applications as a library and is also available as a standalone CLI tool.

## Features

- **SQL query support**: SELECT, INSERT, UPDATE, DELETE with WHERE, ORDER BY, GROUP BY, LIMIT, OFFSET clauses
- **Aggregate functions**: COUNT, SUM, AVG, MIN, MAX
- **File formats**: CSV and JSON backends with automatic type coercion
- **Indexing**: Secondary indexes with unique constraints for fast lookups
- **Type system**: String, Int, Float, Bool, Date, Datetime with validation and defaults
- **CLI tool**: Interactive SQL shell and single-query execution
- **Thread-safe**: Concurrent queries with per-table read/write locks
- **Atomic writes**: Data files and index sidecars use atomic write patterns

## Installation

### As a Go Library

```bash
go get github.com/fernandezvara/filedb
```

### As a CLI Tool

```bash
go install github.com/fernandezvara/filedb/cmd/filedb@latest
```

## Quick Start

Create a YAML configuration file `db.yaml`:

```yaml
tables:
  - name: users
    file: data/users.csv
    format: csv
    columns:
      - name: email
        type: string
        primary_key: true
      - name: name
        type: string
      - name: age
        type: int
        nullable: true
    indexes:
      - name: idx_age
        columns: [age]
```

Run queries using the CLI:

```bash
# Interactive REPL
filedb --config db.yaml repl

# Single query
filedb --config db.yaml query "SELECT * FROM users WHERE age > 25"
```

## Configuration

filedb uses YAML configuration files to define tables. The configuration specifies table names, file paths, formats, schemas, and indexes.

### Example Configuration

```yaml
tables:
  - name: products
    file: data/products.json
    format: json
    columns:
      - name: id
        type: string
        primary_key: true
      - name: name
        type: string
      - name: price
        type: float
      - name: in_stock
        type: bool
        default: true
      - name: created_at
        type: datetime
    indexes:
      - name: idx_price
        columns: [price]
      - name: idx_stock
        columns: [in_stock]
        unique: false
```

### Configuration Reference

- **name**: Table identifier used in SQL statements
- **file**: Path to the CSV or JSON file (relative to config file location)
- **format**: File format - `csv` or `json`
- **columns**: Ordered list of column definitions
  - **name**: Column identifier
  - **type**: Data type - `string`, `int`, `float`, `bool`, `date`, `datetime`
  - **nullable**: Whether null values are allowed (default: false)
  - **default**: Default value when column is absent or null
  - **primary_key**: Marks column as primary key (at most one per table)
- **indexes**: List of secondary indexes
  - **name**: Unique index identifier
  - **columns**: Column names covered by the index
  - **unique**: Enforce uniqueness (default: false)
- **disable_cache**: Disable in-memory index cache for this table

### Auto-Generated Primary Keys

When no column is marked as `primary_key`, filedb automatically adds a hidden `_id` column of type `xid` (globally unique identifier).

## Library Usage

### Opening a Database

```go
import "github.com/fernandezvara/filedb"

// Open from YAML configuration
db, err := filedb.Open("config.yaml")
if err != nil {
    log.Fatal(err)
}
defer db.Close()
```

### Programmatic Configuration

```go
cfg := filedb.Config{
    Tables: []filedb.TableDef{
        {
            Name:   "logs",
            File:   "logs.csv",
            Format: filedb.CSV,
            Columns: []filedb.Column{
                {Name: "timestamp", Type: filedb.TypeDatetime},
                {Name: "level", Type: filedb.TypeString},
                {Name: "message", Type: filedb.TypeString},
            },
        },
    },
}

db, err := filedb.New(cfg)
if err != nil {
    log.Fatal(err)
}
defer db.Close()
```

### Querying Data

```go
rows, err := db.Query("SELECT name, age FROM users WHERE age > ? ORDER BY age")
if err != nil {
    log.Fatal(err)
}
defer rows.Close()

for rows.Next() {
    var name string
    var age int64
    if err := rows.Scan(&name, &age); err != nil {
        log.Fatal(err)
    }
    fmt.Printf("%s: %d\n", name, age)
}
```

### Inserting Data

```go
result, err := db.Exec("INSERT INTO users (email, name, age) VALUES (?, ?, ?)", 
    "alice@example.com", "Alice", 30)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Inserted %d row(s)\n", result.RowsAffected)
```

### Updating Data

```go
result, err := db.Exec("UPDATE users SET age = age + 1 WHERE email = ?", 
    "alice@example.com")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Updated %d row(s)\n", result.RowsAffected)
```

### Deleting Data

```go
result, err := db.Exec("DELETE FROM logs WHERE level = ?", "debug")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Deleted %d row(s)\n", result.RowsAffected)
```

### Using Options

```go
import "log/slog"

logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
db, err := filedb.Open("config.yaml", 
    filedb.WithLogger(logger),
    filedb.WithCacheDisabled(),
)
```

## CLI Usage

### Flags

- `--config`: Path to YAML configuration file (or set `FILEDB_CONFIG` environment variable)
- `--format`: Output format - `table` (default), `json`, `csv`

### Subcommands

#### repl

Start an interactive SQL shell:

```bash
filedb --config db.yaml repl
```

The REPL supports multi-line statements terminated by semicolons. Type `exit` or `quit` to leave.

#### query

Execute a single SQL statement:

```bash
filedb --config db.yaml query "SELECT * FROM users LIMIT 10"
```

### Environment Variables

- `FILEDB_CONFIG`: Default configuration file path (overrides `--config` flag when flag is not provided)

### Examples

```bash
# Interactive shell with JSON output
filedb --config db.yaml --format json repl

# Query with CSV output
filedb --config db.yaml --format csv query "SELECT * FROM products"

# Using environment variable
export FILEDB_CONFIG=db.yaml
filedb query "DELETE FROM logs WHERE timestamp < '2024-01-01'"
```

## SQL Reference

### Supported Statements

#### SELECT

```sql
SELECT column1, column2 FROM table
SELECT * FROM table
SELECT DISTINCT column FROM table
SELECT COUNT(*) FROM table
SELECT column, COUNT(*) FROM table GROUP BY column
SELECT * FROM table WHERE condition ORDER BY column DESC LIMIT 10 OFFSET 5
```

#### INSERT

```sql
INSERT INTO table (col1, col2) VALUES (val1, val2)
INSERT INTO table VALUES (val1, val2, val3)
INSERT INTO table (col1, col2) VALUES (val1, val2), (val3, val4)
```

#### UPDATE

```sql
UPDATE table SET col1 = val1, col2 = val2 WHERE condition
```

#### DELETE

```sql
DELETE FROM table WHERE condition
```

### WHERE Clauses

Supported operators:
- Comparison: `=`, `!=`, `<>`, `<`, `<=`, `>`, `>=`
- Logical: `AND`, `OR`, `NOT`
- Pattern matching: `LIKE`, `NOT LIKE` (supports `%` and `_` wildcards)
- Set membership: `IN`, `NOT IN`
- Null checks: `IS NULL`, `IS NOT NULL`

```sql
SELECT * FROM users WHERE age > 25 AND status = 'active'
SELECT * FROM products WHERE name LIKE '%widget%'
SELECT * FROM orders WHERE id IN (1, 2, 3)
SELECT * FROM users WHERE email IS NOT NULL
```

### Aggregate Functions

- `COUNT(*)`: Count all rows
- `COUNT(column)`: Count non-null values
- `SUM(column)`: Sum of numeric values
- `AVG(column)`: Average of numeric values
- `MIN(column)`: Minimum value
- `MAX(column)`: Maximum value

```sql
SELECT COUNT(*) FROM users
SELECT age, COUNT(*) FROM users GROUP BY age
SELECT SUM(price) FROM products WHERE category = 'electronics'
SELECT AVG(rating) FROM reviews WHERE product_id = 123
```

### ORDER BY

```sql
SELECT * FROM users ORDER BY name ASC
SELECT * FROM products ORDER BY price DESC, name ASC
```

### LIMIT and OFFSET

```sql
SELECT * FROM users LIMIT 10
SELECT * FROM users LIMIT 10 OFFSET 20
```

## Data Types

### Supported Types

- **string**: UTF-8 text
- **int**: 64-bit signed integer
- **float**: 64-bit IEEE 754 floating-point number
- **bool**: true/false
- **date**: Calendar date formatted as YYYY-MM-DD
- **datetime**: Timestamp formatted as RFC3339

### Type Behavior

- **String**: Stored as-is in files
- **Int**: Parsed from decimal strings, coerced from float
- **Float**: Parsed from decimal strings, coerced from int
- **Bool**: Accepts `true`, `false`, `1`, `0`, `yes`, `no` (case-insensitive)
- **Date**: Parsed from YYYY-MM-DD format
- **Datetime**: Parsed from RFC3339 format (e.g., `2024-01-15T10:30:00Z`)

### Null Handling

- Empty strings in CSV or missing fields in JSON are treated as null for non-string types
- Non-nullable columns without defaults return an error on null values
- Use `nullable: true` in column definition to allow null values

## Indexing

### Creating Indexes

Define indexes in the table configuration:

```yaml
indexes:
  - name: idx_email
    columns: [email]
    unique: true
  - name: idx_name_age
    columns: [name, age]
```

### Index Behavior

- Indexes are stored in sidecar files (`.idx` extension)
- Indexes are loaded into memory by default for fast lookups
- Equality checks on indexed columns use index lookups instead of full scans
- Unique indexes enforce constraints on INSERT and UPDATE
- Composite indexes support multi-column lookups

### Cache Control

Disable index caching for specific tables:

```yaml
- name: large_table
  disable_cache: true
```

Or globally:

```go
db, err := filedb.Open("config.yaml", filedb.WithCacheDisabled())
```

## Performance Considerations

### When to Use Indexes

- Add indexes on columns frequently used in WHERE equality conditions
- Use unique indexes for columns that should have unique values
- Composite indexes are useful for queries filtering on multiple columns

### CSV vs JSON

- **CSV**: Supports true append operations; only new rows are indexed on insert
- **JSON**: Requires full file rewrite on any modification; entire index is rebuilt

### Cache Behavior

- Index cache is enabled by default for fast query performance
- Cache persists across queries until a reload or explicit cache disable
- Disable cache for tables that change frequently or when memory is constrained

### Query Optimization

- Use indexed columns in WHERE clauses for equality checks
- Avoid SELECT * when only specific columns are needed
- Use LIMIT to reduce result set size
- Consider using DISTINCT instead of GROUP BY when grouping is not needed

## Limitations

- **Single-table queries**: No JOIN support
- **No transactions**: Operations are atomic per-statement but not across statements
- **No schema migrations**: Changing table definitions requires manual data migration
- **No subqueries**: Nested queries are not supported
- **No window functions**: Analytic functions like ROW_NUMBER are not available
- **No stored procedures**: Only ad-hoc SQL statements are supported
- **JSON backend performance**: Full file rewrite on every write operation
- **Case-sensitive identifiers**: Column and table names are case-sensitive (though LIKE pattern matching is case-sensitive)

## Error Handling

filedb returns sentinel errors for common failure scenarios:

- `ErrTableExists`: Table already registered
- `ErrTableNotFound`: Referenced table does not exist
- `ErrColumnNotFound`: Referenced column does not exist
- `ErrTypeMismatch`: Value cannot be coerced to expected type
- `ErrNullNotAllowed`: Null value for non-nullable column without default
- `ErrUniqueViolation`: Duplicate key in unique index
- `ErrInvalidSchema`: Table definition validation failed
- `ErrInvalidSQL`: SQL parsing or execution error

Use `errors.Is()` to check for specific errors:

```go
if errors.Is(err, filedb.ErrTableNotFound) {
    // Handle missing table
}
```

## License

MIT License
