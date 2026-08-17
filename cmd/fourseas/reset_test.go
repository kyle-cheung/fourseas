package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
)

// resetSettings stores one row, then closes the database so that reset can
// open it.
func resetSettings(t *testing.T) settings {
	t.Helper()
	cfg := settings{dbPath: filepath.Join(t.TempDir(), "fourseas.duckdb")}

	db, err := store.Open(cfg.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := db.Upsert(context.Background(), []model.Transaction{row("txn-1", "2026-08-10")}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return cfg
}

// storedRows counts what the database holds now.
func storedRows(t *testing.T, cfg settings) int {
	t.Helper()
	db, err := store.Open(cfg.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	rows, err := db.Newest(context.Background(), 10)
	if err != nil {
		t.Fatalf("newest: %v", err)
	}
	return len(rows)
}

func TestResetDropsNothingWhenTheAnswerIsNo(t *testing.T) {
	cfg := resetSettings(t)
	var out strings.Builder

	if err := runResetWith(context.Background(), cfg, nil, strings.NewReader("no\n"), &out); err != nil {
		t.Fatalf("runReset: %v", err)
	}
	if got := storedRows(t, cfg); got != 1 {
		t.Errorf("%d rows after the answer no, want 1", got)
	}
	if !strings.Contains(out.String(), "Nothing was changed.") {
		t.Errorf("output = %q, want it to say nothing was changed", out.String())
	}
}

func TestResetDropsEverythingWhenTheAnswerIsYes(t *testing.T) {
	cfg := resetSettings(t)
	var out strings.Builder

	if err := runResetWith(context.Background(), cfg, nil, strings.NewReader("yes\n"), &out); err != nil {
		t.Fatalf("runReset: %v", err)
	}
	if got := storedRows(t, cfg); got != 0 {
		t.Errorf("%d rows after the answer yes, want 0", got)
	}
}

// Nothing on the input is not agreement.
func TestResetTreatsEndOfInputAsNo(t *testing.T) {
	cfg := resetSettings(t)
	var out strings.Builder

	if err := runResetWith(context.Background(), cfg, nil, strings.NewReader(""), &out); err != nil {
		t.Fatalf("runReset: %v", err)
	}
	if got := storedRows(t, cfg); got != 1 {
		t.Errorf("%d rows after no answer, want 1", got)
	}
}

func TestResetRejectsAnUnknownOption(t *testing.T) {
	cfg := resetSettings(t)
	err := runResetWith(context.Background(), cfg, []string{"--force"},
		strings.NewReader(""), &strings.Builder{})
	if err == nil {
		t.Error("error = nil, want an error")
	}
}
