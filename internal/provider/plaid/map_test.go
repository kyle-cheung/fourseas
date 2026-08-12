package plaid

import (
	"encoding/json"
	"testing"

	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

// sampleSyncResponse is a trimmed /transactions/sync response. It covers the
// cases that hide wrongness: a positive purchase, a negative payment, a pending
// row, a row with no merchant name, and a row with no personal finance category.
const sampleSyncResponse = `{
  "accounts": [
    {
      "account_id": "acct-amex",
      "balances": {"available": null, "current": 412.55, "iso_currency_code": "USD", "limit": 10000, "unofficial_currency_code": null},
      "mask": "1234",
      "name": "Gold Card",
      "official_name": "American Express Gold Card",
      "type": "credit",
      "subtype": "credit card"
    },
    {
      "account_id": "acct-scotia",
      "balances": {"available": null, "current": 88.10, "iso_currency_code": "CAD", "limit": 5000, "unofficial_currency_code": null},
      "mask": null,
      "name": "Scotia Momentum Visa",
      "official_name": null,
      "type": "credit",
      "subtype": "credit card"
    }
  ],
  "added": [
    {
      "account_id": "acct-amex",
      "amount": 24.75,
      "iso_currency_code": "USD",
      "unofficial_currency_code": null,
      "category": ["Food and Drink", "Restaurants"],
      "category_id": "13005000",
      "date": "2026-08-10",
      "location": {},
      "name": "BLUE BOTTLE COFFEE",
      "merchant_name": "Blue Bottle Coffee",
      "payment_meta": {},
      "pending": false,
      "pending_transaction_id": null,
      "account_owner": null,
      "transaction_id": "txn-1",
      "authorized_date": "2026-08-09",
      "authorized_datetime": null,
      "datetime": null,
      "payment_channel": "in store",
      "personal_finance_category": {"primary": "FOOD_AND_DRINK", "detailed": "FOOD_AND_DRINK_COFFEE", "confidence_level": "VERY_HIGH"},
      "transaction_code": null
    },
    {
      "account_id": "acct-amex",
      "amount": -500.00,
      "iso_currency_code": "USD",
      "unofficial_currency_code": null,
      "category": ["Payment"],
      "date": "2026-08-05",
      "location": {},
      "name": "ONLINE PAYMENT THANK YOU",
      "merchant_name": null,
      "payment_meta": {},
      "pending": false,
      "pending_transaction_id": null,
      "account_owner": null,
      "transaction_id": "txn-2",
      "authorized_date": null,
      "authorized_datetime": null,
      "datetime": null,
      "payment_channel": "other",
      "personal_finance_category": null,
      "transaction_code": null
    },
    {
      "account_id": "acct-scotia",
      "amount": 13.20,
      "iso_currency_code": "CAD",
      "unofficial_currency_code": null,
      "category": [],
      "date": "2026-08-11",
      "location": {},
      "name": "TIM HORTONS #4821",
      "merchant_name": null,
      "payment_meta": {},
      "pending": true,
      "pending_transaction_id": null,
      "account_owner": null,
      "transaction_id": "txn-3",
      "authorized_date": null,
      "authorized_datetime": null,
      "datetime": null,
      "payment_channel": "in store",
      "personal_finance_category": null,
      "transaction_code": null
    }
  ],
  "modified": [],
  "removed": [{"transaction_id": "txn-gone"}],
  "next_cursor": "cursor-abc",
  "has_more": false,
  "transactions_update_status": "HISTORICAL_UPDATE_COMPLETE",
  "request_id": "req-1"
}`

func decodeSample(t *testing.T) plaidsdk.TransactionsSyncResponse {
	t.Helper()
	var resp plaidsdk.TransactionsSyncResponse
	if err := json.Unmarshal([]byte(sampleSyncResponse), &resp); err != nil {
		t.Fatalf("decode sample: %v", err)
	}
	return resp
}

func TestToModels(t *testing.T) {
	resp := decodeSample(t)
	accounts := newAccountIndex(resp.Accounts)

	got, err := toModels(resp.Added, "item-1", "American Express", accounts)
	if err != nil {
		t.Fatalf("toModels: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}

	tests := []struct {
		name        string
		index       int
		wantID      string
		wantAmount  float64
		wantDate    string
		wantMerch   string
		wantCat     string
		wantPending bool
		wantAccount string
		wantCcy     string
	}{
		{
			name:        "purchase keeps a positive amount",
			index:       0,
			wantID:      "txn-1",
			wantAmount:  24.75,
			wantDate:    "2026-08-10",
			wantMerch:   "Blue Bottle Coffee",
			wantCat:     "FOOD_AND_DRINK",
			wantPending: false,
			wantAccount: "Gold Card ••1234",
			wantCcy:     "USD",
		},
		{
			name:        "card payment keeps a negative amount and falls back to the legacy category",
			index:       1,
			wantID:      "txn-2",
			wantAmount:  -500.00,
			wantDate:    "2026-08-05",
			wantMerch:   "",
			wantCat:     "Payment",
			wantPending: false,
			wantAccount: "Gold Card ••1234",
			wantCcy:     "USD",
		},
		{
			name:        "pending row with no mask and no category",
			index:       2,
			wantID:      "txn-3",
			wantAmount:  13.20,
			wantDate:    "2026-08-11",
			wantMerch:   "",
			wantCat:     "",
			wantPending: true,
			wantAccount: "Scotia Momentum Visa",
			wantCcy:     "CAD",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := got[tc.index]
			if row.ExternalID != tc.wantID {
				t.Errorf("ExternalID = %q, want %q", row.ExternalID, tc.wantID)
			}
			if row.Amount != tc.wantAmount {
				t.Errorf("Amount = %v, want %v", row.Amount, tc.wantAmount)
			}
			if got := row.Date.Format(dateLayout); got != tc.wantDate {
				t.Errorf("Date = %q, want %q", got, tc.wantDate)
			}
			if row.MerchantName != tc.wantMerch {
				t.Errorf("MerchantName = %q, want %q", row.MerchantName, tc.wantMerch)
			}
			if row.Category != tc.wantCat {
				t.Errorf("Category = %q, want %q", row.Category, tc.wantCat)
			}
			if row.Pending != tc.wantPending {
				t.Errorf("Pending = %v, want %v", row.Pending, tc.wantPending)
			}
			if row.AccountName != tc.wantAccount {
				t.Errorf("AccountName = %q, want %q", row.AccountName, tc.wantAccount)
			}
			if row.Currency != tc.wantCcy {
				t.Errorf("Currency = %q, want %q", row.Currency, tc.wantCcy)
			}
			if row.Provider != ProviderName {
				t.Errorf("Provider = %q, want %q", row.Provider, ProviderName)
			}
			if row.ItemID != "item-1" {
				t.Errorf("ItemID = %q, want %q", row.ItemID, "item-1")
			}
			if row.Institution != "American Express" {
				t.Errorf("Institution = %q, want %q", row.Institution, "American Express")
			}
		})
	}
}

func TestToModelRejectsBadDate(t *testing.T) {
	resp := decodeSample(t)
	bad := resp.Added[0]
	bad.Date = "10/08/2026"

	if _, err := toModel(bad, "item-1", "American Express", newAccountIndex(resp.Accounts)); err == nil {
		t.Fatal("want an error for an unparsable date, got nil")
	}
}

func TestUnknownAccountFallsBackToID(t *testing.T) {
	index := newAccountIndex(nil)
	if got := index.display("acct-missing"); got != "acct-missing" {
		t.Errorf("display = %q, want the raw id", got)
	}
}
