package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func dayPtr(s string) *time.Time {
	t := day(s)
	return &t
}

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

func nullDec(s string) decimal.NullDecimal {
	return decimal.NullDecimal{Decimal: dec(s), Valid: true}
}

// newStore opens a database in a directory that does not exist yet, so that
// every test also proves the schema is created from nothing.
func newStore(t *testing.T) *Store {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "nested", "fourseas.duckdb"))
}

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sample() model.Transaction {
	return model.Transaction{
		Provider:             "plaid",
		ExternalID:           "txn-1",
		ItemID:               "item-1",
		AccountID:            "acct-amex",
		Date:                 day("2026-08-10"),
		AuthorizedDate:       dayPtr("2026-08-09"),
		Name:                 "BLUE BOTTLE COFFEE",
		MerchantName:         "Blue Bottle Coffee",
		Amount:               dec("24.75"),
		Currency:             "USD",
		Pending:              true,
		PendingTransactionID: "txn-pending-1",
		SupersededBy:         "",
		Category:             "FOOD_AND_DRINK",
		SyncedAt:             time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC),
	}
}

func sampleAccount() model.Account {
	return model.Account{
		Provider:         "plaid",
		AccountID:        "acct-amex",
		ItemID:           "item-1",
		Name:             "Platinum Card",
		Mask:             "1234",
		Type:             "credit",
		Subtype:          "credit card",
		Currency:         "USD",
		Nickname:         "",
		Tracked:          true,
		BalanceCurrent:   nullDec("1234.5600"),
		BalanceAvailable: nullDec("765.4400"),
		BalanceLimit:     nullDec("2000"),
		BalanceUpdatedAt: dayPtr("2026-08-12"),
		FirstSeenAt:      day("2026-08-01"),
		LastSeenAt:       day("2026-08-12"),
	}
}

func sampleInstitution() model.Institution {
	return model.Institution{
		Provider:        "plaid",
		ItemID:          "item-1",
		InstitutionID:   "ins_10",
		InstitutionName: "American Express",
		Env:             "production",
		LinkedAt:        day("2026-08-01"),
	}
}

// TestDecimalRoundTripsExactly is the reason money is not a float: 0.1 + 0.2
// must be 0.3, not 0.30000000000000004.
func TestDecimalRoundTripsExactly(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	one, two := sample(), sample()
	one.ExternalID, one.Amount = "txn-tenth", dec("0.1")
	two.ExternalID, two.Amount = "txn-fifth", dec("0.2")

	big := sample()
	big.ExternalID, big.Amount = "txn-big", dec("12345678.9012")

	if err := s.Upsert(ctx, []model.Transaction{one, two, big}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.Get(ctx, "plaid", "txn-big")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Amount.Equal(dec("12345678.9012")) {
		t.Errorf("Amount = %s, want 12345678.9012", got.Amount)
	}

	// The sum happens in DuckDB, on the stored column type.
	var raw any
	err = s.db.QueryRowContext(ctx,
		`SELECT sum(amount) FROM transactions WHERE external_id IN ('txn-tenth', 'txn-fifth')`).Scan(&raw)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	sum, err := toDecimal(raw)
	if err != nil {
		t.Fatalf("convert sum: %v", err)
	}
	if !sum.Equal(dec("0.3")) {
		t.Errorf("0.1 + 0.2 = %s, want exactly 0.3", sum)
	}
}

func TestTransactionRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	want := sample()
	want.SupersededBy = "txn-2"
	if err := s.Upsert(ctx, []model.Transaction{want}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.Get(ctx, want.Provider, want.ExternalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertSameTransaction(t, got, want)
}

// TestNullableColumnsRoundTripAsNull proves optional provider fields accept
// nothing at all.
func TestNullableColumnsRoundTripAsNull(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	want := sample()
	want.AuthorizedDate = nil
	want.MerchantName = ""
	want.PendingTransactionID = ""
	want.SupersededBy = ""

	if err := s.Upsert(ctx, []model.Transaction{want}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.Get(ctx, want.Provider, want.ExternalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertSameTransaction(t, got, want)
}

func assertSameTransaction(t *testing.T, got, want model.Transaction) {
	t.Helper()

	if !got.Amount.Equal(want.Amount) {
		t.Errorf("Amount = %s, want %s", got.Amount, want.Amount)
	}
	if !got.Date.Equal(want.Date) {
		t.Errorf("Date = %v, want %v", got.Date, want.Date)
	}
	if !got.SyncedAt.Equal(want.SyncedAt) {
		t.Errorf("SyncedAt = %v, want %v", got.SyncedAt, want.SyncedAt)
	}
	assertSameDate(t, "AuthorizedDate", got.AuthorizedDate, want.AuthorizedDate)

	// Compare everything else field by field, with the compared fields cleared.
	got.Amount, want.Amount = decimal.Decimal{}, decimal.Decimal{}
	got.Date, want.Date = time.Time{}, time.Time{}
	got.SyncedAt, want.SyncedAt = time.Time{}, time.Time{}
	got.AuthorizedDate, want.AuthorizedDate = nil, nil
	if got != want {
		t.Errorf("round trip changed the row:\n got %+v\nwant %+v", got, want)
	}
}

func assertSameDate(t *testing.T, field string, got, want *time.Time) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %v, want %v", field, got, want)
	case !got.Equal(*want):
		t.Errorf("%s = %v, want %v", field, *got, *want)
	}
}
