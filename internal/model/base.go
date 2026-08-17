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
