// Package app holds the account management operations that both the command
// line and the terminal interface call.
//
// It owns validation, ordering, and persistence. It holds no user-facing
// wording: presentation code turns the structured results below into text.
package app

import (
	"context"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// Progress reports one step of a long operation. It may be nil.
type Progress func(string)

// How much transaction history a new link may ask Plaid for. Plaid fixes the
// amount when the item is created and does not permit a later change.
const (
	MinLinkDays = 30
	MaxLinkDays = 730
)

// Config is everything the operations need to reach Plaid and the local files.
type Config struct {
	Plaid      plaid.Config
	DBPath     string
	TokensPath string
}

// SyncState is how the last sync of one institution ended.
type SyncState struct {
	ItemID       string
	Institution  string
	LastSyncedAt *time.Time
	LastStatus   string
}

// AccountData is the stored account list with its sync state.
type AccountData struct {
	Accounts []model.AccountView
	States   []SyncState
}

// LinkedItem is one newly linked institution.
type LinkedItem struct {
	ItemID      string
	Institution string
}

// SyncResult is what one item's sync changed.
type SyncResult struct {
	ItemID   string
	Label    string
	Accounts []model.AccountView
	Skipped  bool
	Err      error
}

// RowCounts is how many rows one item holds or held.
type RowCounts struct {
	Transactions int64
	Accounts     int64
	SyncState    int64
	Institutions int64
}

// UnlinkResult is what one removal deleted.
type UnlinkResult struct {
	Rows RowCounts
	// PlaidItemGone says Plaid no longer had the item when it was removed.
	PlaidItemGone bool
}

// UnlinkData is what a removal would delete, before anything is changed.
type UnlinkData struct {
	ItemID      string
	Institution string
	Accounts    []model.AccountView
	Rows        RowCounts
}

type linkFunc func(context.Context, plaid.Config, int) (plaid.LinkResult, error)
type removeFunc func(context.Context, plaid.Config, string) error
type sourceFunc func(plaid.Config, string, string, string) (provider.Provider, error)

// App is the one façade over the store, the token file, and the provider.
type App struct {
	cfg    Config
	link   linkFunc
	remove removeFunc
	source sourceFunc
}

// ErrProductNotReady lets presentation code classify the error without importing a provider.
var ErrProductNotReady = provider.ErrProductNotReady

// Option changes one dependency of an App.
type Option func(*App)

// WithRemove replaces the call that removes an item at Plaid.
func WithRemove(remove func(context.Context, plaid.Config, string) error) Option {
	return func(a *App) { a.remove = remove }
}

// New builds the façade production callers use.
func New(cfg Config, options ...Option) *App {
	a := newWith(cfg, plaid.Link, plaid.Remove, func(cfg plaid.Config, token, itemID, institution string) (provider.Provider, error) {
		return plaid.NewSource(cfg, token, itemID, institution)
	})
	for _, option := range options {
		option(a)
	}
	return a
}

func newWith(cfg Config, link linkFunc, remove removeFunc, source sourceFunc) *App {
	return &App{cfg: cfg, link: link, remove: remove, source: source}
}

func progress(fn Progress, text string) {
	if fn != nil {
		fn(text)
	}
}

// Label is the terminal-safe name for one linked item.
func Label(item tokens.Item) string {
	if item.Institution != "" {
		return item.Institution
	}
	return item.ItemID
}

// start reads the token file and opens the database. The caller closes the
// database.
func (a *App) start() (tokens.File, *store.Store, error) {
	saved, err := tokens.Load(a.cfg.TokensPath)
	if err != nil {
		return tokens.File{}, nil, err
	}
	db, err := store.Open(a.cfg.DBPath)
	if err != nil {
		return tokens.File{}, nil, err
	}
	return saved, db, nil
}

// rowCounts converts the store's count of deleted rows at the boundary.
func rowCounts(removed store.Removed) RowCounts {
	return RowCounts{
		Transactions: removed.Transactions,
		Accounts:     removed.Accounts,
		SyncState:    removed.SyncState,
		Institutions: removed.Institutions,
	}
}
