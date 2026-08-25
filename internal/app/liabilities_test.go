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
	var enabledErr *LiabilitiesEnabledError
	if errors.As(err, &enabledErr) {
		t.Fatalf("EnableLiabilities error = %T, want a pre-save error", err)
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

func TestEnableLiabilitiesCancellationBeforeConsentIsNotPartial(t *testing.T) {
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
			return plaid.LinkResult{}, context.Canceled
		},
	)

	err := client.EnableLiabilities(context.Background(), accountID, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EnableLiabilities error = %v, want context.Canceled", err)
	}
	var enabledErr *LiabilitiesEnabledError
	if errors.As(err, &enabledErr) {
		t.Fatalf("EnableLiabilities error = %T, want a pre-save cancellation", err)
	}
	if liabilityCalls != 0 {
		t.Errorf("liability calls = %d, want 0", liabilityCalls)
	}
	if loadTokens(t, cfg.TokensPath).Items[0].Liabilities {
		t.Error("liability flag is true after update cancellation")
	}
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
		initialStatus   string
		wantStatus      string
		wantConsent     bool
	}{
		{name: "success replaces the snapshot", initialStatus: liabilitiesConsentRequiredStatus + "old", wantStatus: ""},
		{name: "product not ready preserves the snapshot", liabilityErr: fmt.Errorf("liabilities: %w", provider.ErrProductNotReady), wantOldSnapshot: true, initialStatus: liabilitiesConsentRequiredStatus + "old", wantStatus: ""},
		{name: "additional consent error preserves the snapshot", liabilityErr: fmt.Errorf("liabilities: %w", provider.ErrAdditionalConsentRequired), wantErr: provider.ErrAdditionalConsentRequired, wantOldSnapshot: true, initialStatus: "rate limited", wantConsent: true},
		{name: "temporary error preserves the snapshot", liabilityErr: temporaryErr, wantErr: temporaryErr, wantOldSnapshot: true, initialStatus: liabilitiesConsentRequiredStatus + "old", wantStatus: ""},
		{name: "temporary error preserves unrelated status", liabilityErr: temporaryErr, wantErr: temporaryErr, wantOldSnapshot: true, initialStatus: "rate limited", wantStatus: "rate limited"},
		{name: "cancellation after consent preserves the snapshot", cancelAfterLink: true, wantErr: context.Canceled, wantOldSnapshot: true, initialStatus: liabilitiesConsentRequiredStatus + "old", wantStatus: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tempConfig(t, "sandbox")
			linked := item("item-amex", "American Express", "sandbox")
			linked.AccessToken = "test-enable-policy-access-token"
			accountID := seedLiabilitiesAccount(t, cfg, linked, "credit")
			seedLiabilitySnapshot(t, cfg.DBPath, linked.ItemID, oldFetchedAt)
			db, err := store.Open(cfg.DBPath)
			if err != nil {
				t.Fatalf("open store for initial status: %v", err)
			}
			if err := db.SetStatusOnly(context.Background(), plaid.ProviderName, linked.ItemID, tt.initialStatus); err != nil {
				db.Close()
				t.Fatalf("set initial status: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close store after initial status: %v", err)
			}
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
			err = client.EnableLiabilities(ctx, "  "+accountID+"  ", collect(&lines))
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("EnableLiabilities: %v", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("EnableLiabilities error = %v, want %v", err, tt.wantErr)
			}
			var enabledErr *LiabilitiesEnabledError
			if tt.wantErr != nil {
				if !errors.As(err, &enabledErr) {
					t.Fatalf("EnableLiabilities error = %T, want *LiabilitiesEnabledError", err)
				}
				if !errors.Is(enabledErr.Cause(), tt.wantErr) {
					t.Fatalf("partial cause = %v, want %v", enabledErr.Cause(), tt.wantErr)
				}
				wantWords := []string{"first snapshot"}
				if errors.Is(tt.wantErr, provider.ErrAdditionalConsentRequired) {
					wantWords = append(wantWords, "request was saved", "Plaid still requires consent")
					if strings.Contains(err.Error(), "is enabled") {
						t.Errorf("consent-required error = %q, want no enabled claim", err)
					}
				} else {
					wantWords = append(wantWords, "Statement data is enabled for the whole institution")
				}
				for _, want := range wantWords {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("partial error = %q, want %q", err, want)
					}
				}
				if !errors.Is(tt.wantErr, provider.ErrAdditionalConsentRequired) {
					wantOutcome := "failed"
					if errors.Is(tt.wantErr, context.Canceled) {
						wantOutcome = "canceled"
					}
					if !strings.Contains(err.Error(), wantOutcome) {
						t.Errorf("partial error = %q, want outcome %q", err, wantOutcome)
					}
				}
			} else if errors.As(err, &enabledErr) {
				t.Fatalf("EnableLiabilities error = %T, want no partial error", err)
			}
			if err != nil {
				cause := errors.Unwrap(err)
				if cause == nil {
					t.Fatal("partial error has no sanitized cause")
				}
				if raw := errors.Unwrap(cause); raw != nil {
					t.Fatalf("sanitized cause unwraps to %T, want nil", raw)
				}
				assertNoAccessToken(t, linked.AccessToken, err.Error(), fmt.Sprintf("%#v", err), fmt.Sprintf("%#v", cause))
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
			if len(statesBefore) != 1 || len(statesAfter) != 1 {
				t.Fatalf("sync states changed from %+v to %+v", statesBefore, statesAfter)
			}
			if statesAfter[0].Cursor != statesBefore[0].Cursor ||
				!statesAfter[0].LastSyncedAt.Equal(*statesBefore[0].LastSyncedAt) {
				t.Errorf("sync position changed from %+v to %+v", statesBefore[0], statesAfter[0])
			}
			if tt.wantConsent {
				if !strings.HasPrefix(statesAfter[0].LastStatus, liabilitiesConsentRequiredStatus) {
					t.Errorf("status = %q, want consent marker", statesAfter[0].LastStatus)
				}
			} else if statesAfter[0].LastStatus != tt.wantStatus {
				t.Errorf("status = %q, want %q", statesAfter[0].LastStatus, tt.wantStatus)
			}
			data, accountsErr := client.Accounts(context.Background(), linked.ItemID)
			if accountsErr != nil {
				t.Fatalf("Accounts after enable: %v", accountsErr)
			}
			if len(data.States) != 1 || data.States[0].LiabilitiesConsentRequired != tt.wantConsent {
				t.Errorf("returned account state = %+v, want consent required %v", data.States, tt.wantConsent)
			}
			assertNoAccessToken(t, linked.AccessToken, statesAfter[0].LastStatus)
		})
	}
}

