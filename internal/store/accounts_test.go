package store

import (
	"context"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

func TestAccountRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	want := sampleAccount()
	want.Nickname = "Amex Daily"
	if err := s.UpsertAccounts(ctx, []model.Account{want}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}

	got, err := s.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d accounts, want 1", len(got))
	}
	assertSameAccount(t, got[0], want)
}

func TestAccountBalancesRoundTripAsNull(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	want := sampleAccount()
	want.BalanceCurrent = decimal.NullDecimal{}
	want.BalanceAvailable = decimal.NullDecimal{}
	want.BalanceLimit = decimal.NullDecimal{}
	want.BalanceUpdatedAt = nil
	want.Mask = ""

	if err := s.UpsertAccounts(ctx, []model.Account{want}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}
	got, err := s.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	assertSameAccount(t, got[0], want)
}

// TestUpsertAccountKeepsWhatTheUserOwns proves a sync cannot erase a nickname
// or move the first time an account was seen.
func TestUpsertAccountKeepsWhatTheUserOwns(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	first := sampleAccount()
	first.Nickname = "Amex Daily"
	first.Tracked = false
	if err := s.UpsertAccounts(ctx, []model.Account{first}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// A sync knows nothing about nicknames or tracking.
	fromSync := sampleAccount()
	fromSync.Nickname = ""
	fromSync.Tracked = true
	fromSync.BalanceCurrent = nullDec("99.9900")
	fromSync.FirstSeenAt = day("2026-08-12")
	fromSync.LastSeenAt = day("2026-08-13")
	if err := s.UpsertAccounts(ctx, []model.Account{fromSync}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := s.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d accounts, want 1", len(got))
	}
	if got[0].Nickname != "Amex Daily" {
		t.Errorf("Nickname = %q, want the nickname to survive a sync", got[0].Nickname)
	}
	if got[0].Tracked {
		t.Error("Tracked = true, want the user's choice to survive a sync")
	}
	if !got[0].FirstSeenAt.Equal(first.FirstSeenAt) {
		t.Errorf("FirstSeenAt = %v, want the earlier %v", got[0].FirstSeenAt, first.FirstSeenAt)
	}
	if !got[0].LastSeenAt.Equal(fromSync.LastSeenAt) {
		t.Errorf("LastSeenAt = %v, want the newer %v", got[0].LastSeenAt, fromSync.LastSeenAt)
	}
	if !got[0].BalanceCurrent.Decimal.Equal(dec("99.99")) {
		t.Errorf("BalanceCurrent = %s, want the new balance 99.99", got[0].BalanceCurrent.Decimal)
	}
}

func TestInstitutionRoundTripAndUpsert(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	want := sampleInstitution()
	if err := s.UpsertInstitution(ctx, want); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	renamed := want
	renamed.InstitutionName = "Amex"
	if err := s.UpsertInstitution(ctx, renamed); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := s.Institutions(ctx)
	if err != nil {
		t.Fatalf("institutions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d institutions, want 1", len(got))
	}
	if !got[0].LinkedAt.Equal(want.LinkedAt) {
		t.Errorf("LinkedAt = %v, want %v", got[0].LinkedAt, want.LinkedAt)
	}
	got[0].LinkedAt, renamed.LinkedAt = time.Time{}, time.Time{}
	if got[0] != renamed {
		t.Errorf("got %+v, want %+v", got[0], renamed)
	}
}

func assertSameAccount(t *testing.T, got, want model.Account) {
	t.Helper()

	for _, c := range []struct {
		field     string
		got, want decimal.NullDecimal
	}{
		{"BalanceCurrent", got.BalanceCurrent, want.BalanceCurrent},
		{"BalanceAvailable", got.BalanceAvailable, want.BalanceAvailable},
		{"BalanceLimit", got.BalanceLimit, want.BalanceLimit},
	} {
		if c.got.Valid != c.want.Valid || (c.want.Valid && !c.got.Decimal.Equal(c.want.Decimal)) {
			t.Errorf("%s = %+v, want %+v", c.field, c.got, c.want)
		}
	}
	if !got.FirstSeenAt.Equal(want.FirstSeenAt) {
		t.Errorf("FirstSeenAt = %v, want %v", got.FirstSeenAt, want.FirstSeenAt)
	}
	if !got.LastSeenAt.Equal(want.LastSeenAt) {
		t.Errorf("LastSeenAt = %v, want %v", got.LastSeenAt, want.LastSeenAt)
	}
	assertSameDate(t, "BalanceUpdatedAt", got.BalanceUpdatedAt, want.BalanceUpdatedAt)

	got.BalanceCurrent, want.BalanceCurrent = decimal.NullDecimal{}, decimal.NullDecimal{}
	got.BalanceAvailable, want.BalanceAvailable = decimal.NullDecimal{}, decimal.NullDecimal{}
	got.BalanceLimit, want.BalanceLimit = decimal.NullDecimal{}, decimal.NullDecimal{}
	got.BalanceUpdatedAt, want.BalanceUpdatedAt = nil, nil
	got.FirstSeenAt, want.FirstSeenAt = time.Time{}, time.Time{}
	got.LastSeenAt, want.LastSeenAt = time.Time{}, time.Time{}
	if got != want {
		t.Errorf("round trip changed the account:\n got %+v\nwant %+v", got, want)
	}
}

// TestAccountViewsJoinTheInstitutionName proves the accounts list can show
// which login an account sits behind.
func TestAccountViewsJoinTheInstitutionName(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.UpsertInstitution(ctx, sampleInstitution()); err != nil {
		t.Fatalf("upsert institution: %v", err)
	}

	known := sampleAccount()
	orphan := sampleAccount()
	orphan.AccountID = "acct-unknown"
	orphan.ItemID = "item-not-linked"
	orphan.Name = "Mystery Card"
	if err := s.UpsertAccounts(ctx, []model.Account{known, orphan}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}

	got, err := s.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d views, want 2", len(got))
	}

	byID := map[string]model.AccountView{}
	for _, v := range got {
		byID[v.AccountID] = v
	}
	if name := byID["acct-amex"].InstitutionName; name != "American Express" {
		t.Errorf("InstitutionName = %q, want %q", name, "American Express")
	}
	// An account whose institution row is missing is still listed. Losing a
	// card from the list would be worse than an empty name.
	if name := byID["acct-unknown"].InstitutionName; name != "" {
		t.Errorf("InstitutionName = %q, want empty for an unknown institution", name)
	}
	assertSameAccount(t, byID["acct-amex"].Account, known)
}
