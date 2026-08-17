package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	plaidprovider "github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

func linked() tokens.File {
	return tokens.File{Items: []tokens.Item{
		{ItemID: "item-amex", AccessToken: "amex-token", Institution: "American Express", Env: "sandbox"},
		{ItemID: "item-scotia", AccessToken: "scotia-token", Institution: "Scotiabank"},
	}}
}

// plaidRow is one stored transaction of one item.
func plaidRow(id, itemID string) model.Transaction {
	row := row(id, "2026-08-10")
	row.Provider, row.ItemID = plaidprovider.ProviderName, itemID
	row.AccountID = "acct-" + itemID
	return row
}

// seedUnlink writes two linked items to a database and a token file, then
// closes the database so that the command under test can open it.
func seedUnlink(t *testing.T) settings {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	cfg := settings{
		plaid: plaidprovider.Config{
			ClientID: "id", Secret: "secret", Env: "sandbox", LinkPort: defaultLinkPort,
		},
		dbPath:     filepath.Join(dir, "fourseas.duckdb"),
		tokensPath: filepath.Join(dir, "tokens.json"),
	}

	db, err := store.Open(cfg.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	rows := []model.Transaction{plaidRow("txn-amex", "item-amex"), plaidRow("txn-scotia", "item-scotia")}
	if err := db.Upsert(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for _, item := range linked().Items {
		if err := recordInstitution(ctx, db, item, "sandbox"); err != nil {
			t.Fatalf("record institution: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	if err := tokens.Save(cfg.tokensPath, linked()); err != nil {
		t.Fatalf("save tokens: %v", err)
	}
	return cfg
}

// storedItems reports how many transactions and institutions each item still has.
func storedItems(t *testing.T, cfg settings) map[string]store.Removed {
	t.Helper()
	db, err := store.Open(cfg.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	out := make(map[string]store.Removed)
	for _, itemID := range []string{"item-amex", "item-scotia"} {
		counts, err := db.ItemCounts(context.Background(), plaidprovider.ProviderName, itemID)
		if err != nil {
			t.Fatalf("item counts: %v", err)
		}
		out[itemID] = counts
	}
	return out
}

func loadTokens(t *testing.T, cfg settings) tokens.File {
	t.Helper()
	saved, err := tokens.Load(cfg.tokensPath)
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}
	return saved
}

func TestUnlinkRemovesTheItemAtPlaidThenLocally(t *testing.T) {
	cfg := seedUnlink(t)
	var out strings.Builder
	tokensRemoved := ""

	remove := func(_ context.Context, _ plaidprovider.Config, accessToken string) error {
		tokensRemoved = accessToken
		return nil
	}
	err := runUnlinkWith(context.Background(), cfg, []string{"item-amex"},
		strings.NewReader(" YES \n"), &out, remove)
	if err != nil {
		t.Fatalf("runUnlink: %v", err)
	}

	if tokensRemoved != "amex-token" {
		t.Errorf("removed the token %q at Plaid, want amex-token", tokensRemoved)
	}
	stored := storedItems(t, cfg)
	if stored["item-amex"] != (store.Removed{}) {
		t.Errorf("item-amex still holds %+v, want nothing", stored["item-amex"])
	}
	if stored["item-scotia"].Transactions != 1 || stored["item-scotia"].Institutions != 1 {
		t.Errorf("item-scotia holds %+v, want its rows kept", stored["item-scotia"])
	}
	if _, found := loadTokens(t, cfg).Find("item-amex"); found {
		t.Error("the token of the removed item is still in the token file")
	}
	if _, found := loadTokens(t, cfg).Find("item-scotia"); !found {
		t.Error("the token of the other item was deleted")
	}
}

// The confirmation is what stands between the user and a final removal.
func TestUnlinkDeletesNothingWhenTheAnswerIsNo(t *testing.T) {
	cfg := seedUnlink(t)
	var out strings.Builder
	called := false

	remove := func(_ context.Context, _ plaidprovider.Config, _ string) error {
		called = true
		return nil
	}
	err := runUnlinkWith(context.Background(), cfg, []string{"item-amex"},
		strings.NewReader("no\n"), &out, remove)
	if err != nil {
		t.Fatalf("runUnlink: %v", err)
	}

	if called {
		t.Error("Plaid was called after the answer no")
	}
	if stored := storedItems(t, cfg); stored["item-amex"].Transactions != 1 {
		t.Errorf("item-amex holds %+v, want its rows kept", stored["item-amex"])
	}
	if _, found := loadTokens(t, cfg).Find("item-amex"); !found {
		t.Error("the token was deleted after the answer no")
	}
}

// A removal that failed at Plaid leaves a live item, so the local data and the
// token must stay: they are the only way to try again.
func TestUnlinkKeepsEverythingWhenPlaidFails(t *testing.T) {
	cfg := seedUnlink(t)
	var out strings.Builder

	remove := func(_ context.Context, _ plaidprovider.Config, _ string) error {
		return errors.New("plaid is down")
	}
	err := runUnlinkWith(context.Background(), cfg, []string{"item-amex", "--yes"},
		strings.NewReader(""), &out, remove)
	if err == nil {
		t.Fatal("error = nil, want the Plaid failure")
	}

	if stored := storedItems(t, cfg); stored["item-amex"].Transactions != 1 {
		t.Errorf("item-amex holds %+v, want its rows kept", stored["item-amex"])
	}
	if _, found := loadTokens(t, cfg).Find("item-amex"); !found {
		t.Error("the token was deleted although Plaid still has the item")
	}
}

// An item Plaid does not hold is the state the command wanted, so the local
// delete goes ahead.
func TestUnlinkContinuesWhenPlaidHasNoSuchItem(t *testing.T) {
	cfg := seedUnlink(t)
	var out strings.Builder

	remove := func(_ context.Context, _ plaidprovider.Config, _ string) error {
		return fmt.Errorf("remove item: %w: plaid ITEM_NOT_FOUND", provider.ErrItemGone)
	}
	err := runUnlinkWith(context.Background(), cfg, []string{"item-amex", "--yes"},
		strings.NewReader(""), &out, remove)
	if err != nil {
		t.Fatalf("runUnlink: %v", err)
	}

	if stored := storedItems(t, cfg); stored["item-amex"] != (store.Removed{}) {
		t.Errorf("item-amex still holds %+v, want nothing", stored["item-amex"])
	}
	if _, found := loadTokens(t, cfg).Find("item-amex"); found {
		t.Error("the token of the removed item is still in the token file")
	}
}

func TestUnlinkListShowsTheItemIds(t *testing.T) {
	cfg := seedUnlink(t)
	var out strings.Builder

	remove := func(_ context.Context, _ plaidprovider.Config, _ string) error {
		t.Error("--list called Plaid")
		return nil
	}
	err := runUnlinkWith(context.Background(), cfg, []string{"--list"},
		strings.NewReader(""), &out, remove)
	if err != nil {
		t.Fatalf("runUnlink: %v", err)
	}

	for _, want := range []string{"item-amex", "item-scotia", "American Express"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list = %q, want it to contain %q", out.String(), want)
		}
	}
}

// The environment of an item is checked by app.UnlinkPreview. See
// internal/app/unlink_test.go.
func TestPlanUnlinkRejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		options []string
	}{
		{"no item id", nil},
		{"an unknown option", []string{"item-amex", "--force"}},
		{"an item that is not linked", []string{"item-nothing"}},
		{"two items in one call", []string{"item-amex", "item-scotia"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := planUnlink(linked(), tt.options); err == nil {
				t.Errorf("planUnlink(%q) error = nil, want an error", tt.options)
			}
		})
	}
}
