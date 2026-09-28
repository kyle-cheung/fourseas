package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

type reconnectService interface {
	Reconnect(context.Context, string, app.Progress) (app.LinkedItem, error)
	SyncItem(context.Context, string, app.Progress) ([]model.AccountView, error)
}

// Keep cancellation detectable without repeating the underlying Link wording.
type reconnectCanceledError struct{ cause error }

func (e *reconnectCanceledError) Error() string { return "bank sign-in canceled; no sync was started" }
func (e *reconnectCanceledError) Unwrap() error { return e.cause }

func runReconnect(ctx context.Context, cfg settings, args []string) error {
	return runReconnectWith(ctx, app.New(cfg.appConfig()), args, os.Stdout)
}

func runReconnectWith(ctx context.Context, client reconnectService, args []string, out io.Writer) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: fourseas reconnect <item-id>; find Item IDs with `fourseas unlink --list`")
	}
	item, err := client.Reconnect(ctx, args[0], func(line string) { fmt.Fprintln(out, line) })
	if err != nil {
		if item.ItemID != "" {
			return fmt.Errorf("%w; run `fourseas sync` to fetch data without repeating sign-in", err)
		}
		if errors.Is(err, context.Canceled) {
			return &reconnectCanceledError{cause: err}
		}
		return err
	}
	fmt.Fprintf(out, "Sign-in completed for %s. Syncing accounts…\n", item.Institution)
	if _, err := client.SyncItem(ctx, item.ItemID, func(line string) { fmt.Fprintln(out, line) }); err != nil {
		if errors.Is(err, app.ErrLoginRequired) {
			return fmt.Errorf("sign-in is still required: %w; run `fourseas reconnect %s` again", err, item.ItemID)
		}
		if errors.Is(err, app.ErrAdditionalConsentRequired) {
			return fmt.Errorf("sign-in completed, but statement data needs consent: %w; run `fourseas accounts liabilities enable <account-id>`", err)
		}
		return fmt.Errorf("sign-in completed, but sync did not finish: %w; run `fourseas sync` to retry fetching data", err)
	}
	fmt.Fprintf(out, "Reconnected %s. Accounts refreshed.\n", item.Institution)
	return nil
}

func printSyncReconnectHelp(out io.Writer, results []app.SyncResult) {
	for _, result := range results {
		if errors.Is(result.Err, app.ErrLoginRequired) {
			fmt.Fprintf(out, "%s — Reconnect required. Run `fourseas reconnect %s`.\n", result.Label, result.ItemID)
		}
	}
}
