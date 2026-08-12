package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/shopspring/decimal"
)

// fakeSource returns a fixed list of pages and records the cursors it was given.
type fakeSource struct {
	pages   []provider.Batch
	errs    []error
	call    int
	cursors []string
}

func (f *fakeSource) Name() string { return "fake" }

func (f *fakeSource) Sync(_ context.Context, cursor string) (provider.Batch, error) {
	f.cursors = append(f.cursors, cursor)
	i := f.call
	f.call++

	if i < len(f.errs) && f.errs[i] != nil {
		return provider.Batch{}, f.errs[i]
	}
	if i >= len(f.pages) {
		return provider.Batch{}, errors.New("the fake ran out of pages")
	}
	return f.pages[i], nil
}

func row(id string, date string) model.Transaction {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return model.Transaction{
		Provider:   "fake",
		ExternalID: id,
		ItemID:     "item-1",
		Date:       d,
		Name:       id,
		Amount:     decimal.NewFromInt(10),
		Currency:   "USD",
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "fourseas.duckdb"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDrainFollowsEveryPage(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	source := &fakeSource{pages: []provider.Batch{
		{Added: []model.Transaction{row("a", "2026-08-01")}, NextCursor: "c1", HasMore: true},
		{Added: []model.Transaction{row("b", "2026-08-02")}, NextCursor: "c2", HasMore: true},
		{
			Added:      []model.Transaction{row("c", "2026-08-03")},
			RemovedIDs: []string{"a"},
			NextCursor: "c3",
			HasMore:    false,
		},
	}}

	got, err := drain(ctx, db, source, "item-1")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got.added != 3 || got.removed != 1 {
		t.Errorf("counts = %+v, want 3 added and 1 removed", got)
	}

	// The first call starts from an empty cursor, then follows next_cursor.
	want := []string{"", "c1", "c2"}
	if len(source.cursors) != len(want) {
		t.Fatalf("cursors = %v, want %v", source.cursors, want)
	}
	for i := range want {
		if source.cursors[i] != want[i] {
			t.Errorf("cursor %d = %q, want %q", i, source.cursors[i], want[i])
		}
	}

	// Row "a" was added on page one and removed on page three.
	n, err := db.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("stored %d rows, want 2", n)
	}

	saved, err := db.Cursor(ctx, "fake", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if saved != "c3" {
		t.Errorf("saved cursor = %q, want %q", saved, "c3")
	}
}

func TestDrainResumesFromTheSavedCursor(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	if err := db.SetCursor(ctx, "fake", "item-1", "saved-cursor"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	source := &fakeSource{pages: []provider.Batch{{NextCursor: "next", HasMore: false}}}
	if _, err := drain(ctx, db, source, "item-1"); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if source.cursors[0] != "saved-cursor" {
		t.Errorf("first cursor = %q, want the saved one", source.cursors[0])
	}
}

func TestDrainKeepsTheCursorFromTheLastGoodPage(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	source := &fakeSource{
		pages: []provider.Batch{
			{Added: []model.Transaction{row("a", "2026-08-01")}, NextCursor: "c1", HasMore: true},
		},
		errs: []error{nil, errors.New("plaid is unavailable")},
	}

	if _, err := drain(ctx, db, source, "item-1"); err == nil {
		t.Fatal("want an error from the second page, got nil")
	}

	saved, err := db.Cursor(ctx, "fake", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if saved != "c1" {
		t.Errorf("saved cursor = %q, want the cursor from the last good page", saved)
	}
}

// TestDrainStoresAccountsAndBalances proves the account list Plaid returns on
// every sync is written instead of thrown away.
func TestDrainStoresAccountsAndBalances(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	first := account("acct-1", "500.25")
	second := account("acct-1", "612.75")
	source := &fakeSource{pages: []provider.Batch{
		{Accounts: []model.Account{first}, NextCursor: "c1", HasMore: true},
		{Accounts: []model.Account{second}, NextCursor: "c2", HasMore: false},
	}}

	if _, err := drain(ctx, db, source, "item-1"); err != nil {
		t.Fatalf("drain: %v", err)
	}

	got, err := db.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d accounts, want 1", len(got))
	}
	if !got[0].BalanceCurrent.Valid || !got[0].BalanceCurrent.Decimal.Equal(decimal.RequireFromString("612.75")) {
		t.Errorf("BalanceCurrent = %+v, want the newest balance 612.75", got[0].BalanceCurrent)
	}
}

// TestDrainLeavesTheRowsAndTheCursorWhenAPageFailsPartWay proves the page is
// one unit: the rows before the bad one, and the cursor that page carried, are
// both left as they were.
func TestDrainLeavesTheRowsAndTheCursorWhenAPageFailsPartWay(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	// A balance no DECIMAL(18,4) column can hold fails on insert, after the
	// rows of the same page are written.
	source := &fakeSource{pages: []provider.Batch{
		{Added: []model.Transaction{row("a", "2026-08-01")}, NextCursor: "c1", HasMore: true},
		{
			Added:      []model.Transaction{row("b", "2026-08-02")},
			RemovedIDs: []string{"a"},
			Accounts:   []model.Account{account("acct-1", "99999999999999999999.99")},
			NextCursor: "c2",
			HasMore:    false,
		},
	}}

	if _, err := drain(ctx, db, source, "item-1"); err == nil {
		t.Fatal("want an error from the second page, got nil")
	}

	// Only page one is stored: "b" never landed and "a" was not removed.
	n, err := db.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("stored %d rows, want only the row from the first page", n)
	}
	if _, err := db.Get(ctx, "fake", "a"); err != nil {
		t.Errorf("the row from the first page is gone: %v", err)
	}

	accounts, err := db.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("stored %d accounts from the failed page, want 0", len(accounts))
	}

	saved, err := db.Cursor(ctx, "fake", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if saved != "c1" {
		t.Errorf("saved cursor = %q, want the cursor of the last whole page", saved)
	}
}

// TestDrainWithNothingNewMakesOneCall is the everyday case: a second sync asks
// once and reports nothing.
func TestDrainWithNothingNewMakesOneCall(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	source := &fakeSource{pages: []provider.Batch{{NextCursor: "c1", HasMore: false}}}
	got, err := drain(ctx, db, source, "item-1")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}

	if got.added != 0 || got.modified != 0 || got.removed != 0 {
		t.Errorf("counts = %+v, want all zero", got)
	}
	if source.call != 1 {
		t.Errorf("made %d API calls, want 1", source.call)
	}
}

// TestDrainHidesThePendingRowWhenThePostedRowArrivesLater walks the real PG&E
// case through the whole sync path: the pending row lands in one sync and the
// posted row in the next. Both stay stored, and the view shows one charge.
func TestDrainHidesThePendingRowWhenThePostedRowArrivesLater(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	pending := row("txn-pge-pending", "2026-08-10")
	pending.Name = "Pacific Gas Electric Company"
	pending.Amount = decimal.RequireFromString("138.98")
	pending.Pending = true

	posted := row("txn-pge-posted", "2026-08-11")
	posted.Name = "Pacific Gas And Elecwest"
	posted.Amount = decimal.RequireFromString("138.98")
	posted.PendingTransactionID = "txn-pge-pending"

	first := &fakeSource{pages: []provider.Batch{
		{Added: []model.Transaction{pending}, NextCursor: "c1", HasMore: false},
	}}
	if _, err := drain(ctx, db, first, "item-1"); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	second := &fakeSource{pages: []provider.Batch{
		{Added: []model.Transaction{posted}, NextCursor: "c2", HasMore: false},
	}}
	if _, err := drain(ctx, db, second, "item-1"); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	stored, err := db.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if stored != 2 {
		t.Errorf("transactions holds %d rows, want both versions of the charge", stored)
	}

	shown, err := db.NewestView(ctx, 10)
	if err != nil {
		t.Fatalf("newest view: %v", err)
	}
	if len(shown) != 1 || shown[0].ExternalID != "txn-pge-posted" {
		t.Fatalf("v_transactions shows %d rows %+v, want the posted row only", len(shown), shown)
	}
}

func account(id, balance string) model.Account {
	return model.Account{
		Provider:       "fake",
		AccountID:      id,
		ItemID:         "item-1",
		Name:           "Gold Card",
		Currency:       "USD",
		Tracked:        true,
		BalanceCurrent: decimal.NullDecimal{Decimal: decimal.RequireFromString(balance), Valid: true},
		FirstSeenAt:    time.Now().UTC(),
		LastSeenAt:     time.Now().UTC(),
	}
}
