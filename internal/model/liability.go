package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// CreditLiability is the latest credit liability snapshot for one account.
type CreditLiability struct {
	Provider  string
	ItemID    string
	AccountID string

	PaymentDueDate    *time.Time
	LastPaymentDate   *time.Time
	LastPaymentAmount decimal.NullDecimal
	FetchedAt         time.Time
}
