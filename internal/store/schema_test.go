package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

func TestSchemaIsCreatedFromNothingAndStamped(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("version = %d, want %d", version, SchemaVersion)
	}

	for _, table := range []string{"institutions", "accounts", "transactions", "sync_state", "schema_version"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Errorf("table %s is missing: %v", table, err)
		}
	}
}

func TestReopeningTheSameFileKeepsOneVersionRow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	first := openAt(t, path)
	if err := first.Upsert(ctx, []model.Transaction{sample()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := openAt(t, path)
	var rows int
	if err := second.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_version`).Scan(&rows); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if rows != 1 {
		t.Errorf("schema_version holds %d rows, want 1", rows)
	}

	n, err := second.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("stored rows = %d, want the row written before the reopen", n)
	}
}

func TestAVersionMismatchStopsTheCommand(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	s := openAt(t, path)
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET version = 99`); err != nil {
		t.Fatalf("change version: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err := Open(path)
	if err == nil {
		t.Fatal("want an error opening a database from another schema version, got nil")
	}
	var mismatch *VersionError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %T (%v), want a *VersionError", err, err)
	}
	if mismatch.Found != 99 || mismatch.Want != SchemaVersion {
		t.Errorf("error = %+v, want found 99 and want %d", mismatch, SchemaVersion)
	}
	if !strings.Contains(err.Error(), "fourseas reset") {
		t.Errorf("message %q does not name `fourseas reset`", err.Error())
	}
}

// TestAnUnversionedDatabaseStopsTheCommand covers a database written by the
// probe, before schema_version existed.
func TestAnUnversionedDatabaseStopsTheCommand(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	raw, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE transactions (provider VARCHAR, amount DOUBLE)`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	_, err = Open(path)
	var mismatch *VersionError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want a *VersionError", err)
	}
	if mismatch.Found != 0 {
		t.Errorf("found version = %d, want 0 for a database with no schema_version table", mismatch.Found)
	}
}

func TestResetDropsEverythingAndRebuilds(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	s := openAt(t, path)
	if err := s.Upsert(ctx, []model.Transaction{sample()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.SetCursor(ctx, "plaid", "item-1", "cursor-abc"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET version = 99`); err != nil {
		t.Fatalf("change version: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reset must work on a database this build refuses to open.
	fresh, err := Reset(path)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	defer fresh.Close()

	n, err := fresh.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("stored rows = %d after a reset, want 0", n)
	}
	cursor, err := fresh.Cursor(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor != "" {
		t.Errorf("cursor = %q after a reset, want an empty string", cursor)
	}

	var version int
	if err := fresh.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("version = %d after a reset, want %d", version, SchemaVersion)
	}
}
