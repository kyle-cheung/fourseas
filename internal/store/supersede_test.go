package store

import (
	"context"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// The PG&E pair is the real case this whole file exists for. Plaid gives the
// pending and the posted version of one 138.98 charge different identifiers,
// so a sum over the raw table reports 277.96 for one charge.
func pgePending() model.Transaction {
	t := sample()
	t.ExternalID = "txn-pge-pending"
	t.Name = "Pacific Gas Electric Company"
	t.Date = day("2026-08-10")
	t.Amount = dec("138.98")
	t.BaseAmount = nullDec("138.98")
	t.Pending = true
	t.PendingTransactionID = ""
	t.SupersededBy = ""
	return t
}

func pgePosted() model.Transaction {
	t := sample()
	t.ExternalID = "txn-pge-posted"
	t.Name = "Pacific Gas And Elecwest"
	t.Date = day("2026-08-11")
	t.Amount = dec("138.98")
	t.BaseAmount = nullDec("138.98")
	t.Pending = false
	t.PendingTransactionID = "txn-pge-pending"
	t.SupersededBy = ""
	return t
}

// assertPGEResolved states the whole rule in one place: both rows stay on
// disk, the view shows the posted one only, and the pending row records which
// row replaced it.
func assertPGEResolved(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()

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
	if len(shown) != 1 || shown[0].ExternalID != "txn-pge-posted" {
		t.Fatalf("v_transactions shows %d rows %+v, want the posted row only", len(shown), shown)
	}

	pending, err := s.Get(ctx, "plaid", "txn-pge-pending")
	if err != nil {
		t.Fatalf("get the pending row: %v", err)
	}
	if pending.SupersededBy != "txn-pge-posted" {
		t.Errorf("SupersededBy = %q, want %q", pending.SupersededBy, "txn-pge-posted")
	}
}

// TestSupersedeResolvesAPairInOnePage is the first half of the rule: the
// posted row can arrive in the same page as the pending row it replaces.
func TestSupersedeResolvesAPairInOnePage(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	page := Page{
		Provider: "plaid",
		ItemID:   "item-1",
		Added:    []model.Transaction{pgePending(), pgePosted()},
		Cursor:   "c1",
	}
	if err := s.ApplyPage(ctx, page); err != nil {
		t.Fatalf("apply page: %v", err)
	}

	assertPGEResolved(t, s)
}

// TestSupersedeResolvesAPairAcrossTwoSyncs is the second half: the pending row
// lands in one sync and the posted row in a later one, so the rule has to run
// after every page and not only over the rows that page carried.
func TestSupersedeResolvesAPairAcrossTwoSyncs(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	first := Page{
		Provider: "plaid",
		ItemID:   "item-1",
		Added:    []model.Transaction{pgePending()},
		Cursor:   "c1",
	}
	if err := s.ApplyPage(ctx, first); err != nil {
		t.Fatalf("first page: %v", err)
	}

	// After the first sync the charge is one pending row and nothing is hidden.
	shown, err := s.NewestView(ctx, 10)
	if err != nil {
		t.Fatalf("newest view: %v", err)
	}
	if len(shown) != 1 || shown[0].ExternalID != "txn-pge-pending" {
		t.Fatalf("v_transactions shows %+v, want the pending row while it is alone", shown)
	}

	second := Page{
		Provider: "plaid",
		ItemID:   "item-1",
		Added:    []model.Transaction{pgePosted()},
		Cursor:   "c2",
	}
	if err := s.ApplyPage(ctx, second); err != nil {
		t.Fatalf("second page: %v", err)
	}

	assertPGEResolved(t, s)
}

// TestSupersedeSurvivesTheSamePageTwice covers the modified list: Plaid resends
// a row when it changes, and the upsert writes the provider's empty
// superseded_by over the resolved one. The rule runs after the rows are
// written, so the pending row does not come back into the view.
func TestSupersedeSurvivesTheSamePageTwice(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	page := Page{
		Provider: "plaid",
		ItemID:   "item-1",
		Added:    []model.Transaction{pgePending(), pgePosted()},
		Cursor:   "c1",
	}
	if err := s.ApplyPage(ctx, page); err != nil {
		t.Fatalf("apply page: %v", err)
	}

	again := page
	again.Added = nil
	again.Modified = []model.Transaction{pgePending(), pgePosted()}
	again.Cursor = "c2"
	if err := s.ApplyPage(ctx, again); err != nil {
		t.Fatalf("apply the same page again: %v", err)
	}

	assertPGEResolved(t, s)
}

// TestSupersedeReleasesARowWhenThePostedRowIsRemoved keeps the view honest in
// the other direction: Plaid can take a posted row back, and the charge must
// not stay hidden behind a row that is no longer stored.
func TestSupersedeReleasesARowWhenThePostedRowIsRemoved(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	page := Page{
		Provider: "plaid",
		ItemID:   "item-1",
		Added:    []model.Transaction{pgePending(), pgePosted()},
		Cursor:   "c1",
	}
	if err := s.ApplyPage(ctx, page); err != nil {
		t.Fatalf("apply page: %v", err)
	}

	drop := Page{
		Provider:   "plaid",
		ItemID:     "item-1",
		RemovedIDs: []string{"txn-pge-posted"},
		Cursor:     "c2",
	}
	if err := s.ApplyPage(ctx, drop); err != nil {
		t.Fatalf("remove the posted row: %v", err)
	}

	pending, err := s.Get(ctx, "plaid", "txn-pge-pending")
	if err != nil {
		t.Fatalf("get the pending row: %v", err)
	}
	if pending.SupersededBy != "" {
		t.Errorf("SupersededBy = %q, want it cleared when the posted row is gone", pending.SupersededBy)
	}

	shown, err := s.NewestView(ctx, 10)
	if err != nil {
		t.Fatalf("newest view: %v", err)
	}
	if len(shown) != 1 || shown[0].ExternalID != "txn-pge-pending" {
		t.Fatalf("v_transactions shows %+v, want the pending row back", shown)
	}
}

// TestSupersedeLeavesUnrelatedRowsAlone proves the rule only hides a row some
// other row names, and that it does not reach across providers.
func TestSupersedeLeavesUnrelatedRowsAlone(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	plain := sample()
	plain.ExternalID = "txn-plain"
	plain.PendingTransactionID = ""

	// Same identifier, another provider. Identifiers are only unique within one.
	otherProvider := pgePending()
	otherProvider.Provider = "other"

	page := Page{
		Provider: "plaid",
		ItemID:   "item-1",
		Added:    []model.Transaction{plain, pgePosted()},
		Cursor:   "c1",
	}
	if err := s.ApplyPage(ctx, page); err != nil {
		t.Fatalf("apply page: %v", err)
	}
	if err := s.Upsert(ctx, []model.Transaction{otherProvider}); err != nil {
		t.Fatalf("seed the other provider: %v", err)
	}
	if err := s.ApplyPage(ctx, Page{Provider: "plaid", ItemID: "item-1", Cursor: "c2"}); err != nil {
		t.Fatalf("empty page: %v", err)
	}

	for _, id := range []struct{ provider, external string }{
		{"plaid", "txn-plain"},
		{"plaid", "txn-pge-posted"},
		{"other", "txn-pge-pending"},
	} {
		row, err := s.Get(ctx, id.provider, id.external)
		if err != nil {
			t.Fatalf("get %s/%s: %v", id.provider, id.external, err)
		}
		if row.SupersededBy != "" {
			t.Errorf("%s/%s SupersededBy = %q, want empty", id.provider, id.external, row.SupersededBy)
		}
	}
}
