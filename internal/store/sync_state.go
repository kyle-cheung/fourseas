package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// One cursor covers one institution, not one account, because a provider
// cursor belongs to the access token that covers every account behind it.

// Cursor returns the saved sync cursor, or an empty string on the first run.
func (s *Store) Cursor(ctx context.Context, provider, itemID string) (string, error) {
	var cursor sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor FROM sync_state WHERE provider = ? AND item_id = ?`, provider, itemID).Scan(&cursor)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read cursor for %s/%s: %w", provider, itemID, err)
	}
	return cursor.String, nil
}

// SetCursor saves the cursor the next run starts from, and records that the
// sync succeeded.
func (s *Store) SetCursor(ctx context.Context, provider, itemID, cursor string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_state (provider, item_id, cursor, last_synced_at, last_status)
		VALUES (?, ?, ?, ?, 'ok')
		ON CONFLICT (provider, item_id) DO UPDATE SET
			cursor         = excluded.cursor,
			last_synced_at = excluded.last_synced_at,
			last_status    = excluded.last_status`,
		provider, itemID, textArg(cursor), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("save cursor for %s/%s: %w", provider, itemID, err)
	}
	return nil
}

// SetStatus records how the last sync of one institution ended, without
// touching the cursor. A failure must not move where the next run starts.
func (s *Store) SetStatus(ctx context.Context, provider, itemID, status string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_state (provider, item_id, cursor, last_synced_at, last_status)
		VALUES (?, ?, NULL, ?, ?)
		ON CONFLICT (provider, item_id) DO UPDATE SET
			last_synced_at = excluded.last_synced_at,
			last_status    = excluded.last_status`,
		provider, itemID, time.Now().UTC(), textArg(status))
	if err != nil {
		return fmt.Errorf("save status for %s/%s: %w", provider, itemID, err)
	}
	return nil
}

// SyncStates returns where every institution's next sync starts, and how the
// last one ended.
func (s *Store) SyncStates(ctx context.Context) ([]model.SyncState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, item_id, cursor, last_synced_at, last_status
		FROM sync_state
		ORDER BY provider, item_id`)
	if err != nil {
		return nil, fmt.Errorf("query sync state: %w", err)
	}
	defer rows.Close()

	var out []model.SyncState
	for rows.Next() {
		var (
			state          model.SyncState
			cursor, status sql.NullString
			lastSyncedAt   sql.NullTime
		)
		if err := rows.Scan(&state.Provider, &state.ItemID, &cursor, &lastSyncedAt, &status); err != nil {
			return nil, fmt.Errorf("scan sync state: %w", err)
		}
		state.Cursor = text(cursor)
		state.LastStatus = text(status)
		state.LastSyncedAt = timePtr(lastSyncedAt)
		out = append(out, state)
	}
	return out, rows.Err()
}
