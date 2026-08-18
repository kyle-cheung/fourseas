package app

import (
	"context"
	"fmt"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// PendingSave is the unsaved work of a link whose item Plaid has already
// created. It holds the access token, so every field is unexported and the
// token cannot leave this package: a presenter reads the institution and the
// item id only.
//
// A caller gets one from TokenNotSavedError and gives it back to
// App.CompleteLinkSave, which is the only way to finish the save.
type PendingSave struct {
	path string
	file tokens.File
	item tokens.Item
}

// Institution is the name of the bank of the unsaved item. It is empty when
// Plaid returned no name.
func (p PendingSave) Institution() string { return p.item.Institution }

// ItemID is the id Plaid gave the unsaved item.
func (p PendingSave) ItemID() string { return p.item.ItemID }

// String redacts the access token, so a stray %v or %s cannot print it.
func (p PendingSave) String() string {
	return fmt.Sprintf("PendingSave{item_id:%q institution:%q access_token:[redacted]}",
		p.item.ItemID, p.item.Institution)
}

// TokenNotSavedError says Plaid has created an item and billing has started,
// but the access token did not reach the disk. Pending carries that token and
// completes the save.
//
// A caller that reads this error must never run the browser step again: a
// second link creates a second billed item at the same bank, and the first one
// stays billed with no way to reach it.
type TokenNotSavedError struct {
	// Pending is the handle App.CompleteLinkSave takes.
	Pending PendingSave
	// Err is why the save failed.
	Err error
}

// Error names the failure without the access token.
func (e *TokenNotSavedError) Error() string {
	return "the item is linked at Plaid but its access token was not saved: " + e.Err.Error()
}

// Unwrap keeps the cause reachable for errors.Is and errors.As.
func (e *TokenNotSavedError) Unwrap() error { return e.Err }

// CompleteLinkSave writes the access token of an item Plaid has already
// created. It is the retry of a failed save, and it never calls Plaid.
//
// It reads the token file again first, because the user may have repaired that
// file between the failure and this call. The snapshot taken at the failure is
// used only when the file cannot be read now, so a repair is never overwritten.
//
// It returns the same LinkedItem a successful Link returns. A save that fails
// again returns another *TokenNotSavedError, so the caller keeps the token and
// can try once more.
func (a *App) CompleteLinkSave(pending PendingSave) (LinkedItem, error) {
	if pending.item.ItemID == "" || pending.path == "" {
		return LinkedItem{}, fmt.Errorf("no unsaved link to complete")
	}
	file := pending.file
	if fresh, err := tokens.Load(pending.path); err == nil {
		file = fresh
	}
	if err := tokens.Save(pending.path, file.Upsert(pending.item)); err != nil {
		return LinkedItem{}, &TokenNotSavedError{Pending: pending, Err: err}
	}
	return LinkedItem{ItemID: pending.item.ItemID, Institution: pending.item.Institution}, nil
}

// Link runs the browser part of Plaid Link and saves the access token.
//
// The token is saved before Link returns, because it is the only way left to
// reach the item Plaid now bills. A save that fails returns a
// *TokenNotSavedError, which carries the token in an opaque handle:
// CompleteLinkSave finishes the save from it. Running Link again after such an
// error creates a second billed item and must not be offered.
func (a *App) Link(ctx context.Context, days int, report Progress) (LinkedItem, error) {
	if days < MinLinkDays || days > MaxLinkDays {
		return LinkedItem{}, fmt.Errorf("history must be from %d through %d days, got %d", MinLinkDays, MaxLinkDays, days)
	}
	if err := a.cfg.Plaid.Validate(); err != nil {
		return LinkedItem{}, err
	}
	saved, err := tokens.Load(a.cfg.TokensPath)
	if err != nil {
		return LinkedItem{}, err
	}

	progress(report, "Open "+plaid.LinkURL(a.cfg.Plaid)+" in your browser")
	result, err := a.link(ctx, a.cfg.Plaid, days)
	if err != nil {
		return LinkedItem{}, err
	}
	item := tokens.Item{
		ItemID: result.ItemID, AccessToken: result.AccessToken,
		Institution: result.Institution, Env: a.cfg.Plaid.Env, LinkedAt: time.Now().UTC(),
	}
	if err := tokens.Save(a.cfg.TokensPath, saved.Upsert(item)); err != nil {
		// Plaid bills this item from now on, and item holds the only handle to
		// it. The token goes back to the caller instead of being dropped.
		return LinkedItem{}, &TokenNotSavedError{
			Pending: PendingSave{path: a.cfg.TokensPath, file: saved, item: item},
			Err:     err,
		}
	}
	progress(report, "Link completed")
	return LinkedItem{ItemID: item.ItemID, Institution: item.Institution}, nil
}
