// Package model holds the canonical shapes fourseas stores and prints.
// It must not import any provider SDK.
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// Transaction is one card transaction, normalized across providers.
//
// Amount keeps the Plaid sign convention: a positive amount is money leaving
// the account. On a credit card a purchase is positive and a payment or refund
// is negative.
//
// Money is a decimal, never a float. A float sum of money is wrong, and this is
// a financial planning application.
type Transaction struct {
	Provider   string
	ExternalID string
	ItemID     string
	AccountID  string

	// Date is the posted date. AuthorizedDate is null when the provider does
	// not supply one.
	Date           time.Time
	AuthorizedDate *time.Time

	// Name is the raw description. MerchantName is the cleaned name, and is
	// often empty.
	Name         string
	MerchantName string

	Amount   decimal.Decimal
	Currency string

	Pending bool
	// PendingTransactionID is the pending row that this posted row replaces.
	PendingTransactionID string
	// SupersededBy is set on a pending row when its posted row arrives.
	SupersededBy string

	Category string
	SyncedAt time.Time
}
