package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

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

	accountID = strings.TrimSpace(accountID)
	views, err := db.AccountViews(ctx)
	if err != nil {
		return err
	}
	account, found := accountByID(views, accountID)
	if !found {
		return &store.UnknownAccountError{AccountID: accountID}
	}
	if !strings.EqualFold(strings.TrimSpace(account.Type), "credit") {
		return fmt.Errorf("account %q has type %q; liabilities require a credit account", accountID, account.Type)
	}
	item, err := linkedItem(saved, a.cfg.Plaid.Env, account.ItemID)
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

	return redactAccessToken(a.refreshLiabilities(ctx, db, target), target.AccessToken)
}

func accountByID(views []model.AccountView, accountID string) (model.AccountView, bool) {
	for _, view := range views {
		if view.AccountID == accountID {
			return view, true
		}
	}
	return model.AccountView{}, false
}
