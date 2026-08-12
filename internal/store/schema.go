package store

import (
	"context"
	"database/sql"
	"fmt"
)

// SchemaVersion is the version this build writes and reads. Raise it whenever
// a column changes. There is no migration framework: a mismatch stops the
// command and the user runs `fourseas reset`.
const SchemaVersion = 1

// VersionError says the file on disk belongs to another build.
type VersionError struct {
	Path  string
	Found int
	Want  int
}

func (e *VersionError) Error() string {
	found := fmt.Sprintf("version %d", e.Found)
	if e.Found == 0 {
		found = "no version"
	}
	return fmt.Sprintf("the database at %s has %s and this build needs version %d: "+
		"run `fourseas reset` to drop it and sync again", e.Path, found, e.Want)
}

// tables is every table the schema owns, in the order they are dropped.
var tables = []string{"transactions", "accounts", "institutions", "sync_state", "schema_version"}

// ddl builds the schema. Every money column is DECIMAL, never DOUBLE, because
// a float sum of money is wrong.
//
// Every column of version 1 is here, including the ones issues 4 and 6 are the
// first to fill in. Landing them together keeps later parallel work out of this
// file.
const ddl = `
CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS institutions (
	provider         VARCHAR NOT NULL,
	item_id          VARCHAR NOT NULL,
	institution_id   VARCHAR,
	institution_name VARCHAR,
	env              VARCHAR,
	linked_at        TIMESTAMP,
	PRIMARY KEY (provider, item_id)
);

CREATE TABLE IF NOT EXISTS accounts (
	provider           VARCHAR NOT NULL,
	account_id         VARCHAR NOT NULL,
	item_id            VARCHAR,
	name               VARCHAR,
	mask               VARCHAR,
	type               VARCHAR,
	subtype            VARCHAR,
	currency           VARCHAR,
	nickname           VARCHAR,
	tracked            BOOLEAN NOT NULL DEFAULT true,
	balance_current    DECIMAL(18,4),
	balance_available  DECIMAL(18,4),
	balance_limit      DECIMAL(18,4),
	balance_updated_at TIMESTAMP,
	first_seen_at      TIMESTAMP,
	last_seen_at       TIMESTAMP,
	PRIMARY KEY (provider, account_id)
);

CREATE TABLE IF NOT EXISTS transactions (
	provider               VARCHAR NOT NULL,
	external_id            VARCHAR NOT NULL,
	item_id                VARCHAR,
	account_id             VARCHAR,
	date                   DATE NOT NULL,
	authorized_date        DATE,
	name                   VARCHAR,
	merchant_name          VARCHAR,
	amount                 DECIMAL(18,4),
	currency               VARCHAR,
	base_amount            DECIMAL(18,4),
	base_currency          VARCHAR,
	fx_rate                DECIMAL(18,8),
	fx_date                DATE,
	pending                BOOLEAN,
	pending_transaction_id VARCHAR,
	superseded_by          VARCHAR,
	category               VARCHAR,
	synced_at              TIMESTAMP,
	PRIMARY KEY (provider, external_id)
);

CREATE TABLE IF NOT EXISTS sync_state (
	provider       VARCHAR NOT NULL,
	item_id        VARCHAR NOT NULL,
	cursor         VARCHAR,
	last_synced_at TIMESTAMP,
	last_status    VARCHAR,
	PRIMARY KEY (provider, item_id)
);
` + viewDDL

// ensureSchema checks the version, then creates anything that is missing.
func ensureSchema(ctx context.Context, db *sql.DB) error {
	found, stamped, err := readVersion(ctx, db)
	if err != nil {
		return err
	}

	switch {
	case stamped && found != SchemaVersion:
		return &VersionError{Path: pathOf(ctx, db), Found: found, Want: SchemaVersion}
	case !stamped:
		// A database with tables but no version was written before versioning,
		// so its columns cannot be trusted.
		used, err := hasTables(ctx, db)
		if err != nil {
			return err
		}
		if used {
			return &VersionError{Path: pathOf(ctx, db), Found: 0, Want: SchemaVersion}
		}
	}

	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if !stamped {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, SchemaVersion); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	return nil
}

// readVersion reports the stored version. stamped is false when the
// schema_version table is missing or empty.
func readVersion(ctx context.Context, db *sql.DB) (version int, stamped bool, err error) {
	var exists bool
	err = db.QueryRowContext(ctx,
		`SELECT count(*) > 0 FROM information_schema.tables WHERE table_name = 'schema_version'`).Scan(&exists)
	if err != nil {
		return 0, false, fmt.Errorf("look for schema_version: %w", err)
	}
	if !exists {
		return 0, false, nil
	}

	err = db.QueryRowContext(ctx, `SELECT version FROM schema_version LIMIT 1`).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read schema version: %w", err)
	}
	return version, true, nil
}

// hasTables reports whether the database already holds any table.
func hasTables(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'main'`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("list tables: %w", err)
	}
	return n > 0, nil
}

// dropAll removes every view and table this schema owns.
func dropAll(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `DROP VIEW IF EXISTS `+transactionsView); err != nil {
		return fmt.Errorf("drop view %s: %w", transactionsView, err)
	}
	for _, table := range tables {
		if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			return fmt.Errorf("drop table %s: %w", table, err)
		}
	}
	return nil
}

// pathOf names the open database file for an error message.
func pathOf(ctx context.Context, db *sql.DB) string {
	var name, path string
	err := db.QueryRowContext(ctx,
		`SELECT database_name, path FROM duckdb_databases() WHERE NOT internal LIMIT 1`).Scan(&name, &path)
	if err != nil || path == "" {
		return "the database"
	}
	return path
}
