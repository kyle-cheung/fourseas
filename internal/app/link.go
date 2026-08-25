package app

import (
	"context"
	"fmt"
	"strconv"
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

// String identifies the pending item and redacts its access token.
func (p PendingSave) String() string {
	return fmt.Sprintf("PendingSave{item_id:%q institution:%q access_token:[redacted]}",
		p.item.ItemID, p.item.Institution)
}

// Format makes every fmt verb use the redacted form. This also protects a
// PendingSave that is inside a slice, struct, or interface.
func (p PendingSave) Format(state fmt.State, verb rune) {
	writeRedacted(state, verb, p.String())
}

func writeRedacted(state fmt.State, verb rune, text string) {
	if verb == 'q' {
		fmt.Fprint(state, strconv.Quote(text))
		return
	}
	fmt.Fprint(state, text)
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

// Format prevents diagnostic verbs from expanding Pending and its secrets.
func (e *TokenNotSavedError) Format(state fmt.State, verb rune) {
	writeRedacted(state, verb, e.Error())
}

// Unwrap keeps the cause reachable for errors.Is and errors.As.
func (e *TokenNotSavedError) Unwrap() error { return e.Err }

// CompleteLinkSave writes the access token of an item Plaid has already
// created. It is the retry of a failed save, and it never calls Plaid.
//
// It reloads the token file under an exclusive lock, because the user may have
// repaired that file between the failure and this call.
//
// It returns the same LinkedItem a successful Link returns. A save that fails
// again returns another *TokenNotSavedError, so the caller keeps the token and
// can try once more.
func (a *App) CompleteLinkSave(pending PendingSave) (LinkedItem, error) {
	if pending.item.ItemID == "" || pending.path == "" {
		return LinkedItem{}, fmt.Errorf("no unsaved link to complete")
	}
	if _, err := tokens.Mutate(pending.path, func(current tokens.File) (tokens.File, error) {
		return current.Upsert(pending.item), nil
	}); err != nil {
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
func (a *App) Link(ctx context.Context, days int, liabilities bool, report Progress) (LinkedItem, error) {
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
	result, err := a.link(ctx, a.cfg.Plaid, days, liabilities)
	if err != nil {
		return LinkedItem{}, err
	}
	item := tokens.Item{
		ItemID: result.ItemID, AccessToken: result.AccessToken,
		Institution: result.Institution, Env: a.cfg.Plaid.Env, Liabilities: liabilities,
		LinkedAt: time.Now().UTC(),
	}
	if _, err := tokens.Mutate(a.cfg.TokensPath, func(current tokens.File) (tokens.File, error) {
		return current.Upsert(item), nil
	}); err != nil {
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
