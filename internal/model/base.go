package model

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// BaseCurrency is the currency every total is reported in.
const BaseCurrency = "USD"

// NormalizeCurrency returns the canonical form of a currency code.
func NormalizeCurrency(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}

// FXRate is one exchange rate for a date and currency pair.
type FXRate struct {
	Date         time.Time
	Currency     string
	BaseCurrency string
	Rate         decimal.Decimal
}

// FXCurrency identifies a transaction currency that needs exchange rates.
type FXCurrency struct {
	Currency              string
	OldestTransactionDate time.Time
}

// WithBase returns a copy of t with the base currency columns filled in.
//
// A row already in the base currency converts at a rate of 1.0. A row in any
// other currency keeps all four columns null, because no rate is known in this
// build. A total over those rows is therefore null, not a plausible wrong
// number. Real rates are build 2, and they replace this function.
func WithBase(t Transaction) Transaction {
	if !strings.EqualFold(t.Currency, BaseCurrency) {
		return t
	}

	date := t.Date
	t.BaseAmount = decimal.NullDecimal{Decimal: t.Amount, Valid: true}
	t.BaseCurrency = BaseCurrency
	t.FXRate = decimal.NullDecimal{Decimal: decimal.NewFromInt(1), Valid: true}
	t.FXDate = &date
	return t
}
