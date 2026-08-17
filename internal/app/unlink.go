package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// UnlinkPreview reports what removing one item would delete. Nothing changes.
//
// The checks run in the order that costs the least: the token file and the
// item, then the environment, then the Plaid settings, and only then the
// database. start is not used, because it would open DuckDB before the
// environment is checked.
func (a *App) UnlinkPreview(ctx context.Context, itemID string) (UnlinkData, error) {
	saved, err := tokens.Load(a.cfg.TokensPath)
	if err != nil {
		return UnlinkData{}, err
	}
	item, err := linkedItem(saved, a.cfg.Plaid.Env, itemID)
	if err != nil {
		return UnlinkData{}, err
	}
	if err := a.cfg.Plaid.Validate(); err != nil {
		return UnlinkData{}, err
	}
	db, err := store.Open(a.cfg.DBPath)
	if err != nil {
		return UnlinkData{}, err
	}
	defer db.Close()

	views, err := db.AccountViews(ctx)
	if err != nil {
		return UnlinkData{}, err
	}
	counts, err := db.ItemCounts(ctx, plaid.ProviderName, item.ItemID)
	if err != nil {
		return UnlinkData{}, err
	}

	return UnlinkData{
		ItemID:      item.ItemID,
		Institution: Label(item),
		Accounts:    filterAccounts(views, item.ItemID),
		Rows:        rowCounts(counts),
	}, nil
}

// Unlink removes one linked institution: first at Plaid, then locally.
//
// The order matters. Plaid bills the transactions product for every live item,
// each month, and a token deleted on its own leaves that item alive with no
// way left to reach it. Plaid is therefore called first, the local rows go
// next, and the token last, because a second attempt needs it.
func (a *App) Unlink(ctx context.Context, itemID string, report Progress) (UnlinkResult, error) {
	// Validation comes before start, so unusable provider settings do not open
	// DuckDB.
	if err := a.cfg.Plaid.Validate(); err != nil {
		return UnlinkResult{}, err
	}
	saved, db, err := a.start()
	if err != nil {
		return UnlinkResult{}, err
	}
	defer db.Close()
	item, err := linkedItem(saved, a.cfg.Plaid.Env, itemID)
	if err != nil {
		return UnlinkResult{}, err
	}
	progress(report, "Removing the item at Plaid")
	removeErr := a.remove(ctx, a.cfg.Plaid, item.AccessToken)
	if removeErr != nil && !errors.Is(removeErr, provider.ErrItemGone) {
		return UnlinkResult{}, fmt.Errorf("plaid still has this item, so nothing local was deleted: %w", removeErr)
	}
	progress(report, "Deleting local data")
	removed, err := db.Unlink(ctx, plaid.ProviderName, item.ItemID)
	if err != nil {
		return UnlinkResult{}, fmt.Errorf("the item is gone at Plaid but the local data is not: %w", err)
	}
	if err := tokens.Save(a.cfg.TokensPath, saved.Delete(item.ItemID)); err != nil {
		return UnlinkResult{}, err
	}
	return UnlinkResult{Rows: rowCounts(removed), PlaidItemGone: errors.Is(removeErr, provider.ErrItemGone)}, nil
}

// linkedItem finds one linked item and refuses one from another environment.
//
// The token only works in the environment that issued it, and the removal must
// reach Plaid. Sending it to the other environment would fail with
// INVALID_ACCESS_TOKEN and leave the item live and billed.
func linkedItem(saved tokens.File, env, itemID string) (tokens.Item, error) {
	item, found := saved.Find(itemID)
	if !found {
		return tokens.Item{}, fmt.Errorf("%q is not linked", itemID)
	}
	if !item.UsableIn(env) {
		return tokens.Item{}, fmt.Errorf("%s was linked in %s, and PLAID_ENV is %s: "+
			"set PLAID_ENV to %s to remove it", Label(item), item.Env, env, item.Env)
	}
	return item, nil
}
