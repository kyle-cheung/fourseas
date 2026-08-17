package app

import (
	"context"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
)

// linkOK is a browser step that always completes.
func linkOK(context.Context, plaid.Config, int) (plaid.LinkResult, error) {
	return plaid.LinkResult{
		ItemID: "item-new", AccessToken: "secret", Institution: "TD Canada Trust",
	}, nil
}

// linkNever fails the test when the browser step is reached.
func linkNever(t *testing.T) linkFunc {
	return func(context.Context, plaid.Config, int) (plaid.LinkResult, error) {
		t.Error("the browser step ran although the settings are not usable")
		return plaid.LinkResult{}, nil
	}
}

// The token is what names a live item at Plaid, so it must be on disk before
// Link returns.
func TestLinkSavesCompletedItem(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	link := func(context.Context, plaid.Config, int) (plaid.LinkResult, error) {
		return plaid.LinkResult{
			ItemID: "item-new", AccessToken: "secret", Institution: "TD Canada Trust",
		}, nil
	}
	var reported []string

	linked, err := newWith(cfg, link, nil, nil).Link(context.Background(), 730,
		func(text string) { reported = append(reported, text) })
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	if linked.ItemID != "item-new" || linked.Institution != "TD Canada Trust" {
		t.Errorf("linked = %+v, want item-new at TD Canada Trust", linked)
	}
	saved, found := loadTokens(t, cfg.TokensPath).Find("item-new")
	if !found {
		t.Fatal("the token file has no item-new after Link returned")
	}
	if saved.AccessToken != "secret" || saved.Env != "sandbox" {
		t.Errorf("saved = %+v, want the secret token in sandbox", saved)
	}
	if saved.LinkedAt.IsZero() {
		t.Error("the saved item has no link time")
	}
	if len(reported) == 0 {
		t.Error("Link reported no progress")
	}
}

// Bad settings must be found before anything on disk is touched, so nothing
// can fail halfway.
func TestLinkValidatesBeforeLoadingTokens(t *testing.T) {
	cfg := Config{
		Plaid:      plaid.Config{Env: "sandbox", LinkPort: testPort},
		DBPath:     blockedPath(t),
		TokensPath: blockedPath(t),
	}

	_, err := newWith(cfg, linkNever(t), nil, nil).Link(context.Background(), 90, nil)
	if err == nil {
		t.Fatal("error = nil, want the Plaid validation failure")
	}
	if !strings.Contains(err.Error(), "PLAID_CLIENT_ID") {
		t.Errorf("error = %q, want the Plaid validation failure", err)
	}
}

func TestLinkRejectsOutOfRangeBeforeLoadingTokens(t *testing.T) {
	cfg := Config{
		Plaid:      validPlaid("sandbox"),
		DBPath:     blockedPath(t),
		TokensPath: blockedPath(t),
	}

	for _, days := range []int{MinLinkDays - 1, MaxLinkDays + 1} {
		_, err := newWith(cfg, linkNever(t), nil, nil).Link(context.Background(), days, nil)
		if err == nil {
			t.Fatalf("Link(%d) error = nil, want the day range failure", days)
		}
		if !strings.Contains(err.Error(), "history must be from 30 through 730 days") {
			t.Errorf("Link(%d) error = %q, want the day range failure", days, err)
		}
	}
}

// The database is no part of linking, and the browser step can take ten
// minutes. Holding DuckDB open for that time buys nothing.
func TestLinkDoesNotOpenDuckDB(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	cfg.DBPath = blockedPath(t)

	if _, err := newWith(cfg, linkOK, nil, nil).Link(context.Background(), 730, nil); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if _, found := loadTokens(t, cfg.TokensPath).Find("item-new"); !found {
		t.Error("the token was not saved although the database is not part of Link")
	}
}
