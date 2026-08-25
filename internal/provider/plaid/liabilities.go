package plaid

import (
	"context"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

// Liabilities returns the current credit liability snapshot for one item.
func Liabilities(ctx context.Context, cfg Config, accessToken string) ([]model.CreditLiability, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	return liabilities(ctx, client, accessToken, time.Now().UTC())
}

func liabilities(
	ctx context.Context,
	client *plaidsdk.APIClient,
	accessToken string,
	fetchedAt time.Time,
) ([]model.CreditLiability, error) {
	req := plaidsdk.NewLiabilitiesGetRequest(accessToken)
	resp, httpResp, err := client.PlaidApi.LiabilitiesGet(ctx).
		LiabilitiesGetRequest(*req).Execute()
	if err != nil {
		return nil, apiError("get liabilities", err, httpResp)
	}
	return toCreditLiabilities(resp.Liabilities.Credit, resp.Item.ItemId, fetchedAt), nil
}
