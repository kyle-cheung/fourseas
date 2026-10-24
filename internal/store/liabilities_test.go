package store

import (
	"context"
	"strings"
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
	if got.PaymentDueDate != nil || got.LastPaymentDate != nil ||
		got.LastPaymentAmount.Valid || got.LastStatementBalance.Valid {
		t.Errorf("nullable liability fields = %+v, want all fields null", got)
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
	wantStatement := decimal.RequireFromString("50.2500")
	if !got.LastStatementBalance.Valid || !got.LastStatementBalance.Decimal.Equal(wantStatement) {
		t.Errorf("LastStatementBalance = %+v, want 50.2500", got.LastStatementBalance)
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
	if strings.Contains(err.Error(), "item-1") {
		t.Errorf("replace liabilities error = %q, must not contain the item ID", err)
	}
	if !strings.Contains(err.Error(), "plaid") || !strings.Contains(err.Error(), other.AccountID) {
		t.Errorf("replace liabilities error = %q, want the provider and account ID", err)
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
			payment_due_date, last_payment_date, last_payment_amount,
			last_statement_balance, fetched_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		provider, itemID, accountID,
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
		decimal.RequireFromString("50.2500").String(),
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

func TestRemainingStatementBalance(t *testing.T) {
	issue, due, paid := day("2026-10-01"), day("2026-11-03"), day("2026-10-10")
	fetched := day("2026-10-15")
	account := model.AccountView{Account: model.Account{Provider: "plaid", ItemID: "item-1", AccountID: "acct", Type: "credit", Currency: "USD", FirstSeenAt: day("2026-10-01")}, Liability: &model.CreditLiability{LastStatementIssueDate: &issue, PaymentDueDate: &due, LastStatementBalance: nullDec("100"), FetchedAt: fetched}}
	state := model.SyncState{Provider: "plaid", ItemID: "item-1", Cursor: "cursor", LastStatus: "ok", LastSyncedAt: &fetched, HistoryComplete: true}
	payment := func(id, date, value string) model.Transaction {
		return model.Transaction{ExternalID: id, Date: day(date), Amount: dec(value), Currency: "USD", DetailedCategory: "LOAN_PAYMENTS_CREDIT_CARD_PAYMENT"}
	}
	tests := []struct {
		name   string
		txs    []model.Transaction
		change func(*model.AccountView, *model.SyncState)
		want   string
	}{
		{name: "legacy payment reversal", txs: []model.Transaction{{Date: paid, Amount: dec("-100"), Currency: "USD", Category: "Payment"}, {Date: paid, Amount: dec("100"), Currency: "USD", Category: "Payment"}}, want: "?"},
		{name: "legacy detailed payment reversal", txs: []model.Transaction{{Date: paid, Amount: dec("-100"), Currency: "USD", Category: "Payment > Credit Card"}, {Date: paid, Amount: dec("100"), Currency: "USD", Category: "Payment > Credit Card"}}, want: "?"},
		{name: "stale snapshot rollover", txs: []model.Transaction{payment("a", "2026-10-20", "-100"), payment("b", "2026-11-05", "-100")}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.FetchedAt = day("2026-10-02")
			synced := day("2026-11-05")
			s.LastSyncedAt = &synced
		}, want: "?"},
		{name: "issue-day transfer ambiguous", txs: []model.Transaction{{Date: issue, Amount: dec("-100"), Currency: "USD", Category: "TRANSFER_IN"}}, want: "?"},
		{name: "issue-day missing category ambiguous", txs: []model.Transaction{{Date: issue, Amount: dec("-100"), Currency: "USD"}}, want: "?"},
		{name: "legacy primary payment matches snapshot", txs: []model.Transaction{{Date: paid, Amount: dec("-60"), Currency: "USD", Category: "LOAN_PAYMENTS"}}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("60")
		}, want: "40"},
		{name: "duplicate legacy primary snapshot candidates", txs: []model.Transaction{{Date: paid, Amount: dec("-60"), Currency: "USD", Category: "LOAN_PAYMENTS"}, {Date: paid, Amount: dec("-60"), Currency: "USD", Category: "LOAN_PAYMENTS"}}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("60")
		}, want: "?"},
		{name: "incompatible detailed category cannot match snapshot", txs: []model.Transaction{{Date: paid, Amount: dec("-60"), Currency: "USD", Category: "TRANSFER_IN", DetailedCategory: "GENERAL_MERCHANDISE_OTHER_GENERAL_MERCHANDISE"}}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("60")
		}, want: "?"},
		{name: "legacy label cannot override incompatible detail", txs: []model.Transaction{{Date: paid, Amount: dec("-100"), Currency: "USD", Category: "Payment", DetailedCategory: "GENERAL_MERCHANDISE_OTHER_GENERAL_MERCHANDISE"}}, want: "100"},
		{name: "cleared consent status keeps completed history", change: func(a *model.AccountView, s *model.SyncState) { s.LastStatus = "" }, want: "100"},

		{name: "generic other loan payment ambiguous", txs: []model.Transaction{{Date: paid, Amount: dec("-100"), Currency: "USD", Category: "LOAN_PAYMENTS", DetailedCategory: "LOAN_PAYMENTS_OTHER_PAYMENT"}}, want: "?"},
		{name: "unpaid", want: "100"},
		{name: "multiple partial payments", txs: []model.Transaction{payment("a", "2026-10-05", "-20"), payment("b", "2026-10-10", "-30")}, want: "50"},
		{name: "paid with new purchases", txs: []model.Transaction{payment("a", "2026-10-05", "-100"), {Date: paid, Amount: dec("80"), Currency: "USD", Category: "GENERAL_MERCHANDISE"}}, change: func(a *model.AccountView, s *model.SyncState) { a.BalanceCurrent = nullDec("80") }, want: "0"},
		{name: "overpaid", txs: []model.Transaction{payment("a", "2026-10-05", "-120")}, want: "0"},
		{name: "earlier statement payment excluded", txs: []model.Transaction{payment("a", "2026-09-30", "-100")}, want: "100"},
		{name: "issue day ambiguous", txs: []model.Transaction{payment("a", "2026-10-01", "-100")}, want: "?"},
		{name: "pending excluded", txs: []model.Transaction{func() model.Transaction { p := payment("a", "2026-10-05", "-100"); p.Pending = true; return p }()}, want: "100"},
		{name: "superseded excluded", txs: []model.Transaction{func() model.Transaction { p := payment("a", "2026-10-05", "-100"); p.SupersededBy = "b"; return p }()}, want: "100"},
		{name: "refund excluded", txs: []model.Transaction{{Date: paid, Amount: dec("-100"), Currency: "USD", Category: "GENERAL_MERCHANDISE", DetailedCategory: "GENERAL_MERCHANDISE_OTHER_GENERAL_MERCHANDISE"}}, want: "100"},
		{name: "broad transfer ambiguous", txs: []model.Transaction{{Date: paid, Amount: dec("-100"), Currency: "USD", Category: "TRANSFER_IN"}}, want: "?"},
		{name: "missing category ambiguous", txs: []model.Transaction{{Date: paid, Amount: dec("-100"), Currency: "USD"}}, want: "?"},
		{name: "currency mismatch", txs: []model.Transaction{{Date: paid, Amount: dec("-100"), Currency: "CAD", DetailedCategory: "LOAN_PAYMENTS_CREDIT_CARD_PAYMENT"}}, want: "?"},
		{name: "refund matching snapshot excluded", txs: []model.Transaction{payment("a", "2026-10-10", "-50"), {Date: paid, Amount: dec("-50"), Currency: "USD", Category: "GENERAL_MERCHANDISE", DetailedCategory: "GENERAL_MERCHANDISE_OTHER_GENERAL_MERCHANDISE"}}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("50")
		}, want: "50"},
		{name: "duplicate snapshot candidates ambiguous", txs: []model.Transaction{{Date: paid, Amount: dec("-50"), Currency: "USD", Category: "TRANSFER_IN"}, {Date: paid, Amount: dec("-50"), Currency: "USD", Category: "TRANSFER_IN"}}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("50")
		}, want: "?"},

		{name: "removed full payment with complete history", change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("100")
		}, want: "?"},
		{name: "payment reversal overrides full snapshot", txs: []model.Transaction{payment("a", "2026-10-10", "100")}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("100")
			s.HistoryComplete = false
		}, want: "?"},

		{name: "same-date equal confirmed payments", txs: []model.Transaction{payment("a", "2026-10-10", "-50"), payment("b", "2026-10-10", "-50")}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("50")
		}, want: "0"},
		{name: "issue-day snapshot ambiguous", change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &issue
			a.Liability.LastPaymentAmount = nullDec("100")
		}, want: "?"},

		{name: "missing issue", change: func(a *model.AccountView, s *model.SyncState) { a.Liability.LastStatementIssueDate = nil }, want: "?"},
		{name: "failed sync", change: func(a *model.AccountView, s *model.SyncState) { s.LastStatus = "failed" }, want: "?"},
		{name: "incomplete history", change: func(a *model.AccountView, s *model.SyncState) { s.HistoryComplete = false }, want: "?"},
		{name: "statement outside guaranteed history", change: func(a *model.AccountView, s *model.SyncState) { a.FirstSeenAt = day("2026-11-15") }, want: "?"},
		{name: "snapshot fully covers statement without history", change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("100")
			s.HistoryComplete = false
		}, want: "0"},
		{name: "snapshot never double counted", txs: []model.Transaction{payment("a", "2026-10-10", "-60")}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("60")
		}, want: "40"},
		{name: "snapshot confirms transfer", txs: []model.Transaction{{Date: paid, Amount: dec("-60"), Currency: "USD", Category: "TRANSFER_IN"}}, change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("60")
		}, want: "40"},
		{name: "unmatched snapshot partial", change: func(a *model.AccountView, s *model.SyncState) {
			a.Liability.LastPaymentDate = &paid
			a.Liability.LastPaymentAmount = nullDec("60")
		}, want: "?"},
		{name: "old snapshot cannot pay newer statement", change: func(a *model.AccountView, s *model.SyncState) {
			old := day("2026-09-30")
			a.Liability.LastPaymentDate = &old
			a.Liability.LastPaymentAmount = nullDec("100")
		}, want: "100"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, s := account, state
			l := *account.Liability
			a.Liability = &l
			if tt.change != nil {
				tt.change(&a, &s)
			}
			got := remainingStatementBalance(a, s, tt.txs)
			if tt.want == "?" {
				if got.Valid {
					t.Errorf("got %s; want unknown", got.Decimal)
				}
				return
			}
			if !got.Valid || !got.Decimal.Equal(dec(tt.want)) {
				t.Errorf("got %+v; want %s", got, tt.want)
			}
		})
	}
}

