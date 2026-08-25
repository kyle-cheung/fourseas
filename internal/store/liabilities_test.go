package store

import (
	"context"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

func TestReplaceLiabilitiesReplacesTheWholeItemAndClearsNulls(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	kept, removed, other := sampleAccount(), sampleAccount(), sampleAccount()
	kept.AccountID = "account-kept"
	removed.AccountID = "account-removed"
	other.AccountID, other.ItemID = "account-other", "item-2"
	if err := s.UpsertAccounts(ctx, []model.Account{kept, removed, other}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}

	oldFetchedAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	insertRawLiability(t, s, "plaid", "item-1", kept.AccountID, oldFetchedAt)
	insertRawLiability(t, s, "plaid", "item-1", removed.AccountID, oldFetchedAt)
	insertRawLiability(t, s, "plaid", "item-2", other.AccountID, oldFetchedAt)

	newFetchedAt := time.Date(2026, 8, 21, 11, 30, 0, 0, time.UTC)
	rows := []model.CreditLiability{{
		Provider:  "row-provider-must-not-win",
		ItemID:    "row-item-must-not-win",
		AccountID: kept.AccountID,
		FetchedAt: newFetchedAt,
	}}
	if err := s.ReplaceLiabilities(ctx, "plaid", "item-1", rows); err != nil {
		t.Fatalf("replace liabilities: %v", err)
	}

	if got := countRows(t, s, "account_liabilities", "item-1"); got != 1 {
		t.Errorf("item-1 liabilities = %d, want 1", got)
	}
	if got := countRows(t, s, "account_liabilities", "item-2"); got != 1 {
		t.Errorf("item-2 liabilities = %d, want 1", got)
	}

	views, err := s.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	if len(views) != 3 {
		t.Fatalf("account views = %d, want all 3 accounts", len(views))
	}
	byID := accountViewsByID(views)
	got := byID[kept.AccountID].Liability
	if got == nil {
		t.Fatal("kept account liability is nil, want the new snapshot")
	}
	if got.Provider != "plaid" || got.ItemID != "item-1" || got.AccountID != kept.AccountID {
		t.Errorf("liability identity = %s/%s/%s, want plaid/item-1/%s",
			got.Provider, got.ItemID, got.AccountID, kept.AccountID)
	}
	if got.PaymentDueDate != nil || got.LastPaymentDate != nil || got.LastPaymentAmount.Valid {
		t.Errorf("nullable payment fields = %+v, want all fields null", got)
	}
	if !got.FetchedAt.Equal(newFetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, newFetchedAt)
	}
	removedView, ok := byID[removed.AccountID]
	if !ok {
		t.Fatalf("account %s is missing after its liability was removed", removed.AccountID)
	}
	if removedView.Liability != nil {
		t.Errorf("removed account liability = %+v, want nil", removedView.Liability)
	}
}

func TestReplaceLiabilitiesRollsBackWhenALaterInsertFails(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	oldAccount := sampleAccount()
	oldAccount.AccountID = "account-old"
	newAccount := sampleAccount()
	newAccount.AccountID = "account-new"
	if err := s.UpsertAccounts(ctx, []model.Account{oldAccount, newAccount}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}

	oldFetchedAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	insertRawLiability(t, s, "plaid", "item-1", oldAccount.AccountID, oldFetchedAt)

	newFetchedAt := time.Date(2026, 8, 21, 11, 30, 0, 0, time.UTC)
	amount := decimal.RequireFromString("25.5000")
	duplicate := model.CreditLiability{
		AccountID:         "account-new",
		LastPaymentAmount: decimal.NullDecimal{Decimal: amount, Valid: true},
		FetchedAt:         newFetchedAt,
	}
	err := s.ReplaceLiabilities(ctx, "plaid", "item-1", []model.CreditLiability{duplicate, duplicate})
	if err == nil {
		t.Fatal("replace liabilities error = nil, want the duplicate insert to fail")
	}

	if got := countRows(t, s, "account_liabilities", "item-1"); got != 1 {
		t.Errorf("item-1 liabilities = %d after rollback, want 1", got)
	}
	views, err := s.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	got := accountViewsByID(views)[oldAccount.AccountID].Liability
	if got == nil {
		t.Fatal("old account liability is nil after rollback, want the old snapshot")
	}
	if got.AccountID != oldAccount.AccountID || !got.FetchedAt.Equal(oldFetchedAt) {
		t.Errorf("old liability = %+v, want account-old fetched at %v", got, oldFetchedAt)
	}
	wantDueDate := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	wantPaymentDate := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	if got.PaymentDueDate == nil || !got.PaymentDueDate.Equal(wantDueDate) {
		t.Errorf("PaymentDueDate = %v, want %v", got.PaymentDueDate, wantDueDate)
	}
	if got.LastPaymentDate == nil || !got.LastPaymentDate.Equal(wantPaymentDate) {
		t.Errorf("LastPaymentDate = %v, want %v", got.LastPaymentDate, wantPaymentDate)
	}
	wantAmount := decimal.RequireFromString("50.2500")
	if !got.LastPaymentAmount.Valid || !got.LastPaymentAmount.Decimal.Equal(wantAmount) {
		t.Errorf("LastPaymentAmount = %+v, want 50.2500", got.LastPaymentAmount)
	}
}

