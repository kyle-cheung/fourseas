package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// transactionColumns is the read order used by every transaction query.
const transactionColumns = `
	provider, external_id, item_id, account_id,
	date, authorized_date, name, merchant_name,
	amount, currency, base_amount, base_currency, fx_rate, fx_date,
	pending, pending_transaction_id, superseded_by, category, synced_at`

const upsertTransactionSQL = `
INSERT INTO transactions (
	provider, external_id, item_id, account_id,
	date, authorized_date, name, merchant_name,
	amount, currency, base_amount, base_currency, fx_rate, fx_date,
	pending, pending_transaction_id, superseded_by, category, synced_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, external_id) DO UPDATE SET
	item_id                = excluded.item_id,
	account_id             = excluded.account_id,
	date                   = excluded.date,
	authorized_date        = excluded.authorized_date,
	name                   = excluded.name,
	merchant_name          = excluded.merchant_name,
	amount                 = excluded.amount,
	currency               = excluded.currency,
	base_amount            = excluded.base_amount,
	base_currency          = excluded.base_currency,
	fx_rate                = excluded.fx_rate,
	fx_date                = excluded.fx_date,
	pending                = excluded.pending,
	pending_transaction_id = excluded.pending_transaction_id,
	superseded_by          = excluded.superseded_by,
	category               = excluded.category,
	synced_at              = excluded.synced_at
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

	stmt, err := tx.PrepareContext(ctx, upsertTransactionSQL)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, t := range txs {
		// A row that does not say when it was synced was synced now.
		syncedAt := t.SyncedAt
		if syncedAt.IsZero() {
			syncedAt = time.Now().UTC()
		}

		_, err := stmt.ExecContext(ctx,
			t.Provider, t.ExternalID, textArg(t.ItemID), textArg(t.AccountID),
			t.Date, timeArg(t.AuthorizedDate), textArg(t.Name), textArg(t.MerchantName),
			decimalArg(t.Amount), textArg(t.Currency),
			nullDecimalArg(t.BaseAmount), textArg(t.BaseCurrency),
			nullDecimalArg(t.FXRate), timeArg(t.FXDate),
			t.Pending, textArg(t.PendingTransactionID), textArg(t.SupersededBy),
			textArg(t.Category), syncedAt,
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

// Get returns one stored transaction.
func (s *Store) Get(ctx context.Context, provider, externalID string) (model.Transaction, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+transactionColumns+` FROM transactions WHERE provider = ? AND external_id = ?`,
		provider, externalID)

	t, err := scanTransaction(row)
	if err == sql.ErrNoRows {
		return model.Transaction{}, fmt.Errorf("no transaction %s/%s is stored", provider, externalID)
	}
	if err != nil {
		return model.Transaction{}, fmt.Errorf("read %s/%s: %w", provider, externalID, err)
	}
	return t, nil
}

// Count returns the number of stored transactions, superseded rows included.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM transactions`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count transactions: %w", err)
	}
	return n, nil
}

// Newest returns the n most recent transactions, newest first. It reads the
// table, so it includes superseded rows. Use NewestView for display.
func (s *Store) Newest(ctx context.Context, n int) ([]model.Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+`
		FROM transactions
		ORDER BY date DESC, external_id DESC
		LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("query newest: %w", err)
	}
	defer rows.Close()

	var out []model.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// scanner is what sql.Row and sql.Rows have in common.
type scanner interface {
	Scan(dest ...any) error
}

// scanTransaction reads one row in transactionColumns order.
func scanTransaction(row scanner) (model.Transaction, error) {
	var (
		t                                               model.Transaction
		itemID, accountID, name, merchantName, currency sql.NullString
		baseCurrency, pendingID, supersededBy, category sql.NullString
		authorizedDate, fxDate, syncedAt                sql.NullTime
		amount, baseAmount, fxRate                      any
	)

	err := row.Scan(
		&t.Provider, &t.ExternalID, &itemID, &accountID,
		&t.Date, &authorizedDate, &name, &merchantName,
		&amount, &currency, &baseAmount, &baseCurrency, &fxRate, &fxDate,
		&t.Pending, &pendingID, &supersededBy, &category, &syncedAt,
	)
	if err != nil {
		return model.Transaction{}, err
	}

	if t.Amount, err = toDecimal(amount); err != nil {
		return model.Transaction{}, fmt.Errorf("amount: %w", err)
	}
	if t.BaseAmount, err = toNullDecimal(baseAmount); err != nil {
		return model.Transaction{}, fmt.Errorf("base_amount: %w", err)
	}
	if t.FXRate, err = toNullDecimal(fxRate); err != nil {
		return model.Transaction{}, fmt.Errorf("fx_rate: %w", err)
	}

	t.ItemID = text(itemID)
	t.AccountID = text(accountID)
	t.AuthorizedDate = timePtr(authorizedDate)
	t.Name = text(name)
	t.MerchantName = text(merchantName)
	t.Currency = text(currency)
	t.BaseCurrency = text(baseCurrency)
	t.FXDate = timePtr(fxDate)
	t.PendingTransactionID = text(pendingID)
	t.SupersededBy = text(supersededBy)
	t.Category = text(category)
	if when := timePtr(syncedAt); when != nil {
		t.SyncedAt = *when
	}
	return t, nil
}
