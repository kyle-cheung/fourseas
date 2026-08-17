package store

import (
	"context"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// seedTwoItems stores one transaction, one account, one institution, and one
// cursor for each of two items.
func seedTwoItems(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()

	first, second := sample(), sample()
	second.ExternalID, second.ItemID, second.AccountID = "txn-2", "item-2", "acct-visa"
	if err := s.Upsert(ctx, []model.Transaction{first, second}); err != nil {
		t.Fatalf("upsert transactions: %v", err)
	}

	firstAccount, secondAccount := sampleAccount(), sampleAccount()
	secondAccount.AccountID, secondAccount.ItemID = "acct-visa", "item-2"
	if err := s.UpsertAccounts(ctx, []model.Account{firstAccount, secondAccount}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}

	firstInstitution, secondInstitution := sampleInstitution(), sampleInstitution()
	secondInstitution.ItemID, secondInstitution.InstitutionName = "item-2", "Scotiabank"
	for _, i := range []model.Institution{firstInstitution, secondInstitution} {
		if err := s.UpsertInstitution(ctx, i); err != nil {
			t.Fatalf("upsert institution: %v", err)
		}
	}

	for _, itemID := range []string{"item-1", "item-2"} {
		if err := s.SetCursor(ctx, "plaid", itemID, "cursor-"+itemID); err != nil {
			t.Fatalf("set cursor: %v", err)
		}
	}
}

// countRows counts the rows of one item in one table.
func countRows(t *testing.T, s *Store, table, itemID string) int {
	t.Helper()
	var n int
	err := s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE provider = 'plaid' AND item_id = ?`, itemID).Scan(&n)
	if err != nil {
		t.Fatalf("count %s of %s: %v", table, itemID, err)
	}
	return n
}

// assertItemRows checks how many rows each table holds for one item.
func assertItemRows(t *testing.T, s *Store, itemID string, want int) {
	t.Helper()
	for _, table := range []string{"transactions", "accounts", "institutions", "sync_state"} {
		if got := countRows(t, s, table, itemID); got != want {
			t.Errorf("%s of %s = %d rows, want %d", table, itemID, got, want)
		}
	}
}

func TestUnlinkDeletesOnlyTheRowsOfOneItem(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedTwoItems(t, s)

	removed, err := s.Unlink(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("unlink: %v", err)
	}

	if removed.Transactions != 1 || removed.Accounts != 1 {
		t.Errorf("removed = %+v, want 1 transaction and 1 account", removed)
	}
	assertItemRows(t, s, "item-1", 0)
	assertItemRows(t, s, "item-2", 1)
}

// TestUnlinkOfAnUnknownItemChangesNothing covers a token file that names an
// item the database never stored.
func TestUnlinkOfAnUnknownItemChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedTwoItems(t, s)

	removed, err := s.Unlink(ctx, "plaid", "item-missing")
	if err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if removed.Transactions != 0 || removed.Accounts != 0 {
		t.Errorf("removed = %+v, want nothing", removed)
	}
	assertItemRows(t, s, "item-1", 1)
	assertItemRows(t, s, "item-2", 1)
}

func TestItemCountsWhatAnUnlinkWouldDelete(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedTwoItems(t, s)

	counts, err := s.ItemCounts(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("item counts: %v", err)
	}
	if counts.Transactions != 1 || counts.Accounts != 1 {
		t.Errorf("counts = %+v, want 1 transaction and 1 account", counts)
	}

	// Counting must not delete anything.
	assertItemRows(t, s, "item-1", 1)
}
