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
}
