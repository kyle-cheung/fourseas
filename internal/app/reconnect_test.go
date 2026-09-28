package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

func TestReconnectPreservesLocalDataAndSettings(t *testing.T) {
	for _, outcome := range []error{nil, context.Canceled, errors.New("bank unavailable")} {
		t.Run(fmt.Sprint(outcome), func(t *testing.T) {
			cfg := tempConfig(t, "sandbox")
			linked := item("item-1", "Wealthsimple", "sandbox")
			linked.Liabilities = true
			seedTokens(t, cfg.TokensPath, linked, item("item-2", "Other", "sandbox"))
			seedStore(t, cfg.DBPath, linked)
			a := New(cfg)
			if err := a.SetNickname(context.Background(), "acct-item-1", "Savings"); err != nil {
				t.Fatal(err)
			}
			before, err := a.Accounts(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			beforeTokens, _ := os.ReadFile(cfg.TokensPath)
			beforeCounts := itemCounts(t, cfg.DBPath, linked.ItemID)
			calls := 0
			a.reconnect = func(_ context.Context, got plaid.Config, token string) (plaid.LinkResult, error) {
				calls++
				if got != cfg.Plaid || token != linked.AccessToken {
					t.Fatal("wrong reconnect target")
				}
				return plaid.LinkResult{}, outcome
			}
			result, err := a.Reconnect(context.Background(), linked.ItemID, nil)
			if !errors.Is(err, outcome) || calls != 1 {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			if outcome == nil && (result.ItemID != linked.ItemID || result.Institution != linked.Institution) {
				t.Fatalf("result=%+v", result)
			}
			afterTokens, _ := os.ReadFile(cfg.TokensPath)
			if string(beforeTokens) != string(afterTokens) {
				t.Fatal("token file changed")
			}
			after, err := a.Accounts(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || beforeCounts != itemCounts(t, cfg.DBPath, linked.ItemID) {
				t.Fatal("local data changed")
			}
			db, err := store.Open(cfg.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cursor, err := db.Cursor(context.Background(), plaid.ProviderName, linked.ItemID)
			if err != nil || cursor != "cursor-item-1" {
				t.Fatalf("cursor=%q error=%v", cursor, err)
			}
		})
	}
}

func TestReconnectValidatesBeforeOpeningBrowser(t *testing.T) {
	for _, scenario := range []string{"unknown", "wrong environment", "empty token", "invalid config", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := tempConfig(t, "sandbox")
			linked := item("item-1", "Wealthsimple", "sandbox")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "wrong environment":
				linked.Env = "production"
			case "empty token":
				linked.AccessToken = ""
			case "invalid config":
				cfg.Plaid.ClientID = ""
			case "canceled":
				cancel()
			}
			seedTokens(t, cfg.TokensPath, linked)
			a := New(cfg)
			a.reconnect = func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
				t.Fatal("browser opened")
				return plaid.LinkResult{}, nil
			}
			id := linked.ItemID
			if scenario == "unknown" {
				id = "missing"
			}
			if _, err := a.Reconnect(ctx, id, nil); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestReconnectRedactsTokensAndRechecksTarget(t *testing.T) {
	for _, scenario := range []string{"provider failure", "removed", "replaced", "other item added"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := tempConfig(t, "sandbox")
			linked := item("item-1", "Wealthsimple", "sandbox")
			seedTokens(t, cfg.TokensPath, linked)
			a := New(cfg)
			a.reconnect = func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
				switch scenario {
				case "provider failure":
					return plaid.LinkResult{}, fmt.Errorf("%s: %w", linked.AccessToken, context.Canceled)
				case "removed":
					seedTokens(t, cfg.TokensPath)
				case "replaced":
					replacement := linked
					replacement.AccessToken = "replacement-secret"
					seedTokens(t, cfg.TokensPath, replacement)
				case "other item added":
					seedTokens(t, cfg.TokensPath, linked, item("item-2", "Other", "sandbox"))
				}
				return plaid.LinkResult{}, nil
			}
			_, err := a.Reconnect(context.Background(), linked.ItemID, nil)
			if scenario == "other item added" {
				if err != nil || len(loadTokens(t, cfg.TokensPath).Items) != 2 {
					t.Fatalf("concurrent addition lost: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			assertNoAccessToken(t, linked.AccessToken, err.Error(), fmt.Sprintf("%#v", err))
			if scenario == "provider failure" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation identity")
			}
		})
	}
}

func TestSyncStatesRecognizeSavedLoginRequired(t *testing.T) {
	for _, tt := range []struct {
		status                  string
		login, consent, pending bool
	}{
		{status: "fourseas:login-required: details", login: true},
		{status: "sync item item-1: plaid ITEM_LOGIN_REQUIRED (ITEM_ERROR): login needed", login: true},
		{status: "fourseas:liabilities-consent-required: details", consent: true},
		{status: "fourseas:reconnected:sync-pending", pending: true},
		{status: "ITEM_LOGIN_REQUIRED"},
		{status: "ok"},
		{status: "unrelated error"},
	} {
		got := syncStates([]model.SyncState{{ItemID: "item-1", LastStatus: tt.status}}, tokens.File{}, "")[0]
		if got.ReconnectRequired != tt.login || got.LiabilitiesConsentRequired != tt.consent || got.ReconnectSyncPending != tt.pending {
			t.Fatalf("status=%q classification=%+v", tt.status, got)
		}
	}
}

func TestLoginRequiredRecoveryKeepsCursorAndClearsStatusAfterSync(t *testing.T) {
	ctx := context.Background()
	cfg := tempConfig(t, "sandbox")
	broken := item("item-1", "Wealthsimple", "sandbox")
	healthy := item("item-2", "Other", "sandbox")
	seedTokens(t, cfg.TokensPath, broken, healthy)
	seedStore(t, cfg.DBPath, broken, healthy)
	badSource := &fakeSource{name: plaid.ProviderName, errs: []error{provider.ErrLoginRequired}}
	goodSource := &fakeSource{name: plaid.ProviderName, pages: []provider.Batch{{NextCursor: "healthy-cursor"}}}
	a := New(cfg)
	a.source = func(_ plaid.Config, _, id, _ string) (provider.Provider, error) {
		if id == broken.ItemID {
			return badSource, nil
		}
		return goodSource, nil
	}
	results, err := a.SyncAll(ctx, nil)
	if err != nil || len(results) != 2 || !errors.Is(results[0].Err, ErrLoginRequired) || results[1].Err != nil {
		t.Fatalf("results=%+v error=%v", results, err)
	}
	data, err := a.Accounts(ctx, broken.ItemID)
	if err != nil || len(data.States) != 1 || !data.States[0].ReconnectRequired {
		t.Fatalf("missing persisted recovery status: %+v %v", data.States, err)
	}
	a.reconnect = func(context.Context, plaid.Config, string) (plaid.LinkResult, error) { return plaid.LinkResult{}, nil }
	if _, err := a.Reconnect(ctx, broken.ItemID, nil); err != nil {
		t.Fatal(err)
	}
	repaired := &fakeSource{name: plaid.ProviderName, pages: []provider.Batch{{NextCursor: "repaired-cursor"}}}
	a.source = func(plaid.Config, string, string, string) (provider.Provider, error) { return repaired, nil }
	if _, err := a.SyncItem(ctx, broken.ItemID, nil); err != nil {
		t.Fatal(err)
	}
	if len(repaired.cursors) != 1 || repaired.cursors[0] != "cursor-item-1" {
		t.Fatalf("lost saved cursor: %v", repaired.cursors)
	}
	data, err = a.Accounts(ctx, broken.ItemID)
	if err != nil || data.States[0].ReconnectRequired || data.States[0].LastStatus != "ok" {
		t.Fatalf("stale status: %+v %v", data.States, err)
	}
}

func TestReconnectBrowserExitIsCancellation(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	seedTokens(t, cfg.TokensPath, item("item-1", "Wealthsimple", "sandbox"))
	a := New(cfg)
	a.reconnect = func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
		return plaid.LinkResult{}, plaid.ErrLinkClosed
	}
	if _, err := a.Reconnect(context.Background(), "item-1", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want cancellation", err)
	}
}

func TestTestAppDoesNotDefaultToLiveReconnect(t *testing.T) {
	a := newWith(tempConfig(t, "sandbox"), nil, nil, nil, nil, nil)
	if a.reconnect != nil {
		t.Fatal("test constructor installs live Plaid reconnect")
	}
}

func TestReconnectRecordsPendingSyncEvenWhenCallerCancelsAfterSignIn(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	linked := item("item-1", "Wealthsimple", "sandbox")
	seedTokens(t, cfg.TokensPath, linked)
	seedStore(t, cfg.DBPath, linked)
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetStatus(context.Background(), plaid.ProviderName, linked.ItemID, loginRequiredStatus+"details"); err != nil {
		t.Fatal(err)
	}
	before, err := db.SyncStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := New(cfg)
	a.reconnect = func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
		cancel()
		return plaid.LinkResult{}, nil
	}
	if _, err := a.Reconnect(ctx, linked.ItemID, nil); err != nil {
		t.Fatal(err)
	}
	data, err := New(cfg).Accounts(context.Background(), linked.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.States) != 1 || data.States[0].ReconnectRequired || !data.States[0].ReconnectSyncPending {
		t.Fatalf("stale status after sign-in: %+v", data.States)
	}
	db, err = store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	after, err := db.SyncStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after[0].Cursor != before[0].Cursor || !reflect.DeepEqual(after[0].LastSyncedAt, before[0].LastSyncedAt) {
		t.Fatal("status update changed cursor or last sync time")
	}
}

func TestReconnectStatusFailurePreservesAuthenticationOutcome(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	cfg.DBPath = blockedPath(t)
	seedTokens(t, cfg.TokensPath, item("item-1", "Wealthsimple", "sandbox"))
	a := New(cfg)
	a.reconnect = func(context.Context, plaid.Config, string) (plaid.LinkResult, error) { return plaid.LinkResult{}, nil }
	linked, err := a.Reconnect(context.Background(), "item-1", nil)
	var partial *ReconnectStatusError
	if !errors.As(err, &partial) || partial.Item.ItemID != "item-1" || linked != (LinkedItem{}) || !strings.Contains(err.Error(), "sign-in completed") {
		t.Fatalf("lost typed partial success: %+v %v", linked, err)
	}
}
