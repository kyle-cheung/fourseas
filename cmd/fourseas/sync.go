package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	plaidprovider "github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// maxPages stops a runaway pagination loop.
const maxPages = 100

// maxRestarts is how many times one item may start its pages again because the
// provider's data moved while they were read.
const maxRestarts = 5

// legacyCursorFailures is how many immediate mutation errors show that a saved
// cursor came from an unfinished pagination sequence made by an older version.
const legacyCursorFailures = 2

// rowsToShow is how many rows fourseas prints after a sync.
const rowsToShow = 10

// runSync fetches new transactions for every linked card and prints the newest.
func runSync(ctx context.Context, cfg settings, options []string) error {
	return runSyncWith(ctx, cfg, options, newFXSource(), os.Stdout)
}

func runSyncWith(ctx context.Context, cfg settings, options []string, source fxSource, out io.Writer) error {
	fxOnly, err := parseSyncOptions(options)
	if err != nil {
		return err
	}
	if fxOnly {
		db, err := store.Open(cfg.dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		return runFXPhase(ctx, db, source, time.Now(), true, out)
	}

	if err := cfg.plaid.Validate(); err != nil {
		return err
	}

	saved, err := tokens.Load(cfg.tokensPath)
	if err != nil {
		return err
	}
	if len(saved.Items) == 0 {
		return fmt.Errorf("nothing is linked yet: run `fourseas link` first (looked in %s)", cfg.tokensPath)
	}

	db, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	// One bad card must not stop the others. Report each failure and carry on,
	// then print whatever was stored.
	attempted, failed := 0, 0
	for _, item := range saved.Items {
		if !item.UsableIn(cfg.plaid.Env) {
			fmt.Printf("%-28s skipped: linked in %s, and PLAID_ENV is %s\n",
				label(item), item.Env, cfg.plaid.Env)
			continue
		}

		attempted++
		if err := syncItem(ctx, cfg, db, item); err != nil {
			failed++
			fmt.Printf("%-28s failed: %v\n", label(item), err)
			// Record why, and leave the cursor where it was, so the next run
			// asks for the same page again.
			if err := db.SetStatus(ctx, plaidprovider.ProviderName, item.ItemID, err.Error()); err != nil {
				fmt.Printf("%-28s could not record the failure: %v\n", label(item), err)
			}
		}
	}

	if attempted > 0 && failed == attempted {
		return fmt.Errorf("every card failed to sync")
	}
	if err := runFXPhase(ctx, db, source, time.Now(), false, out); err != nil {
		return err
	}
	return printNewest(ctx, db, rowsToShow)
}

func parseSyncOptions(options []string) (bool, error) {
	if len(options) == 0 {
		return false, nil
	}
	if len(options) == 1 && options[0] == "--fx" {
		return true, nil
	}
	return false, fmt.Errorf("unknown sync options: %q", options)
}

// label is the name to show for an item in terminal output.
func label(item tokens.Item) string {
	if item.Institution != "" {
		return item.Institution
	}
	return item.ItemID
}

// syncItem pages through one item's changes and writes them to the store.
func syncItem(ctx context.Context, cfg settings, db *store.Store, item tokens.Item) error {
	source, err := plaidprovider.NewSource(cfg.plaid, item.AccessToken, item.ItemID, item.Institution)
	if err != nil {
		return err
	}

	// The accounts written below refer to this institution, so record it
	// before the first page lands.
	if err := recordInstitution(ctx, db, item, cfg.plaid.Env); err != nil {
		return err
	}

	counts, err := drain(ctx, db, source, item.ItemID)
	if err != nil {
		return err
	}

	fmt.Printf("%-28s %d added, %d modified, %d removed\n",
		label(item), counts.added, counts.modified, counts.removed)
	return nil
}

// recordInstitution stores the linked item, so the accounts list can name the
// login an account sits behind. What is known lives in the token file: an
// item that was linked before this field existed has no environment, so the
// current one is used.
func recordInstitution(ctx context.Context, db *store.Store, item tokens.Item, env string) error {
	if item.Env != "" {
		env = item.Env
	}
	return db.UpsertInstitution(ctx, model.Institution{
		Provider:        plaidprovider.ProviderName,
		ItemID:          item.ItemID,
		InstitutionName: item.Institution,
		Env:             env,
		LinkedAt:        item.LinkedAt,
	})
}

// counts is what one drain changed.
type counts struct {
	added    int
	modified int
	removed  int
}

// drain reads every page of one item. If the provider changes the data during
// pagination, the next attempt uses the cursor that started the sequence.
func drain(ctx context.Context, db *store.Store, source provider.Provider, itemID string) (counts, error) {
	cursor, err := db.Cursor(ctx, source.Name(), itemID)
	if err != nil {
		return counts{}, err
	}

	immediateFailures := 0
	for attempt := 1; ; attempt++ {
		total, pagesRead, err := drainFrom(ctx, db, source, itemID, cursor)
		if err == nil {
			return total, nil
		}
		if !errors.Is(err, provider.ErrRestartPagination) {
			return total, err
		}
		if attempt >= maxRestarts {
			return total, fmt.Errorf("the data kept changing over %d attempts: %w", attempt, err)
		}

		if pagesRead == 0 && cursor != "" {
			immediateFailures++
		} else {
			immediateFailures = 0
		}

		if immediateFailures >= legacyCursorFailures {
			fmt.Printf("  %s: the saved cursor came from an unfinished page sequence. Reading the whole history once.\n", itemID)
			cursor = ""
			immediateFailures = 0
			if err := db.ClearCursor(ctx, source.Name(), itemID); err != nil {
				return total, err
			}
		} else {
			fmt.Printf("  %s: the data changed while paging. Retrying from the starting cursor (attempt %d of %d).\n",
				itemID, attempt+1, maxRestarts)
		}
	}
}

// drainFrom applies each page, but it keeps the starting cursor as the durable
// checkpoint until the final page completes.
func drainFrom(ctx context.Context, db *store.Store, source provider.Provider, itemID, cursor string) (counts, int, error) {
	var total counts
	startCursor := cursor
	pagesRead := 0

	for page := 0; page < maxPages; page++ {
		batch, err := source.Sync(ctx, cursor)
		if err != nil {
			return total, pagesRead, err
		}

		checkpoint := batch.NextCursor
		if batch.HasMore {
			checkpoint = startCursor
		}
		if err := db.ApplyPage(ctx, toPage(source.Name(), itemID, batch, checkpoint)); err != nil {
			return total, pagesRead, err
		}
		pagesRead++

		total.added += len(batch.Added)
		total.modified += len(batch.Modified)
		total.removed += len(batch.RemovedIDs)
		cursor = batch.NextCursor

		if !batch.HasMore {
			return total, pagesRead, nil
		}
	}

	return total, pagesRead, fmt.Errorf("stopped after %d pages: the provider still reports more", maxPages)
}

// toPage turns one provider batch into the unit the store commits.
func toPage(providerName, itemID string, batch provider.Batch, cursor string) store.Page {
	return store.Page{
		Provider:   providerName,
		ItemID:     itemID,
		Added:      batch.Added,
		Modified:   batch.Modified,
		RemovedIDs: batch.RemovedIDs,
		Accounts:   batch.Accounts,
		Cursor:     cursor,
	}
}

// runShow prints stored rows without calling Plaid.
func runShow(ctx context.Context, cfg settings) error {
	db, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	return printNewest(ctx, db, rowsToShow)
}

func printNewest(ctx context.Context, db *store.Store, n int) error {
	total, err := db.Count(ctx)
	if err != nil {
		return err
	}

	rows, err := db.NewestView(ctx, n)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("\nNo transactions are stored yet.")
		return nil
	}

	fmt.Printf("\nNewest %d of %d stored transactions:\n\n", len(rows), total)
	printTable(rows)
	return nil
}
