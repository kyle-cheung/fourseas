package main

import (
	"context"
	"fmt"
	"time"

	plaidprovider "github.com/kyle-cheung/fourseas2/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas2/providence/internal/tokens"
)

// runLink links one card and saves its access token. Run it once for each card.
func runLink(ctx context.Context, cfg settings) error {
	if err := cfg.plaid.Validate(); err != nil {
		return err
	}

	fmt.Printf("Plaid environment: %s\n", cfg.plaid.Env)
	if cfg.plaid.RedirectURI == "" {
		fmt.Printf("No PLAID_REDIRECT_URI is set. Banks that use OAuth, such as Scotiabank,\n" +
			"will not finish. To use one, register this URI in the Plaid dashboard\n" +
			"under Team Settings > API > Allowed redirect URIs, then put it in .env:\n")
		fmt.Printf("  http://localhost:%d/oauth\n\n", cfg.plaid.LinkPort)
	}

	result, err := plaidprovider.Link(ctx, cfg.plaid)
	if err != nil {
		return err
	}

	saved, err := tokens.Load(cfg.tokensPath)
	if err != nil {
		return err
	}
	saved = saved.Upsert(tokens.Item{
		ItemID:      result.ItemID,
		AccessToken: result.AccessToken,
		Institution: result.Institution,
		LinkedAt:    time.Now().UTC(),
	})
	if err := tokens.Save(cfg.tokensPath, saved); err != nil {
		return err
	}

	name := result.Institution
	if name == "" {
		name = result.ItemID
	}
	fmt.Printf("\nLinked %s. Token saved to %s (%d linked in total).\n",
		name, cfg.tokensPath, len(saved.Items))
	fmt.Println("Run `probe link` again for the next card, or `probe sync` to fetch transactions.")
	return nil
}
