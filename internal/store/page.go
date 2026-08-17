package store

import (
	"context"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// Page is one page of provider changes: the rows it holds and the durable
// cursor the next sync starts from. It is a store type, not a provider type, so
// the store still depends on nothing above it.
type Page struct {
	Provider   string
	ItemID     string
	Added      []model.Transaction
	Modified   []model.Transaction
	RemovedIDs []string
	// Accounts is the account list the same response carried, with balances.
	Accounts []model.Account
	// Cursor stays at the start of a page sequence until its final page lands.
	Cursor string
}

// ApplyPage writes one page and its cursor in one database transaction.
//
// Rows and cursor move together. A failure part way through leaves both as they
// were.
func (s *Store) ApplyPage(ctx context.Context, page Page) error {
	return s.inTx(ctx, func(dbtx execer) error {
		if err := upsertTransactions(ctx, dbtx, page.Added); err != nil {
			return err
		}
		if err := upsertTransactions(ctx, dbtx, page.Modified); err != nil {
			return err
		}
		if err := removeTransactions(ctx, dbtx, page.Provider, page.RemovedIDs); err != nil {
			return err
		}
		// The provider sends its whole account list with every page, so the
		// balances are already here and cost no extra call.
		if err := upsertAccounts(ctx, dbtx, page.Accounts); err != nil {
			return err
		}
		// The rows this page carried can complete a pending and posted pair, so
		// the duplicate is resolved before the cursor moves past it.
		if err := resolveSupersedes(ctx, dbtx, page.Provider); err != nil {
			return err
		}
		return saveCursor(ctx, dbtx, page.Provider, page.ItemID, page.Cursor)
	})
}
