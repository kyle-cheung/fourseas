package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// How much transaction history a new link asks Plaid for. The amount is fixed
// when the item is created, so fourseas asks for everything Plaid permits.
// Plaid raises a request under 30 days to 30, and requests 90 days when nothing
// is asked for.
const defaultLinkDays = app.MaxLinkDays

type linkOptions struct {
	days        int
	liabilities bool
}

// runLink links one card and saves its access token. Run it once for each card.
func runLink(ctx context.Context, cfg settings, arguments []string) error {
	options, err := parseLinkOptions(arguments)
	if err != nil {
		return err
	}
	return runLinkWith(ctx, cfg, options, app.New(cfg.appConfig()))
}

func runLinkWith(ctx context.Context, cfg settings, options linkOptions, svc linkService) error {
	if err := cfg.plaid.Validate(); err != nil {
		return err
	}

	fmt.Printf("Plaid environment: %s\n", cfg.plaid.Env)
	fmt.Printf("History requested: %d days. Plaid fixes this now and cannot change it later.\n", options.days)
	if options.liabilities {
		fmt.Println("Statement data: enabled. Plaid Liabilities can have separate billing.")
	}
	if cfg.plaid.RedirectURI == "" {
		fmt.Printf("No PLAID_REDIRECT_URI is set. Banks that use OAuth, such as Scotiabank,\n" +
			"will not finish. To use one, register this URI in the Plaid dashboard\n" +
			"under Developers > API > Allowed redirect URIs, then put it in .env:\n")
		fmt.Printf("  http://localhost:%d/oauth\n\n", cfg.plaid.LinkPort)
	}

	// app.Link reads the token file before the browser opens, so an unreadable
	// one fails before Plaid creates an item it would bill. Doing it here in
	// the other order would drop the only handle to that item.
	linked, err := linkOnce(ctx, svc, options.days, options.liabilities, signInLine)
	if err != nil {
		return err
	}

	saved, err := tokens.Load(cfg.tokensPath)
	if err != nil {
		return err
	}

	name := linked.Institution
	if name == "" {
		name = linked.ItemID
	}
	fmt.Printf("\nLinked %s. Token saved to %s (%d linked in total).\n",
		name, cfg.tokensPath, len(saved.Items))
	fmt.Println("Run `fourseas link` again for the next card, or `fourseas sync` to fetch transactions.")
	return nil
}

// linkService is the part of the façade the link command uses. *app.App
// satisfies it.
type linkService interface {
	Link(context.Context, int, bool, app.Progress) (app.LinkedItem, error)
	CompleteLinkSave(app.PendingSave) (app.LinkedItem, error)
}

// linkOnce links one card and makes sure its access token reaches the disk.
//
// Plaid bills the item it creates, and the access token is the only handle to
// that item. A save that fails is therefore tried once more from the handle the
// failure carries. The browser step never runs a second time: a new link
// creates a second billed item at the same bank.
func linkOnce(ctx context.Context, svc linkService, days int, liabilities bool, report app.Progress) (app.LinkedItem, error) {
	linked, err := svc.Link(ctx, days, liabilities, report)
	var notSaved *app.TokenNotSavedError
	if !errors.As(err, &notSaved) {
		return linked, err
	}

	fmt.Println("\nThe bank connected. The access token was not saved. Saving it again.")
	saved, retryErr := svc.CompleteLinkSave(notSaved.Pending)
	if retryErr == nil {
		return saved, nil
	}
	// The reason the user reads is why the *retry* failed. A retry that fails
	// with a plain error keeps the handle of the first failure, because that
	// handle still names the item Plaid bills.
	cause := retryErr
	var again *app.TokenNotSavedError
	if errors.As(retryErr, &again) {
		notSaved, cause = again, again.Err
	}
	return app.LinkedItem{}, tokenNotSavedMessage(
		notSaved.Pending.Institution(), notSaved.Pending.ItemID(), cause)
}

// tokenNotSavedMessage is what the user reads when the token of a billed item
// stays unsaved. It names the item, because the Plaid dashboard is the only
// place left to act on it, and it refuses the one recovery the user would try
// first.
func tokenNotSavedMessage(institution, itemID string, cause error) error {
	name := institution
	if name == "" {
		name = itemID
	}
	return fmt.Errorf(`the access token of a billed item was not saved: %w

  Institution: %s
  Item id:     %s

Plaid created this item and bills it each month. The access token is the only
way to reach it, and fourseas did not write it to disk.

Do NOT run `+"`fourseas link`"+` to recover this item. A new link creates a SECOND
billed item at the same bank, and this item stays billed.

Make the token file writable, then link again only if you accept a second item.
To stop the charge for this item, remove it in the Plaid dashboard.`,
		cause, name, itemID)
}

// signInLinePrefix is how app.Link starts the one progress line that names the
// sign-in URL. It is reported once, just before the browser step.
const signInLinePrefix = "Open "

// signInLine prints the sign-in instruction of the command line. Every other
// progress line app.Link reports is for the terminal interface, which shows a
// status line the command line does not have.
func signInLine(line string) {
	if strings.HasPrefix(line, signInLinePrefix) {
		fmt.Println(line + " to sign in to the bank.")
	}
}

// parseLinkOptions reads the link command line. Both options accept a separate
// value or an equals sign.
func parseLinkOptions(arguments []string) (linkOptions, error) {
	options := linkOptions{days: defaultLinkDays, liabilities: true}

	for i := 0; i < len(arguments); i++ {
		option := arguments[i]
		switch {
		case option == "--days":
			if i+1 >= len(arguments) {
				return linkOptions{}, fmt.Errorf("--days needs a number of days, for example `--days 730`")
			}
			i++
			parsed, err := parseLinkDays(arguments[i])
			if err != nil {
				return linkOptions{}, err
			}
			options.days = parsed
		case strings.HasPrefix(option, "--days="):
			parsed, err := parseLinkDays(strings.TrimPrefix(option, "--days="))
			if err != nil {
				return linkOptions{}, err
			}
			options.days = parsed
		case option == "--liabilities":
			if i+1 >= len(arguments) {
				return linkOptions{}, fmt.Errorf("--liabilities needs true or false, for example `--liabilities false`")
			}
			i++
			parsed, err := parseLinkLiabilities(arguments[i])
			if err != nil {
				return linkOptions{}, err
			}
			options.liabilities = parsed
		case strings.HasPrefix(option, "--liabilities="):
			parsed, err := parseLinkLiabilities(strings.TrimPrefix(option, "--liabilities="))
			if err != nil {
				return linkOptions{}, err
			}
			options.liabilities = parsed
		default:
			return linkOptions{}, fmt.Errorf("unknown option %q: `fourseas link` takes --days <n> and --liabilities <bool>", option)
		}
	}

	return options, nil
}

func parseLinkLiabilities(value string) (bool, error) {
	liabilities, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("--liabilities wants true or false, got %q", value)
	}
	return liabilities, nil
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
