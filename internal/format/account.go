// Package format provides terminal-neutral display formatting.
package format

import (
	"strings"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

const missing = "—"

// Money formats a decimal value with two fraction digits and its currency.
func Money(value decimal.NullDecimal, currency string) string {
	if !value.Valid {
		return missing
	}

	fixed := value.Decimal.StringFixed(2)
	sign := ""
	if strings.HasPrefix(fixed, "-") {
		sign, fixed = "-", fixed[1:]
	}
	integer, fraction, _ := strings.Cut(fixed, ".")
	for position := len(integer) - 3; position > 0; position -= 3 {
		integer = integer[:position] + "," + integer[position:]
	}
	formatted := sign + integer + "." + fraction
	if currency = model.NormalizeCurrency(currency); currency != "" {
		formatted += " " + currency
	}
	return formatted
}

// Date formats a date without the year when it is in the current year.
func Date(value *time.Time, now time.Time) string {
	if value == nil {
		return missing
	}
	if value.Year() == now.Year() {
		return value.Format("Jan 02")
	}
	return value.Format("Jan 02, 2006")
}

// LatestPayment formats the date and amount of a complete payment record.
func LatestPayment(date *time.Time, amount decimal.NullDecimal, currency string, now time.Time) string {
	if date == nil || !amount.Valid {
		return missing
	}
	return Date(date, now) + " · " + Money(amount, currency)
}
