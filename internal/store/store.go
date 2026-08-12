// Package store keeps institutions, accounts, transactions, and sync cursors
// in a local DuckDB file.
//
// It must not import any provider SDK.
//
// One file holds each table: schema.go, accounts.go, transactions.go,
// sync_state.go, and views.go.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/duckdb/duckdb-go/v2"
)

// Store is a handle on the DuckDB file.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path and applies the schema.
// Use ":memory:" for a database that is not written to disk.
//
// Open returns a *VersionError when the file was written by another schema
// version. Nothing is changed in that case: the user runs `fourseas reset`.
func Open(path string) (*Store, error) {
	db, err := connect(path)
	if err != nil {
		return nil, err
	}
	if err := ensureSchema(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Reset drops every table and view and builds the schema again. It opens the
// file without checking the version, because a version mismatch is the main
// reason to reset.
//
// All of this data can be fetched from the provider again, so a reset costs
// one full sync and no migration framework.
func Reset(path string) (*Store, error) {
	db, err := connect(path)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := dropAll(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureSchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// connect opens the DuckDB file, creating its directory when it is missing.
func connect(path string) (*sql.DB, error) {
	if path != ":memory:" && path != "" {
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create %s: %w", dir, err)
			}
		}
	}

	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("open duckdb at %s: %w", path, err)
	}
	return db, nil
}
