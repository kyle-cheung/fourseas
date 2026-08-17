// Package provider defines the transaction source contract.
//
// Fourseas has one implementation today (Plaid). A second source is a new
// package that satisfies this interface, not a change to the store or the model.
package provider

import (
	"context"
	"errors"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// ErrRestartPagination says the provider's data moved while a page sequence was
// being read, so the cursors of that sequence are no longer usable. The caller
// must start the item again from the cursor the last whole sync ended on.
//
// A provider reports this by wrapping the error it returns from Sync.
var ErrRestartPagination = errors.New("pagination must start again from the last whole sync")

// Batch is one page of incremental changes from a provider.
type Batch struct {
	Added      []model.Transaction
	Modified   []model.Transaction
	RemovedIDs []string
	// Accounts is the full account list of the institution, with balances.
	// Plaid sends it with every page, so balances cost no extra call.
	Accounts   []model.Account
	NextCursor string
	HasMore    bool
}

// Provider reads transactions for one linked institution.
type Provider interface {
	// Name identifies the provider in stored rows, for example "plaid".
	Name() string

	// Sync returns the changes after cursor. An empty cursor starts from the
	// beginning. Call again while Batch.HasMore is true.
	Sync(ctx context.Context, cursor string) (Batch, error)
}