func TestReplaceLiabilitiesRejectsAccountFromAnotherItemAndRollsBack(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	old, replacement, other := sampleAccount(), sampleAccount(), sampleAccount()
	old.AccountID = "account-old"
	replacement.AccountID = "account-replacement"
	other.AccountID, other.ItemID = "account-other", "item-2"
	if err := s.UpsertAccounts(ctx, []model.Account{old, replacement, other}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}

	oldFetchedAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	insertRawLiability(t, s, "plaid", "item-1", old.AccountID, oldFetchedAt)

	newFetchedAt := time.Date(2026, 8, 21, 11, 30, 0, 0, time.UTC)
	err := s.ReplaceLiabilities(ctx, "plaid", "item-1", []model.CreditLiability{
		{AccountID: replacement.AccountID, FetchedAt: newFetchedAt},
		{AccountID: other.AccountID, FetchedAt: newFetchedAt},
	})
	if err == nil {
		t.Fatal("replace liabilities error = nil, want the cross-item account to fail")
	}

	if got := countRows(t, s, "account_liabilities", "item-1"); got != 1 {
		t.Errorf("item-1 liabilities = %d after rollback, want 1", got)
	}
	views, err := s.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	byID := accountViewsByID(views)
	if got := byID[old.AccountID].Liability; got == nil || !got.FetchedAt.Equal(oldFetchedAt) {
		t.Errorf("old account liability = %+v, want the old snapshot", got)
	}
	if got := byID[replacement.AccountID].Liability; got != nil {
		t.Errorf("replacement account liability = %+v after rollback, want nil", got)
	}
	if got := byID[other.AccountID].Liability; got != nil {
		t.Errorf("other item liability = %+v after rollback, want nil", got)
	}
}

func TestAccountViewsIgnoreLiabilityFromAnotherItem(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	account := sampleAccount()
	account.ItemID = "item-2"
	if err := s.UpsertAccounts(ctx, []model.Account{account}); err != nil {
		t.Fatalf("upsert account: %v", err)
	}
	insertRawLiability(t, s, "plaid", "item-1", account.AccountID,
		time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC))

	views, err := s.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("account views = %d, want 1", len(views))
	}
	if views[0].Liability != nil {
		t.Errorf("cross-item liability = %+v, want nil", views[0].Liability)
	}
}

func insertRawLiability(t *testing.T, s *Store, provider, itemID, accountID string, fetchedAt time.Time) {
	t.Helper()
	_, err := s.db.ExecContext(context.Background(), `
		INSERT INTO account_liabilities (
			provider, item_id, account_id,
			payment_due_date, last_payment_date, last_payment_amount, fetched_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		provider, itemID, accountID,
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
		decimal.RequireFromString("50.2500").String(), fetchedAt)
	if err != nil {
		t.Fatalf("insert raw liability %s/%s: %v", itemID, accountID, err)
	}
}

func accountViewsByID(views []model.AccountView) map[string]model.AccountView {
	byID := make(map[string]model.AccountView, len(views))
	for _, view := range views {
		byID[view.AccountID] = view
	}
	return byID
}
