package plaid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

func TestSyncGetsAccountsWhileTransactionsAreNotReady(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/transactions/sync":
			w.Write([]byte(`{
				"transactions_update_status":"NOT_READY",
				"accounts":[], "added":[], "modified":[], "removed":[],
				"next_cursor":"cursor-after", "has_more":false, "request_id":"sync-request"
			}`))
		case "/accounts/get":
			w.Write([]byte(`{
				"accounts":[{
					"account_id":"account-a",
					"balances":{"available":100,"current":125,"iso_currency_code":"CAD","limit":null,"unofficial_currency_code":null},
					"mask":"K154", "name":"Credit Card", "official_name":"Everyday Credit Card",
					"type":"credit", "subtype":"credit card"
				}],
				"request_id":"accounts-request"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	cfg := plaidsdk.NewConfiguration()
	cfg.Servers = plaidsdk.ServerConfigurations{{URL: backend.URL}}
	source := &Source{
		client:      plaidsdk.NewAPIClient(cfg),
		accessToken: "access-token",
		itemID:      "item-a",
	}

	batch, err := source.Sync(context.Background(), "cursor-before")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if batch.NextCursor != "cursor-after" {
		t.Errorf("NextCursor = %q, want cursor-after", batch.NextCursor)
	}
	if len(batch.Accounts) != 1 {
		t.Fatalf("Accounts = %d, want 1", len(batch.Accounts))
	}
	account := batch.Accounts[0]
	if account.AccountID != "account-a" || account.ItemID != "item-a" || account.Name != "Everyday Credit Card" {
		t.Errorf("Account = %+v, want the account returned by /accounts/get", account)
	}
	if len(batch.Added) != 0 || len(batch.Modified) != 0 || len(batch.RemovedIDs) != 0 {
		t.Errorf("transaction changes = %+v, want none", batch)
	}
}
