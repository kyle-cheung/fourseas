// Package provider defines the transaction source contract.
//
// Fourseas has one implementation today (Plaid). A second source is a new
// package that satisfies this interface, not a change to the store or the model.
package provider

import (
	"context"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// Batch is one page of incremental changes from a provider.
type Batch struct {
	Added      []model.Transaction
	Modified   []model.Transaction
	RemovedIDs []string
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
