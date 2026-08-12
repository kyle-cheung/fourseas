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
	t.base_amount,
	t.base_currency,
	t.category,
	t.pending,
	t.name,
	t.merchant_name,
	t.fx_rate,
	t.fx_date,
	t.pending_transaction_id,
	t.superseded_by,
	t.provider,
	t.external_id,
	t.item_id,
	t.account_id,
	t.synced_at
FROM transactions t
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
		SELECT `+transactionColumns+`, account, institution_name
		FROM `+transactionsView+`
		ORDER BY date DESC, external_id DESC
		LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", transactionsView, err)
	}
	defer rows.Close()

	var out []model.TransactionView
	for rows.Next() {
		var (
			view            model.TransactionView
			label, instName sql.NullString
			row             = &viewScanner{rows: rows, extra: []any{&label, &instName}}
		)
		t, err := scanTransaction(row)
		if err != nil {
			return nil, fmt.Errorf("scan view row: %w", err)
		}
		view.Transaction = t
		view.AccountLabel = text(label)
		view.InstitutionName = text(instName)
		out = append(out, view)
	}
	return out, rows.Err()
}

// viewScanner lets scanTransaction read the transaction columns while the
// view's extra display columns are scanned beside them.
type viewScanner struct {
	rows  *sql.Rows
	extra []any
}

func (v *viewScanner) Scan(dest ...any) error {
	return v.rows.Scan(append(dest, v.extra...)...)
}
