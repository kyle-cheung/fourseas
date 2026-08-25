package plaid

import (
	"errors"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/provider"
)

func TestCodedErrorClassifiesActionableCodes(t *testing.T) {
	tests := []struct {
		code                      string
		restart, gone             bool
		productNotReady           bool
		noLiabilityAccounts       bool
		additionalConsentRequired bool
	}{
		{code: mutationDuringPagination, restart: true},
		{code: itemNotFound, gone: true},
		{code: "ITEM_LOGIN_REQUIRED"},
		{
			code:            productNotReady,
			productNotReady: true,
		},
		{
			code:                noLiabilityAccounts,
			noLiabilityAccounts: true,
		},
		{
			code:                      additionalConsentRequired,
			additionalConsentRequired: true,
		},
	}

	for _, tt := range tests {
		err := codedError("Plaid operation", tt.code, "API_ERROR", "details")
		if got := errors.Is(err, provider.ErrRestartPagination); got != tt.restart {
			t.Errorf("code %s: restart = %v, want %v", tt.code, got, tt.restart)
		}
		if got := errors.Is(err, provider.ErrItemGone); got != tt.gone {
			t.Errorf("code %s: gone = %v, want %v", tt.code, got, tt.gone)
		}
		if got := errors.Is(err, provider.ErrProductNotReady); got != tt.productNotReady {
			t.Errorf("code %s: product not ready = %v, want %v", tt.code, got, tt.productNotReady)
		}
		if got := errors.Is(err, provider.ErrNoLiabilityAccounts); got != tt.noLiabilityAccounts {
			t.Errorf("code %s: no liability accounts = %v, want %v", tt.code, got, tt.noLiabilityAccounts)
		}
		if got := errors.Is(err, provider.ErrAdditionalConsentRequired); got != tt.additionalConsentRequired {
			t.Errorf("code %s: additional consent required = %v, want %v", tt.code, got, tt.additionalConsentRequired)
		}
		if !strings.Contains(err.Error(), tt.code) {
			t.Errorf("error = %q, want code %q", err, tt.code)
		}
		for _, want := range []string{tt.code, "API_ERROR", "details"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want full Plaid detail %q", err, want)
			}
		}
	}
}
