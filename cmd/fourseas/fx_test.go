package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

type fakeFXStore struct {
	currencies    []model.FXCurrency
	requiredErr   error
	requiredCalls int
	requiredBase  string
	upsertErrs    []error
	upsertCalls   [][]model.FXRate
}

func (f *fakeFXStore) RequiredFXCurrencies(_ context.Context, baseCurrency string) ([]model.FXCurrency, error) {
	f.requiredCalls++
	f.requiredBase = baseCurrency
	return f.currencies, f.requiredErr
}

func (f *fakeFXStore) UpsertFXRates(_ context.Context, rates []model.FXRate) error {
	f.upsertCalls = append(f.upsertCalls, rates)
	call := len(f.upsertCalls) - 1
	if call < len(f.upsertErrs) {
		return f.upsertErrs[call]
	}
	return nil
}

type fxRateCall struct {
	currency     string
	baseCurrency string
	from         time.Time
	to           time.Time
}

type fakeFXSource struct {
	rates map[string][]model.FXRate
	errs  map[string]error
	calls []fxRateCall
}

func (f *fakeFXSource) Rates(
	_ context.Context,
	currency string,
	baseCurrency string,
	from time.Time,
	to time.Time,
) ([]model.FXRate, error) {
	f.calls = append(f.calls, fxRateCall{
		currency:     currency,
		baseCurrency: baseCurrency,
		from:         from,
		to:           to,
	})
	return f.rates[currency], f.errs[currency]
}

func TestSyncFXFetchesEachCurrencyForItsFullRange(t *testing.T) {
	now := time.Date(2026, 8, 17, 19, 34, 0, 0, time.FixedZone("PDT", -7*60*60))
	today := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	cadStart := time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)
	eurStart := time.Date(2025, 2, 6, 0, 0, 0, 0, time.UTC)
	store := &fakeFXStore{currencies: []model.FXCurrency{
		{Currency: "CAD", OldestTransactionDate: cadStart},
		{Currency: "EUR", OldestTransactionDate: eurStart},
	}}
	source := &fakeFXSource{rates: map[string][]model.FXRate{
		"CAD": {fxRate("CAD", "2026-08-15")},
		"EUR": {fxRate("EUR", "2026-08-15"), fxRate("EUR", "2026-08-16")},
	}}

	results, err := syncFX(context.Background(), store, source, now)
	if err != nil {
		t.Fatalf("syncFX() error = %v", err)
	}
	if store.requiredCalls != 1 {
		t.Errorf("RequiredFXCurrencies() calls = %d, want 1", store.requiredCalls)
	}
	if store.requiredBase != model.BaseCurrency {
		t.Errorf("RequiredFXCurrencies() base = %q, want %q", store.requiredBase, model.BaseCurrency)
	}
	if len(source.calls) != 2 {
		t.Fatalf("Rates() calls = %d, want 2", len(source.calls))
	}

	wantStarts := []time.Time{cadStart, eurStart}
	for i, call := range source.calls {
		if call.currency != store.currencies[i].Currency {
			t.Errorf("call %d currency = %q, want %q", i, call.currency, store.currencies[i].Currency)
		}
		if call.baseCurrency != model.BaseCurrency {
			t.Errorf("call %d base currency = %q, want %q", i, call.baseCurrency, model.BaseCurrency)
		}
		if !call.from.Equal(wantStarts[i]) {
			t.Errorf("call %d start = %v, want %v", i, call.from, wantStarts[i])
		}
		if !call.to.Equal(today) {
			t.Errorf("call %d end = %v, want %v", i, call.to, today)
		}
	}
	if len(results) != 2 || results[0].Rows != 1 || results[1].Rows != 2 {
		t.Errorf("results = %+v, want row counts 1 and 2", results)
	}
}

func TestSyncFXClampsFutureStartToCurrentUTCDate(t *testing.T) {
	now := time.Date(2026, 8, 17, 23, 30, 0, 0, time.FixedZone("PDT", -7*60*60))
	today := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	store := &fakeFXStore{currencies: []model.FXCurrency{{
		Currency:              "CAD",
		OldestTransactionDate: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC),
	}}}
	source := &fakeFXSource{rates: map[string][]model.FXRate{"CAD": {}}}

	if _, err := syncFX(context.Background(), store, source, now); err != nil {
		t.Fatalf("syncFX() error = %v", err)
	}
	if len(source.calls) != 1 {
		t.Fatalf("Rates() calls = %d, want 1", len(source.calls))
	}
	if !source.calls[0].from.Equal(today) || !source.calls[0].to.Equal(today) {
		t.Errorf("request range = %v through %v, want %v through %v",
			source.calls[0].from, source.calls[0].to, today, today)
	}
}

