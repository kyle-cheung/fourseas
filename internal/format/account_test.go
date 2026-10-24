package format

import (
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
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

func TestPaymentDue(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, now, due, amount, want string
		overdue                      bool
	}{
		{"upcoming", "2026-10-15T23:30:00-07:00", "2026-11-03", "772.70", "772.70 USD · Nov 03 (in 19 days)", false},
		{"tomorrow", "2026-10-15T23:30:00-07:00", "2026-10-16", "1", "1.00 USD · Oct 16 (in 1 day)", false},
		{"today", "2026-10-15T23:30:00-07:00", "2026-10-15", "1", "1.00 USD · Oct 15 (today)", false},
		{"overdue", "2026-10-15T23:30:00-07:00", "2026-10-14", "31.63", "31.63 USD · Oct 14 (overdue 1 day)", true},
		{"overdue plural", "2026-10-15T23:30:00-07:00", "2026-10-13", "1", "1.00 USD · Oct 13 (overdue 2 days)", true},
		{"spring DST", "2026-03-08T23:30:00-07:00", "2026-03-09", "1", "1.00 USD · Mar 09 (in 1 day)", false},
		{"fall DST", "2026-11-01T23:30:00-08:00", "2026-11-02", "1", "1.00 USD · Nov 02 (in 1 day)", false},
		{"year boundary", "2026-12-31T23:30:00-08:00", "2027-01-01", "1", "1.00 USD · Jan 01, 2027 (in 1 day)", false},
		{"paid", "2026-10-15T23:30:00-07:00", "2026-10-14", "0", "Paid", false},
		{"unknown", "2026-10-15T23:30:00-07:00", "2026-10-14", "", "Check statement", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tt.now)
			now = now.In(loc)
			due, _ := time.Parse("2006-01-02", tt.due)
			a := model.AccountView{Account: model.Account{Type: "credit", Currency: "USD"}, Liability: &model.CreditLiability{PaymentDueDate: &due}}
			if tt.amount != "" {
				a.RemainingStatementBalance = amount(tt.amount)
			}
			got, overdue := PaymentDue(a, now)
			if got != tt.want || overdue != tt.overdue {
				t.Errorf("PaymentDue = %q, %v; want %q, %v", got, overdue, tt.want, tt.overdue)
			}
		})
	}
	got, _ := PaymentDue(model.AccountView{Account: model.Account{Type: "depository"}}, time.Now())
	if got != "—" {
		t.Errorf("noncredit = %q", got)
	}
}
