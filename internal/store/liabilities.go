package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

const insertLiabilitySQL = `
INSERT INTO account_liabilities (
	provider, item_id, account_id,
	payment_due_date, last_payment_date, last_payment_amount,
	last_statement_balance, last_statement_issue_date, fetched_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

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
				return fmt.Errorf("account %s does not belong to %s", row.AccountID, provider)
			}
			_, err := stmt.ExecContext(ctx,
				provider, itemID, row.AccountID,
				timeArg(row.PaymentDueDate), timeArg(row.LastPaymentDate),
				nullDecimalArg(row.LastPaymentAmount), nullDecimalArg(row.LastStatementBalance), timeArg(row.LastStatementIssueDate), row.FetchedAt)
			if err != nil {
				return fmt.Errorf("insert liability %s/%s: %w", provider, row.AccountID, err)
			}
		}
		return nil
	})
}

// remainingStatementBalance uses posted payment evidence only. The latest
// payment snapshot can confirm one row, or prove full coverage on its own;
// it is never added to the transaction total.
func remainingStatementBalance(a model.AccountView, state model.SyncState, txs []model.Transaction) decimal.NullDecimal {
	unknown := decimal.NullDecimal{}
	l := a.Liability
	if a.Type != "credit" || l == nil || l.LastStatementIssueDate == nil || l.PaymentDueDate == nil || !l.LastStatementBalance.Valid || a.Currency == "" || l.FetchedAt.IsZero() || (state.LastStatus != "" && state.LastStatus != "ok") {
		return unknown
	}
	issue := l.LastStatementIssueDate.Format("2006-01-02")
	due := l.PaymentDueDate.Format("2006-01-02")
	fetched := l.FetchedAt.UTC().Format("2006-01-02")
	paid, synced := "", ""
	if l.LastPaymentDate != nil {
		paid = l.LastPaymentDate.Format("2006-01-02")
	}
	if state.LastSyncedAt != nil {
		synced = state.LastSyncedAt.UTC().Format("2006-01-02")
	}
	if issue >= due || issue > fetched {
		return unknown
	}
	statement := l.LastStatementBalance.Decimal
	if !statement.IsPositive() {
		return decimal.NewNullDecimal(decimal.Zero)
	}
	if paid == issue && l.LastPaymentAmount.Valid && l.LastPaymentAmount.Decimal.IsPositive() {
		return unknown
	}
	latestAfterIssue := paid > issue
	if latestAfterIssue && (paid > fetched || !l.LastPaymentAmount.Valid || l.LastPaymentAmount.Decimal.IsNegative()) {
		return unknown
	}
	matchesSnapshot := func(t model.Transaction, date string) bool {
		return latestAfterIssue && date == paid && t.Amount.IsNegative() && (confirmedCardPayment(t) || ambiguousCardPayment(t)) && t.Amount.Neg().Equal(l.LastPaymentAmount.Decimal)
	}
	total := decimal.Zero
	matchedLatest := !latestAfterIssue
	snapshotMatches := 0
	needsSnapshot := false
	for _, t := range txs {
		date := t.Date.Format("2006-01-02")
		if t.Pending || t.SupersededBy != "" || date < issue {
			continue
		}
		confirmed := confirmedCardPayment(t)
		if t.Amount.IsPositive() && confirmed {
			return unknown
		}
		if !t.Amount.IsNegative() {
			continue
		}
		snapshotMatch := matchesSnapshot(t, date)
		payment := confirmed || snapshotMatch
		ambiguous := ambiguousCardPayment(t)
		if date == issue {
			if payment || ambiguous {
				return unknown
			}
			continue
		}
		if payment {
			// Do not apply newer-cycle payments to an old liability snapshot when
			// transaction sync succeeds but the liabilities refresh is not ready.
			if model.NormalizeCurrency(t.Currency) != model.NormalizeCurrency(a.Currency) || date > fetched || (synced != "" && date > synced) {
				return unknown
			}
			total = total.Sub(t.Amount)
			if snapshotMatch {
				matchedLatest = true
				snapshotMatches++
				needsSnapshot = needsSnapshot || !confirmed
			}
		} else if ambiguous {
			return unknown
		}
	}
	if snapshotMatches > 1 && needsSnapshot {
		return unknown
	}
	// A snapshot may independently prove coverage when historical metadata is
	// absent. Known reversals or ambiguous evidence above still take precedence.
	if !state.HistoryComplete && latestAfterIssue && l.LastPaymentAmount.Decimal.GreaterThanOrEqual(statement) {
		return decimal.NewNullDecimal(decimal.Zero)
	}
	// Linking requests at least 30 days of history. A completed historical
	// update covers that window even for quiet cards with no old transactions.
	if !state.HistoryComplete || state.Cursor == "" || state.LastSyncedAt == nil || a.FirstSeenAt.IsZero() || a.FirstSeenAt.AddDate(0, 0, -30).Format("2006-01-02") > issue || (latestAfterIssue && paid > synced) || !matchedLatest {
		return unknown
	}
	remaining := statement.Sub(total)
	if remaining.IsNegative() {
		remaining = decimal.Zero
	}
	return decimal.NewNullDecimal(remaining)
}

func (s *Store) statementBalance(ctx context.Context, a model.AccountView, state model.SyncState) (decimal.NullDecimal, error) {
	if a.Liability == nil || a.Liability.LastStatementIssueDate == nil {
		return decimal.NullDecimal{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE provider=? AND item_id=? AND account_id=? AND date>=?`, a.Provider, a.ItemID, a.AccountID, *a.Liability.LastStatementIssueDate)
	if err != nil {
		return decimal.NullDecimal{}, fmt.Errorf("query statement payments: %w", err)
	}
	defer rows.Close()
	var txs []model.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return decimal.NullDecimal{}, err
		}
		txs = append(txs, t)
	}
	if err := rows.Err(); err != nil {
		return decimal.NullDecimal{}, err
	}
	return remainingStatementBalance(a, state, txs), nil
}

// ambiguousCardPayment identifies credits that might be payments, without
// overriding a specific incompatible category such as a merchant refund.
func ambiguousCardPayment(t model.Transaction) bool {
	if t.DetailedCategory != "" && !strings.HasPrefix(t.DetailedCategory, "TRANSFER_") && !strings.HasPrefix(t.DetailedCategory, "LOAN_PAYMENTS_") {
		return false
	}
	return t.Category == "" || strings.HasPrefix(t.Category, "TRANSFER") || strings.HasPrefix(t.Category, "LOAN_PAYMENTS") || t.Category == "Transfer"
}

func confirmedCardPayment(t model.Transaction) bool {
	if t.DetailedCategory != "" {
		return t.DetailedCategory == "LOAN_PAYMENTS_CREDIT_CARD_PAYMENT"
	}
	return t.Category == "Payment" || t.Category == "Payment > Credit Card"
}
