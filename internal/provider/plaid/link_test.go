package plaid

import (
	"reflect"
	"testing"

	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

// The amount of history is fixed when the item is created, so it must ride on
// the link token request. days_requested in a later /transactions/sync call has
// no effect once transactions is initialized.
func TestLinkTokenRequestCarriesNewItemProductsAndRequestedDays(t *testing.T) {
	tests := []struct {
		name        string
		liabilities bool
		additional  []plaidsdk.Products
	}{
		{name: "liabilities enabled", liabilities: true, additional: []plaidsdk.Products{plaidsdk.PRODUCTS_LIABILITIES}},
		{name: "liabilities disabled", liabilities: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := linkTokenRequest(Config{}, linkRequest{days: 365, liabilities: tt.liabilities})

			wantProducts := []plaidsdk.Products{plaidsdk.PRODUCTS_TRANSACTIONS}
			if got := req.GetProducts(); !reflect.DeepEqual(got, wantProducts) {
				t.Errorf("products = %v, want %v", got, wantProducts)
			}
			additional, set := req.GetAdditionalConsentedProductsOk()
			if set != tt.liabilities {
				t.Errorf("additional_consented_products set = %t, want %t", set, tt.liabilities)
			}
			if set && !reflect.DeepEqual(*additional, tt.additional) {
				t.Errorf("additional_consented_products = %v, want %v", *additional, tt.additional)
			}

			transactions, ok := req.GetTransactionsOk()
			if !ok {
				t.Fatal("the transactions object is absent, want days_requested")
			}
			if got := transactions.GetDaysRequested(); got != 365 {
				t.Errorf("days_requested = %d, want 365", got)
			}
		})
	}
}

func TestLinkTokenRequestCarriesOnlyUpdateLiabilitiesConsent(t *testing.T) {
	const accessToken = "test-update-access-token"
	cfg := Config{RedirectURI: "https://example.test/oauth"}

	req := linkTokenRequest(cfg, linkRequest{accessToken: accessToken})

	if got := req.GetClientName(); got != "Fourseas" {
		t.Errorf("client_name = %q, want Fourseas", got)
	}
	if got := req.GetLanguage(); got != "en" {
		t.Errorf("language = %q, want en", got)
	}
	wantCountries := []plaidsdk.CountryCode{plaidsdk.COUNTRYCODE_US, plaidsdk.COUNTRYCODE_CA}
	if got := req.GetCountryCodes(); !reflect.DeepEqual(got, wantCountries) {
		t.Errorf("country_codes = %v, want %v", got, wantCountries)
	}
	user := req.GetUser()
	if got := user.GetClientUserId(); got != "fourseas-local-user" {
		t.Errorf("user.client_user_id = %q, want fourseas-local-user", got)
	}
	if got := req.GetRedirectUri(); got != cfg.RedirectURI {
		t.Errorf("redirect_uri = %q, want %q", got, cfg.RedirectURI)
	}
	if got := req.GetAccessToken(); got != accessToken {
		t.Error("access_token does not match the existing Item token")
	}
	wantAdditional := []plaidsdk.Products{plaidsdk.PRODUCTS_LIABILITIES}
	if got := req.GetAdditionalConsentedProducts(); !reflect.DeepEqual(got, wantAdditional) {
		t.Errorf("additional_consented_products = %v, want %v", got, wantAdditional)
	}
	if _, set := req.GetProductsOk(); set {
		t.Error("products is set in update mode")
	}
	if _, set := req.GetTransactionsOk(); set {
		t.Error("transactions is set in update mode")
	}
}
