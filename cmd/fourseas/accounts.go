package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
)

// nicknameUsage says how the one subcommand of `fourseas accounts` is called.
const nicknameUsage = "usage: fourseas accounts nickname <account-id> \"<name>\", " +
	"with an empty name to clear it"

// runAccounts prints every stored account with its balance. It reads the
// database only: balances arrive with a sync.
//
// Its one subcommand, nickname, names an account.
func runAccounts(ctx context.Context, cfg settings, args []string) error {
	if len(args) > 0 && args[0] == "nickname" {
		return runNickname(ctx, cfg, args[1:])
	}
	if len(args) > 0 {
		return fmt.Errorf("unknown option %q: `fourseas accounts` takes nothing, or `nickname`", args[0])
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

// runNickname stores the name the user calls one account by. The nickname is
// the user's own: the provider's name stays in the list beside it.
func runNickname(ctx context.Context, cfg settings, args []string) error {
	accountID, nickname, err := nicknameArgs(args)
	if err != nil {
		return err
	}

	db, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	return setNickname(ctx, db, accountID, nickname)
}

// setNickname writes the nickname and reports what changed.
func setNickname(ctx context.Context, db *store.Store, accountID, nickname string) error {
	if err := db.SetNickname(ctx, accountID, nickname); err != nil {
		return err
	}

	if nickname == "" {
		fmt.Printf("Account %s has no nickname now.\n", accountID)
	} else {
		fmt.Printf("Account %s is now %q.\n", accountID, nickname)
	}
	return nil
}

// nicknameArgs reads the account id and the new name. An empty name is how a
// nickname is cleared, so only the id must have a value.
func nicknameArgs(args []string) (accountID, nickname string, err error) {
	if len(args) != 2 {
		return "", "", fmt.Errorf("`fourseas accounts nickname` takes an account id and a name: %s",
			nicknameUsage)
	}

	accountID = strings.TrimSpace(args[0])
	nickname = strings.TrimSpace(args[1])
	if accountID == "" {
		return "", "", fmt.Errorf("the account id is empty: %s", nicknameUsage)
	}
	return accountID, nickname, nil
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
