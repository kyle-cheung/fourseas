package plaid

import (
	"encoding/json"
	"testing"
	"time"

	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
	"github.com/shopspring/decimal"
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

	got, err := toModels(resp.Added, "item-1")
	if err != nil {
		t.Fatalf("toModels: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}

	tests := []struct {
		name           string
		index          int
		wantID         string
		wantAmount     string
		wantDate       string
		wantAuthorized string
		wantMerch      string
		wantCat        string
		wantPending    bool
		wantAccountID  string
		wantCcy        string
	}{
		{
			name:           "purchase keeps a positive amount",
			index:          0,
			wantID:         "txn-1",
			wantAmount:     "24.75",
			wantDate:       "2026-08-10",
			wantAuthorized: "2026-08-09",
			wantMerch:      "Blue Bottle Coffee",
			wantCat:        "FOOD_AND_DRINK",
			wantPending:    false,
			wantAccountID:  "acct-amex",
			wantCcy:        "USD",
		},
		{
			name:          "card payment keeps a negative amount and falls back to the legacy category",
			index:         1,
			wantID:        "txn-2",
			wantAmount:    "-500",
			wantDate:      "2026-08-05",
			wantMerch:     "",
			wantCat:       "Payment",
			wantPending:   false,
			wantAccountID: "acct-amex",
			wantCcy:       "USD",
		},
		{
			name:          "pending row with no mask and no category",
			index:         2,
			wantID:        "txn-3",
			wantAmount:    "13.2",
			wantDate:      "2026-08-11",
			wantMerch:     "",
			wantCat:       "",
			wantPending:   true,
			wantAccountID: "acct-scotia",
			wantCcy:       "CAD",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := got[tc.index]
			if row.ExternalID != tc.wantID {
				t.Errorf("ExternalID = %q, want %q", row.ExternalID, tc.wantID)
			}
			want, err := decimal.NewFromString(tc.wantAmount)
			if err != nil {
				t.Fatalf("bad want amount %q: %v", tc.wantAmount, err)
			}
			if !row.Amount.Equal(want) {
				t.Errorf("Amount = %v, want %v", row.Amount, want)
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
			if row.AccountID != tc.wantAccountID {
				t.Errorf("AccountID = %q, want %q", row.AccountID, tc.wantAccountID)
			}
			switch {
			case tc.wantAuthorized == "":
				if row.AuthorizedDate != nil {
					t.Errorf("AuthorizedDate = %v, want none", row.AuthorizedDate)
				}
			case row.AuthorizedDate == nil:
				t.Errorf("AuthorizedDate = none, want %q", tc.wantAuthorized)
			default:
				if got := row.AuthorizedDate.Format(dateLayout); got != tc.wantAuthorized {
					t.Errorf("AuthorizedDate = %q, want %q", got, tc.wantAuthorized)
				}
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
		})
	}
}

// TestToModelCarriesThePendingTransactionID keeps the link Plaid draws between
// the posted row and the pending row it replaces. Without it the store cannot
// hide the duplicate, and one charge is counted two times.
func TestToModelCarriesThePendingTransactionID(t *testing.T) {
	resp := decodeSample(t)

	posted := resp.Added[0]
	posted.SetPendingTransactionId("txn-pending-1")
	got, err := toModel(posted, "item-1")
	if err != nil {
		t.Fatalf("toModel: %v", err)
	}
	if got.PendingTransactionID != "txn-pending-1" {
		t.Errorf("PendingTransactionID = %q, want %q", got.PendingTransactionID, "txn-pending-1")
	}
	// Only the store sets superseded_by, from what it holds. A provider row
	// never proposes one.
	if got.SupersededBy != "" {
		t.Errorf("SupersededBy = %q, want a mapped row to leave it empty", got.SupersededBy)
	}

	// Plaid sends null for a row that replaces nothing, which is most rows.
	none, err := toModel(resp.Added[1], "item-1")
	if err != nil {
		t.Fatalf("toModel: %v", err)
	}
	if none.PendingTransactionID != "" {
		t.Errorf("PendingTransactionID = %q, want empty when Plaid sends null", none.PendingTransactionID)
	}
}

func TestToModelRejectsBadDate(t *testing.T) {
	resp := decodeSample(t)
	bad := resp.Added[0]
	bad.Date = "10/08/2026"

	if _, err := toModel(bad, "item-1"); err == nil {
		t.Fatal("want an error for an unparsable date, got nil")
	}
}

func TestToModelRejectsBadAuthorizedDate(t *testing.T) {
	resp := decodeSample(t)
	bad := resp.Added[0]
	bad.SetAuthorizedDate("09/08/2026")

	if _, err := toModel(bad, "item-1"); err == nil {
		t.Fatal("want an error for an unparsable authorized date, got nil")
	}
}

func TestToAccounts(t *testing.T) {
	resp := decodeSample(t)
	seen := time.Date(2026, 8, 12, 15, 4, 5, 0, time.UTC)

	got := toAccounts(resp.Accounts, "item-1", seen)
	if len(got) != 2 {
		t.Fatalf("got %d accounts, want 2", len(got))
	}

	amex, scotia := got[0], got[1]

	// The official name wins, because it is the one printed on the card.
	if amex.Name != "American Express Gold Card" {
		t.Errorf("Name = %q, want the official name", amex.Name)
	}
	if scotia.Name != "Scotia Momentum Visa" {
		t.Errorf("Name = %q, want the short name when there is no official one", scotia.Name)
	}
	if amex.Mask != "1234" {
		t.Errorf("Mask = %q, want %q", amex.Mask, "1234")
	}
	if scotia.Mask != "" {
		t.Errorf("Mask = %q, want empty when Plaid sends null", scotia.Mask)
	}
	if amex.Type != "credit" || amex.Subtype != "credit card" {
		t.Errorf("Type/Subtype = %q/%q, want credit/credit card", amex.Type, amex.Subtype)
	}
	if amex.Currency != "USD" || scotia.Currency != "CAD" {
		t.Errorf("Currency = %q and %q, want USD and CAD", amex.Currency, scotia.Currency)
	}

	if !amex.BalanceCurrent.Valid || !amex.BalanceCurrent.Decimal.Equal(decimal.RequireFromString("412.55")) {
		t.Errorf("BalanceCurrent = %+v, want 412.55", amex.BalanceCurrent)
	}
	if !amex.BalanceLimit.Valid || !amex.BalanceLimit.Decimal.Equal(decimal.RequireFromString("10000")) {
		t.Errorf("BalanceLimit = %+v, want 10000", amex.BalanceLimit)
	}
	if amex.BalanceAvailable.Valid {
		t.Errorf("BalanceAvailable = %+v, want null when Plaid sends null", amex.BalanceAvailable)
	}

	if amex.BalanceUpdatedAt == nil || !amex.BalanceUpdatedAt.Equal(seen) {
		t.Errorf("BalanceUpdatedAt = %v, want the sync time %v", amex.BalanceUpdatedAt, seen)
	}
	if !amex.FirstSeenAt.Equal(seen) || !amex.LastSeenAt.Equal(seen) {
		t.Errorf("seen times = %v and %v, want both %v", amex.FirstSeenAt, amex.LastSeenAt, seen)
	}

	if amex.Provider != ProviderName || amex.ItemID != "item-1" || amex.AccountID != "acct-amex" {
		t.Errorf("identity = %q/%q/%q, want plaid/item-1/acct-amex",
			amex.Provider, amex.ItemID, amex.AccountID)
	}
	// A new account is tracked until the user says otherwise. The upsert keeps
	// the stored choice, so this value only applies the first time.
	if !amex.Tracked {
		t.Error("Tracked = false, want a new account to be tracked")
	}
	// A nickname belongs to the user, so a sync must never propose one.
	if amex.Nickname != "" {
		t.Errorf("Nickname = %q, want a sync to leave it empty", amex.Nickname)
	}
}

// TestToAccountUsesThePlaidBalanceTimeWhenThereIsOne covers Capital One, the
// one institution that reports when the balance was last updated.
func TestToAccountUsesThePlaidBalanceTimeWhenThereIsOne(t *testing.T) {
	resp := decodeSample(t)
	reported := time.Date(2026, 8, 11, 6, 30, 0, 0, time.UTC)
	resp.Accounts[0].Balances.SetLastUpdatedDatetime(reported)

	got := toAccounts(resp.Accounts, "item-1", time.Date(2026, 8, 12, 15, 4, 5, 0, time.UTC))
	if got[0].BalanceUpdatedAt == nil || !got[0].BalanceUpdatedAt.Equal(reported) {
		t.Errorf("BalanceUpdatedAt = %v, want the time Plaid reported %v", got[0].BalanceUpdatedAt, reported)
	}
}
