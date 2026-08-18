package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// testPort is a link port that passes Plaid validation.
const testPort = 8080

// validPlaid is a Plaid configuration that passes Validate.
func validPlaid(env string) plaid.Config {
	return plaid.Config{ClientID: "id", Secret: "secret", Env: env, LinkPort: testPort}
}

// tempConfig returns a configuration whose database and token file are in a
// temporary directory. Neither file exists yet.
func tempConfig(t *testing.T, env string) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		Plaid:      validPlaid(env),
		DBPath:     filepath.Join(dir, "fourseas.duckdb"),
		TokensPath: filepath.Join(dir, "tokens.json"),
	}
}

// blockedPath returns a path that cannot be read or created, because its
// parent is a regular file. It makes both tokens.Load and store.Open fail.
func blockedPath(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	return filepath.Join(file, "child", "name")
}

// skipAsRoot leaves out a test whose only lever is a file permission. Root
// ignores the permission bits of a file it owns, so such a test would report a
// pass it did not earn.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions, so this test cannot make the write fail")
	}
}

// item is one linked institution of the given environment.
func item(itemID, institution, env string) tokens.Item {
	return tokens.Item{
		ItemID:      itemID,
		AccessToken: itemID + "-token",
		Institution: institution,
		Env:         env,
		LinkedAt:    time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
}

// seedTokens writes the token file the operations read.
func seedTokens(t *testing.T, path string, items ...tokens.Item) {
	t.Helper()
	if err := tokens.Save(path, tokens.File{Items: items}); err != nil {
		t.Fatalf("save tokens: %v", err)
	}
}

// loadTokens reads the token file back.
func loadTokens(t *testing.T, path string) tokens.File {
	t.Helper()
	saved, err := tokens.Load(path)
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}
	return saved
}

// seedStore gives each item one institution, one account, one transaction, and
// one sync state, then closes the database so an operation can open it.
func seedStore(t *testing.T, path string, items ...tokens.Item) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	for _, it := range items {
		institution := model.Institution{
			Provider:        plaid.ProviderName,
			ItemID:          it.ItemID,
			InstitutionName: it.Institution,
			Env:             it.Env,
			LinkedAt:        it.LinkedAt,
		}
		if err := db.UpsertInstitution(ctx, institution); err != nil {
			t.Fatalf("upsert institution: %v", err)
		}
		if err := db.UpsertAccounts(ctx, []model.Account{testAccount(it.ItemID)}); err != nil {
			t.Fatalf("upsert accounts: %v", err)
		}
		if err := db.Upsert(ctx, []model.Transaction{testTransaction(it.ItemID)}); err != nil {
			t.Fatalf("upsert transactions: %v", err)
		}
		if err := db.SetCursor(ctx, plaid.ProviderName, it.ItemID, "cursor-"+it.ItemID); err != nil {
			t.Fatalf("set cursor: %v", err)
		}
	}
}

func testAccount(itemID string) model.Account {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	return model.Account{
		Provider:    plaid.ProviderName,
		AccountID:   "acct-" + itemID,
		ItemID:      itemID,
		Name:        "Gold Card",
		Currency:    "USD",
		Tracked:     true,
		FirstSeenAt: now,
		LastSeenAt:  now,
	}
}

func testTransaction(itemID string) model.Transaction {
	return model.Transaction{
		Provider:   plaid.ProviderName,
		ExternalID: "txn-" + itemID,
		ItemID:     itemID,
		AccountID:  "acct-" + itemID,
		Date:       time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		Name:       "coffee",
		Amount:     decimal.NewFromInt(10),
		Currency:   "USD",
	}
}

// itemCounts reports what one item still holds in the database.
func itemCounts(t *testing.T, dbPath, itemID string) store.Removed {
	t.Helper()
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	counts, err := db.ItemCounts(context.Background(), plaid.ProviderName, itemID)
	if err != nil {
		t.Fatalf("item counts: %v", err)
	}
	return counts
}

func TestLabelFallsBackToTheItemID(t *testing.T) {
	tests := []struct {
		name string
		item tokens.Item
		want string
	}{
		{"an institution name", tokens.Item{ItemID: "item-1", Institution: "Amex"}, "Amex"},
		{"no institution name", tokens.Item{ItemID: "item-1"}, "item-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Label(tt.item); got != tt.want {
				t.Errorf("Label = %q, want %q", got, tt.want)
			}
		})
	}
}
