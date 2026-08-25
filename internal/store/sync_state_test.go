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

func TestSetStatusOnlyPreservesCursorAndLastSyncTime(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.SetCursor(ctx, "plaid", "item-1", "cursor-abc"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}
	before, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states before status-only write: %v", err)
	}
	if len(before) != 1 || before[0].LastSyncedAt == nil {
		t.Fatalf("state before status-only write = %+v, want one timestamped row", before)
	}

	if err := s.SetStatusOnly(ctx, "plaid", "item-1", "consent is required"); err != nil {
		t.Fatalf("set status only: %v", err)
	}
	afterSet, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states after status-only write: %v", err)
	}
	if afterSet[0].Cursor != before[0].Cursor {
		t.Errorf("cursor = %q, want %q", afterSet[0].Cursor, before[0].Cursor)
	}
	if !afterSet[0].LastSyncedAt.Equal(*before[0].LastSyncedAt) {
		t.Errorf("last sync = %v, want %v", afterSet[0].LastSyncedAt, before[0].LastSyncedAt)
	}
	if afterSet[0].LastStatus != "consent is required" {
		t.Errorf("status = %q, want consent is required", afterSet[0].LastStatus)
	}

	if err := s.SetStatusOnly(ctx, "plaid", "item-1", ""); err != nil {
		t.Fatalf("clear status only: %v", err)
	}
	afterClear, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states after status-only clear: %v", err)
	}
	if afterClear[0].Cursor != before[0].Cursor || afterClear[0].LastStatus != "" {
		t.Errorf("state after clear = %+v, want the cursor and an empty status", afterClear[0])
	}
	if !afterClear[0].LastSyncedAt.Equal(*before[0].LastSyncedAt) {
		t.Errorf("last sync after clear = %v, want %v", afterClear[0].LastSyncedAt, before[0].LastSyncedAt)
	}
}

func TestSetStatusOnlyCreatesAnUntimestampedState(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.SetStatusOnly(ctx, "plaid", "item-new", "consent is required"); err != nil {
		t.Fatalf("set status only: %v", err)
	}
	states, err := s.SyncStates(ctx)
	if err != nil {
		t.Fatalf("sync states: %v", err)
	}
	if len(states) != 1 || states[0].LastStatus != "consent is required" {
		t.Fatalf("states = %+v, want one consent state", states)
	}
	if states[0].Cursor != "" || states[0].LastSyncedAt != nil {
		t.Errorf("new status-only state = %+v, want no cursor or sync time", states[0])
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
