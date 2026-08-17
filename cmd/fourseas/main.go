// Command fourseas pulls real credit card transactions from Plaid into a
// local DuckDB file and reads them back.
//
//	fourseas link      link one card, then repeat for the next card
//	fourseas link --days 365  request less history than the default 730 days
//	fourseas sync      fetch new transactions and FX rates, then print the newest rows
//	fourseas sync --fx fetch FX rates only
//	fourseas accounts  list the stored accounts with their balances
//	fourseas accounts nickname <account-id> "Amex Daily"  name an account
//	fourseas show      print the newest stored rows without calling Plaid
//	fourseas unlink    remove one card at Plaid and delete its local data
//	fourseas unlink --list  show the linked cards with their item ids
//	fourseas reset     drop everything and start the database again
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const usage = `fourseas - Plaid to DuckDB transactions

Usage:
  fourseas link      Link one card through Plaid Link in your browser. Requests
                     730 days of history, the most Plaid permits
  fourseas link --days 365
                     Link with less history. 30 to 730 days. Plaid fixes the
                     amount when the card is linked and cannot change it later
  fourseas sync      Fetch new transactions and FX rates, then print the newest rows
  fourseas sync --fx Fetch FX rates only
  fourseas accounts  List the stored accounts with their balances and ids
  fourseas accounts nickname <account-id> "Amex Daily"
                     Name an account. An empty name clears the nickname
  fourseas show      Print the newest stored rows without calling Plaid
  fourseas unlink <item-id>
                     Remove one card at Plaid, then delete its local token,
                     accounts, and transactions. Asks first. Plaid bills every
                     live card each month, and a re-link is the only way to
                     change how much history a card holds
  fourseas unlink --list
                     List the linked cards with the item ids unlink takes
  fourseas reset     Drop all data and build the schema again. Asks first

Settings come from .env. See .env.example.
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		return fmt.Errorf("no command given")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := loadSettings()

	switch os.Args[1] {
	case "link":
		return runLink(ctx, cfg, os.Args[2:])
	case "sync":
		return runSync(ctx, cfg, os.Args[2:])
	case "accounts":
		return runAccounts(ctx, cfg, os.Args[2:])
	case "show":
		return runShow(ctx, cfg)
	case "unlink":
		return runUnlink(ctx, cfg, os.Args[2:])
	case "reset":
		return runReset(ctx, cfg, os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
