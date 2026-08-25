package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

func TestTruncatePreservesUTF8(t *testing.T) {
	input := strings.Repeat("a", 28) + "é suffix"
	want := strings.Repeat("a", 28) + "é…"

	got := truncate(input)
	if got != want {
		t.Errorf("truncate(%q) = %q, want %q", input, got, want)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncate(%q) = %q, want valid UTF-8", input, got)
	}
}

func TestPrintAccountsShowsLiabilityDetailsAndSharedMoneyFormatting(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	due := time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)
	paid := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	rows := []model.AccountView{
		{
			Account: model.Account{
				AccountID:      "acct-full",
				Name:           "Blue Cash",
				Mask:           "1001",
				Type:           "credit",
				Subtype:        "credit card",
				Currency:       " usd ",
				Nickname:       "Everyday",
				BalanceCurrent: decimal.NewNullDecimal(decimal.RequireFromString("1284.21")),
				BalanceLimit:   decimal.NewNullDecimal(decimal.RequireFromString("25000")),
			},
			InstitutionName: "American Express",
			Liability: &model.CreditLiability{
				PaymentDueDate:    &due,
				LastPaymentDate:   &paid,
				LastPaymentAmount: decimal.NewNullDecimal(decimal.RequireFromString("500")),
			},
		},
		{
			Account: model.Account{
				AccountID:      "acct-missing",
				Name:           "Freedom",
				Mask:           "2002",
				Type:           "credit",
				Subtype:        "credit card",
				Currency:       "CAD",
				Nickname:       "Backup",
				BalanceCurrent: decimal.NewNullDecimal(decimal.RequireFromString("-320.1")),
			},
			InstitutionName: "Chase",
		},
	}

	output := captureStdout(t, func() { printAccountsAt(rows, now) })
	lines := strings.Split(output, "\n")
	if len(lines) < 3 {
		t.Fatalf("account output = %q, want a header and two rows", output)
	}
	normalize := func(line string) string {
		return regexp.MustCompile(` {2,}`).ReplaceAllString(strings.TrimSpace(line), "\t")
	}
	want := []string{
		"INSTITUTION\tNAME\tMASK\tTYPE\tBALANCE\tLIMIT\tDUE\tLAST PAYMENT\tUPDATED\tNICKNAME\tACCOUNT ID",
		"American Express\tBlue Cash\t1001\tcredit card\t1,284.21 USD\t25,000.00 USD\tSep 12\tAug 20 · 500.00 USD\t-\tEveryday\tacct-full",
		"Chase\tFreedom\t2002\tcredit card\t-320.10 CAD\t—\t—\t—\t-\tBackup\tacct-missing",
	}
	for i := range want {
		if got := normalize(lines[i]); got != want[i] {
			t.Errorf("account output line %d = %q, want %q", i+1, got, want[i])
		}
	}
	if strings.Contains(lines[0], "CCY") {
		t.Errorf("account header = %q, want no separate currency column", lines[0])
	}
}

func TestAccountUpdatedShowsADashWhenThereIsNoValue(t *testing.T) {
	if got := accountUpdated(model.Account{}); got != "-" {
		t.Errorf("accountUpdated = %q, want a dash", got)
	}
}

func TestLiabilitiesEnableArgsAcceptOnlyTheExactCommand(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"enable"},
		{"enable", "acct-amex", "extra"},
		{"enable", "   "},
		{"disable", "acct-amex"},
		{"replace", "acct-amex"},
	} {
		if _, err := liabilitiesEnableArgs(args); err == nil {
			t.Errorf("liabilitiesEnableArgs(%q) = no error, want one", args)
		}
	}

	id, err := liabilitiesEnableArgs([]string{"enable", "  acct-amex  "})
	if err != nil {
		t.Fatalf("liabilitiesEnableArgs: %v", err)
	}
	if id != "acct-amex" {
		t.Errorf("account id = %q, want acct-amex", id)
	}
}

