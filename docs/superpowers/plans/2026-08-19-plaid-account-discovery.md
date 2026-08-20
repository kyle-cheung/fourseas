# Plaid Account Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Return linked accounts to the TUI when the first Transactions Sync response is not ready.

**Architecture:** Keep Transactions Sync as the primary account source. When it reports `NOT_READY` with no accounts, use Plaid's cached `/accounts/get` response in the same provider batch. Preserve the Transactions Sync cursor and reuse the existing account mapper, store path, nickname flow, and recovery behavior.

**Tech Stack:** Go 1.25, Plaid Go SDK v40, `net/http/httptest`, standard Go tests.

---

### Task 1: Fall back to Plaid Accounts

**Files:**
- Create: `internal/provider/plaid/sync_test.go`
- Modify: `internal/provider/plaid/sync.go:42-78`

- [ ] **Step 1: Write the failing provider test**

Create `internal/provider/plaid/sync_test.go`:

```go
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
```

- [ ] **Step 2: Run the test and verify the expected failure**

Run:

```bash
go test ./internal/provider/plaid -run TestSyncGetsAccountsWhileTransactionsAreNotReady -count=1
```

Expected: FAIL because `batch.Accounts` has length 0. The current source does not call `/accounts/get`.

- [ ] **Step 3: Add the minimal fallback**

In `Source.Sync`, after the successful Transactions Sync response and before transaction mapping, select the account response:

```go
	accounts := resp.Accounts
	if resp.GetTransactionsUpdateStatus() == plaidsdk.TRANSACTIONSUPDATESTATUS_NOT_READY && len(accounts) == 0 {
		req := plaidsdk.NewAccountsGetRequest(s.accessToken)
		accountResp, httpResp, err := s.client.PlaidApi.AccountsGet(ctx).
			AccountsGetRequest(*req).Execute()
		if err != nil {
			return provider.Batch{}, apiError(fmt.Sprintf("get accounts for item %s", s.itemID), err, httpResp)
		}
		accounts = accountResp.Accounts
		if len(accounts) == 0 {
			return provider.Batch{}, fmt.Errorf("sync item %s: %w", s.itemID, provider.ErrProductNotReady)
		}
	}
```

Change the existing batch field to use the selected account slice:

```go
		Accounts: toAccounts(accounts, s.itemID, time.Now().UTC()),
```

- [ ] **Step 4: Run focused and full verification**

Run:

```bash
gofmt -w internal/provider/plaid/sync.go internal/provider/plaid/sync_test.go
go test ./internal/provider/plaid -run TestSyncGetsAccountsWhileTransactionsAreNotReady -count=1
go test ./...
go vet ./...
git diff --check
```

Expected: all commands pass with no warnings or formatting errors.

- [ ] **Step 5: Commit the bug fix**

```bash
git add internal/provider/plaid/sync.go internal/provider/plaid/sync_test.go
git commit -m "fix: discover accounts before transactions are ready"
```
