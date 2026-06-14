package filedb

// DataType identifies the storage and comparison semantics of a column value.
type DataType string

const (
	// TypeString stores values as UTF-8 text.
	TypeString DataType = "string"
	// TypeInt stores values as 64-bit signed integers.
	TypeInt DataType = "int"
	// TypeFloat stores values as 64-bit IEEE 754 floating-point numbers.
	TypeFloat DataType = "float"
	// TypeBool stores values as true/false booleans.
	TypeBool DataType = "bool"
	// TypeDate stores calendar dates formatted as YYYY-MM-DD.
	TypeDate DataType = "date"
	// TypeDatetime stores timestamps formatted as RFC3339.
	TypeDatetime DataType = "datetime"
)

// Format identifies the on-disk representation of a table's backing file.
type Format string

const (
	// CSV indicates a comma-separated values file whose first row contains
	// column names.
	CSV Format = "csv"
	// JSON indicates a file containing a JSON array of objects.
	JSON Format = "json"
)

// Column describes a single field in a table schema.
type Column struct {
	// Name is the column identifier used in SQL statements and file headers.
	Name string `yaml:"name"`

	// Type is the data type of the column.
	Type DataType `yaml:"type"`

	// Nullable indicates whether the column accepts null (missing) values.
	// When false and no Default is provided, inserting or reading a null
	// value returns ErrNullNotAllowed.
	Nullable bool `yaml:"nullable"`

	// Default is the value applied when the column is absent or null in the
	// source file. It must be assignable to the column Type.
	Default any `yaml:"default,omitempty"`

	// PrimaryKey marks this column as the table's primary key. At most one
	// column may carry this flag. When no column is marked, the library
	// automatically adds a hidden _id column of type xid.
	PrimaryKey bool `yaml:"primary_key,omitempty"`
}

// Index describes a secondary index over one or more columns of a table.
type Index struct {
	// Name is a unique identifier for the index within its table.
	Name string `yaml:"name"`

	// Columns lists the column names covered by the index. Multiple columns
	// produce a composite index whose key is the concatenation of the
	// individual values.
	Columns []string `yaml:"columns"`

	// Unique enforces that no two rows may share the same index key.
	Unique bool `yaml:"unique,omitempty"`
}

// TableDef is the complete definition of a table: its name, backing file,
// format, schema, and indexes.
type TableDef struct {
	// Name is the table identifier used in SQL statements.
	Name string `yaml:"name"`

	// File is the path to the backing CSV or JSON file.
	File string `yaml:"file"`

	// Format is the file format: CSV or JSON.
	Format Format `yaml:"format"`

	// Columns is the ordered list of column definitions. The library
	// automatically prepends a hidden _id column when no column has
	// PrimaryKey set to true.
	Columns []Column `yaml:"columns"`

	// Indexes is the list of secondary indexes maintained for this table.
	Indexes []Index `yaml:"indexes,omitempty"`

	// DisableCache disables the in-memory index cache for this table.
	// When true the sidecar index file is consulted on every indexed query.
	DisableCache bool `yaml:"disable_cache,omitempty"`
}
