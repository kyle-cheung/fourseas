package store

import (
	"context"
	"testing"
	"time"
)

func TestCursorStartsEmptyAndPersists(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	got, err := s.Cursor(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("first cursor read: %v", err)
	}
	if got != "" {
		t.Errorf("cursor = %q on a new store, want an empty string", got)
	}

	if err := s.SetCursor(ctx, "plaid", "item-1", "cursor-abc"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}
	if err := s.SetCursor(ctx, "plaid", "item-1", "cursor-def"); err != nil {
		t.Fatalf("overwrite cursor: %v", err)
	}

	got, err = s.Cursor(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("second cursor read: %v", err)
	}
	if got != "cursor-def" {
		t.Errorf("cursor = %q, want %q", got, "cursor-def")
	}
}

func TestSetCursorRecordsWhenTheSyncHappened(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	before := time.Now().Add(-time.Second)
	if err := s.SetCursor(ctx, "plaid", "item-1", "cursor-abc"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	states, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("got %d sync states, want 1", len(states))
	}
	if states[0].LastSyncedAt == nil || states[0].LastSyncedAt.Before(before) {
		t.Errorf("LastSyncedAt = %v, want a time after %v", states[0].LastSyncedAt, before)
	}
}

func TestSetStatusKeepsTheCursor(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.SetCursor(ctx, "plaid", "item-1", "cursor-abc"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}
	if err := s.SetStatus(ctx, "plaid", "item-1", "rate limited"); err != nil {
		t.Fatalf("set status: %v", err)
	}

	states, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states: %v", err)
	}
	if states[0].Cursor != "cursor-abc" {
		t.Errorf("cursor = %q, want it to survive a status write", states[0].Cursor)
	}
	if states[0].LastStatus != "rate limited" {
		t.Errorf("LastStatus = %q, want %q", states[0].LastStatus, "rate limited")
	}
}

func TestClearCursorKeepsTheLastSyncOutcome(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.SetCursor(ctx, "plaid", "item-1", "legacy-cursor"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}
	if err := s.SetStatus(ctx, "plaid", "item-1", "previous result"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	before, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states before clear: %v", err)
	}

	if err := s.ClearCursor(ctx, "plaid", "item-1"); err != nil {
		t.Fatalf("clear cursor: %v", err)
	}
	after, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states after clear: %v", err)
	}

	if after[0].Cursor != "" {
		t.Errorf("cursor = %q, want empty", after[0].Cursor)
	}
	if after[0].LastStatus != before[0].LastStatus {
		t.Errorf("status = %q, want %q", after[0].LastStatus, before[0].LastStatus)
	}
	if !after[0].LastSyncedAt.Equal(*before[0].LastSyncedAt) {
		t.Errorf("last sync = %v, want %v", after[0].LastSyncedAt, before[0].LastSyncedAt)
	}
}

// TestSetStatusOnAnItemThatNeverSynced covers a failure on the very first run,
// when there is no cursor row yet.
func TestSetStatusOnAnItemThatNeverSynced(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.SetStatus(ctx, "plaid", "item-new", "plaid is unavailable"); err != nil {
		t.Fatalf("set status: %v", err)
	}

	states, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states: %v", err)
	}
	if len(states) != 1 || states[0].LastStatus != "plaid is unavailable" {
		t.Fatalf("got %+v, want one row holding the failure reason", states)
	}
	if states[0].Cursor != "" {
		t.Errorf("cursor = %q, want an empty cursor", states[0].Cursor)
	}
}
