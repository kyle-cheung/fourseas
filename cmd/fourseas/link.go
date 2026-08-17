package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	plaidprovider "github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// How much transaction history a new link asks Plaid for. The amount is fixed
// when the item is created, so fourseas asks for everything Plaid permits.
// Plaid raises a request under 30 days to 30, and requests 90 days when nothing
// is asked for.
const defaultLinkDays = app.MaxLinkDays

// runLink links one card and saves its access token. Run it once for each card.
func runLink(ctx context.Context, cfg settings, options []string) error {
	days, err := parseLinkOptions(options)
	if err != nil {
		return err
	}
	if err := cfg.plaid.Validate(); err != nil {
		return err
	}

	fmt.Printf("Plaid environment: %s\n", cfg.plaid.Env)
	fmt.Printf("History requested: %d days. Plaid fixes this now and cannot change it later.\n", days)
	if cfg.plaid.RedirectURI == "" {
		fmt.Printf("No PLAID_REDIRECT_URI is set. Banks that use OAuth, such as Scotiabank,\n" +
			"will not finish. To use one, register this URI in the Plaid dashboard\n" +
			"under Developers > API > Allowed redirect URIs, then put it in .env:\n")
		fmt.Printf("  http://localhost:%d/oauth\n\n", cfg.plaid.LinkPort)
	}

	fmt.Printf("Open %s in your browser to sign in to the bank.\n", plaidprovider.LinkURL(cfg.plaid))

	result, err := plaidprovider.Link(ctx, cfg.plaid, days)
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
		Env:         cfg.plaid.Env,
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
	fmt.Println("Run `fourseas link` again for the next card, or `fourseas sync` to fetch transactions.")
	return nil
}

// parseLinkOptions reads the link command line. It accepts --days N and
// --days=N, and returns the amount of history to request.
func parseLinkOptions(options []string) (int, error) {
	days := defaultLinkDays

	for i := 0; i < len(options); i++ {
		option := options[i]
		switch {
		case option == "--days":
			if i+1 >= len(options) {
				return 0, fmt.Errorf("--days needs a number of days, for example `--days 730`")
			}
			i++
			parsed, err := parseLinkDays(options[i])
			if err != nil {
				return 0, err
			}
			days = parsed
		case strings.HasPrefix(option, "--days="):
			parsed, err := parseLinkDays(strings.TrimPrefix(option, "--days="))
			if err != nil {
				return 0, err
			}
			days = parsed
		default:
			return 0, fmt.Errorf("unknown option %q: `fourseas link` takes --days <n> or nothing", option)
		}
	}

	return days, nil
}

// parseLinkDays reads one --days value. The bounds are the ones Plaid honours,
// so a value it would round or refuse fails here, before the browser opens.
func parseLinkDays(value string) (int, error) {
	days, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("--days wants a whole number of days, got %q", value)
	}
	if days < app.MinLinkDays || days > app.MaxLinkDays {
		return 0, fmt.Errorf("--days wants %d to %d days, got %d", app.MinLinkDays, app.MaxLinkDays, days)
	}
	return days, nil
}
