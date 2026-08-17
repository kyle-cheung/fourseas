package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
)

// An access token only works in the environment that issued it. Removing an
// item needs Plaid, so a mismatch is refused first and says what to change,
// instead of failing at Plaid with the item still live and billed.
func TestUnlinkPreviewRejectsAnotherEnvironment(t *testing.T) {
	cfg := tempConfig(t, "production")
	// Neither the Plaid settings nor the database are usable, so an error from
	// either one would show that the environment check ran too late.
	cfg.Plaid = plaid.Config{Env: "production", LinkPort: testPort}
	cfg.DBPath = blockedPath(t)
	seedTokens(t, cfg.TokensPath, item("item-amex", "American Express", "sandbox"))

	_, err := New(cfg).UnlinkPreview(context.Background(), "item-amex")
	if err == nil {
		t.Fatal("error = nil, want the environment mismatch")
	}
	for _, want := range []string{"sandbox", "production", "PLAID_ENV"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "PLAID_CLIENT_ID") {
		t.Errorf("error = %q, want the environment mismatch before Plaid validation", err)
	}
}

// An item linked before the environment was recorded has to be removable, and
// trying is the only way to know where it works.
func TestUnlinkPreviewAcceptsAnItemWithNoRecordedEnvironment(t *testing.T) {
	cfg := tempConfig(t, "production")
	scotia := item("item-scotia", "Scotiabank", "")
	seedTokens(t, cfg.TokensPath, scotia)
	seedStore(t, cfg.DBPath, scotia)

	preview, err := New(cfg).UnlinkPreview(context.Background(), "item-scotia")
	if err != nil {
		t.Fatalf("UnlinkPreview: %v", err)
	}
	if preview.Institution != "Scotiabank" || preview.Rows.Transactions != 1 {
		t.Errorf("preview = %+v, want one Scotiabank transaction", preview)
	}
	if len(preview.Accounts) != 1 {
		t.Errorf("preview accounts = %+v, want the one stored account", preview.Accounts)
	}
}

// Plaid bills every live item. A token deleted on its own leaves the item
// alive with no way left to reach it, so Plaid goes first, then the rows, then
// the token.
func TestUnlinkRemovesPlaidThenRowsThenToken(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	amex := item("item-amex", "American Express", "sandbox")
	scotia := item("item-scotia", "Scotiabank", "sandbox")
	seedTokens(t, cfg.TokensPath, amex, scotia)
	seedStore(t, cfg.DBPath, amex, scotia)

	var (
		removedToken  string
		rowsAtRemove  store.Removed
		tokenAtRemove bool
	)
	remove := func(_ context.Context, _ plaid.Config, accessToken string) error {
		removedToken = accessToken
		rowsAtRemove = itemCounts(t, cfg.DBPath, "item-amex")
		_, tokenAtRemove = loadTokens(t, cfg.TokensPath).Find("item-amex")
		return nil
	}

	result, err := New(cfg, WithRemove(remove)).Unlink(context.Background(), "item-amex", nil)
	if err != nil {
		t.Fatalf("Unlink: %v", err)
	}

	if removedToken != "item-amex-token" {
		t.Errorf("removed %q at Plaid, want item-amex-token", removedToken)
	}
	if rowsAtRemove.Transactions != 1 || !tokenAtRemove {
		t.Errorf("at the Plaid call the rows were %+v and the token present = %v, "+
			"want both still there", rowsAtRemove, tokenAtRemove)
	}
	if result.PlaidItemGone {
		t.Error("PlaidItemGone = true after a plain removal")
	}
	if result.Rows.Transactions != 1 || result.Rows.Accounts != 1 {
		t.Errorf("rows = %+v, want one transaction and one account", result.Rows)
	}
	if got := itemCounts(t, cfg.DBPath, "item-amex"); got != (store.Removed{}) {
		t.Errorf("item-amex still holds %+v, want nothing", got)
	}
	if _, found := loadTokens(t, cfg.TokensPath).Find("item-amex"); found {
		t.Error("the token of the removed item is still in the token file")
	}
	if got := itemCounts(t, cfg.DBPath, "item-scotia"); got.Transactions != 1 {
		t.Errorf("item-scotia holds %+v, want its rows kept", got)
	}
	if _, found := loadTokens(t, cfg.TokensPath).Find("item-scotia"); !found {
		t.Error("the token of the other item was deleted")
	}
}

// A removal that failed at Plaid leaves a live item, so the local data and the
// token must stay: they are the only way to try again.
func TestUnlinkKeepsRowsAndTokenWhenPlaidFails(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	amex := item("item-amex", "American Express", "sandbox")
	seedTokens(t, cfg.TokensPath, amex)
	seedStore(t, cfg.DBPath, amex)

	remove := func(context.Context, plaid.Config, string) error {
		return errors.New("plaid is down")
	}
	_, err := New(cfg, WithRemove(remove)).Unlink(context.Background(), "item-amex", nil)
	if err == nil {
		t.Fatal("error = nil, want the Plaid failure")
	}

	if got := itemCounts(t, cfg.DBPath, "item-amex"); got.Transactions != 1 {
		t.Errorf("item-amex holds %+v, want its rows kept", got)
	}
	if _, found := loadTokens(t, cfg.TokensPath).Find("item-amex"); !found {
		t.Error("the token was deleted although Plaid still has the item")
	}
}

// The token names the local rows. While they are there the token has to stay,
// because a second run needs it.
func TestUnlinkKeepsTokenWhenLocalCleanupFails(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	amex := item("item-amex", "American Express", "sandbox")
	seedTokens(t, cfg.TokensPath, amex)
	seedStore(t, cfg.DBPath, amex)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remove := func(context.Context, plaid.Config, string) error {
		cancel()
		return nil
	}

	_, err := New(cfg, WithRemove(remove)).Unlink(ctx, "item-amex", nil)
	if err == nil {
		t.Fatal("error = nil, want the local cleanup failure")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want the canceled context", err)
	}
	if _, found := loadTokens(t, cfg.TokensPath).Find("item-amex"); !found {
		t.Error("the token was deleted although the local data is still there")
	}
}
