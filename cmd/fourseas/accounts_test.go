package main

import (
	"context"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

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

// TestNicknameArgsNeedAnIDAndAName proves the command says what it wants
// instead of guessing when the arguments are wrong.
func TestNicknameArgsNeedAnIDAndAName(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"acct-amex"},
		{"acct-amex", "Amex Daily", "extra"},
		{"   ", "Amex Daily"},
	} {
		if _, _, err := nicknameArgs(args); err == nil {
			t.Errorf("nicknameArgs(%q) = no error, want one", args)
		}
	}

	id, nickname, err := nicknameArgs([]string{" acct-amex ", "  Amex Daily  "})
	if err != nil {
		t.Fatalf("nicknameArgs: %v", err)
	}
	if id != "acct-amex" || nickname != "Amex Daily" {
		t.Errorf("nicknameArgs = %q, %q, want the trimmed id and name", id, nickname)
	}

	// An empty name is how a nickname is cleared, so it is not an error.
	if _, nickname, err = nicknameArgs([]string{"acct-amex", ""}); err != nil || nickname != "" {
		t.Errorf("nicknameArgs with an empty name = %q, %v, want it to clear the nickname", nickname, err)
	}
}

// TestSetNicknameShowsUpInTheAccountsList proves the stored nickname reaches
// the rows `fourseas accounts` prints.
func TestSetNicknameShowsUpInTheAccountsList(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	stored := account("acct-1", "10.00")
	if err := db.UpsertAccounts(ctx, []model.Account{stored}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}
	if err := setNickname(ctx, db, "acct-1", "Amex Daily"); err != nil {
		t.Fatalf("set nickname: %v", err)
	}

	views, err := db.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	if views[0].Nickname != "Amex Daily" {
		t.Errorf("Nickname = %q, want the nickname in the accounts list", views[0].Nickname)
	}
	if views[0].Name != stored.Name {
		t.Errorf("Name = %q, want the provider's name %q to stay", views[0].Name, stored.Name)
	}
}

// TestSetNicknameReportsAnUnknownID proves a mistyped id fails loudly.
func TestSetNicknameReportsAnUnknownID(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	err := setNickname(ctx, db, "acct-typo", "Amex Daily")
	if err == nil {
		t.Fatal("setNickname on an unknown id = no error, want one")
	}
	if !strings.Contains(err.Error(), "acct-typo") {
		t.Errorf("error %q does not name the account id", err)
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