func TestRunAccountLiabilitiesRejectsInvalidArgumentsBeforeStarting(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"enable"},
		{"enable", "acct-amex", "extra"},
		{"enable", " "},
		{"disable", "acct-amex"},
	} {
		called := false
		err := runAccountLiabilitiesWith(context.Background(), args,
			func(context.Context, string, app.Progress) error {
				called = true
				return nil
			})
		if err == nil {
			t.Errorf("runAccountLiabilitiesWith(%q) = no error, want one", args)
		}
		if called {
			t.Errorf("runAccountLiabilitiesWith(%q) started the app", args)
		}
	}
}

func TestRunAccountLiabilitiesEnablesTheWholeInstitutionAndRequestsASnapshot(t *testing.T) {
	called := 0
	gotID := ""
	output := captureStdout(t, func() {
		err := runAccountLiabilitiesWith(context.Background(), []string{"enable", " acct-amex "},
			func(_ context.Context, accountID string, report app.Progress) error {
				called++
				gotID = accountID
				report("Open http://localhost:8080 in your browser")
				return nil
			})
		if err != nil {
			t.Fatalf("runAccountLiabilitiesWith: %v", err)
		}
	})

	if called != 1 || gotID != "acct-amex" {
		t.Errorf("enable call = %d for %q, want one call for acct-amex", called, gotID)
	}
	for _, want := range []string{
		"Open http://localhost:8080 in your browser",
		"Statement data is enabled for the whole institution.",
		"The first snapshot was requested.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output = %q, want it to contain %q", output, want)
		}
	}
}

type safeTestError struct {
	text string
	err  error
}

func (e *safeTestError) Error() string { return e.text }

func (e *safeTestError) Unwrap() error { return e.err }

func TestRunAccountLiabilitiesReportsPartialEnableWithoutFullSuccess(t *testing.T) {
	const accessToken = "test-cli-partial-access-token"
	failureIdentity := errors.New("snapshot failure identity")
	cleanupIdentity := errors.New("save consent status: database is busy")
	tests := []struct {
		name           string
		partialErr     error
		identity       error
		statusCleanup  bool
		snapshotStored bool
		wantWords      []string
		forbid         string
	}{
		{
			name: "failure",
			partialErr: &safeTestError{
				text: "snapshot endpoint failed with [REDACTED]",
				err:  fmt.Errorf("snapshot endpoint failed with %s: %w", accessToken, failureIdentity),
			},
			identity:  failureIdentity,
			wantWords: []string{"Statement data is enabled for the whole institution", "first snapshot", "failed"},
		},
		{
			name:       "cancellation",
			partialErr: context.Canceled,
			identity:   context.Canceled,
			wantWords:  []string{"Statement data is enabled for the whole institution", "first snapshot", "canceled"},
		},
		{
			name:       "additional consent",
			partialErr: fmt.Errorf("Plaid response: %w", app.ErrAdditionalConsentRequired),
			identity:   app.ErrAdditionalConsentRequired,
			wantWords:  []string{"request was saved", "Plaid still requires consent", "first snapshot"},
			forbid:     "is enabled",
		},
		{
			name:           "product not ready status cleanup",
			partialErr:     cleanupIdentity,
			identity:       cleanupIdentity,
			statusCleanup:  true,
			snapshotStored: false,
			wantWords:      []string{"Statement data is enabled", "first snapshot is not ready", "status"},
			forbid:         "snapshot was stored",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			partial := app.NewLiabilitiesEnabledError(tt.partialErr, accessToken)
			if tt.statusCleanup {
				partial = app.NewLiabilitiesStatusError(tt.partialErr, accessToken, tt.snapshotStored)
			}
			var gotErr error
			output := captureStdout(t, func() {
				gotErr = runAccountLiabilitiesWith(context.Background(), []string{"enable", "acct-amex"},
					func(context.Context, string, app.Progress) error { return partial })
			})

			if !errors.Is(gotErr, tt.identity) {
				t.Fatalf("command error = %v, want cause %v", gotErr, tt.identity)
			}
			var gotPartial *app.LiabilitiesEnabledError
			if !errors.As(gotErr, &gotPartial) || gotPartial != partial {
				t.Fatalf("command error = %T, want the original partial error", gotErr)
			}
			for _, want := range tt.wantWords {
				if !strings.Contains(gotErr.Error(), want) {
					t.Errorf("command error = %q, want %q", gotErr, want)
				}
			}
			if tt.forbid != "" && strings.Contains(gotErr.Error(), tt.forbid) {
				t.Errorf("command error = %q, want no %q", gotErr, tt.forbid)
			}
			if strings.Contains(output, "The first snapshot was requested.") {
				t.Errorf("output = %q, want no full-success snapshot line", output)
			}
			stderrText := fmt.Sprintf("fourseas: %v\n", gotErr)
			debugText := fmt.Sprintf("%#v", gotErr)
			if strings.Contains(gotErr.Error(), accessToken) || strings.Contains(output, accessToken) ||
				strings.Contains(stderrText, accessToken) || strings.Contains(debugText, accessToken) {
				t.Fatal("command output contains the access token")
			}
		})
	}
}

