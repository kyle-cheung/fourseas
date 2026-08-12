package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas2/providence/internal/model"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "nested", "probe.duckdb"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sample() model.Transaction {
	return model.Transaction{
		Provider:     "plaid",
		ExternalID:   "txn-1",
		ItemID:       "item-1",
		AccountID:    "acct-amex",
		AccountName:  "Gold Card ••1234",
		Institution:  "American Express",
		Date:         day("2026-08-10"),
		Name:         "BLUE BOTTLE COFFEE",
		MerchantName: "Blue Bottle Coffee",
		Amount:       24.75,
		Currency:     "USD",
		Pending:      true,
		Category:     "FOOD_AND_DRINK",
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	want := sample()
	if err := s.Upsert(ctx, []model.Transaction{want}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.Newest(ctx, 10)
	if err != nil {
		t.Fatalf("newest: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if !got[0].Date.Equal(want.Date) {
		t.Errorf("Date = %v, want %v", got[0].Date, want.Date)
	}
	got[0].Date, want.Date = time.Time{}, time.Time{}
	if got[0] != want {
		t.Errorf("round trip changed the row:\n got %+v\nwant %+v", got[0], want)
	}
}

func TestUpsertIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	first := sample()
	if err := s.Upsert(ctx, []model.Transaction{first}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// The same transaction comes back settled, with a real merchant name.
	second := sample()
	second.Pending = false
	second.Amount = 25.10
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

	got, err := s.Newest(ctx, 1)
	if err != nil {
		t.Fatalf("newest: %v", err)
	}
	if got[0].Pending {
		t.Error("Pending = true, want the updated value false")
	}
	if got[0].Amount != 25.10 {
		t.Errorf("Amount = %v, want the updated value 25.10", got[0].Amount)
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

func TestCursorStartsEmptyAndPersists(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	got, err := s.Cursor(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("first cursor read: %v", err)
	}
	if got != "" {
		t.Errorf("cursor = %q on a new store, want an empty string", got)
	}

	if err := s.SetCursor(ctx, "plaid", "item-1", "cursor-abc"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}
	if err := s.SetCursor(ctx, "plaid", "item-1", "cursor-def"); err != nil {
		t.Fatalf("overwrite cursor: %v", err)
	}

	got, err = s.Cursor(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("second cursor read: %v", err)
	}
	if got != "cursor-def" {
		t.Errorf("cursor = %q, want %q", got, "cursor-def")
	}
}
