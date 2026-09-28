package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// Reconnect repairs an existing Item's authentication and replaces an old login
// warning with a pending-sync status. It preserves data, tokens, and settings.
// Callers sync separately so a fetch can be retried without repeating sign-in.
// A nonempty result with an error means authentication completed for the verified
// Item, but its local status could not be saved. Callers must retry sync only.
func (a *App) Reconnect(ctx context.Context, itemID string, report Progress) (LinkedItem, error) {
	if err := ctx.Err(); err != nil {
		return LinkedItem{}, err
	}
	if err := a.cfg.Plaid.Validate(); err != nil {
		return LinkedItem{}, err
	}
	saved, err := tokens.Load(a.cfg.TokensPath)
	if err != nil {
		return LinkedItem{}, err
	}
	item, found := saved.Find(itemID)
	if !found {
		return LinkedItem{}, fmt.Errorf("%q is not linked", itemID)
	}
	if !item.UsableIn(a.cfg.Plaid.Env) {
		return LinkedItem{}, fmt.Errorf("%s was linked in %s: set PLAID_ENV to %s to reconnect it", Label(item), item.Env, item.Env)
	}
	if strings.TrimSpace(item.AccessToken) == "" {
		return LinkedItem{}, errors.New("the linked Item has no access token")
	}
	if a.reconnect == nil {
		return LinkedItem{}, errors.New("reconnect is not configured")
	}
	progress(report, "Open "+plaid.LinkURL(a.cfg.Plaid)+" in your browser")
	if _, err := a.reconnect(ctx, a.cfg.Plaid, item.AccessToken); err != nil {
		if errors.Is(err, plaid.ErrLinkClosed) {
			return LinkedItem{}, redactAccessToken(fmt.Errorf("%w: %w", context.Canceled, err), item.AccessToken)
		}
		return LinkedItem{}, redactAccessToken(err, item.AccessToken)
	}
	// The browser can stay open for minutes. Verify that the same Item is
	// still linked before allowing callers to sync it; never overwrite tokens.
	fresh, err := tokens.Load(a.cfg.TokensPath)
	if err != nil {
		return LinkedItem{}, fmt.Errorf("sign-in completed, but the linked Item could not be checked: %w", err)
	}
	current, found := fresh.Find(itemID)
	if !found || current.AccessToken != item.AccessToken || current.Env != item.Env {
		return LinkedItem{}, errors.New("sign-in completed, but the linked Item changed during reconnect; sync was not started")
	}
	linked := LinkedItem{ItemID: item.ItemID, Institution: Label(current)}
	if err := a.recordReconnect(ctx, item.ItemID); err != nil {
		return linked, fmt.Errorf("sign-in completed, but its local status could not be saved: %w", err)
	}
	return linked, nil
}

func (a *App) recordReconnect(ctx context.Context, itemID string) error {
	// Successful sign-in survives cancellation. Persist that fact with a bounded
	// local context so a canceled follow-up sync cannot resurrect the old warning.
	local, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	db, err := store.Open(a.cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	states, err := db.SyncStates(local)
	if err != nil {
		return err
	}
	for _, state := range states {
		if state.Provider == plaid.ProviderName && state.ItemID == itemID && loginRequired(state.LastStatus) {
			return db.SetStatusOnly(local, plaid.ProviderName, itemID, reconnectSyncPendingStatus)
		}
	}
	return nil
}
