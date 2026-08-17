package model

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestWithBaseFillsUSDRows(t *testing.T) {
	date := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	got := WithBase(Transaction{
		Amount:   decimal.RequireFromString("24.75"),
		Currency: "USD",
		Date:     date,
	})

	if !got.BaseAmount.Valid || !got.BaseAmount.Decimal.Equal(decimal.RequireFromString("24.75")) {
		t.Errorf("BaseAmount = %+v, want 24.75", got.BaseAmount)
	}
	if got.BaseCurrency != BaseCurrency {
		t.Errorf("BaseCurrency = %q, want %q", got.BaseCurrency, BaseCurrency)
	}
	if !got.FXRate.Valid || !got.FXRate.Decimal.Equal(decimal.NewFromInt(1)) {
		t.Errorf("FXRate = %+v, want 1", got.FXRate)
	}
	if got.FXDate == nil || !got.FXDate.Equal(date) {
		t.Errorf("FXDate = %v, want the posted date %v", got.FXDate, date)
	}
}

func TestWithBaseLeavesOtherCurrenciesNull(t *testing.T) {
	for _, currency := range []string{"CAD", "EUR", ""} {
		t.Run("currency "+currency, func(t *testing.T) {
			got := WithBase(Transaction{
				Amount:   decimal.RequireFromString("13.20"),
				Currency: currency,
				Date:     time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
			})

			if got.BaseAmount.Valid {
				t.Errorf("BaseAmount = %+v, want null", got.BaseAmount)
			}
			if got.BaseCurrency != "" {
				t.Errorf("BaseCurrency = %q, want empty", got.BaseCurrency)
			}
			if got.FXRate.Valid {
				t.Errorf("FXRate = %+v, want null", got.FXRate)
			}
			if got.FXDate != nil {
				t.Errorf("FXDate = %v, want null", got.FXDate)
			}
		})
	}
}

// TestWithBaseAcceptsALowercaseCode guards the boundary: a provider that sends
// a lowercase code still means USD.
func TestWithBaseAcceptsALowercaseCode(t *testing.T) {
	got := WithBase(Transaction{
		Amount:   decimal.RequireFromString("5"),
		Currency: "usd",
		Date:     time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
	})
	if !got.BaseAmount.Valid {
		t.Fatalf("BaseAmount = %+v, want it filled in", got.BaseAmount)
	}
	if got.BaseCurrency != BaseCurrency {
		t.Errorf("BaseCurrency = %q, want the canonical %q", got.BaseCurrency, BaseCurrency)
	}
}

// TestWithBaseDoesNotMutateItsInput keeps the conversion a pure function.
func TestWithBaseDoesNotMutateItsInput(t *testing.T) {
	in := Transaction{
		Amount:   decimal.RequireFromString("24.75"),
		Currency: "USD",
		Date:     time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
	}
	_ = WithBase(in)

	if in.BaseAmount.Valid || in.BaseCurrency != "" || in.FXRate.Valid || in.FXDate != nil {
		t.Errorf("WithBase changed its input: %+v", in)
	}
}

func TestNormalizeCurrency(t *testing.T) {
	for input, want := range map[string]string{
		"cad":   "CAD",
		" Usd ": "USD",
		"":      "",
	} {
		if got := NormalizeCurrency(input); got != want {
			t.Errorf("NormalizeCurrency(%q) = %q, want %q", input, got, want)
		}
	}
}
