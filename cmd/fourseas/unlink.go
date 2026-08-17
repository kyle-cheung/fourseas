package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	plaidprovider "github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// unlinkPlan is what one `fourseas unlink` command line asks for.
type unlinkPlan struct {
	// list asks for the linked institutions instead of a removal.
	list bool
	// item is the institution to remove. It is empty when list is true.
	item tokens.Item
	// confirmed is true when --yes was given, so nothing is asked.
	confirmed bool
}

// removeFunc removes the item at the provider. Tests supply their own.
type removeFunc func(ctx context.Context, cfg plaidprovider.Config, accessToken string) error

// runUnlink removes one linked institution: first at Plaid, then locally.
//
// The order matters. Plaid bills the transactions product for every live item,
// each month, and a token deleted on its own leaves that item alive with no way
// left to reach it. Plaid is therefore called first, and the local data is only
// deleted once Plaid has agreed.
func runUnlink(ctx context.Context, cfg settings, options []string) error {
	return runUnlinkWith(ctx, cfg, options, os.Stdin, os.Stdout, plaidprovider.Remove)
}

func runUnlinkWith(ctx context.Context, cfg settings, options []string,
	in io.Reader, out io.Writer, remove removeFunc) error {

	saved, err := tokens.Load(cfg.tokensPath)
	if err != nil {
		return err
	}
	plan, err := planUnlink(saved, cfg.plaid.Env, options)
	if err != nil {
		return err
	}
	if plan.list {
		printLinked(out, saved)
		return nil
	}
	if err := cfg.plaid.Validate(); err != nil {
		return err
	}

	db, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	counts, err := db.ItemCounts(ctx, plaidprovider.ProviderName, plan.item.ItemID)
	if err != nil {
		return err
	}

	if !plan.confirmed {
		confirmed, err := confirmUnlink(in, out, plan.item, counts)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(out, "Nothing was changed.")
			return nil
		}
	}

	// A failure here keeps the token and the rows, so the command can be run
	// again. An item Plaid no longer holds is what was wanted, so it continues.
	if err := remove(ctx, cfg.plaid, plan.item.AccessToken); err != nil {
		if !errors.Is(err, provider.ErrItemGone) {
			return fmt.Errorf("plaid still has this item, so nothing local was deleted: %w", err)
		}
		fmt.Fprintln(out, "Plaid does not have this item any more. Deleting the local data.")
	} else {
		fmt.Fprintf(out, "Removed the item at Plaid. It is no longer billed.\n")
	}

	removed, err := db.Unlink(ctx, plaidprovider.ProviderName, plan.item.ItemID)
	if err != nil {
		return fmt.Errorf("the item is gone at Plaid but the local data is not: %w", err)
	}

	// The token is last: while the local rows are there, the token is what
	// names them, and a second run needs it.
	if err := tokens.Save(cfg.tokensPath, saved.Delete(plan.item.ItemID)); err != nil {
		return err
	}

	fmt.Fprintf(out, "Unlinked %s: %d transactions and %d accounts deleted from %s.\n",
		label(plan.item), removed.Transactions, removed.Accounts, cfg.dbPath)
	fmt.Fprintln(out, "Run `fourseas link` to link the card again with a fresh history window.")
	return nil
}

// planUnlink reads the unlink command line against what is linked.
//
// One call removes one item. Two ids in one call is refused rather than
// guessed at, because every removal is final.
func planUnlink(saved tokens.File, env string, options []string) (unlinkPlan, error) {
	var (
		plan   unlinkPlan
		itemID string
	)

	for _, option := range options {
		switch {
		case option == "--list" || option == "-l":
			plan.list = true
		case yesOption(option):
			plan.confirmed = true
		case strings.HasPrefix(option, "-"):
			return unlinkPlan{}, fmt.Errorf("unknown option %q: `fourseas unlink` takes "+
				"<item-id>, --list, and --yes", option)
		case itemID != "":
			return unlinkPlan{}, fmt.Errorf("unlink takes one item id, got %q and %q: "+
				"remove one institution at a time", itemID, option)
		default:
			itemID = option
		}
	}

	switch {
	case plan.list && itemID != "":
		return unlinkPlan{}, fmt.Errorf("--list takes no item id, got %q", itemID)
	case plan.list:
		return plan, nil
	case itemID == "":
		return unlinkPlan{}, fmt.Errorf("unlink needs an item id: " +
			"run `fourseas unlink --list` to see the linked institutions")
	}

	item, found := saved.Find(itemID)
	if !found {
		return unlinkPlan{}, fmt.Errorf("%q is not linked. Linked: %s",
			itemID, strings.Join(itemIDs(saved), ", "))
	}
	// The token only works in the environment that issued it, and the removal
	// must reach Plaid. Sending it to the other environment would fail with
	// INVALID_ACCESS_TOKEN and leave the item live and billed.
	if !item.UsableIn(env) {
		return unlinkPlan{}, fmt.Errorf("%s was linked in %s, and PLAID_ENV is %s: "+
			"set PLAID_ENV to %s to remove it", label(item), item.Env, env, item.Env)
	}
	plan.item = item
	return plan, nil
}

// confirmUnlink shows what will be deleted and waits for the word yes.
func confirmUnlink(in io.Reader, out io.Writer, item tokens.Item, counts store.Removed) (bool, error) {
	fmt.Fprintf(out, "This removes %s (%s) at Plaid and deletes it here:\n",
		label(item), item.ItemID)
	fmt.Fprintf(out, "  %d transactions\n", counts.Transactions)
	fmt.Fprintf(out, "  %d accounts\n", counts.Accounts)
	fmt.Fprintln(out, "The removal at Plaid cannot be undone. To get this data back you link the")
	fmt.Fprintln(out, "card again, which builds a new history window and new transaction ids.")

	return confirmYes(in, out)
}

// printLinked lists what can be unlinked, with the ids the command takes.
func printLinked(out io.Writer, saved tokens.File) {
	if len(saved.Items) == 0 {
		fmt.Fprintln(out, "Nothing is linked. Run `fourseas link` first.")
		return
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "INSTITUTION\tENV\tLINKED\tITEM ID")
	for _, item := range saved.Items {
		linkedAt := ""
		if !item.LinkedAt.IsZero() {
			linkedAt = item.LinkedAt.Format("2006-01-02")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", truncate(label(item)), item.Env, linkedAt, item.ItemID)
	}
	w.Flush()

	fmt.Fprintln(out, "\nRun `fourseas unlink <item-id>` to remove one of these.")
}

func itemIDs(saved tokens.File) []string {
	if len(saved.Items) == 0 {
		return []string{"nothing"}
	}
	out := make([]string, 0, len(saved.Items))
	for _, item := range saved.Items {
		out = append(out, item.ItemID)
	}
	return out
}
