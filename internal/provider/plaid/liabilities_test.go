package plaid

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

func TestLiabilitiesMapsCreditRowsAndSkipsInvalidAccountIDs(t *testing.T) {
	const accessToken = "access-sandbox-fake"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/liabilities/get" {
			t.Errorf("path = %q, want /liabilities/get", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(body) != 1 || body["access_token"] != accessToken {
			t.Errorf("request body = %#v, want only the fake access token", body)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"accounts":[
				{
					"account_id":"credit-full",
					"balances":{"available":500,"current":125.50,"iso_currency_code":"USD","limit":1000,"unofficial_currency_code":null},
					"mask":"1234","name":"Credit Card","official_name":"Everyday Credit Card",
					"persistent_account_id":"persistent-credit-full","type":"credit","subtype":"credit card"
				},
				{
					"account_id":"credit-null",
					"balances":{"available":null,"current":0,"iso_currency_code":"USD","limit":500,"unofficial_currency_code":null},
					"mask":"5678","name":"Second Credit Card","official_name":null,
					"persistent_account_id":"persistent-credit-null","type":"credit","subtype":"credit card"
				},
				{
					"account_id":"credit-malformed",
					"balances":{"available":100,"current":50,"iso_currency_code":"USD","limit":750,"unofficial_currency_code":null},
					"mask":"9012","name":"Third Credit Card","official_name":"Rewards Credit Card",
					"persistent_account_id":"persistent-credit-malformed","type":"credit","subtype":"credit card"
				}
			],
			"item":{
				"item_id":"item-liabilities","institution_id":"ins-test","webhook":null,"error":null,
				"available_products":[],"billed_products":["liabilities"],"products":["liabilities"],
				"consent_expiration_time":null,"update_type":"background"
			},
			"liabilities":{
				"credit":[
					{"account_id":"credit-full","aprs":[],"is_overdue":false,"last_payment_amount":123.45,"last_payment_date":"2026-08-10","last_statement_issue_date":"2026-08-12","last_statement_balance":456.78,"minimum_payment_amount":25,"next_payment_due_date":"2026-09-15"},
					{"account_id":"credit-null","aprs":[],"is_overdue":null,"last_payment_amount":null,"last_payment_date":"","last_statement_issue_date":null,"last_statement_balance":null,"minimum_payment_amount":null,"next_payment_due_date":null},
					{"account_id":"credit-malformed","aprs":[],"is_overdue":false,"last_payment_amount":8.5,"last_payment_date":"2026-02-30","last_statement_issue_date":"2026-08-12","last_statement_balance":50,"minimum_payment_amount":10,"next_payment_due_date":"   "},
					{"account_id":"","aprs":[],"is_overdue":false,"last_payment_amount":1,"last_payment_date":"2026-08-01","last_statement_issue_date":"2026-08-12","last_statement_balance":1,"minimum_payment_amount":1,"next_payment_due_date":"2026-09-01"},
					{"account_id":"   ","aprs":[],"is_overdue":false,"last_payment_amount":2,"last_payment_date":"2026-08-02","last_statement_issue_date":"2026-08-12","last_statement_balance":2,"minimum_payment_amount":2,"next_payment_due_date":"2026-09-02"},
					{"account_id":null,"aprs":[],"is_overdue":null,"last_payment_amount":null,"last_payment_date":null,"last_statement_issue_date":null,"last_statement_balance":null,"minimum_payment_amount":null,"next_payment_due_date":null}
				],
				"mortgage":[],
				"student":[]
			},
			"request_id":"request-liabilities"
		}`))
	}))
	defer backend.Close()

	cfg := plaidsdk.NewConfiguration()
	cfg.Servers = plaidsdk.ServerConfigurations{{URL: backend.URL}}
	fetchedAt := time.Date(2026, time.August, 24, 17, 30, 0, 0, time.UTC)

	got, err := liabilities(context.Background(), plaidsdk.NewAPIClient(cfg), accessToken, fetchedAt)
	if err != nil {
		t.Fatalf("liabilities: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("liabilities = %d rows, want 3: %+v", len(got), got)
	}

	full := got[0]
	if full.Provider != ProviderName || full.ItemID != "item-liabilities" || full.AccountID != "credit-full" {
		t.Errorf("full row identity = %+v, want Plaid item and account identity", full)
	}
	if !full.LastPaymentAmount.Valid || full.LastPaymentAmount.Decimal.String() != "123.45" {
		t.Errorf("last payment amount = %v, want exact decimal 123.45", full.LastPaymentAmount)
	}
	wantDueDate := time.Date(2026, time.September, 15, 0, 0, 0, 0, time.UTC)
	wantLastPaymentDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	if full.PaymentDueDate == nil || !full.PaymentDueDate.Equal(wantDueDate) {
		t.Errorf("payment due date = %v, want %v", full.PaymentDueDate, wantDueDate)
	}
	if full.LastPaymentDate == nil || !full.LastPaymentDate.Equal(wantLastPaymentDate) {
		t.Errorf("last payment date = %v, want %v", full.LastPaymentDate, wantLastPaymentDate)
	}
	if !full.FetchedAt.Equal(fetchedAt) {
		t.Errorf("fetched at = %v, want %v", full.FetchedAt, fetchedAt)
	}

	nullFields := got[1]
	if nullFields.AccountID != "credit-null" {
		t.Errorf("second account ID = %q, want credit-null", nullFields.AccountID)
	}
	if nullFields.PaymentDueDate != nil || nullFields.LastPaymentDate != nil || nullFields.LastPaymentAmount.Valid {
		t.Errorf("null fields = %+v, want absent dates and amount", nullFields)
	}

	malformedDates := got[2]
	if malformedDates.AccountID != "credit-malformed" {
		t.Errorf("third account ID = %q, want credit-malformed", malformedDates.AccountID)
	}
	if malformedDates.PaymentDueDate != nil || malformedDates.LastPaymentDate != nil {
		t.Errorf("malformed dates = due %v, last payment %v; want both nil", malformedDates.PaymentDueDate, malformedDates.LastPaymentDate)
	}
}

func TestLiabilitiesClassifiesActionableErrorsWithoutLeakingAccessToken(t *testing.T) {
	tests := []struct {
		code     string
		sentinel error
	}{
		{code: noLiabilityAccounts, sentinel: provider.ErrNoLiabilityAccounts},
		{code: additionalConsentRequired, sentinel: provider.ErrAdditionalConsentRequired},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			const accessToken = "access-sandbox-secret-fake"
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/liabilities/get" {
					t.Errorf("path = %q, want /liabilities/get", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if len(body) != 1 || body["access_token"] != accessToken {
					t.Errorf("request body = %#v, want only the fake access token", body)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{
					"error_type":"INVALID_REQUEST",
					"error_code":"` + tt.code + `",
					"error_message":"action is required",
					"request_id":"request-error"
				}`))
			}))
			defer backend.Close()

			cfg := plaidsdk.NewConfiguration()
			cfg.Servers = plaidsdk.ServerConfigurations{{URL: backend.URL}}
			_, err := liabilities(
				context.Background(),
				plaidsdk.NewAPIClient(cfg),
				accessToken,
				time.Date(2026, time.August, 24, 17, 30, 0, 0, time.UTC),
			)
			if !errors.Is(err, tt.sentinel) {
				t.Errorf("error = %v, want sentinel %v", err, tt.sentinel)
			}
			if err != nil && strings.Contains(err.Error(), accessToken) {
				t.Errorf("error contains the fake access token: %v", err)
			}
		})
	}
}
