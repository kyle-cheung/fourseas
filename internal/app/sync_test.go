package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// fakeSource returns a fixed list of pages and records the cursors it was given.
type fakeSource struct {
	pages   []provider.Batch
	errs    []error
	call    int
	cursors []string
}

func (f *fakeSource) Name() string { return "fake" }

func (f *fakeSource) Sync(_ context.Context, cursor string) (provider.Batch, error) {
	f.cursors = append(f.cursors, cursor)
	i := f.call
	f.call++

	if i < len(f.errs) && f.errs[i] != nil {
		return provider.Batch{}, f.errs[i]
	}
	if i >= len(f.pages) {
		return provider.Batch{}, errors.New("the fake ran out of pages")
	}
	return f.pages[i], nil
}

func row(id string, date string) model.Transaction {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return model.Transaction{
		Provider:   "fake",
		ExternalID: id,
		ItemID:     "item-1",
		Date:       d,
		Name:       id,
		Amount:     decimal.NewFromInt(10),
		Currency:   "USD",
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "fourseas.duckdb"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func account(id, balance string) model.Account {
	return model.Account{
		Provider:       "fake",
		AccountID:      id,
		ItemID:         "item-1",
		Name:           "Gold Card",
		Currency:       "USD",
		Tracked:        true,
		BalanceCurrent: decimal.NullDecimal{Decimal: decimal.RequireFromString(balance), Valid: true},
		FirstSeenAt:    time.Now().UTC(),
		LastSeenAt:     time.Now().UTC(),
	}
}

func TestDrainFollowsEveryPage(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	source := &fakeSource{pages: []provider.Batch{
		{Added: []model.Transaction{row("a", "2026-08-01")}, NextCursor: "c1", HasMore: true},
		{Added: []model.Transaction{row("b", "2026-08-02")}, NextCursor: "c2", HasMore: true},
		{
			Added:      []model.Transaction{row("c", "2026-08-03")},
			RemovedIDs: []string{"a"},
			NextCursor: "c3",
			HasMore:    false,
		},
	}}

	got, err := drain(ctx, db, source, "item-1", nil)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got.added != 3 || got.removed != 1 {
		t.Errorf("counts = %+v, want 3 added and 1 removed", got)
	}

	// The first call starts from an empty cursor, then follows next_cursor.
	want := []string{"", "c1", "c2"}
	if len(source.cursors) != len(want) {
		t.Fatalf("cursors = %v, want %v", source.cursors, want)
	}
	for i := range want {
		if source.cursors[i] != want[i] {
			t.Errorf("cursor %d = %q, want %q", i, source.cursors[i], want[i])
		}
	}

	// Row "a" was added on page one and removed on page three.
	n, err := db.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("stored %d rows, want 2", n)
	}

	saved, err := db.Cursor(ctx, "fake", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if saved != "c3" {
		t.Errorf("saved cursor = %q, want %q", saved, "c3")
	}
}

func TestDrainResumesFromTheSavedCursor(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	if err := db.SetCursor(ctx, "fake", "item-1", "saved-cursor"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	source := &fakeSource{pages: []provider.Batch{{NextCursor: "next", HasMore: false}}}
	if _, err := drain(ctx, db, source, "item-1", nil); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if source.cursors[0] != "saved-cursor" {
		t.Errorf("first cursor = %q, want the saved one", source.cursors[0])
	}
}

func TestDrainKeepsTheStartingCursorUntilTheLastPage(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)
	if err := db.SetCursor(ctx, "fake", "item-1", "start"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	source := &fakeSource{
		pages: []provider.Batch{
			{Added: []model.Transaction{row("a", "2026-08-01")}, NextCursor: "c1", HasMore: true},
		},
		errs: []error{nil, errors.New("plaid is unavailable")},
	}

	if _, err := drain(ctx, db, source, "item-1", nil); err == nil {
		t.Fatal("want an error from the second page, got nil")
	}

	saved, err := db.Cursor(ctx, "fake", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if saved != "start" {
		t.Errorf("saved cursor = %q, want the starting cursor", saved)
	}
}

// TestDrainStoresAccountsAndBalances proves the account list Plaid returns on
// every sync is written instead of thrown away.
func TestDrainStoresAccountsAndBalances(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	first := account("acct-1", "500.25")
	second := account("acct-1", "612.75")
	source := &fakeSource{pages: []provider.Batch{
		{Accounts: []model.Account{first}, NextCursor: "c1", HasMore: true},
		{Accounts: []model.Account{second}, NextCursor: "c2", HasMore: false},
	}}

	if _, err := drain(ctx, db, source, "item-1", nil); err != nil {
		t.Fatalf("drain: %v", err)
	}

	got, err := db.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d accounts, want 1", len(got))
	}
	if !got[0].BalanceCurrent.Valid || !got[0].BalanceCurrent.Decimal.Equal(decimal.RequireFromString("612.75")) {
		t.Errorf("BalanceCurrent = %+v, want the newest balance 612.75", got[0].BalanceCurrent)
	}
}

// A page is one unit: its row changes and durable cursor land together.
func TestDrainLeavesTheRowsAndTheCursorWhenAPageFailsPartWay(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	// A balance no DECIMAL(18,4) column can hold fails on insert, after the
	// rows of the same page are written.
	source := &fakeSource{pages: []provider.Batch{
		{Added: []model.Transaction{row("a", "2026-08-01")}, NextCursor: "c1", HasMore: true},
		{
			Added:      []model.Transaction{row("b", "2026-08-02")},
			RemovedIDs: []string{"a"},
			Accounts:   []model.Account{account("acct-1", "99999999999999999999.99")},
			NextCursor: "c2",
			HasMore:    false,
		},
	}}

	if _, err := drain(ctx, db, source, "item-1", nil); err == nil {
		t.Fatal("want an error from the second page, got nil")
	}

	// Only page one is stored: "b" never landed and "a" was not removed.
	n, err := db.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("stored %d rows, want only the row from the first page", n)
	}
	if _, err := db.Get(ctx, "fake", "a"); err != nil {
		t.Errorf("the row from the first page is gone: %v", err)
	}

	accounts, err := db.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("stored %d accounts from the failed page, want 0", len(accounts))
	}

	saved, err := db.Cursor(ctx, "fake", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if saved != "" {
		t.Errorf("saved cursor = %q, want the starting cursor", saved)
	}
}

// TestDrainWithNothingNewMakesOneCall is the everyday case: a second sync asks
// once and reports nothing.
func TestDrainWithNothingNewMakesOneCall(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	source := &fakeSource{pages: []provider.Batch{{NextCursor: "c1", HasMore: false}}}
	got, err := drain(ctx, db, source, "item-1", nil)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}

	if got.added != 0 || got.modified != 0 || got.removed != 0 {
		t.Errorf("counts = %+v, want all zero", got)
	}
	if source.call != 1 {
		t.Errorf("made %d API calls, want 1", source.call)
	}
}

// TestDrainHidesThePendingRowWhenThePostedRowArrivesLater walks the real PG&E
// case through the whole sync path: the pending row lands in one sync and the
// posted row in the next. Both stay stored, and the view shows one charge.
func TestDrainHidesThePendingRowWhenThePostedRowArrivesLater(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	pending := row("txn-pge-pending", "2026-08-10")
	pending.Name = "Pacific Gas Electric Company"
	pending.Amount = decimal.RequireFromString("138.98")
	pending.Pending = true

	posted := row("txn-pge-posted", "2026-08-11")
	posted.Name = "Pacific Gas And Elecwest"
	posted.Amount = decimal.RequireFromString("138.98")
	posted.PendingTransactionID = "txn-pge-pending"

	first := &fakeSource{pages: []provider.Batch{
		{Added: []model.Transaction{pending}, NextCursor: "c1", HasMore: false},
	}}
	if _, err := drain(ctx, db, first, "item-1", nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	second := &fakeSource{pages: []provider.Batch{
		{Added: []model.Transaction{posted}, NextCursor: "c2", HasMore: false},
	}}
	if _, err := drain(ctx, db, second, "item-1", nil); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	stored, err := db.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if stored != 2 {
		t.Errorf("transactions holds %d rows, want both versions of the charge", stored)
	}

	shown, err := db.NewestView(ctx, 10)
	if err != nil {
		t.Fatalf("newest view: %v", err)
	}
	if len(shown) != 1 || shown[0].ExternalID != "txn-pge-posted" {
		t.Fatalf("v_transactions shows %d rows %+v, want the posted row only", len(shown), shown)
	}
}

// movedError is what a provider returns when its data changed while a page
// sequence was read.
func movedError() error {
	return fmt.Errorf("sync item item-1: %w: plaid TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION",
		provider.ErrRestartPagination)
}

func TestDrainRetriesAMutationFromTheStartingCursor(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	if err := db.SetCursor(ctx, "fake", "item-1", "start"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	source := &fakeSource{
		pages: []provider.Batch{
			{Added: []model.Transaction{row("a", "2026-08-01")}, NextCursor: "c1", HasMore: true},
			{},
			{Added: []model.Transaction{row("a", "2026-08-01")}, NextCursor: "c1", HasMore: true},
			{Added: []model.Transaction{row("b", "2026-08-02")}, NextCursor: "c2"},
		},
		errs: []error{nil, movedError()},
	}

	got, err := drain(ctx, db, source, "item-1", nil)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}

	want := []string{"start", "c1", "start", "c1"}
	if len(source.cursors) != len(want) {
		t.Fatalf("cursors = %v, want %v", source.cursors, want)
	}
	for i := range want {
		if source.cursors[i] != want[i] {
			t.Errorf("cursor %d = %q, want %q", i, source.cursors[i], want[i])
		}
	}

	if got.added != 2 {
		t.Errorf("added = %d, want 2 from the attempt that finished", got.added)
	}

	saved, err := db.Cursor(ctx, "fake", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if saved != "c2" {
		t.Errorf("saved cursor = %q, want %q", saved, "c2")
	}
}

func TestDrainFallsBackFromALegacyIntermediateCursor(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	if err := db.SetCursor(ctx, "fake", "item-1", "dead-cursor"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	source := &fakeSource{
		pages: []provider.Batch{{}, {}, {NextCursor: "fresh"}},
		errs:  []error{movedError(), movedError()},
	}

	if _, err := drain(ctx, db, source, "item-1", nil); err != nil {
		t.Fatalf("drain: %v", err)
	}

	want := []string{"dead-cursor", "dead-cursor", ""}
	if len(source.cursors) != len(want) {
		t.Fatalf("cursors = %v, want %v", source.cursors, want)
	}
	for i := range want {
		if source.cursors[i] != want[i] {
			t.Errorf("cursor %d = %q, want %q", i, source.cursors[i], want[i])
		}
	}

	saved, err := db.Cursor(ctx, "fake", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if saved != "fresh" {
		t.Errorf("saved cursor = %q, want fresh", saved)
	}
}

// TestDrainGivesUpWhenTheDataKeepsMoving proves the restart is bounded.
func TestDrainGivesUpWhenTheDataKeepsMoving(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	errs := make([]error, maxRestarts)
	for i := range errs {
		errs[i] = movedError()
	}
	source := &fakeSource{errs: errs}

	if _, err := drain(ctx, db, source, "item-1", nil); err == nil {
		t.Fatal("drain error = nil, want it to give up")
	}
	if source.call != maxRestarts {
		t.Errorf("made %d attempts, want %d", source.call, maxRestarts)
	}
}

// TestRecordInstitutionNamesTheAccountsList proves an account can be shown
// with the login it sits behind after a sync.
func TestRecordInstitutionNamesTheAccountsList(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	item := tokens.Item{
		ItemID:      "item-1",
		Institution: "American Express",
		Env:         "sandbox",
		LinkedAt:    time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := recordInstitution(ctx, db, item, "production"); err != nil {
		t.Fatalf("record institution: %v", err)
	}

	stored, err := db.Institutions(ctx)
	if err != nil {
		t.Fatalf("institutions: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("got %d institutions, want 1", len(stored))
	}
	if stored[0].InstitutionName != "American Express" {
		t.Errorf("InstitutionName = %q, want %q", stored[0].InstitutionName, "American Express")
	}
	// The item's own environment wins: it is where the token works.
	if stored[0].Env != "sandbox" {
		t.Errorf("Env = %q, want the environment the item was linked in", stored[0].Env)
	}

	plaidAccount := account("acct-1", "10.00")
	plaidAccount.Provider = "plaid"
	if err := db.UpsertAccounts(ctx, []model.Account{plaidAccount}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}

	views, err := db.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	if views[0].InstitutionName != "American Express" {
		t.Errorf("InstitutionName = %q, want the joined name", views[0].InstitutionName)
	}
}

// TestRecordInstitutionFallsBackToTheCurrentEnvironment covers an item saved
// before the token file recorded an environment.
func TestRecordInstitutionFallsBackToTheCurrentEnvironment(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	if err := recordInstitution(ctx, db, tokens.Item{ItemID: "item-1"}, "production"); err != nil {
		t.Fatalf("record institution: %v", err)
	}
	stored, err := db.Institutions(ctx)
	if err != nil {
		t.Fatalf("institutions: %v", err)
	}
	if stored[0].Env != "production" {
		t.Errorf("Env = %q, want the current environment", stored[0].Env)
	}
}

// syncAccount is one account of one item, as a page would carry it.
func syncAccount(itemID, accountID string) model.Account {
	stored := account(accountID, "10.00")
	stored.Provider = plaid.ProviderName
	stored.ItemID = itemID
	return stored
}

// onePage is a source that returns everything in a single final page.
func onePage(accounts ...model.Account) *fakeSource {
	return &fakeSource{pages: []provider.Batch{{Accounts: accounts, NextCursor: "c1"}}}
}

// failingSource is a source whose first call fails with the given error.
func failingSource(err error) *fakeSource {
	return &fakeSource{errs: []error{err}}
}

// sourcesByItem hands each item its own fake provider.
func sourcesByItem(sources map[string]*fakeSource) sourceFunc {
	return func(_ plaid.Config, _, itemID, _ string) (provider.Provider, error) {
		source, found := sources[itemID]
		if !found {
			return nil, fmt.Errorf("the test has no source for %q", itemID)
		}
		return source, nil
	}
}

// collect records every progress line an operation reports.
func collect(lines *[]string) Progress {
	return func(text string) { *lines = append(*lines, text) }
}

// countLines is how many reported lines hold every one of the given parts.
func countLines(lines []string, parts ...string) int {
	found := 0
	for _, line := range lines {
		holds := true
		for _, part := range parts {
			if !strings.Contains(line, part) {
				holds = false
			}
		}
		if holds {
			found++
		}
	}
	return found
}

// lastStatus reads the recorded failure of one item.
func lastStatus(t *testing.T, dbPath, itemID string) string {
	t.Helper()
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	states, err := db.SyncStates(context.Background())
	if err != nil {
		t.Fatalf("sync states: %v", err)
	}
	for _, state := range states {
		if state.ItemID == itemID && state.LastStatus != "" {
			return state.LastStatus
		}
	}
	return ""
}

// The TUI shows one institution at a time, so a single sync must answer with
// that item's accounts only.
func TestSyncItemReturnsOnlyAccountsForTheNewItem(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	amex := item("item-amex", "American Express", "sandbox")
	scotia := item("item-scotia", "Scotiabank", "sandbox")
	seedTokens(t, cfg.TokensPath, amex, scotia)
	seedStore(t, cfg.DBPath, scotia)

	sources := sourcesByItem(map[string]*fakeSource{
		"item-amex": onePage(syncAccount("item-amex", "acct-amex")),
	})

	accounts, err := newWith(cfg, nil, nil, sources).SyncItem(context.Background(), "item-amex", nil)
	if err != nil {
		t.Fatalf("SyncItem: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %+v, want only the accounts of item-amex", accounts)
	}
	if accounts[0].AccountID != "acct-amex" {
		t.Errorf("AccountID = %q, want acct-amex", accounts[0].AccountID)
	}
}

// A new link is not ready to sync at once. The whole Plaid detail is kept, so
// the next screen can say what happened instead of "sync failed".
func TestSyncItemRecordsFullProductNotReadyError(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	amex := item("item-amex", "American Express", "sandbox")
	seedTokens(t, cfg.TokensPath, amex)

	const message = "the transactions product is not yet ready"
	plaidErr := fmt.Errorf("sync item item-amex: %w: plaid PRODUCT_NOT_READY (ITEM_ERROR): %s",
		provider.ErrProductNotReady, message)
	sources := sourcesByItem(map[string]*fakeSource{"item-amex": failingSource(plaidErr)})

	_, err := newWith(cfg, nil, nil, sources).SyncItem(context.Background(), "item-amex", nil)
	if err == nil {
		t.Fatal("SyncItem error = nil, want the Plaid failure")
	}
	if !errors.Is(err, ErrProductNotReady) {
		t.Errorf("error = %v, want it to keep the typed PRODUCT_NOT_READY error", err)
	}

	status := lastStatus(t, cfg.DBPath, "item-amex")
	for _, want := range []string{"PRODUCT_NOT_READY", "ITEM_ERROR", message} {
		if !strings.Contains(status, want) {
			t.Errorf("last_status = %q, want it to contain %q", status, want)
		}
	}
}

// One bad card must not stop the others, and each item must be reported once.
func TestSyncAllContinuesAfterOneItemFails(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	other := item("item-other", "Bank of Nowhere", "production")
	amex := item("item-amex", "American Express", "sandbox")
	scotia := item("item-scotia", "Scotiabank", "sandbox")
	seedTokens(t, cfg.TokensPath, other, amex, scotia)

	sources := sourcesByItem(map[string]*fakeSource{
		"item-amex":   failingSource(errors.New("plaid is down")),
		"item-scotia": onePage(syncAccount("item-scotia", "acct-scotia")),
	})
	var lines []string

	results, err := newWith(cfg, nil, nil, sources).SyncAll(context.Background(), collect(&lines))
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v, want one for every linked item", results)
	}

	if !results[0].Skipped || results[0].Err != nil {
		t.Errorf("results[0] = %+v, want the other environment skipped", results[0])
	}
	if results[1].Err == nil || results[1].ItemID != "item-amex" {
		t.Errorf("results[1] = %+v, want the item-amex failure", results[1])
	}
	if results[2].Err != nil || len(results[2].Accounts) != 1 {
		t.Errorf("results[2] = %+v, want item-scotia synced after the failure", results[2])
	}
	if results[2].Label != "Scotiabank" {
		t.Errorf("results[2].Label = %q, want Scotiabank", results[2].Label)
	}

	if got := countLines(lines, "Bank of Nowhere", "skipped"); got != 1 {
		t.Errorf("reported the skip %d times in %q, want once", got, lines)
	}
	if got := countLines(lines, "American Express", "failed", "plaid is down"); got != 1 {
		t.Errorf("reported the failure %d times in %q, want once", got, lines)
	}
	if got := countLines(lines, "Scotiabank", "0 added, 0 modified, 0 removed"); got != 1 {
		t.Errorf("reported the success %d times in %q, want once", got, lines)
	}

	if status := lastStatus(t, cfg.DBPath, "item-amex"); !strings.Contains(status, "plaid is down") {
		t.Errorf("last_status = %q, want the recorded failure", status)
	}
}

// An item linked before the institution name was recorded still needs a name
// to show, and its id is the only one there is.
func TestSyncAllUsesItemIDWhenInstitutionIsEmpty(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	nameless := item("item-nameless", "", "sandbox")
	seedTokens(t, cfg.TokensPath, nameless)

	sources := sourcesByItem(map[string]*fakeSource{"item-nameless": onePage()})
	var lines []string

	results, err := newWith(cfg, nil, nil, sources).SyncAll(context.Background(), collect(&lines))
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(results) != 1 || results[0].Label != "item-nameless" {
		t.Fatalf("results = %+v, want the item id as the label", results)
	}
	if got := countLines(lines, "item-nameless"); got != 1 {
		t.Errorf("reported %d lines naming the item in %q, want one", got, lines)
	}
}

// A cancellation is the error of the whole operation, not a state of one card.
// Both callers ask whether the run was cancelled, and neither reads the item
// results to find out. Nothing about the cancellation may be recorded either:
// the item is unchanged, and a stored "context canceled" would be shown as a
// failed sync until the next successful run.
func TestSyncAllReportsCancellationAsTheOperationError(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	amex := item("item-amex", "American Express", "sandbox")
	seedTokens(t, cfg.TokensPath, amex)

	sources := sourcesByItem(map[string]*fakeSource{
		"item-amex": onePage(syncAccount("item-amex", "acct-amex")),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := newWith(cfg, nil, nil, sources).SyncAll(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncAll error = %v, want it to report the cancellation", err)
	}
	// The partial results of the run still come back with it.
	if len(results) != 1 || results[0].ItemID != "item-amex" {
		t.Errorf("results = %+v, want the item reached before the stop", results)
	}
	// Recording the failure would use the same dead context and fail as well,
	// so the item error must not carry a second, meaningless write failure.
	if got := results[0].Err; got == nil || strings.Contains(got.Error(), "save status") {
		t.Errorf("item error = %v, want the read failure alone", got)
	}
	if status := lastStatus(t, cfg.DBPath, "item-amex"); status != "" {
		t.Errorf("last_status = %q, want a cancellation to record nothing", status)
	}
}

// Nothing linked is a setup mistake, not an empty success.
func TestSyncAllRefusesWhenNothingIsLinked(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	seedTokens(t, cfg.TokensPath)

	_, err := newWith(cfg, nil, nil, nil).SyncAll(context.Background(), nil)
	if err == nil {
		t.Fatal("SyncAll error = nil, want the nothing-linked failure")
	}
	if !strings.Contains(err.Error(), "fourseas link") {
		t.Errorf("error = %q, want it to name the link command", err)
	}
}
