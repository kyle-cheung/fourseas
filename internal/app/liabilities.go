package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// LiabilitiesEnabledError reports that consent and the enabled flag were
// saved, but the first snapshot operation did not finish cleanly.
type LiabilitiesEnabledError struct {
	cause               error
	statusCleanupFailed bool
	snapshotStored      bool
}

func (e *LiabilitiesEnabledError) Error() string {
	if e == nil || e.cause == nil {
		return "The statement data request was saved, but the first snapshot failed"
	}
	if e.statusCleanupFailed {
		if e.snapshotStored {
			return fmt.Sprintf("Statement data is enabled for the whole institution, and the first snapshot was stored, but its consent status could not be updated: %v",
				e.cause)
		}
		return fmt.Sprintf("Statement data is enabled for the whole institution, but the first snapshot is not ready, and its consent status could not be updated: %v",
			e.cause)
	}
	if errors.Is(e.cause, provider.ErrAdditionalConsentRequired) {
		return fmt.Sprintf("The statement data request was saved for the whole institution, but Plaid still requires consent before the first snapshot: %v",
			e.cause)
	}
	outcome := "failed"
	if errors.Is(e.cause, context.Canceled) {
		outcome = "was canceled"
	}
	return fmt.Sprintf("Statement data is enabled for the whole institution, but the first snapshot %s: %v",
		outcome, e.cause)
}

