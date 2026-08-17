package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
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

func TestRunSyncWithoutFXOptionUsesNormalPlaidValidation(t *testing.T) {
	err := runSyncWith(context.Background(), settings{}, nil, &fakeFXSource{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("runSyncWith() error = nil, want invalid Plaid configuration")
	}
	if !strings.Contains(err.Error(), "PLAID_CLIENT_ID") {
		t.Errorf("runSyncWith() error = %q, want Plaid validation error", err)
	}
}
