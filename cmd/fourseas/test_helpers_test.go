package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/shopspring/decimal"
)

// row is one stored transaction of the test item.
func row(id string, date string) model.Transaction {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return model.Transaction{
		Provider:   "fake",
		ExternalID: id,
		ItemID:     "item-1",
		Date:       d,
		Name:       id,
		Amount:     decimal.NewFromInt(10),
		Currency:   "USD",
	}
}

// newTestStore opens an empty database in a temporary directory.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "fourseas.duckdb"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// account is one stored account of the test item.
func account(id, balance string) model.Account {
	return model.Account{
		Provider:       "fake",
		AccountID:      id,
		ItemID:         "item-1",
		Name:           "Gold Card",
		Currency:       "USD",
		Tracked:        true,
		BalanceCurrent: decimal.NullDecimal{Decimal: decimal.RequireFromString(balance), Valid: true},
		FirstSeenAt:    time.Now().UTC(),
		LastSeenAt:     time.Now().UTC(),
	}
}
