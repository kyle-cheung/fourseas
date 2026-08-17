package plaid

import "testing"

// The amount of history is fixed when the item is created, so it must ride on
// the link token request. days_requested in a later /transactions/sync call has
// no effect once transactions is initialized.
func TestLinkTokenRequestCarriesTheRequestedDays(t *testing.T) {
	req := linkTokenRequest(Config{}, 365)

	transactions, ok := req.GetTransactionsOk()
	if !ok {
		t.Fatal("the transactions object is absent, want days_requested")
	}
	if got := transactions.GetDaysRequested(); got != 365 {
		t.Errorf("days_requested = %d, want 365", got)
	}
	if got := req.GetProducts(); len(got) != 1 || got[0] != "transactions" {
		t.Errorf("products = %v, want [transactions]", got)
	}
}

// Nothing asked for means nothing sent, so Plaid applies its own default.
func TestLinkTokenRequestOmitsDaysWhenNotSet(t *testing.T) {
	for _, days := range []int{0, -1} {
		if _, ok := linkTokenRequest(Config{}, days).GetTransactionsOk(); ok {
			t.Errorf("days %d: the transactions object is present, want it absent", days)
		}
	}
}

func TestLinkTokenRequestSetsTheRedirectURIOnlyWhenConfigured(t *testing.T) {
	with := linkTokenRequest(Config{RedirectURI: "http://localhost:8080/oauth"}, 90)
	if got := with.GetRedirectUri(); got != "http://localhost:8080/oauth" {
		t.Errorf("redirect_uri = %q, want the configured one", got)
	}
	if _, ok := linkTokenRequest(Config{}, 90).GetRedirectUriOk(); ok {
		t.Error("redirect_uri is set, want it absent")
	}
}
