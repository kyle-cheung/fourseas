package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/kyle-cheung/fourseas/providence/internal/store"
)

// runReset drops every table and builds the schema again.
//
// There is no migration framework on purpose: everything here can be fetched
// from the provider again, so a reset costs one full sync. Nothing is dropped
// until the user types yes, or passes --yes.
func runReset(ctx context.Context, cfg settings, args []string) error {
	return runResetWith(ctx, cfg, args, os.Stdin, os.Stdout)
}

func runResetWith(_ context.Context, cfg settings, args []string,
	in io.Reader, out io.Writer) error {

	confirmed := false
	for _, arg := range args {
		if !yesOption(arg) {
			return fmt.Errorf("unknown option %q: `fourseas reset` takes --yes or nothing", arg)
		}
		confirmed = true
	}

	if !confirmed {
		fmt.Fprintf(out, "This drops every account, transaction, and cursor in %s.\n", cfg.dbPath)
		fmt.Fprintf(out, "The next sync fetches everything again. ")

		agreed, err := confirmYes(in, out)
		if err != nil {
			return err
		}
		if !agreed {
			fmt.Fprintln(out, "Nothing was changed.")
			return nil
		}
	}

	db, err := store.Reset(cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Fprintf(out, "%s is empty and at schema version %d. Run `fourseas sync` to fetch again.\n",
		cfg.dbPath, store.SchemaVersion)
	return nil
}
