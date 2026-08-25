package app

import (
	"context"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
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
	if len(all.LiabilitiesEnabled) != 2 {
		t.Errorf("all liability flags = %d, want 2", len(all.LiabilitiesEnabled))
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
	if len(one.LiabilitiesEnabled) != 1 {
		t.Fatalf("filtered liability flags = %d, want 1", len(one.LiabilitiesEnabled))
	}
	if _, found := one.LiabilitiesEnabled["item-amex"]; !found {
		t.Error("filtered liability flags do not contain item-amex")
	}
	if _, found := one.LiabilitiesEnabled["item-scotia"]; found {
		t.Error("filtered liability flags contain item-scotia")
	}
	// The institution name comes from the token file, so the list can name a
	// login the database has no institution row for yet.
	if got := one.States[0].Institution; got != "American Express" {
		t.Errorf("state institution = %q, want American Express", got)
	}
}

func TestAccountsExposesLiabilityConfiguration(t *testing.T) {
	ctx := context.Background()
	cfg := tempConfig(t, "sandbox")
	oldItem := item("item-old", "Old Bank", "sandbox")
	enabled := item("item-enabled", "Enabled Bank", "sandbox")
	enabled.Liabilities = true
	consent := item("item-consent", "Consent Bank", "sandbox")
	consent.Liabilities = true
	incidental := item("item-incidental", "Incidental Bank", "sandbox")
	incidental.Liabilities = true
	seedTokens(t, cfg.TokensPath, oldItem, enabled, consent, incidental)
	seedStore(t, cfg.DBPath, oldItem, enabled, consent, incidental)

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := db.SetStatus(ctx, plaid.ProviderName, consent.ItemID,
		"fourseas:liabilities-consent-required: consent is required"); err != nil {
		db.Close()
		t.Fatalf("set consent status: %v", err)
	}
	if err := db.SetStatus(ctx, plaid.ProviderName, incidental.ItemID,
		"unrelated text before plaid ADDITIONAL_CONSENT_REQUIRED (ITEM_ERROR): not a persisted app marker"); err != nil {
		db.Close()
		t.Fatalf("set incidental status: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	data, err := New(cfg).Accounts(ctx, "")
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if data.LiabilitiesEnabled[oldItem.ItemID] {
		t.Error("old Item liabilities = true, want false")
	}
	for _, itemID := range []string{enabled.ItemID, consent.ItemID, incidental.ItemID} {
		if !data.LiabilitiesEnabled[itemID] {
			t.Errorf("%s liabilities = false, want true", itemID)
		}
	}

	states := make(map[string]SyncState, len(data.States))
	for _, state := range data.States {
		states[state.ItemID] = state
	}
	if !states[consent.ItemID].LiabilitiesConsentRequired {
		t.Error("coded consent status was not classified")
	}
	if states[incidental.ItemID].LiabilitiesConsentRequired {
		t.Error("incidental consent text was classified as a coded Plaid error")
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
