package store

import (
	"context"
	"fmt"
)

// Removed counts the rows that belong to one linked item.
type Removed struct {
	Transactions int64
	Accounts     int64
	SyncState    int64
	Institutions int64
}

// itemTable is one table keyed by item, with an optional count for its delete.
type itemTable struct {
	table string
	into  *int64
}

// itemTables names every table keyed by item, in the order rows are deleted:
// the rows that refer to an item first, the item itself last.
func itemTables(counts *Removed) []itemTable {
	return []itemTable{
		{"transactions", &counts.Transactions},
		{"account_liabilities", nil},
		{"accounts", &counts.Accounts},
		{"sync_state", &counts.SyncState},
		{"institutions", &counts.Institutions},
	}
}

// ItemCounts reports how much data one item holds, so the user can see what an
// unlink would delete before it happens. Nothing is changed.
func (s *Store) ItemCounts(ctx context.Context, provider, itemID string) (Removed, error) {
	var counts Removed
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM transactions WHERE provider = ? AND item_id = ?),
			(SELECT count(*) FROM accounts     WHERE provider = ? AND item_id = ?),
			(SELECT count(*) FROM sync_state   WHERE provider = ? AND item_id = ?),
			(SELECT count(*) FROM institutions WHERE provider = ? AND item_id = ?)`,
		provider, itemID, provider, itemID, provider, itemID, provider, itemID,
	).Scan(&counts.Transactions, &counts.Accounts, &counts.SyncState, &counts.Institutions)
	if err != nil {
		return Removed{}, fmt.Errorf("count the rows of %s/%s: %w", provider, itemID, err)
	}
	return counts, nil
}

// Unlink deletes every stored row of one item and returns what was deleted.
//
// The deletes share one transaction, so a failure part way through leaves the
// whole item stored and the command can be run again.
//
// The transactions go too. A new link gives fresh provider ids to the same
// charges, so rows that were kept would hold every charge twice.
func (s *Store) Unlink(ctx context.Context, provider, itemID string) (Removed, error) {
	var removed Removed
	err := s.inTx(ctx, func(dbtx execer) error {
		var err error
		removed, err = deleteItemRows(ctx, dbtx, provider, itemID)
		return err
	})
	if err != nil {
		return Removed{}, err
	}
	return removed, nil
}

// deleteItemRows deletes the rows of one item through the caller's handle, so
// that every table is emptied in the caller's transaction.
func deleteItemRows(ctx context.Context, db execer, provider, itemID string) (Removed, error) {
	var removed Removed
	for _, t := range itemTables(&removed) {
		n, err := deleteFromTable(ctx, db, t.table, provider, itemID)
		if err != nil {
			return Removed{}, err
		}
		if t.into != nil {
			*t.into = n
		}
	}
	return removed, nil
}

// deleteFromTable deletes one item's rows from one table. The table name is
// from itemTables, never from user input.
func deleteFromTable(ctx context.Context, db execer, table, provider, itemID string) (int64, error) {
	result, err := db.ExecContext(ctx,
		`DELETE FROM `+table+` WHERE provider = ? AND item_id = ?`, provider, itemID)
	if err != nil {
		return 0, fmt.Errorf("delete the %s rows of %s/%s: %w", table, provider, itemID, err)
	}

	// A driver that cannot report the count is not a reason to fail a delete
	// that worked, so an unknown count is reported as zero.
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return rows, nil
}