func TestPaymentEvidenceRoundTripAndAccountView(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := sampleAccount()
	a.FirstSeenAt = day("2026-10-01")
	a.Currency = "USD"
	second := a
	second.AccountID = "second-card"
	if err := s.UpsertAccounts(ctx, []model.Account{a, second}); err != nil {
		t.Fatal(err)
	}
	issue, due, paid := day("2026-10-01"), day("2026-11-03"), day("2026-10-10")
	if err := s.ReplaceLiabilities(ctx, a.Provider, a.ItemID, []model.CreditLiability{{AccountID: a.AccountID, LastStatementIssueDate: &issue, PaymentDueDate: &due, LastPaymentDate: &paid, LastPaymentAmount: nullDec("25"), LastStatementBalance: nullDec("100"), FetchedAt: day("2026-10-15")}, {AccountID: second.AccountID, LastStatementIssueDate: &issue, PaymentDueDate: &due, LastPaymentDate: &paid, LastPaymentAmount: nullDec("60"), LastStatementBalance: nullDec("200"), FetchedAt: day("2026-10-15")}}); err != nil {
		t.Fatal(err)
	}
	tx := sample()
	tx.AccountID = a.AccountID
	tx.ItemID = a.ItemID
	tx.Date = paid
	tx.Amount = dec("-25")
	tx.Currency = "USD"
	tx.DetailedCategory = "LOAN_PAYMENTS_CREDIT_CARD_PAYMENT"
	tx.Pending = false
	otherPayment := tx
	otherPayment.ExternalID = "second-payment"
	otherPayment.AccountID = second.AccountID
	otherPayment.Amount = dec("-60")
	if err := s.ApplyPage(ctx, Page{Provider: a.Provider, ItemID: a.ItemID, Cursor: "cursor", HistoryComplete: true, Added: []model.Transaction{tx, otherPayment}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, tx.Provider, tx.ExternalID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DetailedCategory != tx.DetailedCategory {
		t.Errorf("category = %q", got.DetailedCategory)
	}
	views, err := s.AccountViews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("views = %+v; want both cards", views)
	}
	for _, view := range views {
		if view.Liability.LastStatementIssueDate == nil || !view.Liability.LastStatementIssueDate.Equal(issue) {
			t.Errorf("issue date for %s = %v", view.AccountID, view.Liability.LastStatementIssueDate)
		}
		want := "75"
		if view.AccountID == second.AccountID {
			want = "140"
		}
		remaining := view.RemainingStatementBalance
		if !remaining.Valid || !remaining.Decimal.Equal(dec(want)) {
			t.Errorf("remaining for %s = %+v; want %s with other card payments excluded", view.AccountID, remaining, want)
		}
	}
}