func TestRefreshLiabilitiesValidatesAnEnabledCreditAccount(t *testing.T) {
	tests := []struct {
		name        string
		accountID   string
		accountType string
		enabled     bool
		wantText    string
	}{
		{name: "unknown account", accountID: "acct-missing", accountType: "credit", enabled: true, wantText: "no stored account"},
		{name: "non-credit account", accountID: "acct-item-amex", accountType: "depository", enabled: true, wantText: "credit"},
		{name: "disabled item", accountID: "acct-item-amex", accountType: "credit", wantText: "not enabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tempConfig(t, "sandbox")
			linked := item("item-amex", "American Express", "sandbox")
			linked.Liabilities = tt.enabled
			seedLiabilitiesAccount(t, cfg, linked, tt.accountType)
			liabilityCalls := 0
			client := newWith(cfg, nil, nil, nil,
				func(context.Context, plaid.Config, string) ([]model.CreditLiability, error) {
					liabilityCalls++
					return nil, nil
				},
				func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
					t.Fatal("RefreshLiabilities called update consent")
					return plaid.LinkResult{}, nil
				},
			)

			err := client.RefreshLiabilities(context.Background(), tt.accountID)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.wantText) {
				t.Fatalf("RefreshLiabilities error = %v, want text %q", err, tt.wantText)
			}
			if liabilityCalls != 0 {
				t.Errorf("liability calls = %d, want 0", liabilityCalls)
			}
		})
	}
}

