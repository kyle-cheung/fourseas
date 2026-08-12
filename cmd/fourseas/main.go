// Command fourseas pulls real credit card transactions from Plaid into a
// local DuckDB file and reads them back.
//
//	fourseas link    link one card, then repeat for the next card
//	fourseas sync    fetch new transactions, store them, print the newest rows
//	fourseas show    print the newest stored rows without calling Plaid
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
  fourseas link    Link one card through Plaid Link in your browser
  fourseas sync    Fetch new transactions, store them, and print the newest rows
  fourseas show    Print the newest stored rows without calling Plaid

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
		return runLink(ctx, cfg)
	case "sync":
		return runSync(ctx, cfg)
	case "show":
		return runShow(ctx, cfg)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
