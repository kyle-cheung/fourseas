package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// maxPages stops a runaway pagination loop.
const maxPages = 100

// maxRestarts is how many times one item may start its pages again because the
// provider's data moved while they were read.
const maxRestarts = 5

// legacyCursorFailures is how many immediate mutation errors show that a saved
// cursor came from an unfinished pagination sequence made by an older version.
const legacyCursorFailures = 2

// labelWidth keeps the item name of every reported line in one column.
const labelWidth = 28

// SyncItem fetches the new transactions of one linked institution and returns
// the accounts that item holds afterwards.
func (a *App) SyncItem(ctx context.Context, itemID string, report Progress) ([]model.AccountView, error) {
	if err := a.cfg.Plaid.Validate(); err != nil {
		return nil, err
	}
	saved, db, err := a.start()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	item, err := linkedItem(saved, a.cfg.Plaid.Env, itemID)
	if err != nil {
		return nil, err
	}
	return a.syncItem(ctx, db, item, report)
}

// SyncAll fetches the new transactions of every linked institution.
//
// One bad card must not stop the others, so an item error is carried in its own
// result and the next item is tried. Only a canceled context ends the run, and
// that cancellation is the error of the whole operation: every caller asks
// whether the operation was cancelled, not whether one item was. The results
// gathered before the stop are returned with it.
func (a *App) SyncAll(ctx context.Context, report Progress) ([]SyncResult, error) {
	// Validation comes first, and the empty token file next, so neither an
	// unusable configuration nor an empty setup opens DuckDB.
	if err := a.cfg.Plaid.Validate(); err != nil {
		return nil, err
	}
	saved, err := tokens.Load(a.cfg.TokensPath)
	if err != nil {
		return nil, err
	}
	if len(saved.Items) == 0 {
		return nil, fmt.Errorf("nothing is linked yet: run `fourseas link` first (looked in %s)", a.cfg.TokensPath)
	}
	db, err := store.Open(a.cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	results := make([]SyncResult, 0, len(saved.Items))
	for _, item := range saved.Items {
		result := SyncResult{ItemID: item.ItemID, Label: Label(item)}
		if item.UsableIn(a.cfg.Plaid.Env) {
			result.Accounts, result.Err = a.syncItem(ctx, db, item, report)
			if result.Err != nil {
				progress(report, itemLine(item, "failed: %v", result.Err))
			}
		} else {
			result.Skipped = true
			progress(report, itemLine(item, "skipped: linked in %s, and PLAID_ENV is %s",
				item.Env, a.cfg.Plaid.Env))
		}

		results = append(results, result)
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
	}
	return results, nil
}

// syncItem reads one item and records why a failed one failed.
//
// The cursor is left where it was, so the next run asks for the same page
// again. The stored status is the whole provider error, because it is the only
// place the detail survives.
//
// A cancelled context records nothing. The write would use the same dead
// context and fail, and a cancellation is not a state of the item: storing it
// would show "Sync failed: context canceled" until the next successful run.
func (a *App) syncItem(ctx context.Context, db *store.Store, item tokens.Item, report Progress) ([]model.AccountView, error) {
	views, err := a.readItem(ctx, db, item, report)
	if err == nil {
		return views, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	statusErr := db.SetStatus(ctx, plaid.ProviderName, item.ItemID, err.Error())
	if statusErr != nil {
		return nil, errors.Join(err, statusErr)
	}
	return nil, err
}

// readItem pages through one item's changes and writes them to the store.
func (a *App) readItem(ctx context.Context, db *store.Store, item tokens.Item, report Progress) ([]model.AccountView, error) {
	source, err := a.source(a.cfg.Plaid, item.AccessToken, item.ItemID, item.Institution)
	if err != nil {
		return nil, err
	}

	// The accounts written below refer to this institution, so record it
	// before the first page lands.
	if err := recordInstitution(ctx, db, item, a.cfg.Plaid.Env); err != nil {
		return nil, err
	}

	counts, err := drain(ctx, db, source, item.ItemID, report)
	if err != nil {
		return nil, err
	}
	views, err := db.AccountViews(ctx)
	if err != nil {
		return nil, err
	}

	progress(report, itemLine(item, "%d added, %d modified, %d removed",
		counts.added, counts.modified, counts.removed))
	return filterAccounts(views, item.ItemID), nil
}

// itemLine is one reported line about one item, in the shared name column.
func itemLine(item tokens.Item, format string, args ...any) string {
	return fmt.Sprintf("%-*s %s", labelWidth, Label(item), fmt.Sprintf(format, args...))
}

// recordInstitution stores the linked item, so the accounts list can name the
// login an account sits behind. What is known lives in the token file: an
// item that was linked before this field existed has no environment, so the
// current one is used.
func recordInstitution(ctx context.Context, db *store.Store, item tokens.Item, env string) error {
	if item.Env != "" {
		env = item.Env
	}
	return db.UpsertInstitution(ctx, model.Institution{
		Provider:        plaid.ProviderName,
		ItemID:          item.ItemID,
		InstitutionName: item.Institution,
		Env:             env,
		LinkedAt:        item.LinkedAt,
	})
}

// counts is what one drain changed.
type counts struct {
	added    int
	modified int
	removed  int
}

// drain reads every page of one item. If the provider changes the data during
// pagination, the next attempt uses the cursor that started the sequence.
func drain(ctx context.Context, db *store.Store, source provider.Provider, itemID string, report Progress) (counts, error) {
	cursor, err := db.Cursor(ctx, source.Name(), itemID)
	if err != nil {
		return counts{}, err
	}

	immediateFailures := 0
	for attempt := 1; ; attempt++ {
		total, pagesRead, err := drainFrom(ctx, db, source, itemID, cursor, report)
		if err == nil {
			return total, nil
		}
		if !errors.Is(err, provider.ErrRestartPagination) {
			return total, err
		}
		if attempt >= maxRestarts {
			return total, fmt.Errorf("the data kept changing over %d attempts: %w", attempt, err)
		}

		if pagesRead == 0 && cursor != "" {
			immediateFailures++
		} else {
			immediateFailures = 0
		}

		if immediateFailures >= legacyCursorFailures {
			progress(report, fmt.Sprintf("  %s: the saved cursor came from an unfinished page sequence. Reading the whole history once.", itemID))
			cursor = ""
			immediateFailures = 0
			if err := db.ClearCursor(ctx, source.Name(), itemID); err != nil {
				return total, err
			}
		} else {
			progress(report, fmt.Sprintf("  %s: the data changed while paging. Retrying from the starting cursor (attempt %d of %d).",
				itemID, attempt+1, maxRestarts))
		}
	}
}

// drainFrom applies each page, but it keeps the starting cursor as the durable
// checkpoint until the final page completes.
func drainFrom(ctx context.Context, db *store.Store, source provider.Provider, itemID, cursor string, report Progress) (counts, int, error) {
	var total counts
	startCursor := cursor
	pagesRead := 0

	for page := 0; page < maxPages; page++ {
		batch, err := source.Sync(ctx, cursor)
		if err != nil {
			return total, pagesRead, err
		}

		checkpoint := batch.NextCursor
		if batch.HasMore {
			checkpoint = startCursor
		}
		if err := db.ApplyPage(ctx, toPage(source.Name(), itemID, batch, checkpoint)); err != nil {
			return total, pagesRead, err
		}
		pagesRead++

		total.added += len(batch.Added)
		total.modified += len(batch.Modified)
		total.removed += len(batch.RemovedIDs)
		cursor = batch.NextCursor

		if !batch.HasMore {
			return total, pagesRead, nil
		}
	}

	return total, pagesRead, fmt.Errorf("stopped after %d pages: the provider still reports more", maxPages)
}

// toPage turns one provider batch into the unit the store commits.
func toPage(providerName, itemID string, batch provider.Batch, cursor string) store.Page {
	return store.Page{
		Provider:   providerName,
		ItemID:     itemID,
		Added:      batch.Added,
		Modified:   batch.Modified,
		RemovedIDs: batch.RemovedIDs,
		Accounts:   batch.Accounts,
		Cursor:     cursor,
	}
}
