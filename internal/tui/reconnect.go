package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
)

func (m *Model) startReconnect(itemID string) tea.Cmd {
	m.reconnected = app.LinkedItem{}
	cmd := m.start(reconnectOperation, func(ctx context.Context) (any, error) {
		return m.app.Reconnect(ctx, itemID, m.report)
	})
	m.recovery.retry = func() tea.Cmd { return m.startReconnect(itemID) }
	return cmd
}

// These stages have independent retries: authentication, sync, then local read.
func (m *Model) startReconnectSync() tea.Cmd {
	itemID := m.reconnected.ItemID
	cmd := m.start(reconnectSyncOperation, func(ctx context.Context) (any, error) {
		return m.app.SyncItem(ctx, itemID, m.report)
	})
	m.recovery.retry = m.startReconnectSync
	m.recovery.retryLabel = "Retry sync"
	return cmd
}

func (m *Model) startReconnectRefresh() tea.Cmd {
	return m.startActionRefresh(accountRefresh{
		destination:   accountsScreen,
		success:       "Reconnected " + linkedName(m.reconnected) + ". Accounts refreshed.",
		failurePrefix: "Reconnected and synced, but refreshing the account list ",
		retryLabel:    "Retry refresh",
		retryNote:     "Retry refresh reloads the stored accounts.",
	})
}

func (m *Model) showReconnectSyncFailure(err error) tea.Cmd {
	m.showFailure(err)
	if errors.Is(err, app.ErrLoginRequired) {
		itemID := m.reconnected.ItemID
		m.recovery.message = "Plaid still requires sign-in for this institution."
		m.recovery.notes = []string{"Choose Reconnect institution to sign in again."}
		m.recovery.retryLabel = "Reconnect institution"
		m.recovery.retry = func() tea.Cmd { return m.startReconnect(itemID) }
		return nil
	}
	if errors.Is(err, app.ErrAdditionalConsentRequired) {
		m.recovery.message = "Sign-in completed, but statement data still needs consent."
		m.recovery.notes = []string{"Open a credit account's details and choose Enable statement data."}
		m.recovery.retry = nil
		return nil
	}
	outcome := "failed"
	if errors.Is(err, context.Canceled) {
		outcome = "was canceled"
	}
	m.recovery.message = "Sign-in completed, but sync " + outcome + "."
	m.recovery.notes = []string{"Reason: " + displayError(err), "Retry sync fetches data without reopening sign-in."}
	return nil
}

func (m *Model) dropReconnectedResult() {
	results := make([]app.SyncResult, 0, len(m.syncResults))
	for _, result := range m.syncResults {
		if result.ItemID != m.reconnected.ItemID {
			results = append(results, result)
		}
	}
	m.syncResults = results
}

func (m *Model) showReconnectStatusFailure(err error) tea.Cmd {
	m.showFailure(err)
	m.recovery.message = "Sign-in completed, but its local status could not be saved."
	m.recovery.notes = []string{"Reason: " + displayError(err), "Retry sync fetches data without reopening sign-in."}
	m.recovery.retryLabel = "Retry sync"
	m.recovery.retry = m.startReconnectSync
	return nil
}
