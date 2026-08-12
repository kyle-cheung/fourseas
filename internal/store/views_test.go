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
