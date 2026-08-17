package app

import (
	"context"
	"strings"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// Accounts returns the stored accounts with the sync state of each linked
// institution. An empty itemID returns everything; any other value returns
// only what belongs to that item.
func (a *App) Accounts(ctx context.Context, itemID string) (AccountData, error) {
	saved, db, err := a.start()
	if err != nil {
		return AccountData{}, err
	}
	defer db.Close()

	views, err := db.AccountViews(ctx)
	if err != nil {
		return AccountData{}, err
	}
	states, err := db.SyncStates(ctx)
	if err != nil {
		return AccountData{}, err
	}

	data := AccountData{
		Accounts: filterAccounts(views, itemID),
		States:   syncStates(states, saved, itemID),
	}
	return data, nil
}

// SetNickname gives one account the name the user calls it by. A blank
// nickname clears it.
func (a *App) SetNickname(ctx context.Context, accountID, nickname string) error {
	_, db, err := a.start()
	if err != nil {
		return err
	}
	defer db.Close()

	return db.SetNickname(ctx, accountID, strings.TrimSpace(nickname))
}

// filterAccounts keeps the accounts of one item. The store reads every account
// in one ordered query, so the filter is applied here rather than in SQL.
func filterAccounts(views []model.AccountView, itemID string) []model.AccountView {
	if itemID == "" {
		return views
	}
	out := make([]model.AccountView, 0, len(views))
	for _, view := range views {
		if view.ItemID == itemID {
			out = append(out, view)
		}
	}
	return out
}

// syncStates joins each stored state to the institution name in the token
// file, which names a login even before its institution row is stored.
func syncStates(states []model.SyncState, saved tokens.File, itemID string) []SyncState {
	out := make([]SyncState, 0, len(states))
	for _, state := range states {
		if itemID != "" && state.ItemID != itemID {
			continue
		}
		institution := state.ItemID
		if item, found := saved.Find(state.ItemID); found {
			institution = Label(item)
		}
		out = append(out, SyncState{
			ItemID:       state.ItemID,
			Institution:  institution,
			LastSyncedAt: state.LastSyncedAt,
			LastStatus:   state.LastStatus,
		})
	}
	return out
}
