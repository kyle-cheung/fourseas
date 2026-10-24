// Package format provides terminal-neutral display formatting.
package format

import (
	"fmt"
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

// PaymentDue formats the remaining statement obligation and its calendar due
// date. Provider dates have no time zone; only now is converted to local time.
func PaymentDue(a model.AccountView, now time.Time) (string, bool) {
	if a.Type != "credit" {
		return missing, false
	}
	if a.Liability == nil || !a.RemainingStatementBalance.Valid || a.Liability.PaymentDueDate == nil {
		return "Check statement", false
	}
	if !a.RemainingStatementBalance.Decimal.IsPositive() {
		return "Paid", false
	}
	due := a.Liability.PaymentDueDate
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	date := time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, time.UTC)
	days := int(date.Sub(today) / (24 * time.Hour))
	relative := "today"
	n := days
	if n < 0 {
		n = -n
	}
	unit := "days"
	if n == 1 {
		unit = "day"
	}
	if days > 0 {
		relative = fmt.Sprintf("in %d %s", n, unit)
	} else if days < 0 {
		relative = fmt.Sprintf("overdue %d %s", n, unit)
	}
	return Money(a.RemainingStatementBalance, a.Currency) + " · " + Date(due, now) + " (" + relative + ")", days < 0
}
