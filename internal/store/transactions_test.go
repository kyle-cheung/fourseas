package store

import (
	"context"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

func TestUpsertIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	first := sample()
	if err := s.Upsert(ctx, []model.Transaction{first}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// The same transaction comes back settled, for a slightly larger amount.
	second := sample()
	second.Pending = false
	second.Amount = dec("25.10")
	if err := s.Upsert(ctx, []model.Transaction{second}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("got %d rows after re-inserting the same transaction, want 1", n)
	}

	got, err := s.Get(ctx, first.Provider, first.ExternalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Pending {
		t.Error("Pending = true, want the updated value false")
	}
	if !got.Amount.Equal(dec("25.10")) {
		t.Errorf("Amount = %s, want the updated value 25.10", got.Amount)
	}
}

func TestNewestOrdersByDateDescending(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	older, newer := sample(), sample()
	older.ExternalID, older.Date = "txn-old", day("2026-07-01")
	newer.ExternalID, newer.Date = "txn-new", day("2026-08-11")

	if err := s.Upsert(ctx, []model.Transaction{older, newer}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.Newest(ctx, 1)
	if err != nil {
		t.Fatalf("newest: %v", err)
	}
	if len(got) != 1 || got[0].ExternalID != "txn-new" {
		t.Fatalf("got %+v, want only txn-new", got)
	}
}

func TestRemove(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.Upsert(ctx, []model.Transaction{sample()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.Remove(ctx, "plaid", []string{"txn-1"}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("got %d rows after remove, want 0", n)
	}
}

// TestSumOfBaseAmountIsNullWhenARowHasNoRate is the point of the base currency
// columns: a total that mixes a USD row with a CAD row is null, not a plausible
// wrong number.
func TestSumOfBaseAmountIsNullWhenARowHasNoRate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	usd := model.WithBase(model.Transaction{
		Provider: "plaid", ExternalID: "txn-usd", AccountID: "acct-amex",
		Date: day("2026-08-10"), Amount: dec("24.75"), Currency: "USD",
	})
	cad := model.WithBase(model.Transaction{
		Provider: "plaid", ExternalID: "txn-cad", AccountID: "acct-scotia",
		Date: day("2026-08-11"), Amount: dec("13.20"), Currency: "CAD",
	})

	if err := s.Upsert(ctx, []model.Transaction{usd, cad}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// DuckDB's sum skips nulls, so the null has to be found with a count.
	var raw any
	var missing int
	err := s.db.QueryRowContext(ctx, `
		SELECT sum(base_amount), count(*) FILTER (base_amount IS NULL)
		FROM transactions`).Scan(&raw, &missing)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if missing != 1 {
		t.Errorf("%d rows have no base amount, want 1 (the CAD row)", missing)
	}

	sum, err := toDecimal(raw)
	if err != nil {
		t.Fatalf("convert sum: %v", err)
	}
	if !sum.Equal(dec("24.75")) {
		t.Errorf("sum(base_amount) = %s, want only the USD row 24.75", sum)
	}

	stored, err := s.Get(ctx, "plaid", "txn-cad")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.BaseAmount.Valid || stored.BaseCurrency != "" || stored.FXRate.Valid || stored.FXDate != nil {
		t.Errorf("stored CAD row = %+v, want all four base columns null", stored)
	}
}

func TestGetReportsAMissingRow(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if _, err := s.Get(ctx, "plaid", "nothing-here"); err == nil {
		t.Fatal("want an error for a transaction that is not stored, got nil")
	}
}
