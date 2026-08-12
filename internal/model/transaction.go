// Package model holds the canonical shapes the probe stores and prints.
// It must not import any provider SDK.
package model

import "time"

// Transaction is one card transaction, normalized across providers.
//
// Amount keeps the Plaid sign convention: a positive amount is money leaving
// the account. On a credit card a purchase is positive and a payment or refund
// is negative.
type Transaction struct {
	Provider     string
	ExternalID   string
	ItemID       string
	AccountID    string
	AccountName  string
	Institution  string
	Date         time.Time
	Name         string
	MerchantName string
	Amount       float64
	Currency     string
	Pending      bool
	Category     string
}
