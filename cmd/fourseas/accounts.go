package main

import (
	"context"
	"fmt"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
)

// runAccounts prints every stored account with its balance. It reads the
// database only: balances arrive with a sync.
func runAccounts(ctx context.Context, cfg settings, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unknown option %q: `fourseas accounts` takes nothing", args[0])
	}

	db, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	accounts, err := db.AccountViews(ctx)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Println("No accounts are stored yet. Run `fourseas sync` to fetch them.")
		return nil
	}

	fmt.Printf("\n%d accounts:\n\n", len(accounts))
	printAccounts(accounts)
	return nil
}

// accountBalance is one balance cell, or a dash when the provider sent none.
func accountBalance(d model.Account) string {
	if !d.BalanceCurrent.Valid {
		return "-"
	}
	return d.BalanceCurrent.Decimal.StringFixed(2)
}

// accountLimit is the credit limit cell, or a dash for an account that has no
// limit, such as a checking account.
func accountLimit(d model.Account) string {
	if !d.BalanceLimit.Valid {
		return "-"
	}
	return d.BalanceLimit.Decimal.StringFixed(2)
}

// accountUpdated is when the balance was last read, or a dash before the first
// sync that returned one.
func accountUpdated(d model.Account) string {
	if d.BalanceUpdatedAt == nil {
		return "-"
	}
	return d.BalanceUpdatedAt.Local().Format("2006-01-02 15:04")
}
