package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// Institution is one linked item, for example an American Express login.
// One institution covers every account behind that login.
type Institution struct {
	Provider        string
	ItemID          string
	InstitutionID   string
	InstitutionName string
	// Env is the provider environment the link was made in, for example
	// "sandbox" or "production".
	Env      string
	LinkedAt time.Time
}

// Account is one card or account inside an institution.
//
// Name holds the provider's official name, or its short name when there is no
// official name. There is no separate official name field.
type Account struct {
	Provider  string
	AccountID string
	ItemID    string

	Name     string
	Mask     string
	Type     string
	Subtype  string
	Currency string

	// Nickname is set by the user and is empty until it is set.
	Nickname string
	// Tracked controls what is shown, not what is fetched. One account cannot
	// be synced alone, because a cursor covers a whole institution.
	Tracked bool

	BalanceCurrent   decimal.NullDecimal
	BalanceAvailable decimal.NullDecimal
	BalanceLimit     decimal.NullDecimal
	BalanceUpdatedAt *time.Time

	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// AccountView is one row of the accounts list: an account with the name of the
// institution it sits behind joined in, for display.
type AccountView struct {
	Account
	// InstitutionName is empty when the institution row is not stored yet.
	InstitutionName string
	// Liability is nil when no credit liability snapshot is stored.
	Liability *CreditLiability
}

// SyncState is where the next incremental sync of one institution starts.
type SyncState struct {
	Provider     string
	ItemID       string
	Cursor       string
	LastSyncedAt *time.Time
	// LastStatus is "ok" or a short failure reason.
	LastStatus string
}

// TransactionView is one row of v_transactions: a transaction with the account
// and institution names joined in, for display.
type TransactionView struct {
	Transaction
	// BaseAmount is Amount in the base currency. It is null when no rate is
	// known, so that a total which cannot be trusted is null instead of a
	// plausible wrong number.
	BaseAmount   decimal.NullDecimal
	BaseCurrency string
	FXRate       decimal.NullDecimal
	// AccountLabel is the nickname, or the account name, or the raw account
	// id when neither is known yet.
	AccountLabel    string
	InstitutionName string
}
