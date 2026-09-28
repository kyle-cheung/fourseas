package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

const (
	loginRequiredStatus              = "fourseas:login-required: "
	liabilitiesConsentRequiredStatus = "fourseas:liabilities-consent-required: "
	reconnectSyncPendingStatus       = "fourseas:reconnected:sync-pending"
)

// A completed action survives caller cancellation. Status writes use a bounded
// local context; the variable lets tests exercise expiration without waiting.
var localStatusTimeout = 30 * time.Second

// statusForError preserves the error detail while marking actionable failures.
// Callers must redact credentials before encoding the error for storage.
func statusForError(err error) string {
	if err == nil {
		return ""
	}
	status := err.Error()
	if errors.Is(err, provider.ErrLoginRequired) {
		status = loginRequiredStatus + status
	}
	if errors.Is(err, provider.ErrAdditionalConsentRequired) {
		status = liabilitiesConsentRequiredStatus + status
	}
	return status
}

// loginRequired also recognizes full Plaid errors saved before these markers.
func loginRequired(status string) bool {
	return strings.HasPrefix(status, loginRequiredStatus) ||
		strings.Contains(status, "plaid ITEM_LOGIN_REQUIRED (")
}

func liabilitiesConsentRequired(status string) bool {
	return strings.HasPrefix(status, liabilitiesConsentRequiredStatus)
}

func reconnectSyncPending(status string) bool {
	return status == reconnectSyncPendingStatus
}

// replaceStatus changes only a matching marker for this Item. It leaves other
// errors, the sync cursor, and the last-sync time intact.
func replaceStatus(ctx context.Context, db *store.Store, itemID string, matches func(string) bool, replacement string) error {
	states, err := db.SyncStates(ctx)
	if err != nil {
		return err
	}
	for _, state := range states {
		if state.Provider == plaid.ProviderName && state.ItemID == itemID && matches(state.LastStatus) {
			return db.SetStatusOnly(ctx, plaid.ProviderName, itemID, replacement)
		}
	}
	return nil
}

// recordReconnect replaces an old login warning only after confirmed sign-in.
func (a *App) recordReconnect(ctx context.Context, itemID string) error {
	local, cancel := context.WithTimeout(context.WithoutCancel(ctx), localStatusTimeout)
	defer cancel()
	db, err := store.Open(a.cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return replaceStatus(local, db, itemID, loginRequired, reconnectSyncPendingStatus)
}

// recordLiabilitiesConsentStatus updates only the consent marker. Clearing it
// must not erase a login warning or another failure from transaction sync.
func (a *App) recordLiabilitiesConsentStatus(ctx context.Context, db *store.Store, item tokens.Item, refreshErr error) error {
	safeErr := redactAccessToken(refreshErr, item.AccessToken)
	local, cancel := context.WithTimeout(context.WithoutCancel(ctx), localStatusTimeout)
	defer cancel()

	var statusErr error
	if errors.Is(safeErr, provider.ErrAdditionalConsentRequired) {
		statusErr = db.SetStatusOnly(local, plaid.ProviderName, item.ItemID, statusForError(safeErr))
	} else {
		statusErr = replaceStatus(local, db, item.ItemID, liabilitiesConsentRequired, "")
	}
	if safeErr == nil {
		return statusErr
	}
	if statusErr == nil {
		return safeErr
	}
	return errors.Join(safeErr, statusErr)
}
