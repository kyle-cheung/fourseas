package plaid

import (
	"context"
	"fmt"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

// pageSize is how many transactions to ask for in one call.
const pageSize = 500

// Source reads transactions for one linked item. It satisfies provider.Provider.
type Source struct {
	client      *plaidsdk.APIClient
	accessToken string
	itemID      string
	institution string
}

// NewSource builds a reader for one linked institution.
func NewSource(cfg Config, accessToken, itemID, institution string) (*Source, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Source{
		client:      client,
		accessToken: accessToken,
		itemID:      itemID,
		institution: institution,
	}, nil
}

// Name returns the provider name written to every row.
func (s *Source) Name() string { return ProviderName }

// Sync returns one page of changes after cursor. An empty cursor starts from
// the beginning of the available history.
func (s *Source) Sync(ctx context.Context, cursor string) (provider.Batch, error) {
	req := plaidsdk.NewTransactionsSyncRequest(s.accessToken)
	req.SetCount(pageSize)
	if cursor != "" {
		req.SetCursor(cursor)
	}

	resp, httpResp, err := s.client.PlaidApi.TransactionsSync(ctx).
		TransactionsSyncRequest(*req).Execute()
	if err != nil {
		return provider.Batch{}, apiError(fmt.Sprintf("sync item %s", s.itemID), err, httpResp)
	}
	accounts := resp.Accounts
	if resp.GetTransactionsUpdateStatus() == plaidsdk.TRANSACTIONSUPDATESTATUS_NOT_READY && len(accounts) == 0 {
		req := plaidsdk.NewAccountsGetRequest(s.accessToken)
		accountResp, httpResp, err := s.client.PlaidApi.AccountsGet(ctx).
			AccountsGetRequest(*req).Execute()
		if err != nil {
			return provider.Batch{}, apiError(fmt.Sprintf("get accounts for item %s", s.itemID), err, httpResp)
		}
		accounts = accountResp.Accounts
		if len(accounts) == 0 {
			return provider.Batch{}, fmt.Errorf("sync item %s: %w", s.itemID, provider.ErrProductNotReady)
		}
	}

	added, err := toModels(resp.Added, s.itemID)
	if err != nil {
		return provider.Batch{}, err
	}
	modified, err := toModels(resp.Modified, s.itemID)
	if err != nil {
		return provider.Batch{}, err
	}

	removed := make([]string, 0, len(resp.Removed))
	for _, r := range resp.Removed {
		removed = append(removed, r.GetTransactionId())
	}

	return provider.Batch{
		Added:      added,
		Modified:   modified,
		RemovedIDs: removed,
		// A ready sync carries the accounts. The fallback above supplies them
		// while the transaction data is still pending.
		Accounts:   toAccounts(accounts, s.itemID, time.Now().UTC()),
		NextCursor: resp.NextCursor,
		HasMore:    resp.HasMore,
	}, nil
}
