package store

import (
	"context"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// badRow holds an amount that no DECIMAL(18,4) column can take. It fails on
// insert, which is how these tests break a page part way through.
func badRow() model.Transaction {
	t := sample()
	t.ExternalID = "txn-too-big"
	t.Amount = dec("99999999999999999999.99")
	t.BaseAmount = nullDec("99999999999999999999.99")
	return t
}

func TestApplyPageWritesRowsAccountsAndCursorTogether(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	page := Page{
		Provider: "plaid",
		ItemID:   "item-1",
		Added:    []model.Transaction{sample()},
		Accounts: []model.Account{sampleAccount()},
		Cursor:   "c1",
	}
	if err := s.ApplyPage(ctx, page); err != nil {
		t.Fatalf("apply page: %v", err)
	}

	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("stored %d transactions, want 1", n)
	}

	accounts, err := s.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Errorf("stored %d accounts, want 1", len(accounts))
	}

	cursor, err := s.Cursor(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if cursor != "c1" {
		t.Errorf("cursor = %q, want %q", cursor, "c1")
	}
}

// TestApplyPageLeavesNothingBehindWhenARowFails is the point of the whole
// change: a page either lands whole or not at all. Rows written before the
// failure, the account list, and the cursor all go back to what they were.
func TestApplyPageLeavesNothingBehindWhenARowFails(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	first := Page{Provider: "plaid", ItemID: "item-1", Cursor: "c1"}
	if err := s.ApplyPage(ctx, first); err != nil {
		t.Fatalf("first page: %v", err)
	}

	good := sample()
	good.ExternalID = "txn-good"
	broken := Page{
		Provider: "plaid",
		ItemID:   "item-1",
		Added:    []model.Transaction{good, badRow()},
		Accounts: []model.Account{sampleAccount()},
		Cursor:   "c2",
	}
	if err := s.ApplyPage(ctx, broken); err == nil {
		t.Fatal("want an error from the unwritable row, got nil")
	}

	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("stored %d transactions after the failed page, want 0", n)
	}

	accounts, err := s.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("stored %d accounts after the failed page, want 0", len(accounts))
	}

	cursor, err := s.Cursor(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if cursor != "c1" {
		t.Errorf("cursor = %q, want the cursor of the last good page %q", cursor, "c1")
	}
}

// TestApplyPageDoesNotDeleteWhenALaterStepFails covers the other half of the
// page: a removal must not survive a failure that comes after it.
func TestApplyPageDoesNotDeleteWhenALaterStepFails(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.Upsert(ctx, []model.Transaction{sample()}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	broken := Page{
		Provider:   "plaid",
		ItemID:     "item-1",
		RemovedIDs: []string{"txn-1"},
		Added:      []model.Transaction{badRow()},
		Cursor:     "c1",
	}
	if err := s.ApplyPage(ctx, broken); err == nil {
		t.Fatal("want an error from the unwritable row, got nil")
	}

	if _, err := s.Get(ctx, "plaid", "txn-1"); err != nil {
		t.Errorf("the removed row is gone after a failed page: %v", err)
	}
}

// TestApplyPageRecordsAnEmptyPage proves a sync with nothing new still moves
// the cursor and records the time, so the next run asks for the next page.
func TestApplyPageRecordsAnEmptyPage(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.ApplyPage(ctx, Page{Provider: "plaid", ItemID: "item-1", Cursor: "c1"}); err != nil {
		t.Fatalf("apply page: %v", err)
	}

	states, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("got %d sync states, want 1", len(states))
	}
	if states[0].Cursor != "c1" {
		t.Errorf("cursor = %q, want %q", states[0].Cursor, "c1")
	}
	if states[0].LastSyncedAt == nil {
		t.Error("LastSyncedAt is not set, want the time the page landed")
	}
	if states[0].LastStatus != "ok" {
		t.Errorf("LastStatus = %q, want %q", states[0].LastStatus, "ok")
	}
}
