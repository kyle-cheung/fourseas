package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/fx/frankfurter"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

const frankfurterBaseURL = "https://api.frankfurter.dev"

type fxStore interface {
	RequiredFXCurrencies(context.Context, string) ([]model.FXCurrency, error)
	UpsertFXRates(context.Context, []model.FXRate) error
}

type fxSource interface {
	Rates(context.Context, string, string, time.Time, time.Time) ([]model.FXRate, error)
}

type fxResult struct {
	Currency string
	Rows     int
	Err      error
}

// syncFX refreshes the full required rate range for each stored currency.
func syncFX(ctx context.Context, db fxStore, source fxSource, now time.Time) ([]fxResult, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, fmt.Errorf("sync FX rates: %w", err)
	}

	currencies, err := db.RequiredFXCurrencies(ctx, model.BaseCurrency)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, fmt.Errorf("find required FX currencies: %w", cause)
		}
		return nil, fmt.Errorf("find required FX currencies: %w", err)
	}

	utcNow := now.UTC()
	today := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC)
	results := make([]fxResult, 0, len(currencies))
	for _, required := range currencies {
		if err := context.Cause(ctx); err != nil {
			return results, fmt.Errorf("sync FX rates: %w", err)
		}

		result := fxResult{Currency: required.Currency}
		start := required.OldestTransactionDate
		if start.After(today) {
			start = today
		}

		rates, err := source.Rates(ctx, required.Currency, model.BaseCurrency, start, today)
		if err != nil {
			if cause := context.Cause(ctx); cause != nil {
				result.Err = fmt.Errorf("fetch %s FX rates: %w", required.Currency, cause)
				results = append(results, result)
				return results, result.Err
			}
			result.Err = fmt.Errorf("fetch %s FX rates: %w", required.Currency, err)
			results = append(results, result)
			continue
		}
		if err := context.Cause(ctx); err != nil {
			result.Err = fmt.Errorf("fetch %s FX rates: %w", required.Currency, err)
			results = append(results, result)
			return results, result.Err
		}

		if err := db.UpsertFXRates(ctx, rates); err != nil {
			if cause := context.Cause(ctx); cause != nil {
				result.Err = fmt.Errorf("store %s FX rates: %w", required.Currency, cause)
				results = append(results, result)
				return results, result.Err
			}
			result.Err = fmt.Errorf("store %s FX rates: %w", required.Currency, err)
			results = append(results, result)
			continue
		}
		result.Rows = len(rates)
		results = append(results, result)
	}

	if err := context.Cause(ctx); err != nil {
		return results, fmt.Errorf("sync FX rates: %w", err)
	}
	return results, nil
}

// runFXPhase applies the command's warning or strict failure policy.
func runFXPhase(
	ctx context.Context,
	db fxStore,
	source fxSource,
	now time.Time,
	strict bool,
	out io.Writer,
) error {
	results, syncErr := syncFX(ctx, db, source, now)
	failures := make([]error, 0, len(results)+1)
	for _, result := range results {
		if result.Err != nil {
			fmt.Fprintf(out, "%-28s FX warning: %v\n", result.Currency, result.Err)
			failures = append(failures, result.Err)
			continue
		}
		fmt.Fprintf(out, "%-28s %d FX rates stored\n", result.Currency, result.Rows)
	}

	if syncErr != nil {
		if context.Cause(ctx) != nil {
			return syncErr
		}
		fmt.Fprintf(out, "FX warning: %v\n", syncErr)
		failures = append(failures, syncErr)
	}
	if err := context.Cause(ctx); err != nil {
		return fmt.Errorf("sync FX rates: %w", err)
	}
	if len(results) == 0 && syncErr == nil {
		fmt.Fprintln(out, "No FX rates are required.")
	}
	if strict && len(failures) > 0 {
		return fmt.Errorf("sync FX rates: %w", errors.Join(failures...))
	}
	return nil
}

func newFXSource() fxSource {
	return frankfurter.NewClient(
		&http.Client{Timeout: 30 * time.Second},
		frankfurterBaseURL,
	)
}
