package store

import (
	"context"
	"fmt"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

const insertLiabilitySQL = `
INSERT INTO account_liabilities (
	provider, item_id, account_id,
	payment_due_date, last_payment_date, last_payment_amount, fetched_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`

// ReplaceLiabilities replaces the complete liability snapshot for one item.
func (s *Store) ReplaceLiabilities(
	ctx context.Context,
	provider, itemID string,
	rows []model.CreditLiability,
) error {
	return s.inTx(ctx, func(dbtx execer) error {
		if _, err := dbtx.ExecContext(ctx,
			`DELETE FROM account_liabilities WHERE provider = ? AND item_id = ?`,
			provider, itemID); err != nil {
			return fmt.Errorf("delete liabilities of %s/%s: %w", provider, itemID, err)
		}

		stmt, err := dbtx.PrepareContext(ctx, insertLiabilitySQL)
		if err != nil {
			return fmt.Errorf("prepare liability insert: %w", err)
		}
		defer stmt.Close()
		accountExists, err := dbtx.PrepareContext(ctx, `
			SELECT count(*) > 0
			FROM accounts
			WHERE provider = ? AND item_id = ? AND account_id = ?`)
		if err != nil {
			return fmt.Errorf("prepare liability account lookup: %w", err)
		}
		defer accountExists.Close()

		for _, row := range rows {
			var exists bool
			if err := accountExists.QueryRowContext(ctx, provider, itemID, row.AccountID).Scan(&exists); err != nil {
				return fmt.Errorf("look for liability account %s/%s: %w", provider, row.AccountID, err)
			}
			if !exists {
				return fmt.Errorf("account %s does not belong to %s/%s", row.AccountID, provider, itemID)
			}
			_, err := stmt.ExecContext(ctx,
				provider, itemID, row.AccountID,
				timeArg(row.PaymentDueDate), timeArg(row.LastPaymentDate),
				nullDecimalArg(row.LastPaymentAmount), row.FetchedAt)
			if err != nil {
				return fmt.Errorf("insert liability %s/%s: %w", provider, row.AccountID, err)
			}
		}
		return nil
	})
}
