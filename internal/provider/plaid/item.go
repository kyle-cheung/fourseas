package plaid

import (
	"context"

	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

// Remove deletes the item at Plaid.
//
// This is what stops the monthly bill for the item, and it is the only way to
// change how much history the item holds: Plaid fixes the window when the item
// is created, so more history needs a new item.
//
// A removed item's access token stops working at once. Call this before the
// local data is deleted, so that a failure leaves everything as it was.
func Remove(ctx context.Context, cfg Config, accessToken string) error {
	client, err := newClient(cfg)
	if err != nil {
		return err
	}

	req := plaidsdk.NewItemRemoveRequest(accessToken)
	_, httpResp, err := client.PlaidApi.ItemRemove(ctx).ItemRemoveRequest(*req).Execute()
	if err != nil {
		return apiError("remove item", err, httpResp)
	}
	return nil
}