func (e *LiabilitiesEnabledError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Cause returns the sanitized reason that the post-save operation failed.
func (e *LiabilitiesEnabledError) Cause() error { return e.Unwrap() }

// SnapshotStored reports whether the first snapshot succeeded before a local
// consent-status update failed.
func (e *LiabilitiesEnabledError) SnapshotStored() bool {
	return e != nil && e.snapshotStored
}

// StatusCleanupFailed reports whether the typed outcome came from the local
// consent-status update after the provider request.
func (e *LiabilitiesEnabledError) StatusCleanupFailed() bool {
	return e != nil && e.statusCleanupFailed
}

// NewLiabilitiesEnabledError builds a safe partial-success value and removes
// the known access token from its display text.
func NewLiabilitiesEnabledError(err error, accessToken string) *LiabilitiesEnabledError {
	return &LiabilitiesEnabledError{cause: redactAccessToken(err, accessToken)}
}

// NewLiabilitiesStatusError reports that consent-status cleanup failed and
// records whether this request stored or cleared a snapshot first.
func NewLiabilitiesStatusError(err error, accessToken string, snapshotStored bool) *LiabilitiesEnabledError {
	return &LiabilitiesEnabledError{
		cause:               redactAccessToken(err, accessToken),
		statusCleanupFailed: true,
		snapshotStored:      snapshotStored,
	}
}

// The timeout is a variable so a test can make status cleanup expire.
var liabilitiesStatusTimeout = 30 * time.Second

// EnableLiabilities collects consent for the institution that owns accountID,
// saves the enabled flag, and requests the first liability snapshot.
func (a *App) EnableLiabilities(ctx context.Context, accountID string, report Progress) error {
	if err := a.cfg.Plaid.Validate(); err != nil {
		return err
	}
	saved, db, err := a.start()
	if err != nil {
		return err
	}
	defer db.Close()

	item, err := a.liabilityItem(ctx, db, saved, accountID)
	if err != nil {
		return err
	}
	if a.updateLiabilities == nil {
		return errors.New("liabilities consent update is not configured")
	}

	progress(report, "Open "+plaid.LinkURL(a.cfg.Plaid)+" in your browser")
	if _, err := a.updateLiabilities(ctx, a.cfg.Plaid, item.AccessToken); err != nil {
		return redactAccessToken(err, item.AccessToken)
	}

	// Link can stay open for minutes. Reload under the write lock so that an
	// Item added during that wait is not overwritten.
	var target tokens.Item
	if _, err := tokens.Mutate(a.cfg.TokensPath, func(current tokens.File) (tokens.File, error) {
		fresh, found := current.Find(item.ItemID)
		if !found {
			return tokens.File{}, fmt.Errorf("%q is not linked", item.ItemID)
		}
		if fresh.Env != item.Env || fresh.AccessToken != item.AccessToken {
			return tokens.File{}, errors.New("the linked Item changed during the update; liabilities were not enabled")
		}
		fresh.Liabilities = true
		target = fresh
		return current.Upsert(fresh), nil
	}); err != nil {
		return err
	}

	refresh := a.refreshLiabilitiesResult(ctx, db, target)
	outcomeErr := a.recordLiabilitiesConsentStatus(ctx, db, target, refresh.err)
	if refresh.err != nil {
		return NewLiabilitiesEnabledError(outcomeErr, target.AccessToken)
	}
	if outcomeErr != nil {
		return NewLiabilitiesStatusError(outcomeErr, target.AccessToken, refresh.snapshotStored)
	}
	return nil
}

// RefreshLiabilities retries the liability snapshot of an already-enabled
// institution. It does not repeat update consent or transaction sync.
func (a *App) RefreshLiabilities(ctx context.Context, accountID string) error {
	if err := a.cfg.Plaid.Validate(); err != nil {
		return err
	}
	saved, db, err := a.start()
	if err != nil {
		return err
	}
	defer db.Close()

	item, err := a.liabilityItem(ctx, db, saved, accountID)
	if err != nil {
		return err
	}
	if !item.Liabilities {
		return fmt.Errorf("statement data is not enabled for %s", Label(item))
	}
	refresh := a.refreshLiabilitiesResult(ctx, db, item)
	return a.recordLiabilitiesConsentStatus(ctx, db, item, refresh.err)
}

func (a *App) liabilityItem(
	ctx context.Context,
	db *store.Store,
	saved tokens.File,
	accountID string,
) (tokens.Item, error) {
	accountID = strings.TrimSpace(accountID)
	views, err := db.AccountViews(ctx)
	if err != nil {
		return tokens.Item{}, err
	}
	account, found := accountByID(views, accountID)
	if !found {
		return tokens.Item{}, &store.UnknownAccountError{AccountID: accountID}
	}
	if !strings.EqualFold(strings.TrimSpace(account.Type), "credit") {
		return tokens.Item{}, fmt.Errorf("account %q has type %q; liabilities require a credit account", accountID, account.Type)
	}
	return linkedItem(saved, a.cfg.Plaid.Env, account.ItemID)
}

// recordLiabilitiesConsentStatus changes only the app-owned consent marker.
// The saved flag survives caller cancellation, so this local status update
// also uses a bounded context that ignores that cancellation.
func (a *App) recordLiabilitiesConsentStatus(
	ctx context.Context,
	db *store.Store,
	item tokens.Item,
	refreshErr error,
) error {
	safeErr := redactAccessToken(refreshErr, item.AccessToken)
	local, stop := context.WithTimeout(context.WithoutCancel(ctx), liabilitiesStatusTimeout)
	defer stop()

	var statusErr error
	if errors.Is(safeErr, provider.ErrAdditionalConsentRequired) {
		statusErr = db.SetStatusOnly(local, plaid.ProviderName, item.ItemID,
			liabilitiesConsentRequiredStatus+safeErr.Error())
	} else {
		states, err := db.SyncStates(local)
		if err != nil {
			statusErr = err
		} else {
			for _, state := range states {
				if state.Provider == plaid.ProviderName && state.ItemID == item.ItemID &&
					liabilitiesConsentRequired(state.LastStatus) {
					statusErr = db.SetStatusOnly(local, plaid.ProviderName, item.ItemID, "")
					break
				}
			}
		}
	}
	if safeErr == nil {
		return statusErr
	}
	if statusErr == nil {
		return safeErr
	}
	return errors.Join(safeErr, statusErr)
}

func accountByID(views []model.AccountView, accountID string) (model.AccountView, bool) {
	for _, view := range views {
		if view.AccountID == accountID {
			return view, true
		}
	}
	return model.AccountView{}, false
}
