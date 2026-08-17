package app

import (
	"context"
	"fmt"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// Link runs the browser part of Plaid Link and saves the access token.
//
// The token is saved before Link returns, because it is the only way left to
// reach the item Plaid now bills.
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
		return LinkedItem{}, err
	}
	progress(report, "Link completed")
	return LinkedItem{ItemID: item.ItemID, Institution: item.Institution}, nil
}
