package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
)

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

	// One bad card must not stop the others: SyncAll reports every skip,
	// success, and failure through the progress lines below and carries on.
	client := app.New(cfg.appConfig())
	results, err := client.SyncAll(ctx, func(line string) { fmt.Fprintln(out, line) })
	if err != nil {
		return err
	}

	attempted, failed := 0, 0
	for _, result := range results {
		if result.Skipped {
			continue
		}
		attempted++
		if result.Err != nil {
			failed++
		}
	}
	if attempted > 0 && failed == attempted {
		return fmt.Errorf("every card failed to sync")
	}

	db, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

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