func TestEnableLiabilitiesStatusCleanupFailureIsTypedAfterSnapshotSaved(t *testing.T) {
	oldFetchedAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	newFetchedAt := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cfg := tempConfig(t, "sandbox")
	linked := item("item-amex", "American Express", "sandbox")
	linked.AccessToken = "test-status-cleanup-access-token"
	accountID := seedLiabilitiesAccount(t, cfg, linked, "credit")
	seedLiabilitySnapshot(t, cfg.DBPath, linked.ItemID, oldFetchedAt)
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := db.SetStatusOnly(context.Background(), plaid.ProviderName, linked.ItemID,
		liabilitiesConsentRequiredStatus+"old"); err != nil {
		db.Close()
		t.Fatalf("set stale consent status: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	updateCalls := 0
	liabilityCalls := 0
	client := newWith(cfg, nil, nil, nil,
		func(context.Context, plaid.Config, string) ([]model.CreditLiability, error) {
			liabilityCalls++
			return []model.CreditLiability{{
				Provider: plaid.ProviderName, ItemID: linked.ItemID,
				AccountID: accountID, FetchedAt: newFetchedAt,
			}}, nil
		},
		func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
			updateCalls++
			return plaid.LinkResult{}, nil
		},
	)

	oldTimeout := liabilitiesStatusTimeout
	liabilitiesStatusTimeout = 0
	defer func() { liabilitiesStatusTimeout = oldTimeout }()
	err = client.EnableLiabilities(context.Background(), accountID, nil)
	liabilitiesStatusTimeout = oldTimeout
	var enabledErr *LiabilitiesEnabledError
	if !errors.As(err, &enabledErr) {
		t.Fatalf("EnableLiabilities error = %T, want *LiabilitiesEnabledError", err)
	}
	if !enabledErr.SnapshotStored() {
		t.Fatal("partial outcome does not report that the snapshot was stored")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnableLiabilities error = %v, want context.DeadlineExceeded", err)
	}
	for _, want := range []string{"Statement data is enabled", "snapshot was stored", "status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("EnableLiabilities error = %q, want %q", err, want)
		}
	}
	assertNoAccessToken(t, linked.AccessToken, err.Error(), fmt.Sprintf("%#v", err))
	if !loadTokens(t, cfg.TokensPath).Items[0].Liabilities {
		t.Fatal("liability flag is false after saved consent")
	}
	if got := accountViews(t, cfg.DBPath)[0].Liability.FetchedAt; !got.Equal(newFetchedAt) {
		t.Errorf("stored snapshot time = %v, want %v", got, newFetchedAt)
	}
	if updateCalls != 1 || liabilityCalls != 1 {
		t.Fatalf("calls after cleanup failure = update %d, liabilities %d; want 1 each",
			updateCalls, liabilityCalls)
	}

	if err := client.RefreshLiabilities(context.Background(), accountID); err != nil {
		t.Fatalf("RefreshLiabilities retry: %v", err)
	}
	if updateCalls != 1 || liabilityCalls != 2 {
		t.Fatalf("calls after retry = update %d, liabilities %d; want 1 and 2",
			updateCalls, liabilityCalls)
	}
	data, err := client.Accounts(context.Background(), linked.ItemID)
	if err != nil {
		t.Fatalf("Accounts after retry: %v", err)
	}
	if len(data.States) != 1 || data.States[0].LiabilitiesConsentRequired {
		t.Errorf("state after retry = %+v, want cleared consent marker", data.States)
	}
}

