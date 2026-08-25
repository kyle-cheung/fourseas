package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

func seedLiabilitiesAccount(t *testing.T, cfg Config, linked tokens.Item, accountType string) string {
	t.Helper()
	seedTokens(t, cfg.TokensPath, linked)
	seedStore(t, cfg.DBPath, linked)

	accountID := "acct-" + linked.ItemID
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	account := testAccount(linked.ItemID)
	account.Type = accountType
	if err := db.UpsertAccounts(context.Background(), []model.Account{account}); err != nil {
		t.Fatalf("upsert account type: %v", err)
	}
	return accountID
}

func syncStatesForTest(t *testing.T, path string) []model.SyncState {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	states, err := db.SyncStates(context.Background())
	if err != nil {
		t.Fatalf("sync states: %v", err)
	}
	return states
}

func TestEnableLiabilitiesValidatesAndResolvesACreditAccount(t *testing.T) {
	tests := []struct {
		name        string
		accountID   string
		accountType string
		itemEnv     string
		configEnv   string
		invalidCfg  bool
		wantUnknown bool
		wantText    string
	}{
		{name: "unknown account", accountID: "acct-missing", accountType: "credit", itemEnv: "sandbox", configEnv: "sandbox", wantUnknown: true},
		{name: "blank account", accountID: "  ", accountType: "credit", itemEnv: "sandbox", configEnv: "sandbox", wantUnknown: true},
		{name: "non-credit account", accountID: "acct-item-amex", accountType: "depository", itemEnv: "sandbox", configEnv: "sandbox", wantText: "credit"},
		{name: "other environment", accountID: "acct-item-amex", accountType: "credit", itemEnv: "production", configEnv: "sandbox", wantText: "production"},
		{name: "invalid configuration", accountID: "acct-item-amex", accountType: "credit", itemEnv: "sandbox", configEnv: "sandbox", invalidCfg: true, wantText: "PLAID_CLIENT_ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tempConfig(t, tt.configEnv)
			if tt.invalidCfg {
				cfg.Plaid.ClientID = ""
			}
			linked := item("item-amex", "American Express", tt.itemEnv)
			seedLiabilitiesAccount(t, cfg, linked, tt.accountType)
			updateCalls := 0
			liabilityCalls := 0
			client := newWith(cfg, nil, nil, nil,
				func(context.Context, plaid.Config, string) ([]model.CreditLiability, error) {
					liabilityCalls++
					return nil, nil
				},
				func(_ context.Context, _ plaid.Config, accessToken string) (plaid.LinkResult, error) {
					updateCalls++
					if accessToken != linked.AccessToken {
						t.Error("update received an unexpected access token")
					}
					return plaid.LinkResult{}, nil
				},
			)

			err := client.EnableLiabilities(context.Background(), tt.accountID, nil)
			if err == nil {
				t.Fatal("EnableLiabilities error = nil, want validation failure")
			}
			if tt.wantUnknown {
				var unknown *store.UnknownAccountError
				if !errors.As(err, &unknown) {
					t.Fatalf("error = %v, want *store.UnknownAccountError", err)
				}
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantText)
			}
			if updateCalls != 0 || liabilityCalls != 0 {
				t.Errorf("provider calls = update %d, liabilities %d; want none", updateCalls, liabilityCalls)
			}
		})
	}
}

func TestEnableLiabilitiesFailureBeforeConsentChangesNothing(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	linked := item("item-amex", "American Express", "sandbox")
	linked.AccessToken = "test-enable-failure-access-token"
	accountID := seedLiabilitiesAccount(t, cfg, linked, "credit")
	liabilityCalls := 0
	var lines []string
	client := newWith(cfg, nil, nil, nil,
		func(context.Context, plaid.Config, string) ([]model.CreditLiability, error) {
			liabilityCalls++
			return nil, nil
		},
		func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
			return plaid.LinkResult{}, fmt.Errorf("update failed with %s", linked.AccessToken)
		},
	)

	err := client.EnableLiabilities(context.Background(), accountID, collect(&lines))
	if err == nil {
		t.Fatal("EnableLiabilities error = nil, want update failure")
	}
	if liabilityCalls != 0 {
		t.Errorf("liability calls = %d, want 0", liabilityCalls)
	}
	if loadTokens(t, cfg.TokensPath).Items[0].Liabilities {
		t.Error("liability flag is true after update failure")
	}
	if len(lines) != 1 || lines[0] != "Open "+plaid.LinkURL(cfg.Plaid)+" in your browser" {
		t.Errorf("progress = %q, want only the Link URL instruction", lines)
	}
	assertNoAccessToken(t, linked.AccessToken, err.Error(), strings.Join(lines, "\n"))
}

