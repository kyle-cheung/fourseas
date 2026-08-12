package main

import (
	"context"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
	"github.com/shopspring/decimal"
)

// TestRecordInstitutionNamesTheAccountsList proves an account can be shown
// with the login it sits behind after a sync.
func TestRecordInstitutionNamesTheAccountsList(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	item := tokens.Item{
		ItemID:      "item-1",
		Institution: "American Express",
		Env:         "sandbox",
		LinkedAt:    time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := recordInstitution(ctx, db, item, "production"); err != nil {
		t.Fatalf("record institution: %v", err)
	}

	stored, err := db.Institutions(ctx)
	if err != nil {
		t.Fatalf("institutions: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("got %d institutions, want 1", len(stored))
	}
	if stored[0].InstitutionName != "American Express" {
		t.Errorf("InstitutionName = %q, want %q", stored[0].InstitutionName, "American Express")
	}
	// The item's own environment wins: it is where the token works.
	if stored[0].Env != "sandbox" {
		t.Errorf("Env = %q, want the environment the item was linked in", stored[0].Env)
	}

	plaidAccount := account("acct-1", "10.00")
	plaidAccount.Provider = "plaid"
	if err := db.UpsertAccounts(ctx, []model.Account{plaidAccount}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}

	views, err := db.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	if views[0].InstitutionName != "American Express" {
		t.Errorf("InstitutionName = %q, want the joined name", views[0].InstitutionName)
	}
}

// TestRecordInstitutionFallsBackToTheCurrentEnvironment covers an item saved
// before the token file recorded an environment.
func TestRecordInstitutionFallsBackToTheCurrentEnvironment(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	if err := recordInstitution(ctx, db, tokens.Item{ItemID: "item-1"}, "production"); err != nil {
		t.Fatalf("record institution: %v", err)
	}
	stored, err := db.Institutions(ctx)
	if err != nil {
		t.Fatalf("institutions: %v", err)
	}
	if stored[0].Env != "production" {
		t.Errorf("Env = %q, want the current environment", stored[0].Env)
	}
}

func TestAccountCellsShowADashWhenThereIsNoValue(t *testing.T) {
	empty := model.Account{}
	if got := accountBalance(empty); got != "-" {
		t.Errorf("accountBalance = %q, want a dash", got)
	}
	if got := accountLimit(empty); got != "-" {
		t.Errorf("accountLimit = %q, want a dash", got)
	}
	if got := accountUpdated(empty); got != "-" {
		t.Errorf("accountUpdated = %q, want a dash", got)
	}

	filled := model.Account{
		BalanceCurrent: decimal.NullDecimal{Decimal: decimal.RequireFromString("412.5"), Valid: true},
		BalanceLimit:   decimal.NullDecimal{Decimal: decimal.RequireFromString("10000"), Valid: true},
	}
	if got := accountBalance(filled); got != "412.50" {
		t.Errorf("accountBalance = %q, want %q", got, "412.50")
	}
	if got := accountLimit(filled); got != "10000.00" {
		t.Errorf("accountLimit = %q, want %q", got, "10000.00")
	}
}

func TestAccountKindPrefersTheSubtype(t *testing.T) {
	if got := accountKind(model.Account{Type: "credit", Subtype: "credit card"}); got != "credit card" {
		t.Errorf("accountKind = %q, want %q", got, "credit card")
	}
	if got := accountKind(model.Account{Type: "depository"}); got != "depository" {
		t.Errorf("accountKind = %q, want the type when there is no subtype", got)
	}
}