func TestEnableLiabilitiesProductNotReadyCleanupFailureDoesNotClaimSnapshotStored(t *testing.T) {
	oldFetchedAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cfg := tempConfig(t, "sandbox")
	linked := item("item-amex", "American Express", "sandbox")
	linked.AccessToken = "test-product-not-ready-cleanup-access-token"
	accountID := seedLiabilitiesAccount(t, cfg, linked, "credit")
	seedLiabilitySnapshot(t, cfg.DBPath, linked.ItemID, oldFetchedAt)
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := db.SetStatusOnly(context.Background(), plaid.ProviderName, linked.ItemID,
		liabilitiesConsentRequiredStatus+"old"); err != nil {
		db.Close()
		t.Fatalf("set stale consent status: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	updateCalls := 0
	liabilityCalls := 0
	client := newWith(cfg, nil, nil, nil,
		func(context.Context, plaid.Config, string) ([]model.CreditLiability, error) {
			liabilityCalls++
			return nil, fmt.Errorf("liabilities: %w", provider.ErrProductNotReady)
		},
		func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
			updateCalls++
			return plaid.LinkResult{}, nil
		},
	)

	oldTimeout := liabilitiesStatusTimeout
	liabilitiesStatusTimeout = 0
	defer func() { liabilitiesStatusTimeout = oldTimeout }()
	err = client.EnableLiabilities(context.Background(), accountID, nil)
	liabilitiesStatusTimeout = oldTimeout
	var enabledErr *LiabilitiesEnabledError
	if !errors.As(err, &enabledErr) {
		t.Fatalf("EnableLiabilities error = %T, want *LiabilitiesEnabledError", err)
	}
	if enabledErr.SnapshotStored() {
		t.Fatal("PRODUCT_NOT_READY cleanup outcome claims that a snapshot was stored")
	}
	if !enabledErr.StatusCleanupFailed() {
		t.Fatal("partial outcome does not classify the status cleanup failure")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnableLiabilities error = %v, want context.DeadlineExceeded", err)
	}
	for _, want := range []string{"Statement data is enabled", "snapshot is not ready", "status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("EnableLiabilities error = %q, want %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "snapshot was stored") {
		t.Errorf("EnableLiabilities error = %q, want no stored-snapshot claim", err)
	}
	if got := accountViews(t, cfg.DBPath)[0].Liability.FetchedAt; !got.Equal(oldFetchedAt) {
		t.Errorf("snapshot time = %v, want old snapshot %v", got, oldFetchedAt)
	}
	if updateCalls != 1 || liabilityCalls != 1 {
		t.Fatalf("calls = update %d, liabilities %d; want 1 each", updateCalls, liabilityCalls)
	}

	if err := client.RefreshLiabilities(context.Background(), accountID); err != nil {
		t.Fatalf("RefreshLiabilities retry: %v", err)
	}
	if updateCalls != 1 || liabilityCalls != 2 {
		t.Fatalf("calls after retry = update %d, liabilities %d; want 1 and 2", updateCalls, liabilityCalls)
	}
}

func TestRefreshLiabilitiesUsesOnlyTheSnapshotEndpoint(t *testing.T) {
	oldFetchedAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	newFetchedAt := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cfg := tempConfig(t, "sandbox")
	linked := item("item-amex", "American Express", "sandbox")
	linked.Liabilities = true
	linked.AccessToken = "test-refresh-liabilities-access-token"
	accountID := seedLiabilitiesAccount(t, cfg, linked, "credit")
	seedLiabilitySnapshot(t, cfg.DBPath, linked.ItemID, oldFetchedAt)
	sentinel := errors.New("snapshot endpoint is unavailable")
	liabilityErr := error(fmt.Errorf("fetch with %s: %w", linked.AccessToken, sentinel))
	liabilityCalls := 0
	sourceCalls := 0
	updateCalls := 0
	client := newWith(cfg, nil, nil,
		func(plaid.Config, string, string, string) (provider.Provider, error) {
			sourceCalls++
			return nil, errors.New("transaction source must not be called")
		},
		func(context.Context, plaid.Config, string) ([]model.CreditLiability, error) {
			liabilityCalls++
			if liabilityErr != nil {
				return nil, liabilityErr
			}
			return []model.CreditLiability{{
				Provider: plaid.ProviderName, ItemID: linked.ItemID,
				AccountID: accountID, FetchedAt: newFetchedAt,
			}}, nil
		},
		func(context.Context, plaid.Config, string) (plaid.LinkResult, error) {
			updateCalls++
			return plaid.LinkResult{}, errors.New("update consent must not be called")
		},
	)

	err := client.RefreshLiabilities(context.Background(), accountID)
	if !errors.Is(err, sentinel) {
		t.Fatalf("first RefreshLiabilities error = %v, want sentinel", err)
	}
	assertNoAccessToken(t, linked.AccessToken, err.Error(), fmt.Sprintf("%#v", err))
	if got := accountViews(t, cfg.DBPath)[0].Liability.FetchedAt; !got.Equal(oldFetchedAt) {
		t.Errorf("snapshot after failure = %v, want %v", got, oldFetchedAt)
	}

	liabilityErr = nil
	if err := client.RefreshLiabilities(context.Background(), accountID); err != nil {
		t.Fatalf("second RefreshLiabilities: %v", err)
	}
	if got := accountViews(t, cfg.DBPath)[0].Liability.FetchedAt; !got.Equal(newFetchedAt) {
		t.Errorf("snapshot after success = %v, want %v", got, newFetchedAt)
	}
	if liabilityCalls != 2 || sourceCalls != 0 || updateCalls != 0 {
		t.Errorf("provider calls = liabilities %d, source %d, update %d; want 2, 0, 0",
			liabilityCalls, sourceCalls, updateCalls)
	}
}

func TestLiabilitiesEnabledErrorNilValuesAreSafe(t *testing.T) {
	var nilError *LiabilitiesEnabledError
	for _, err := range []*LiabilitiesEnabledError{nilError, &LiabilitiesEnabledError{}} {
		if got := err.Error(); got == "" {
			t.Error("Error() returned an empty string")
		}
		if errors.Unwrap(err) != nil {
			t.Error("empty LiabilitiesEnabledError unwraps to a cause")
		}
	}
}
