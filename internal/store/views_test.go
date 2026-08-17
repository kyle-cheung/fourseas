package store

import (
	"context"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// TestViewHidesSupersededRows uses the real PG&E pair: Plaid gives the pending
// and the posted version of one charge different identifiers, so a naive sum
// counts it two times.
func TestViewHidesSupersededRows(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	pending, posted := sample(), sample()
	pending.ExternalID = "txn-pending"
	pending.Name = "Pacific Gas Electric Company"
	pending.Date = day("2026-08-10")
	pending.Pending = true
	pending.Amount = dec("138.98")
	pending.SupersededBy = "txn-posted"

	posted.ExternalID = "txn-posted"
	posted.Name = "Pacific Gas And Elecwest"
	posted.Date = day("2026-08-11")
	posted.Pending = false
	posted.Amount = dec("138.98")
	posted.PendingTransactionID = "txn-pending"
	posted.SupersededBy = ""

	if err := s.Upsert(ctx, []model.Transaction{pending, posted}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	stored, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if stored != 2 {
		t.Errorf("transactions holds %d rows, want both the pending and the posted row", stored)
	}

	shown, err := s.NewestView(ctx, 10)
	if err != nil {
		t.Fatalf("newest view: %v", err)
	}
	if len(shown) != 1 || shown[0].ExternalID != "txn-posted" {
		t.Fatalf("v_transactions shows %+v, want only the posted row", shown)
	}
}

// TestViewExposesTheDocumentedColumns holds the names the README tells people
// to query by. Renaming one is a change to the surface, not a refactor.
func TestViewExposesTheDocumentedColumns(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	rows, err := s.db.QueryContext(ctx, `SELECT * FROM `+transactionsView+` LIMIT 0`)
	if err != nil {
		t.Fatalf("query %s: %v", transactionsView, err)
	}
	defer rows.Close()

	names, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	found := make(map[string]bool, len(names))
	for _, name := range names {
		found[name] = true
	}

	for _, want := range []string{
		"date", "account", "nickname", "institution_name", "description",
		"amount", "currency", "base_amount", "base_currency", "fx_rate",
		"category", "pending",
	} {
		if !found[want] {
			t.Errorf("%s has no column %q, and the README tells people to query it",
				transactionsView, want)
		}
	}
	if found["fx_date"] {
		t.Errorf("%s exposes fx_date, want conversion dates to stay internal", transactionsView)
	}
}

func TestViewCalculatesBaseValuesWithASOFRates(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	transactions := []model.Transaction{
		viewTransaction("txn-usd", "2026-08-11", "24.75", "USD"),
		viewTransaction("txn-cad-exact", "2026-08-07", "10", "CAD"),
		viewTransaction("txn-cad-weekend", "2026-08-09", "20", "CAD"),
		viewTransaction("txn-cad-future", "2026-08-12", "30", "CAD"),
		viewTransaction("txn-cad-too-old", "2026-08-05", "40", "CAD"),
	}
	if err := s.Upsert(ctx, transactions); err != nil {
		t.Fatalf("upsert transactions: %v", err)
	}

	rates := []model.FXRate{
		{Date: day("2026-08-06"), Currency: "CAD", BaseCurrency: "USD", Rate: dec("0.72")},
		{Date: day("2026-08-07"), Currency: "CAD", BaseCurrency: "USD", Rate: dec("0.73")},
		{Date: day("2026-08-10"), Currency: "CAD", BaseCurrency: "USD", Rate: dec("0.74")},
	}
	if err := s.UpsertFXRates(ctx, rates); err != nil {
		t.Fatalf("upsert rates: %v", err)
	}

	got, err := s.NewestView(ctx, len(transactions))
	if err != nil {
		t.Fatalf("newest view: %v", err)
	}
	byID := make(map[string]model.TransactionView, len(got))
	for _, row := range got {
		byID[row.ExternalID] = row
	}

	tests := []struct {
		name       string
		id         string
		wantAmount string
		wantRate   string
		wantValid  bool
	}{
		{name: "USD needs no rate row", id: "txn-usd", wantAmount: "24.75", wantRate: "1", wantValid: true},
		{name: "CAD uses an exact-date rate", id: "txn-cad-exact", wantAmount: "7.3", wantRate: "0.73", wantValid: true},
		{name: "CAD uses the prior Friday on a weekend", id: "txn-cad-weekend", wantAmount: "14.6", wantRate: "0.73", wantValid: true},
		{name: "future CAD uses the newest stored rate", id: "txn-cad-future", wantAmount: "22.2", wantRate: "0.74", wantValid: true},
		{name: "CAD older than the first rate stays null", id: "txn-cad-too-old", wantValid: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row, ok := byID[tc.id]
			if !ok {
				t.Fatalf("view has no row %q", tc.id)
			}
			if row.BaseAmount.Valid != tc.wantValid {
				t.Fatalf("BaseAmount.Valid = %v, want %v", row.BaseAmount.Valid, tc.wantValid)
			}
			if row.FXRate.Valid != tc.wantValid {
				t.Fatalf("FXRate.Valid = %v, want %v", row.FXRate.Valid, tc.wantValid)
			}
			if !tc.wantValid {
				if row.BaseCurrency != "" {
					t.Errorf("BaseCurrency = %q, want empty", row.BaseCurrency)
				}
				return
			}
			if !row.BaseAmount.Decimal.Equal(dec(tc.wantAmount)) {
				t.Errorf("BaseAmount = %s, want %s", row.BaseAmount.Decimal, tc.wantAmount)
			}
			if row.BaseCurrency != model.BaseCurrency {
				t.Errorf("BaseCurrency = %q, want %q", row.BaseCurrency, model.BaseCurrency)
			}
			if !row.FXRate.Decimal.Equal(dec(tc.wantRate)) {
				t.Errorf("FXRate = %s, want %s", row.FXRate.Decimal, tc.wantRate)
			}
		})
	}
}

func viewTransaction(id, date, amount, currency string) model.Transaction {
	return model.Transaction{
		Provider:   "plaid",
		ExternalID: id,
		AccountID:  "acct-amex",
		Date:       day(date),
		Amount:     dec(amount),
		Currency:   currency,
	}
}

func TestViewLabelsAnAccountByNicknameThenNameThenID(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.Upsert(ctx, []model.Transaction{sample()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// No account row yet, because accounts arrive in issue 3.
	rows, err := s.NewestView(ctx, 1)
	if err != nil {
		t.Fatalf("newest view: %v", err)
	}
	if rows[0].AccountLabel != "acct-amex" {
		t.Errorf("AccountLabel = %q, want the raw account id", rows[0].AccountLabel)
	}

	account := sampleAccount()
	if err := s.UpsertAccounts(ctx, []model.Account{account}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}
	if err := s.UpsertInstitution(ctx, sampleInstitution()); err != nil {
		t.Fatalf("upsert institution: %v", err)
	}

	rows, err = s.NewestView(ctx, 1)
	if err != nil {
		t.Fatalf("newest view: %v", err)
	}
	if rows[0].AccountLabel != "Platinum Card" {
		t.Errorf("AccountLabel = %q, want the account name", rows[0].AccountLabel)
	}
	if rows[0].InstitutionName != "American Express" {
		t.Errorf("InstitutionName = %q, want %q", rows[0].InstitutionName, "American Express")
	}
	if !rows[0].Amount.Equal(dec("24.75")) {
		t.Errorf("Amount = %s, want 24.75", rows[0].Amount)
	}

	account.Nickname = "Amex Daily"
	if err := s.UpsertAccounts(ctx, []model.Account{account}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}
	rows, err = s.NewestView(ctx, 1)
	if err != nil {
		t.Fatalf("newest view: %v", err)
	}
	if rows[0].AccountLabel != "Amex Daily" {
		t.Errorf("AccountLabel = %q, want the nickname", rows[0].AccountLabel)
	}
}
