package filedb

import (
	"fmt"
	"log/slog"
	"os"

	yaml "github.com/goccy/go-yaml"
)

// Config holds the complete configuration for a DB instance created with New.
type Config struct {
	// Tables is the list of table definitions to register on startup.
	Tables []TableDef `yaml:"tables"`
}

// fileConfig mirrors Config for YAML unmarshalling from disk.
type fileConfig struct {
	Tables []TableDef `yaml:"tables"`
}

// loadConfig reads and parses a YAML configuration file from path.
func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("filedb: reading config %q: %w", path, err)
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return Config{}, fmt.Errorf("filedb: parsing config %q: %w", path, err)
	}
	return Config{Tables: fc.Tables}, nil
}

// options accumulates values set by Option functions.
type options struct {
	logger       *slog.Logger
	disableCache bool
}

// Option is a functional configuration modifier for Open and New.
type Option func(*options)

// WithLogger injects a structured logger into the DB. When not provided the
// DB operates silently; all errors are still returned as Go error values.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithCacheDisabled turns off the in-memory index cache for all tables opened
// in this DB instance. Individual tables may also set DisableCache in their
// TableDef, which takes precedence over this option.
func WithCacheDisabled() Option {
	return func(o *options) { o.disableCache = true }
}

// applyOptions folds a slice of Option functions into a default options value.
func applyOptions(opts []Option) options {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
