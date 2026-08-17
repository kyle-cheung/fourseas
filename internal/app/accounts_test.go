package app

import (
	"context"
	"testing"
)

func TestAccountsFiltersOneItemAndIncludesSyncState(t *testing.T) {
	ctx := context.Background()
	cfg := tempConfig(t, "sandbox")
	amex := item("item-amex", "American Express", "sandbox")
	scotia := item("item-scotia", "Scotiabank", "sandbox")
	seedTokens(t, cfg.TokensPath, amex, scotia)
	seedStore(t, cfg.DBPath, amex, scotia)
	client := New(cfg)

	all, err := client.Accounts(ctx, "")
	if err != nil {
		t.Fatalf("Accounts(all): %v", err)
	}
	if len(all.Accounts) != 2 || len(all.States) != 2 {
		t.Fatalf("all = %d accounts and %d states, want 2 and 2",
			len(all.Accounts), len(all.States))
	}

	one, err := client.Accounts(ctx, "item-amex")
	if err != nil {
		t.Fatalf("Accounts(item-amex): %v", err)
	}
	if len(one.Accounts) != 1 || one.Accounts[0].ItemID != "item-amex" {
		t.Fatalf("filtered accounts = %+v, want only item-amex", one.Accounts)
	}
	if len(one.States) != 1 || one.States[0].ItemID != "item-amex" {
		t.Fatalf("filtered states = %+v, want only item-amex", one.States)
	}
	// The institution name comes from the token file, so the list can name a
	// login the database has no institution row for yet.
	if got := one.States[0].Institution; got != "American Express" {
		t.Errorf("state institution = %q, want American Express", got)
	}
}

func TestSetNicknameSetsAndClears(t *testing.T) {
	ctx := context.Background()
	cfg := tempConfig(t, "sandbox")
	amex := item("item-amex", "American Express", "sandbox")
	seedTokens(t, cfg.TokensPath, amex)
	seedStore(t, cfg.DBPath, amex)
	client := New(cfg)

	if err := client.SetNickname(ctx, "acct-item-amex", "  Travel card  "); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}
	if got := nickname(t, client, "acct-item-amex"); got != "Travel card" {
		t.Errorf("nickname = %q, want the trimmed Travel card", got)
	}

	if err := client.SetNickname(ctx, "acct-item-amex", "   "); err != nil {
		t.Fatalf("SetNickname(blank): %v", err)
	}
	if got := nickname(t, client, "acct-item-amex"); got != "" {
		t.Errorf("nickname = %q, want it cleared", got)
	}

	if err := client.SetNickname(ctx, "acct-nothing", "Travel card"); err == nil {
		t.Error("error = nil, want an unknown account to be refused")
	}
}

func nickname(t *testing.T, client *App, accountID string) string {
	t.Helper()
	data, err := client.Accounts(context.Background(), "")
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	for _, view := range data.Accounts {
		if view.AccountID == accountID {
			return view.Nickname
		}
	}
	t.Fatalf("no stored account has id %q", accountID)
	return ""
}
