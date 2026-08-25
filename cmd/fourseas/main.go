// Command fourseas pulls real credit card transactions from Plaid into a
// local DuckDB file and reads them back.
//
//	fourseas link      link one card with statement data, then repeat for the next card
//	fourseas link --days 365  request less history than the default 730 days
//	fourseas link --liabilities=false  opt out of statement data before linking
//	fourseas sync      fetch new transactions and FX rates, then print the newest rows
//	fourseas sync --fx fetch FX rates only
//	fourseas accounts  list the stored accounts with their balances
//	fourseas accounts nickname <account-id> "Amex Daily"  name an account
//	fourseas accounts liabilities enable <account-id>  enable statement data
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

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/tui"
)

const usage = `fourseas - Plaid to DuckDB transactions

Usage:
  fourseas link      Link one card through Plaid Link in your browser. Requests
                     730 days of history, the most Plaid permits
                     New links enable statement data through Plaid Liabilities by default.
                     On paid Production plans, Liabilities can incur subscription charges under your Plaid agreement.
  fourseas link --days 365
                     Link with less history. 30 to 730 days. Plaid fixes the
                     amount when the card is linked and cannot change it later
  fourseas link --liabilities=false
                     --liabilities=false opts out before linking.
  fourseas sync      Fetch new transactions and FX rates, then print the newest rows
  fourseas sync --fx Fetch FX rates only
  fourseas accounts  List the stored accounts with their balances and ids
  fourseas accounts nickname <account-id> "Amex Daily"
                     Name an account. An empty name clears the nickname
  fourseas accounts liabilities enable <account-id>
                     Enable statement data for the whole institution and
                     request its first snapshot
  fourseas show      Print the newest stored rows without calling Plaid
  fourseas unlink <item-id>
                     Remove one card at Plaid, then delete its local token,
                     accounts, and transactions. Asks first. Active subscriptions
                     for the Item end at Plaid. A re-link is the only way to change
                     how much history a card holds
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
	cfg := loadSettings()
	if len(os.Args) < 2 {
		return tui.Run(app.New(cfg.appConfig()))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
