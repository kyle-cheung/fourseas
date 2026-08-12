package plaid

import (
	"fmt"
	"strings"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
	"github.com/shopspring/decimal"
)

// ProviderName is written to every stored row.
const ProviderName = "plaid"

// dateLayout is the format Plaid uses for the transaction date field.
const dateLayout = "2006-01-02"

// toModel converts one Plaid transaction into the canonical shape.
//
// The account and institution names are not copied onto the row. They live in
// the accounts and institutions tables, and v_transactions joins them in.
func toModel(t plaidsdk.Transaction, itemID string) (model.Transaction, error) {
	date, err := time.Parse(dateLayout, t.Date)
	if err != nil {
		return model.Transaction{}, fmt.Errorf("transaction %s: parse date %q: %w", t.TransactionId, t.Date, err)
	}

	authorized, err := optionalDate(t.GetAuthorizedDate())
	if err != nil {
		return model.Transaction{}, fmt.Errorf("transaction %s: parse authorized date: %w", t.TransactionId, err)
	}

	currency := t.GetIsoCurrencyCode()
	if currency == "" {
		currency = t.GetUnofficialCurrencyCode()
	}

	return model.Transaction{
		Provider:       ProviderName,
		ExternalID:     t.TransactionId,
		ItemID:         itemID,
		AccountID:      t.AccountId,
		Date:           date,
		AuthorizedDate: authorized,
		Name:           t.Name,
		MerchantName:   t.GetMerchantName(),
		// Plaid sends the amount as a JSON number, so this is the one place a
		// float touches the money. It becomes a decimal here and stays one.
		Amount:   decimal.NewFromFloat(t.Amount),
		Currency: currency,
		Pending:  t.Pending,
		Category: category(t),
	}, nil
}

// optionalDate parses a date Plaid may leave empty.
func optionalDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	d, err := time.Parse(dateLayout, s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// category prefers Plaid's personal finance category and falls back to the
// legacy category array.
func category(t plaidsdk.Transaction) string {
	if pfc, ok := t.GetPersonalFinanceCategoryOk(); ok && pfc != nil && pfc.Primary != "" {
		return pfc.Primary
	}
	return strings.Join(t.Category, " > ")
}

// toModels converts a list and stops at the first bad row.
func toModels(txs []plaidsdk.Transaction, itemID string) ([]model.Transaction, error) {
	out := make([]model.Transaction, 0, len(txs))
	for _, t := range txs {
		m, err := toModel(t, itemID)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