func TestEnableLiabilitiesReloadsTheTokenFileAndDoesNotRecreateARemovedItem(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	linked := item("item-amex", "American Express", "sandbox")
	accountID := seedLiabilitiesAccount(t, cfg, linked, "credit")
	liabilityCalls := 0
	client := newWith(cfg, nil, nil, nil,
		func(context.Context, plaid.Config, string) ([]model.CreditLiability, error) {
			liabilityCalls++
			return nil, nil
		},
		func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
			if err := tokens.Save(cfg.TokensPath, tokens.File{}); err != nil {
				t.Fatalf("remove Item during update: %v", err)
			}
			return plaid.LinkResult{}, nil
		},
	)

	err := client.EnableLiabilities(context.Background(), accountID, nil)
	if err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("EnableLiabilities error = %v, want removed Item failure", err)
	}
	if len(loadTokens(t, cfg.TokensPath).Items) != 0 {
		t.Error("the removed Item was recreated")
	}
	if liabilityCalls != 0 {
		t.Errorf("liability calls = %d, want 0", liabilityCalls)
	}
}

func TestEnableLiabilitiesRejectsAnItemWhoseEnvironmentChangedDuringConsent(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	linked := item("item-amex", "American Express", "")
	accountID := seedLiabilitiesAccount(t, cfg, linked, "credit")
	liabilityCalls := 0
	client := newWith(cfg, nil, nil, nil,
		func(context.Context, plaid.Config, string) ([]model.CreditLiability, error) {
			liabilityCalls++
			return nil, nil
		},
		func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
			changed := loadTokens(t, cfg.TokensPath)
			target, _ := changed.Find(linked.ItemID)
			target.Env = "sandbox"
			if err := tokens.Save(cfg.TokensPath, changed.Upsert(target)); err != nil {
				t.Fatalf("change Item environment during consent: %v", err)
			}
			return plaid.LinkResult{}, nil
		},
	)

	err := client.EnableLiabilities(context.Background(), accountID, nil)
	if err == nil || !strings.Contains(err.Error(), "changed during the update") {
		t.Fatalf("EnableLiabilities error = %v, want changed Item failure", err)
	}
	if liabilityCalls != 0 {
		t.Errorf("liability calls = %d, want 0", liabilityCalls)
	}
	target, found := loadTokens(t, cfg.TokensPath).Find(linked.ItemID)
	if !found || target.Env != "sandbox" || target.Liabilities {
		t.Errorf("changed target = %+v, want its new environment and disabled flag preserved", target)
	}
}

func TestEnableLiabilitiesRejectsAnItemWhoseAccessTokenChangedDuringConsent(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	linked := item("item-amex", "American Express", "sandbox")
	accountID := seedLiabilitiesAccount(t, cfg, linked, "credit")
	const replacementToken = "replacement-access-token"
	liabilityCalls := 0
	client := newWith(cfg, nil, nil, nil,
		func(context.Context, plaid.Config, string) ([]model.CreditLiability, error) {
			liabilityCalls++
			return nil, nil
		},
		func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
			changed := loadTokens(t, cfg.TokensPath)
			target, _ := changed.Find(linked.ItemID)
			target.AccessToken = replacementToken
			if err := tokens.Save(cfg.TokensPath, changed.Upsert(target)); err != nil {
				t.Fatalf("change Item token during consent: %v", err)
			}
			return plaid.LinkResult{}, nil
		},
	)

	err := client.EnableLiabilities(context.Background(), accountID, nil)
	if err == nil || !strings.Contains(err.Error(), "changed during the update") {
		t.Fatalf("EnableLiabilities error = %v, want changed Item failure", err)
	}
	assertNoAccessToken(t, linked.AccessToken, err.Error())
	assertNoAccessToken(t, replacementToken, err.Error())
	if liabilityCalls != 0 {
		t.Errorf("liability calls = %d, want 0", liabilityCalls)
	}
	target, found := loadTokens(t, cfg.TokensPath).Find(linked.ItemID)
	if !found || target.AccessToken != replacementToken || target.Liabilities {
		t.Error("changed target token or disabled flag was not preserved")
	}
}

