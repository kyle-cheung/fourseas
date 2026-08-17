package frankfurter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestClientRatesFetchesAndNormalizesRates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/v2/rates" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/v2/rates")
		}
		query := r.URL.Query()
		for key, want := range map[string]string{
			"base":   "CAD",
			"quotes": "USD",
			"from":   "2024-01-01",
			"to":     "2024-01-03",
		} {
			if got := query.Get(key); got != want {
				t.Errorf("query %q = %q, want %q", key, got, want)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"date":"2024-01-02","base":"cad","quote":"usd","rate":0.71234567}]`))
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL)
	rates, err := client.Rates(
		context.Background(),
		" cad ",
		"usd",
		time.Date(2024, 1, 1, 13, 45, 0, 0, time.FixedZone("PST", -8*60*60)),
		time.Date(2024, 1, 3, 2, 15, 0, 0, time.FixedZone("PST", -8*60*60)),
	)
	if err != nil {
		t.Fatalf("Rates() error = %v", err)
	}
	if len(rates) != 1 {
		t.Fatalf("len(Rates()) = %d, want 1", len(rates))
	}

	got := rates[0]
	if got.Currency != "CAD" {
		t.Errorf("Currency = %q, want %q", got.Currency, "CAD")
	}
	if got.BaseCurrency != "USD" {
		t.Errorf("BaseCurrency = %q, want %q", got.BaseCurrency, "USD")
	}
	if !got.Date.Equal(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v, want 2024-01-02", got.Date)
	}
	if !got.Rate.Equal(decimal.RequireFromString("0.71234567")) {
		t.Errorf("Rate = %s, want 0.71234567", got.Rate)
	}
}

func TestClientRatesRejectsInvalidResponses(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name       string
		statusCode int
		body       string
	}{
		{
			name:       "non-2xx status",
			statusCode: http.StatusBadGateway,
			body:       `upstream unavailable`,
		},
		{
			name:       "malformed JSON",
			statusCode: http.StatusOK,
			body:       `[`,
		},
		{
			name:       "null response",
			statusCode: http.StatusOK,
			body:       `null`,
		},
		{
			name:       "invalid response date",
			statusCode: http.StatusOK,
			body:       `[{"date":"2024-13-01","base":"CAD","quote":"USD","rate":0.71234567}]`,
		},
		{
			name:       "wrong echoed pair",
			statusCode: http.StatusOK,
			body:       `[{"date":"2024-01-02","base":"CAD","quote":"EUR","rate":0.71234567}]`,
		},
		{
			name:       "zero rate",
			statusCode: http.StatusOK,
			body:       `[{"date":"2024-01-02","base":"CAD","quote":"USD","rate":0}]`,
		},
		{
			name:       "negative rate",
			statusCode: http.StatusOK,
			body:       `[{"date":"2024-01-02","base":"CAD","quote":"USD","rate":-1}]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			_, err := NewClient(server.Client(), server.URL).Rates(context.Background(), "CAD", "USD", from, to)
			if err == nil {
				t.Fatal("Rates() error = nil, want an error")
			}
		})
	}
}

func TestClientRatesRejectsInvertedRange(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := NewClient(server.Client(), server.URL).Rates(
		context.Background(),
		"CAD",
		"USD",
		time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	if err == nil {
		t.Fatal("Rates() error = nil, want an error")
	}
	if called {
		t.Fatal("Rates() made an HTTP request for an inverted range")
	}
}

func TestClientRatesUsesCalendarDatesForRangeValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		from     time.Time
		to       time.Time
		fromDate string
		toDate   string
	}{
		{
			name:     "same calendar date",
			from:     time.Date(2024, 1, 2, 0, 30, 0, 0, time.FixedZone("UTC-12", -12*60*60)),
			to:       time.Date(2024, 1, 2, 23, 30, 0, 0, time.FixedZone("UTC+14", 14*60*60)),
			fromDate: "2024-01-02",
			toDate:   "2024-01-02",
		},
		{
			name:     "forward calendar date",
			from:     time.Date(2024, 1, 2, 0, 30, 0, 0, time.FixedZone("UTC-12", -12*60*60)),
			to:       time.Date(2024, 1, 3, 0, 0, 0, 0, time.FixedZone("UTC+14", 14*60*60)),
			fromDate: "2024-01-02",
			toDate:   "2024-01-03",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !test.to.Before(test.from) {
				t.Fatal("test setup requires the to instant to be before the from instant")
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("from"); got != test.fromDate {
					t.Errorf("from = %q, want %q", got, test.fromDate)
				}
				if got := r.URL.Query().Get("to"); got != test.toDate {
					t.Errorf("to = %q, want %q", got, test.toDate)
				}
				_, _ = w.Write([]byte(`[]`))
			}))
			defer server.Close()

			rates, err := NewClient(server.Client(), server.URL).Rates(
				context.Background(), "CAD", "USD", test.from, test.to,
			)
			if err != nil {
				t.Fatalf("Rates() error = %v", err)
			}
			if len(rates) != 0 {
				t.Errorf("len(Rates()) = %d, want 0", len(rates))
			}
		})
	}
}

func TestClientRatesRejectsCalendarInvertedRange(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	from := time.Date(2024, 1, 3, 0, 0, 0, 0, time.FixedZone("UTC+14", 14*60*60))
	to := time.Date(2024, 1, 2, 23, 0, 0, 0, time.FixedZone("UTC-12", -12*60*60))
	if !to.After(from) {
		t.Fatal("test setup requires the to instant to be after the from instant")
	}

	_, err := NewClient(server.Client(), server.URL).Rates(context.Background(), "CAD", "USD", from, to)
	if err == nil {
		t.Fatal("Rates() error = nil, want an error")
	}
	if called {
		t.Fatal("Rates() made an HTTP request for an inverted calendar range")
	}
}

func TestClientRatesAcceptsEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	rates, err := NewClient(server.Client(), server.URL).Rates(
		context.Background(),
		"CAD",
		"USD",
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("Rates() error = %v", err)
	}
	if len(rates) != 0 {
		t.Errorf("len(Rates()) = %d, want 0", len(rates))
	}
}

func TestClientRatesAcceptsDateBeforeRequestRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"date":"1989-12-30","base":"CAD","quote":"USD","rate":0.86397}]`))
	}))
	defer server.Close()

	rates, err := NewClient(server.Client(), server.URL).Rates(
		context.Background(),
		"CAD",
		"USD",
		time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(1990, 1, 3, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("Rates() error = %v", err)
	}
	if len(rates) != 1 {
		t.Fatalf("len(Rates()) = %d, want 1", len(rates))
	}
	if !rates[0].Date.Equal(time.Date(1989, 12, 30, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v, want 1989-12-30", rates[0].Date)
	}
	if !rates[0].Rate.Equal(decimal.RequireFromString("0.86397")) {
		t.Errorf("Rate = %s, want 0.86397", rates[0].Rate)
	}
}
