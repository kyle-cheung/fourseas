// Package plaid reads transactions from Plaid.
package plaid

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

// Config holds everything fourseas needs to talk to Plaid.
type Config struct {
	ClientID string
	Secret   string
	// Env is "sandbox" or "production".
	Env string
	// RedirectURI is optional. OAuth institutions such as Scotiabank need one,
	// and it must first be registered in the Plaid dashboard.
	RedirectURI string
	// LinkPort is the local port the one-shot Link server listens on.
	LinkPort int
}

// Validate reports missing or wrong settings before any network call.
func (c Config) Validate() error {
	var missing []string
	if c.ClientID == "" {
		missing = append(missing, "PLAID_CLIENT_ID")
	}
	if c.Secret == "" {
		missing = append(missing, "PLAID_SECRET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing settings in .env: %v", missing)
	}
	if _, err := environment(c.Env); err != nil {
		return err
	}
	if c.LinkPort <= 0 || c.LinkPort > 65535 {
		return fmt.Errorf("link port %d is not usable", c.LinkPort)
	}
	return nil
}

func environment(env string) (plaidsdk.Environment, error) {
	switch env {
	case "sandbox":
		return plaidsdk.Sandbox, nil
	case "production":
		return plaidsdk.Production, nil
	default:
		return "", fmt.Errorf("PLAID_ENV is %q, want \"sandbox\" or \"production\"", env)
	}
}

// newClient builds a Plaid API client from the config.
func newClient(c Config) (*plaidsdk.APIClient, error) {
	env, err := environment(c.Env)
	if err != nil {
		return nil, err
	}

	cfg := plaidsdk.NewConfiguration()
	cfg.AddDefaultHeader("PLAID-CLIENT-ID", c.ClientID)
	cfg.AddDefaultHeader("PLAID-SECRET", c.Secret)
	cfg.UseEnvironment(env)
	return plaidsdk.NewAPIClient(cfg), nil
}

// apiError turns a Plaid SDK error into a message that says what went wrong.
// The SDK returns a generic error, and the useful cause is in the body.
func apiError(op string, err error, resp *http.Response) error {
	if err == nil {
		return nil
	}

	var plaidErr plaidsdk.GenericOpenAPIError
	if errors.As(err, &plaidErr) {
		var body plaidsdk.PlaidError
		if jsonErr := body.UnmarshalJSON(plaidErr.Body()); jsonErr == nil && body.ErrorCode != "" {
			return codedError(op, body.ErrorCode, string(body.GetErrorType()), body.GetErrorMessage())
		}
		if len(plaidErr.Body()) > 0 {
			return fmt.Errorf("%s: plaid error: %s", op, plaidErr.Body())
		}
	}
	if resp != nil {
		return fmt.Errorf("%s: %w (http %d)", op, err, resp.StatusCode)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// mutationDuringPagination is the code Plaid returns when the transaction data
// of an item changed while a page sequence was being read. The cursors of that
// sequence are then dead, and the item must start again from the cursor the last
// whole sync ended on.
const mutationDuringPagination = "TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION"

// codedError builds the error for one Plaid error body. Codes the caller can act
// on carry a sentinel, so that acting on them needs no string matching.
func codedError(op, code, errType, message string) error {
	err := fmt.Errorf("plaid %s (%s): %s", code, errType, message)
	if code == mutationDuringPagination {
		err = fmt.Errorf("%w: %w", provider.ErrRestartPagination, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// institutionName looks up the display name for an item's institution. A
// failure here is not fatal, so it returns an empty string instead of an error.
func institutionName(ctx context.Context, client *plaidsdk.APIClient, accessToken string) string {
	itemResp, _, err := client.PlaidApi.ItemGet(ctx).
		ItemGetRequest(*plaidsdk.NewItemGetRequest(accessToken)).Execute()
	if err != nil {
		return ""
	}

	id := itemResp.Item.GetInstitutionId()
	if id == "" {
		return ""
	}

	req := plaidsdk.NewInstitutionsGetByIdRequest(id, []plaidsdk.CountryCode{
		plaidsdk.COUNTRYCODE_US, plaidsdk.COUNTRYCODE_CA,
	})
	instResp, _, err := client.PlaidApi.InstitutionsGetById(ctx).
		InstitutionsGetByIdRequest(*req).Execute()
	if err != nil {
		return ""
	}
	return instResp.Institution.Name
}
