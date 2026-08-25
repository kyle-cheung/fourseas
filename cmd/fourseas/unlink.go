package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	plaidprovider "github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
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
// The command owns the wording and the confirmation. The order of the
// irreversible steps belongs to app.Unlink.
func runUnlink(ctx context.Context, cfg settings, options []string) error {
	return runUnlinkWith(ctx, cfg, options, os.Stdin, os.Stdout, plaidprovider.Remove)
}

func runUnlinkWith(ctx context.Context, cfg settings, options []string,
	in io.Reader, out io.Writer, remove removeFunc) error {

	saved, err := tokens.Load(cfg.tokensPath)
	if err != nil {
		return err
	}
	plan, err := planUnlink(saved, options)
	if err != nil {
		return err
	}
	if plan.list {
		printLinked(out, saved)
		return nil
	}

	client := app.New(cfg.appConfig(), app.WithRemove(remove))

	preview, err := client.UnlinkPreview(ctx, plan.item.ItemID)
	if err != nil {
		return err
	}

	if !plan.confirmed {
		confirmed, err := confirmUnlink(in, out, plan.item, preview.Rows)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(out, "Nothing was changed.")
			return nil
		}
	}

	result, err := client.Unlink(ctx, plan.item.ItemID, nil)
	if err != nil {
		return err
	}

	if result.PlaidItemGone {
		fmt.Fprintln(out, "Plaid does not have this item any more. Deleting the local data.")
	} else {
		fmt.Fprintln(out, "Removed the item at Plaid. Active subscriptions for this Item end.")
	}

	fmt.Fprintf(out, "Unlinked %s: %d transactions and %d accounts deleted from %s.\n",
		app.Label(plan.item), result.Rows.Transactions, result.Rows.Accounts, cfg.dbPath)
	fmt.Fprintln(out, "Run `fourseas link` to link the card again with a fresh history window.")
	return nil
}

// planUnlink reads the unlink command line against what is linked.
//
// One call removes one item. Two ids in one call is refused rather than
// guessed at, because every removal is final. The environment of the item is
// checked by app.UnlinkPreview, not here.
func planUnlink(saved tokens.File, options []string) (unlinkPlan, error) {
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
	plan.item = item
	return plan, nil
}

// confirmUnlink shows what will be deleted and waits for the word yes.
func confirmUnlink(in io.Reader, out io.Writer, item tokens.Item, counts app.RowCounts) (bool, error) {
	fmt.Fprintf(out, "This removes %s (%s) at Plaid and deletes it here:\n",
		app.Label(item), item.ItemID)
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", truncate(app.Label(item)), item.Env, linkedAt, item.ItemID)
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
