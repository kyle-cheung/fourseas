package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// transactionsView is the surface for exploration in the DuckDB command line
// tool. The raw tables stay available for anyone who wants superseded rows.
const transactionsView = "v_transactions"

// transactionViewColumns is the read order used by NewestView.
const transactionViewColumns = `
	provider, external_id, item_id, account_id,
	date, authorized_date, name, merchant_name,
	amount, currency,
	pending, pending_transaction_id, superseded_by, category, synced_at,
	base_amount, base_currency, fx_rate, account, institution_name`

// viewDDL joins transactions to their account and institution, and hides the
// pending row of a charge whose posted row has arrived. Without that, a sum
// counts one charge two times.
const viewDDL = `
CREATE OR REPLACE VIEW ` + transactionsView + ` AS
SELECT
	t.date,
	t.authorized_date,
	coalesce(a.nickname, a.name, t.account_id) AS account,
	a.nickname,
	a.name          AS account_name,
	a.mask,
	i.institution_name,
	coalesce(t.merchant_name, t.name) AS description,
	t.amount,
	t.currency,
	CAST(CASE
		WHEN t.currency = 'USD' THEN t.amount
		WHEN r.rate IS NOT NULL THEN
			CAST(t.amount AS DECIMAL(38,4)) * CAST(r.rate AS DECIMAL(38,8))
	END AS DECIMAL(18,4)) AS base_amount,
	CASE
		WHEN t.currency = 'USD' OR r.rate IS NOT NULL THEN 'USD'
	END AS base_currency,
	t.category,
	t.pending,
	t.name,
	t.merchant_name,
	CAST(CASE
		WHEN t.currency = 'USD' THEN 1
		WHEN r.rate IS NOT NULL THEN r.rate
	END AS DECIMAL(18,8)) AS fx_rate,
	t.pending_transaction_id,
	t.superseded_by,
	t.provider,
	t.external_id,
	t.item_id,
	t.account_id,
	t.synced_at
FROM transactions t
ASOF LEFT JOIN fx_rates r
	ON r.currency = t.currency
	AND r.base_currency = 'USD'
	AND r.date <= t.date
LEFT JOIN accounts a
	ON a.provider = t.provider AND a.account_id = t.account_id
LEFT JOIN institutions i
	ON i.provider = t.provider AND i.item_id = t.item_id
WHERE t.superseded_by IS NULL;
`

// NewestView returns the n most recent rows a person should see: superseded
// rows are left out, and each row carries its account and institution name.
func (s *Store) NewestView(ctx context.Context, n int) ([]model.TransactionView, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionViewColumns+`
		FROM `+transactionsView+`
		ORDER BY date DESC, external_id DESC
		LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", transactionsView, err)
	}
	defer rows.Close()

	var out []model.TransactionView
	for rows.Next() {
		view, err := scanTransactionView(rows)
		if err != nil {
			return nil, fmt.Errorf("scan view row: %w", err)
		}
		out = append(out, view)
	}
	return out, rows.Err()
}

// scanTransactionView reads one row in transactionViewColumns order.
func scanTransactionView(row scanner) (model.TransactionView, error) {
	var (
		view                                                             model.TransactionView
		itemID, accountID, name, merchantName, currency                  sql.NullString
		pendingID, supersededBy, category, baseCurrency, label, instName sql.NullString
		authorizedDate, syncedAt                                         sql.NullTime
		amount, baseAmount, fxRate                                       any
	)

	err := row.Scan(
		&view.Provider, &view.ExternalID, &itemID, &accountID,
		&view.Date, &authorizedDate, &name, &merchantName,
		&amount, &currency,
		&view.Pending, &pendingID, &supersededBy, &category, &syncedAt,
		&baseAmount, &baseCurrency, &fxRate, &label, &instName,
	)
	if err != nil {
		return model.TransactionView{}, err
	}

	if view.Amount, err = toDecimal(amount); err != nil {
		return model.TransactionView{}, fmt.Errorf("amount: %w", err)
	}
	if view.BaseAmount, err = toNullDecimal(baseAmount); err != nil {
		return model.TransactionView{}, fmt.Errorf("base_amount: %w", err)
	}
	if view.FXRate, err = toNullDecimal(fxRate); err != nil {
		return model.TransactionView{}, fmt.Errorf("fx_rate: %w", err)
	}

	view.ItemID = text(itemID)
	view.AccountID = text(accountID)
	view.AuthorizedDate = timePtr(authorizedDate)
	view.Name = text(name)
	view.MerchantName = text(merchantName)
	view.Currency = text(currency)
	view.PendingTransactionID = text(pendingID)
	view.SupersededBy = text(supersededBy)
	view.Category = text(category)
	if when := timePtr(syncedAt); when != nil {
		view.SyncedAt = *when
	}
	view.BaseCurrency = text(baseCurrency)
	view.AccountLabel = text(label)
	view.InstitutionName = text(instName)
	return view, nil
}
