package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// ReconnectStatusError means sign-in completed for Item, but its local status
// could not be saved. Retry sync rather than authentication.
type ReconnectStatusError struct {
	Item LinkedItem
	Err  error
}

func (e *ReconnectStatusError) Error() string {
	return fmt.Sprintf("sign-in completed, but its local status could not be saved: %v", e.Err)
}

func (e *ReconnectStatusError) Unwrap() error { return e.Err }

// Reconnect repairs an existing Item's authentication and replaces an old login
// warning with a pending-sync status. It preserves data, tokens, and settings.
// Callers sync separately so a fetch can be retried without repeating sign-in.
// A ReconnectStatusError carries the verified Item when authentication completed
// but its local status could not be saved. Callers must retry sync only.
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
		return LinkedItem{}, &ReconnectStatusError{Item: linked, Err: err}
	}
	return linked, nil
}
