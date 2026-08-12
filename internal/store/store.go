// Package store keeps transactions in a local DuckDB file.
//
// It must not import any provider SDK.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

const schema = `
CREATE TABLE IF NOT EXISTS transactions (
	provider      VARCHAR NOT NULL,
	external_id   VARCHAR NOT NULL,
	item_id       VARCHAR,
	account_id    VARCHAR,
	account_name  VARCHAR,
	institution   VARCHAR,
	date          DATE    NOT NULL,
	name          VARCHAR,
	merchant_name VARCHAR,
	amount        DOUBLE,
	currency      VARCHAR,
	pending       BOOLEAN,
	category      VARCHAR,
	PRIMARY KEY (provider, external_id)
);

CREATE TABLE IF NOT EXISTS sync_state (
	provider VARCHAR NOT NULL,
	item_id  VARCHAR NOT NULL,
	cursor   VARCHAR NOT NULL,
	PRIMARY KEY (provider, item_id)
);
`

// Store is a handle on the DuckDB file.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path and applies the schema.
// Use ":memory:" for a database that is not written to disk.
func Open(path string) (*Store, error) {
	if path != ":memory:" {
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
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

const upsertSQL = `
INSERT INTO transactions (
	provider, external_id, item_id, account_id, account_name, institution,
	date, name, merchant_name, amount, currency, pending, category
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, external_id) DO UPDATE SET
	item_id       = excluded.item_id,
	account_id    = excluded.account_id,
	account_name  = excluded.account_name,
	institution   = excluded.institution,
	date          = excluded.date,
	name          = excluded.name,
	merchant_name = excluded.merchant_name,
	amount        = excluded.amount,
	currency      = excluded.currency,
	pending       = excluded.pending,
	category      = excluded.category
`

// Upsert writes rows, replacing any row with the same provider and external id.
// Running a sync more than one time therefore does not create duplicates.
func (s *Store) Upsert(ctx context.Context, txs []model.Transaction) error {
	if len(txs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, upsertSQL)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, t := range txs {
		_, err := stmt.ExecContext(ctx,
			t.Provider, t.ExternalID, t.ItemID, t.AccountID, t.AccountName, t.Institution,
			t.Date, t.Name, t.MerchantName, t.Amount, t.Currency, t.Pending, t.Category,
		)
		if err != nil {
			return fmt.Errorf("upsert %s/%s: %w", t.Provider, t.ExternalID, err)
		}
	}
	return tx.Commit()
}

// Remove deletes rows the provider says no longer exist.
func (s *Store) Remove(ctx context.Context, provider string, externalIDs []string) error {
	for _, id := range externalIDs {
		_, err := s.db.ExecContext(ctx,
			`DELETE FROM transactions WHERE provider = ? AND external_id = ?`, provider, id)
		if err != nil {
			return fmt.Errorf("remove %s/%s: %w", provider, id, err)
		}
	}
	return nil
}

// Cursor returns the saved sync cursor, or an empty string on the first run.
func (s *Store) Cursor(ctx context.Context, provider, itemID string) (string, error) {
	var cursor string
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor FROM sync_state WHERE provider = ? AND item_id = ?`, provider, itemID).Scan(&cursor)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read cursor for %s/%s: %w", provider, itemID, err)
	}
	return cursor, nil
}

// SetCursor saves the sync cursor for the next run.
func (s *Store) SetCursor(ctx context.Context, provider, itemID, cursor string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_state (provider, item_id, cursor) VALUES (?, ?, ?)
		ON CONFLICT (provider, item_id) DO UPDATE SET cursor = excluded.cursor`,
		provider, itemID, cursor)
	if err != nil {
		return fmt.Errorf("save cursor for %s/%s: %w", provider, itemID, err)
	}
	return nil
}

// Count returns the number of stored transactions.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM transactions`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count transactions: %w", err)
	}
	return n, nil
}

// Newest returns the n most recent transactions, newest first.
func (s *Store) Newest(ctx context.Context, n int) ([]model.Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, external_id, item_id, account_id, account_name, institution,
		       date, name, merchant_name, amount, currency, pending, category
		FROM transactions
		ORDER BY date DESC, external_id DESC
		LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("query newest: %w", err)
	}
	defer rows.Close()

	var out []model.Transaction
	for rows.Next() {
		var t model.Transaction
		err := rows.Scan(
			&t.Provider, &t.ExternalID, &t.ItemID, &t.AccountID, &t.AccountName, &t.Institution,
			&t.Date, &t.Name, &t.MerchantName, &t.Amount, &t.Currency, &t.Pending, &t.Category,
		)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
