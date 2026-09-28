package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
)

// accountRefresh describes an account read that follows a completed action.
// Each retry captures the same outcome rather than reading mutable flow state.
type accountRefresh struct {
	destination   screen
	success       string
	failurePrefix string
	retryLabel    string
	retryNote     string
}

type accountRefreshResult struct {
	data    app.AccountData
	outcome accountRefresh
}

func (m *Model) startActionRefresh(outcome accountRefresh) tea.Cmd {
	m.screen = outcome.destination
	cmd := m.start(actionRefreshOperation, func(ctx context.Context) (any, error) {
		data, err := m.app.Accounts(ctx, "")
		return accountRefreshResult{data: data, outcome: outcome}, err
	})
	m.recovery.retry = func() tea.Cmd { return m.startActionRefresh(outcome) }
	m.recovery.retryLabel = outcome.retryLabel
	return cmd
}

func (m *Model) showActionRefreshFailure(outcome accountRefresh, err error) tea.Cmd {
	m.showFailure(err)
	ending := "failed."
	if errors.Is(err, context.Canceled) {
		ending = "was canceled."
	}
	m.recovery.message = outcome.failurePrefix + ending
	m.recovery.notes = []string{"Reason: " + displayError(err), outcome.retryNote}
	return nil
}