func TestSyncFXContinuesAfterCurrencyFailures(t *testing.T) {
	store := &fakeFXStore{
		currencies: []model.FXCurrency{
			{Currency: "CAD", OldestTransactionDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
			{Currency: "EUR", OldestTransactionDate: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
			{Currency: "JPY", OldestTransactionDate: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)},
		},
		upsertErrs: []error{errors.New("database unavailable")},
	}
	source := &fakeFXSource{
		rates: map[string][]model.FXRate{
			"EUR": {fxRate("EUR", "2026-08-17")},
			"JPY": {fxRate("JPY", "2026-08-17")},
		},
		errs: map[string]error{"CAD": errors.New("provider unavailable")},
	}

	results, err := syncFX(context.Background(), store, source, time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("syncFX() error = %v", err)
	}
	if len(source.calls) != 3 {
		t.Fatalf("Rates() calls = %d, want 3", len(source.calls))
	}
	if len(store.upsertCalls) != 2 || store.upsertCalls[0][0].Currency != "EUR" || store.upsertCalls[1][0].Currency != "JPY" {
		t.Fatalf("upserts = %+v, want EUR followed by JPY", store.upsertCalls)
	}
	if len(results) != 3 || results[0].Err == nil || results[1].Err == nil || results[2].Err != nil {
		t.Fatalf("results = %+v, want CAD and EUR errors followed by JPY success", results)
	}
	if !strings.Contains(results[0].Err.Error(), "CAD") {
		t.Errorf("CAD error = %q, want currency name", results[0].Err)
	}
	if !strings.Contains(results[1].Err.Error(), "EUR") {
		t.Errorf("EUR error = %q, want currency name", results[1].Err)
	}
}

func TestSyncFXLeavesRowsZeroWhenUpsertFails(t *testing.T) {
	store := &fakeFXStore{
		currencies: []model.FXCurrency{{
			Currency:              "CAD",
			OldestTransactionDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}},
		upsertErrs: []error{errors.New("database is read-only")},
	}
	source := &fakeFXSource{rates: map[string][]model.FXRate{
		"CAD": {fxRate("CAD", "2026-08-16"), fxRate("CAD", "2026-08-17")},
	}}

	results, err := syncFX(context.Background(), store, source, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("syncFX() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one result", results)
	}
	if results[0].Rows != 0 || results[0].Err == nil {
		t.Errorf("result = %+v, want zero rows and an error", results[0])
	}
	if !strings.Contains(results[0].Err.Error(), "CAD") {
		t.Errorf("upsert error = %q, want currency name", results[0].Err)
	}
}

func TestRunFXPhaseWarnsInNormalModeAndFailsInStrictMode(t *testing.T) {
	for _, test := range []struct {
		name    string
		strict  bool
		wantErr bool
	}{
		{name: "normal mode", strict: false, wantErr: false},
		{name: "strict FX-only mode", strict: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeFXStore{currencies: []model.FXCurrency{{
				Currency:              "CAD",
				OldestTransactionDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			}}}
			source := &fakeFXSource{errs: map[string]error{"CAD": errors.New("provider unavailable")}}
			var out bytes.Buffer

			err := runFXPhase(
				context.Background(), store, source,
				time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), test.strict, &out,
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("runFXPhase() error = %v, wantErr %v", err, test.wantErr)
			}
			if !strings.Contains(out.String(), "warning") || !strings.Contains(out.String(), "CAD") {
				t.Errorf("output = %q, want a CAD warning", out.String())
			}
			if err != nil && !strings.Contains(err.Error(), "CAD") {
				t.Errorf("error = %q, want currency name", err)
			}
		})
	}
}

func TestRunFXPhaseAppliesPolicyToRequiredCurrenciesFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		strict  bool
		wantErr bool
	}{
		{name: "normal mode", strict: false, wantErr: false},
		{name: "strict FX-only mode", strict: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeFXStore{requiredErr: errors.New("database unavailable")}
			var out bytes.Buffer

			err := runFXPhase(
				context.Background(), store, &fakeFXSource{},
				time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), test.strict, &out,
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("runFXPhase() error = %v, wantErr %v", err, test.wantErr)
			}
			if !strings.Contains(out.String(), "warning") || !strings.Contains(out.String(), "database unavailable") {
				t.Errorf("output = %q, want database warning", out.String())
			}
		})
	}
}

func TestRunFXPhaseAlwaysReturnsContextErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		strict bool
	}{
		{name: "normal mode", strict: false},
		{name: "strict FX-only mode", strict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
				ctx, cancel := context.WithCancelCause(context.Background())
				cancel(cause)

				err := runFXPhase(
					ctx, &fakeFXStore{}, &fakeFXSource{},
					time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), test.strict, &bytes.Buffer{},
				)
				if !errors.Is(err, cause) {
					t.Errorf("runFXPhase() error = %v, want errors.Is(_, %v)", err, cause)
				}
			}
		})
	}
}

func TestSyncFXStopsAndPreservesOperationContextErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		sourceErr  error
		upsertErr  error
		wantErr    error
		wantUpsert int
	}{
		{
			name:       "fetch canceled",
			sourceErr:  context.Canceled,
			wantErr:    context.Canceled,
			wantUpsert: 0,
		},
		{
			name:       "upsert deadline exceeded",
			upsertErr:  context.DeadlineExceeded,
			wantErr:    context.DeadlineExceeded,
			wantUpsert: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeFXStore{
				currencies: []model.FXCurrency{
					{Currency: "CAD", OldestTransactionDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
					{Currency: "EUR", OldestTransactionDate: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
				},
				upsertErrs: []error{test.upsertErr},
			}
			source := &fakeFXSource{
				rates: map[string][]model.FXRate{"CAD": {fxRate("CAD", "2026-08-17")}},
				errs:  map[string]error{"CAD": test.sourceErr},
			}

			_, err := syncFX(
				context.Background(), store, source,
				time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("syncFX() error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
			if len(source.calls) != 1 {
				t.Errorf("Rates() calls = %d, want 1", len(source.calls))
			}
			if len(store.upsertCalls) != test.wantUpsert {
				t.Errorf("UpsertFXRates() calls = %d, want %d", len(store.upsertCalls), test.wantUpsert)
			}
		})
	}
}

func fxRate(currency, date string) model.FXRate {
	parsed, err := time.Parse(time.DateOnly, date)
	if err != nil {
		panic(err)
	}
	return model.FXRate{
		Date:         parsed,
		Currency:     currency,
		BaseCurrency: model.BaseCurrency,
		Rate:         decimal.NewFromInt(1),
	}
}
