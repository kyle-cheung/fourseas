package format

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestAccountFormatting(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"money groups thousands", Money(amount("1284.21"), "USD"), "1,284.21 USD"},
		{"negative money", Money(amount("-320.1"), "CAD"), "-320.10 CAD"},
		{"missing money", Money(decimal.NullDecimal{}, "USD"), "—"},
		{"zero", Money(amount("0"), "USD"), "0.00 USD"},
		{"sub-thousand money", Money(amount("12.3"), ""), "12.30"},
		{"large money", Money(amount("1234567890.12"), " usd "), "1,234,567,890.12 USD"},
		{"date this year", Date(date(2026, time.September, 12), now), "Sep 12"},
		{"date another year", Date(date(2025, time.December, 1), now), "Dec 01, 2025"},
		{"missing date", Date(nil, now), "—"},
		{"latest payment", LatestPayment(date(2026, time.August, 20), amount("500"), "USD", now), "Aug 20 · 500.00 USD"},
		{"latest payment needs a date", LatestPayment(nil, amount("500"), "USD", now), "—"},
		{"latest payment needs an amount", LatestPayment(date(2026, time.August, 20), decimal.NullDecimal{}, "USD", now), "—"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("formatted value = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func amount(value string) decimal.NullDecimal {
	return decimal.NewNullDecimal(decimal.RequireFromString(value))
}

func date(year int, month time.Month, day int) *time.Time {
	value := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return &value
}