func TestUsageIncludesTheExactLiabilitiesEnableCommand(t *testing.T) {
	if !strings.Contains(usage, "fourseas accounts liabilities enable <account-id>") {
		t.Error("usage does not include the exact liabilities enable command")
	}
}

// TestNicknameArgsNeedAnIDAndAName proves the command says what it wants
// instead of guessing when the arguments are wrong.
func TestNicknameArgsNeedAnIDAndAName(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"acct-amex"},
		{"acct-amex", "Amex Daily", "extra"},
		{"   ", "Amex Daily"},
	} {
		if _, _, err := nicknameArgs(args); err == nil {
			t.Errorf("nicknameArgs(%q) = no error, want one", args)
		}
	}

	id, nickname, err := nicknameArgs([]string{" acct-amex ", "  Amex Daily  "})
	if err != nil {
		t.Fatalf("nicknameArgs: %v", err)
	}
	if id != "acct-amex" || nickname != "Amex Daily" {
		t.Errorf("nicknameArgs = %q, %q, want the trimmed id and name", id, nickname)
	}

	// An empty name is how a nickname is cleared, so it is not an error.
	if _, nickname, err = nicknameArgs([]string{"acct-amex", ""}); err != nil || nickname != "" {
		t.Errorf("nicknameArgs with an empty name = %q, %v, want it to clear the nickname", nickname, err)
	}
}

// TestSetNicknameShowsUpInTheAccountsList proves the stored nickname reaches
// the rows `fourseas accounts` prints.
func TestSetNicknameShowsUpInTheAccountsList(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	stored := account("acct-1", "10.00")
	if err := db.UpsertAccounts(ctx, []model.Account{stored}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}
	if err := setNickname(ctx, db, "acct-1", "Amex Daily"); err != nil {
		t.Fatalf("set nickname: %v", err)
	}

	views, err := db.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	if views[0].Nickname != "Amex Daily" {
		t.Errorf("Nickname = %q, want the nickname in the accounts list", views[0].Nickname)
	}
	if views[0].Name != stored.Name {
		t.Errorf("Name = %q, want the provider's name %q to stay", views[0].Name, stored.Name)
	}
}

// TestSetNicknameReportsAnUnknownID proves a mistyped id fails loudly.
func TestSetNicknameReportsAnUnknownID(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	err := setNickname(ctx, db, "acct-typo", "Amex Daily")
	if err == nil {
		t.Fatal("setNickname on an unknown id = no error, want one")
	}
	if !strings.Contains(err.Error(), "acct-typo") {
		t.Errorf("error %q does not name the account id", err)
	}
}

func TestAccountKindPrefersTheSubtype(t *testing.T) {
	if got := accountKind(model.Account{Type: "credit", Subtype: "credit card"}); got != "credit card" {
		t.Errorf("accountKind = %q, want %q", got, "credit card")
	}
	if got := accountKind(model.Account{Type: "depository"}); got != "depository" {
		t.Errorf("accountKind = %q, want the type when there is no subtype", got)
	}
}