func TestEnableLiabilitiesSavesConsentThenAppliesTheImmediateFetchPolicy(t *testing.T) {
	oldFetchedAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	newFetchedAt := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	temporaryErr := errors.New("liabilities are temporarily unavailable")
	tests := []struct {
		name            string
		liabilityErr    error
		cancelAfterLink bool
		wantErr         error
		wantOldSnapshot bool
	}{
		{name: "success replaces the snapshot", wantOldSnapshot: false},
		{name: "product not ready preserves the snapshot", liabilityErr: fmt.Errorf("liabilities: %w", provider.ErrProductNotReady), wantOldSnapshot: true},
		{name: "additional consent error preserves the snapshot", liabilityErr: fmt.Errorf("liabilities: %w", provider.ErrAdditionalConsentRequired), wantErr: provider.ErrAdditionalConsentRequired, wantOldSnapshot: true},
		{name: "temporary error preserves the snapshot", liabilityErr: temporaryErr, wantErr: temporaryErr, wantOldSnapshot: true},
		{name: "cancellation after consent preserves the snapshot", cancelAfterLink: true, wantErr: context.Canceled, wantOldSnapshot: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tempConfig(t, "sandbox")
			linked := item("item-amex", "American Express", "sandbox")
			linked.AccessToken = "test-enable-policy-access-token"
			accountID := seedLiabilitiesAccount(t, cfg, linked, "credit")
			seedLiabilitySnapshot(t, cfg.DBPath, linked.ItemID, oldFetchedAt)
			statesBefore := syncStatesForTest(t, cfg.DBPath)
			second := item("item-second", "Second Bank", "sandbox")
			second.AccessToken = "test-second-access-token"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			updateCalls := 0
			liabilityCalls := 0
			sourceCalls := 0
			client := newWith(cfg, nil, nil,
				func(plaid.Config, string, string, string) (provider.Provider, error) {
					sourceCalls++
					return nil, errors.New("transaction source must not be called")
				},
				func(gotCtx context.Context, _ plaid.Config, accessToken string) ([]model.CreditLiability, error) {
					liabilityCalls++
					if accessToken != linked.AccessToken {
						t.Error("liability fetch received an unexpected access token")
					}
					if err := gotCtx.Err(); err != nil {
						return nil, err
					}
					if tt.liabilityErr != nil {
						return nil, fmt.Errorf("fetch with %s: %w", accessToken, tt.liabilityErr)
					}
					return []model.CreditLiability{{
						Provider: plaid.ProviderName, ItemID: linked.ItemID,
						AccountID: accountID, FetchedAt: newFetchedAt,
					}}, nil
				},
				func(_ context.Context, _ plaid.Config, accessToken string) (plaid.LinkResult, error) {
					updateCalls++
					if accessToken != linked.AccessToken {
						t.Error("update received an unexpected access token")
					}
					fresh := loadTokens(t, cfg.TokensPath)
					if err := tokens.Save(cfg.TokensPath, fresh.Upsert(second)); err != nil {
						t.Fatalf("add second Item during update: %v", err)
					}
					if tt.cancelAfterLink {
						cancel()
					}
					return plaid.LinkResult{}, nil
				},
			)

			var lines []string
			err := client.EnableLiabilities(ctx, "  "+accountID+"  ", collect(&lines))
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("EnableLiabilities: %v", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("EnableLiabilities error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				assertNoAccessToken(t, linked.AccessToken, err.Error())
			}
			if updateCalls != 1 || liabilityCalls != 1 {
				t.Errorf("provider calls = update %d, liabilities %d; want 1 each", updateCalls, liabilityCalls)
			}
			if sourceCalls != 0 {
				t.Errorf("transaction source calls = %d, want 0", sourceCalls)
			}
			if len(lines) != 1 {
				t.Errorf("progress lines = %q, want only the Link URL", lines)
			}
			assertNoAccessToken(t, linked.AccessToken, strings.Join(lines, "\n"))

			saved := loadTokens(t, cfg.TokensPath)
			gotTarget, found := saved.Find(linked.ItemID)
			if !found || !gotTarget.Liabilities {
				t.Error("target Item does not have liabilities enabled")
			}
			if gotTarget.AccessToken != linked.AccessToken || gotTarget.ItemID != linked.ItemID {
				t.Error("target Item token or id changed")
			}
			gotSecond, found := saved.Find(second.ItemID)
			if !found || gotSecond.AccessToken != second.AccessToken {
				t.Error("the Item added during Link was not preserved")
			}

			views := accountViews(t, cfg.DBPath)
			if len(views) != 1 || views[0].Liability == nil {
				t.Fatalf("stored views = %+v, want the liability snapshot", views)
			}
			wantFetchedAt := newFetchedAt
			if tt.wantOldSnapshot {
				wantFetchedAt = oldFetchedAt
			}
			if !views[0].Liability.FetchedAt.Equal(wantFetchedAt) {
				t.Errorf("snapshot time = %v, want %v", views[0].Liability.FetchedAt, wantFetchedAt)
			}
			statesAfter := syncStatesForTest(t, cfg.DBPath)
			if fmt.Sprint(statesAfter) != fmt.Sprint(statesBefore) {
				t.Errorf("sync state changed from %+v to %+v", statesBefore, statesAfter)
			}
		})
	}
}
