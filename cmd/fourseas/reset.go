package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kyle-cheung/fourseas/providence/internal/store"
)

// runReset drops every table and builds the schema again.
//
// There is no migration framework on purpose: everything here can be fetched
// from the provider again, so a reset costs one full sync. Nothing is dropped
// until the user types yes, or passes --yes.
func runReset(_ context.Context, cfg settings, args []string) error {
	confirmed := false
	for _, arg := range args {
		switch arg {
		case "--yes", "-y":
			confirmed = true
		default:
			return fmt.Errorf("unknown option %q: `fourseas reset` takes --yes or nothing", arg)
		}
	}

	if !confirmed {
		fmt.Printf("This drops every account, transaction, and cursor in %s.\n", cfg.dbPath)
		fmt.Printf("The next sync fetches everything again. Type yes to continue: ")

		answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return fmt.Errorf("read the answer: %w", err)
		}
		if strings.TrimSpace(strings.ToLower(answer)) != "yes" {
			fmt.Println("Nothing was changed.")
			return nil
		}
	}

	db, err := store.Reset(cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Printf("%s is empty and at schema version %d. Run `fourseas sync` to fetch again.\n",
		cfg.dbPath, store.SchemaVersion)
	return nil
}
