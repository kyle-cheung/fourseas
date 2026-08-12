package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/kyle-cheung/fourseas/providence/internal/provider"
	"github.com/kyle-cheung/fourseas/providence/internal/store"
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
		Amount:     10,
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

	got, err := drain(ctx, db, source, "item-1")
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
	if _, err := drain(ctx, db, source, "item-1"); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if source.cursors[0] != "saved-cursor" {
		t.Errorf("first cursor = %q, want the saved one", source.cursors[0])
	}
}

func TestDrainKeepsTheCursorFromTheLastGoodPage(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)

	source := &fakeSource{
		pages: []provider.Batch{
			{Added: []model.Transaction{row("a", "2026-08-01")}, NextCursor: "c1", HasMore: true},
		},
		errs: []error{nil, errors.New("plaid is unavailable")},
	}

	if _, err := drain(ctx, db, source, "item-1"); err == nil {
		t.Fatal("want an error from the second page, got nil")
	}

	saved, err := db.Cursor(ctx, "fake", "item-1")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if saved != "c1" {
		t.Errorf("saved cursor = %q, want the cursor from the last good page", saved)
	}
}
