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
		// A posted row names the pending row it replaces. The store uses it to
		// hide the duplicate; nothing here proposes a superseded_by.
		PendingTransactionID: t.GetPendingTransactionId(),
		Category:             category(t),
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

// toAccount converts one Plaid account into the canonical shape.
//
// seen is the time of the sync that returned the account. It stamps
// first_seen_at, last_seen_at, and the balance, so every row of one sync
// carries the same time.
//
// Nickname and Tracked are left at their zero and default values on purpose:
// they belong to the user, and the store keeps the stored ones.
func toAccount(a plaidsdk.AccountBase, itemID string, seen time.Time) model.Account {
	name := a.GetOfficialName()
	if name == "" {
		name = a.Name
	}

	currency := a.Balances.GetIsoCurrencyCode()
	if currency == "" {
		currency = a.Balances.GetUnofficialCurrencyCode()
	}

	// Plaid reports when the balance was last updated for one institution
	// only (Capital One). For every other institution the sync time is the
	// best answer available.
	updated := seen
	if reported, ok := a.Balances.GetLastUpdatedDatetimeOk(); ok && reported != nil {
		updated = *reported
	}

	return model.Account{
		Provider:  ProviderName,
		AccountID: a.AccountId,
		ItemID:    itemID,

		Name:     name,
		Mask:     a.GetMask(),
		Type:     string(a.Type),
		Subtype:  string(a.GetSubtype()),
		Currency: currency,

		Tracked: true,

		// Plaid sends balances as JSON numbers, so this is where a float
		// becomes a decimal and stays one.
		BalanceCurrent:   optionalAmount(a.Balances.GetCurrentOk()),
		BalanceAvailable: optionalAmount(a.Balances.GetAvailableOk()),
		BalanceLimit:     optionalAmount(a.Balances.GetLimitOk()),
		BalanceUpdatedAt: &updated,

		FirstSeenAt: seen,
		LastSeenAt:  seen,
	}
}

// optionalAmount reads a balance Plaid may send as null.
func optionalAmount(value *float64, ok bool) decimal.NullDecimal {
	if !ok || value == nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: decimal.NewFromFloat(*value), Valid: true}
}

// toAccounts converts the account list one sync response carries.
func toAccounts(accounts []plaidsdk.AccountBase, itemID string, seen time.Time) []model.Account {
	out := make([]model.Account, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, toAccount(a, itemID, seen))
	}
	return out
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
