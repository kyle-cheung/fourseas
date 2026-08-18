package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	plaidprovider "github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// The sync engine itself lives in internal/app. See internal/app/sync_test.go.

func TestRunSyncFXOnlyBypassesPlaidConfigAndTokenLoading(t *testing.T) {
	cfg := settings{
		dbPath:     filepath.Join(t.TempDir(), "fourseas.duckdb"),
		tokensPath: filepath.Join(t.TempDir(), "missing-tokens.json"),
	}
	var out bytes.Buffer

	if err := runSyncWith(context.Background(), cfg, []string{"--fx"}, &fakeFXSource{}, &out); err != nil {
		t.Fatalf("runSyncWith(--fx) error = %v", err)
	}
	if got, want := out.String(), "No FX rates are required.\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRunSyncRejectsUnknownFXOptionsBeforePlaidValidation(t *testing.T) {
	for _, options := range [][]string{
		{"--future"},
		{"--fx", "--future"},
	} {
		err := runSyncWith(context.Background(), settings{}, options, &fakeFXSource{}, &bytes.Buffer{})
		if err == nil {
			t.Fatalf("runSyncWith(%q) error = nil, want an error", options)
		}
		for _, option := range options {
			if !strings.Contains(err.Error(), option) {
				t.Errorf("runSyncWith(%q) error = %q, want option %q", options, err, option)
			}
		}
		if strings.Contains(err.Error(), "PLAID_CLIENT_ID") {
			t.Errorf("runSyncWith(%q) error = %q, parsed after Plaid validation", options, err)
		}
	}
}

// Ctrl+c is not a failed card. With one linked card the old count made
// attempted equal failed and reported "every card failed to sync", and the FX
// phase then ran on the dead context and blamed FX for the stop.
func TestRunSyncStopsAtACancellationWithoutTheFXPhase(t *testing.T) {
	dir := t.TempDir()
	cfg := settings{
		plaid:      plaidprovider.Config{ClientID: "id", Secret: "secret", Env: "sandbox", LinkPort: 8080},
		dbPath:     filepath.Join(dir, "fourseas.duckdb"),
		tokensPath: filepath.Join(dir, "tokens.json"),
	}
	if err := tokens.Save(cfg.tokensPath, tokens.File{Items: []tokens.Item{{
		ItemID: "item-amex", AccessToken: "token", Institution: "American Express", Env: "sandbox",
	}}}); err != nil {
		t.Fatalf("save tokens: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	source := &fakeFXSource{}
	var out bytes.Buffer
	err := runSyncWith(ctx, cfg, nil, source, &out)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runSyncWith() error = %v, want the cancellation", err)
	}
	if strings.Contains(err.Error(), "every card failed") {
		t.Errorf("runSyncWith() error = %q, want a stop, not a failed sync", err)
	}
	if len(source.calls) != 0 {
		t.Errorf("the FX source was asked %d times, want none after a cancellation", len(source.calls))
	}
}

func TestRunSyncWithoutFXOptionUsesNormalPlaidValidation(t *testing.T) {
	err := runSyncWith(context.Background(), settings{}, nil, &fakeFXSource{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("runSyncWith() error = nil, want invalid Plaid configuration")
	}
	if !strings.Contains(err.Error(), "PLAID_CLIENT_ID") {
		t.Errorf("runSyncWith() error = %q, want Plaid validation error", err)
	}
}
