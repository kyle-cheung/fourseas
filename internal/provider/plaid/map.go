package plaid

import (
	"fmt"
	"strings"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

// ProviderName is written to every stored row.
const ProviderName = "plaid"

// dateLayout is the format Plaid uses for the transaction date field.
const dateLayout = "2006-01-02"

// account is the small part of a Plaid account that a row needs.
type account struct {
	Name string
	Mask string
}

// accountIndex maps a Plaid account_id to its display details.
type accountIndex map[string]account

func newAccountIndex(accounts []plaidsdk.AccountBase) accountIndex {
	index := make(accountIndex, len(accounts))
	for _, a := range accounts {
		index[a.AccountId] = account{Name: a.Name, Mask: a.GetMask()}
	}
	return index
}

// display returns a human readable account label, for example "Gold Card ••1234".
func (i accountIndex) display(accountID string) string {
	a, ok := i[accountID]
	if !ok {
		return accountID
	}
	if a.Mask == "" {
		return a.Name
	}
	return fmt.Sprintf("%s ••%s", a.Name, a.Mask)
}

// toModel converts one Plaid transaction into the canonical shape.
func toModel(t plaidsdk.Transaction, itemID, institution string, accounts accountIndex) (model.Transaction, error) {
	date, err := time.Parse(dateLayout, t.Date)
	if err != nil {
		return model.Transaction{}, fmt.Errorf("transaction %s: parse date %q: %w", t.TransactionId, t.Date, err)
	}

	currency := t.GetIsoCurrencyCode()
	if currency == "" {
		currency = t.GetUnofficialCurrencyCode()
	}

	return model.Transaction{
		Provider:     ProviderName,
		ExternalID:   t.TransactionId,
		ItemID:       itemID,
		AccountID:    t.AccountId,
		AccountName:  accounts.display(t.AccountId),
		Institution:  institution,
		Date:         date,
		Name:         t.Name,
		MerchantName: t.GetMerchantName(),
		Amount:       t.Amount,
		Currency:     currency,
		Pending:      t.Pending,
		Category:     category(t),
	}, nil
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
func toModels(txs []plaidsdk.Transaction, itemID, institution string, accounts accountIndex) ([]model.Transaction, error) {
	out := make([]model.Transaction, 0, len(txs))
	for _, t := range txs {
		m, err := toModel(t, itemID, institution, accounts)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
