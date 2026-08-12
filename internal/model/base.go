package model

import (
	"strings"

	"github.com/shopspring/decimal"
)

// BaseCurrency is the currency every total is reported in.
const BaseCurrency = "USD"

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
