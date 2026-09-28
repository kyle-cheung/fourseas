package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

func reconnectDetail(t *testing.T, fake *fakeService) (*Model, tea.Cmd) {
	t.Helper()
	fake.data.Accounts = []model.AccountView{creditAccount("acc-1", "Wealthsimple")}
	m := ready(t, fake)
	openDetail(t, m, 0)
	for i, action := range m.detailActions() {
		if detailActionLabel(action) == "Reconnect institution" {
			m.detail.cursor = i
			if !strings.Contains(content(m), "all accounts on this connection") {
				t.Fatal("missing institution scope note")
			}
			return m, press(t, m, codeKey(tea.KeyEnter))
		}
	}
	t.Fatal("no Reconnect institution action")
	return nil, nil
}

func TestReconnectFromDetailsSyncsOnlyItsItemAndRefreshesAccounts(t *testing.T) {
	fake := &fakeService{}
	m, cmd := reconnectDetail(t, fake)
	cmd = runOperation(t, m, cmd)
	if len(fake.reconnectCalls) != 1 || len(fake.syncCalls) != 0 {
		t.Fatal("wrong authentication sequence")
	}
	cmd = runOperation(t, m, cmd)
	runOperation(t, m, cmd)
	if len(fake.syncCalls) != 1 || fake.syncCalls[0] != "item-1" || fake.syncAllCalls != 0 {
		t.Fatalf("wrong sync target: %v", fake.syncCalls)
	}
	if m.screen != accountsScreen || !strings.Contains(content(m), "Reconnected Wealthsimple") {
		t.Fatalf("unexpected result: %s", content(m))
	}
	if len(fake.linkCalls) != 0 || len(fake.enableCalls) != 0 || len(fake.nicknameCalls) != 0 {
		t.Fatal("reconnect changed another setting")
	}
}

func TestReconnectCancellationReturnsToDetailsWithoutSync(t *testing.T) {
	fake := &fakeService{reconnectErr: context.Canceled}
	m, cmd := reconnectDetail(t, fake)
	if next := runOperation(t, m, cmd); next != nil {
		t.Fatal("cancellation started another operation")
	}
	if m.screen != detailScreen || len(fake.syncCalls) != 0 {
		t.Fatal("cancellation did not return to details")
	}
}

func TestReconnectSyncFailureRetriesOnlySync(t *testing.T) {
	for _, cause := range []error{errors.New("network unavailable"), context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			fake := &fakeService{syncErrs: []error{cause, nil}}
			m, cmd := reconnectDetail(t, fake)
			cmd = runOperation(t, m, cmd)
			runOperation(t, m, cmd)
			if m.screen != recoveryScreen || !strings.Contains(content(m), "Retry sync") || !strings.Contains(content(m), "Sign-in completed") {
				t.Fatalf("wrong recovery: %s", content(m))
			}
			cmd = press(t, m, codeKey(tea.KeyEnter))
			cmd = runOperation(t, m, cmd)
			runOperation(t, m, cmd)
			if len(fake.reconnectCalls) != 1 || len(fake.syncCalls) != 2 {
				t.Fatal("retry repeated sign-in or missed sync")
			}
		})
	}
}

func TestReconnectAccountRefreshFailureRetriesOnlyRead(t *testing.T) {
	for _, cause := range []error{errors.New("read failed"), context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			fake := &fakeService{}
			m, cmd := reconnectDetail(t, fake)
			cmd = runOperation(t, m, cmd)
			cmd = runOperation(t, m, cmd)
			fake.accountsErr = cause
			runOperation(t, m, cmd)
			if !strings.Contains(content(m), "Retry refresh") || !strings.Contains(content(m), "Reconnected and synced") {
				t.Fatalf("wrong refresh failure: %s", content(m))
			}
			// A second failure must keep the same retry and destination.
			runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
			if m.returnTo != accountsScreen || !strings.Contains(content(m), "Retry refresh") {
				t.Fatalf("retry lost its outcome: %s", content(m))
			}
			fake.accountsErr = nil
			runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
			if len(fake.reconnectCalls) != 1 || len(fake.syncCalls) != 1 || m.screen != accountsScreen {
				t.Fatal("refresh repeated a previous stage or changed its destination")
			}
			if !strings.Contains(content(m), "Reconnected Wealthsimple") {
				t.Fatalf("lost success message: %s", content(m))
			}
		})
	}
}

func TestLoginRequiredSyncSummaryGivesRecoveryAction(t *testing.T) {
	fake := &fakeService{syncAllResults: []app.SyncResult{
		{ItemID: "earlier", Label: "Earlier", Err: errors.New("network error")},
		{ItemID: "item-1", Label: "Wealthsimple", Err: app.ErrLoginRequired},
		{ItemID: "item-2", Label: "Other", Accounts: []model.AccountView{creditAccount("other", "Other")}},
	}}
	m := ready(t, fake)
	runOperation(t, m, m.startSyncAll())
	body := strings.Join(strings.Fields(content(m)), " ")
	for _, want := range []string{"Reconnect required", "Reconnect institution", "fourseas reconnect item-1", "Other"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q: %s", want, body)
		}
	}
	if len(fake.reconnectCalls) != 0 {
		t.Fatal("sync opened sign-in automatically")
	}
}

func TestReconnectWarningSurvivesNewerFailures(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	states := []app.SyncState{
		{ItemID: "item-1", Institution: "Wealthsimple", ReconnectRequired: true, LastStatus: "login required", LastSyncedAt: &older},
		{ItemID: "item-2", Institution: "Other", LastStatus: "network error", LastSyncedAt: &now},
	}
	got, _ := syncStatus(states, now)
	if !strings.Contains(got, "Reconnect required: Wealthsimple") {
		t.Fatalf("hidden reconnect warning: %s", got)
	}
}

func TestPendingReconnectSyncDoesNotAskForAnotherSignIn(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	newest := app.SyncState{Institution: "Wealthsimple", ReconnectSyncPending: true, LastSyncedAt: &now}
	old := app.SyncState{Institution: "Older", ReconnectSyncPending: true, LastSyncedAt: &older}
	undated := app.SyncState{Institution: "Undated", ReconnectSyncPending: true}
	for _, states := range [][]app.SyncState{
		{newest}, {newest, old, undated}, {undated, old, newest},
	} {
		got, _ := syncStatus(states, now)
		if !strings.Contains(got, "Sync pending: Wealthsimple") || strings.Contains(got, "Reconnect required") {
			t.Fatalf("incorrect pending state: %s", got)
		}
	}
}

func TestReconnectStatusFailureRetriesSyncWithoutRepeatingSignIn(t *testing.T) {
	for _, cause := range []error{errors.New("saving status failed"), context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			partial := &app.ReconnectStatusError{Item: app.LinkedItem{ItemID: "item-1", Institution: "Wealthsimple"}, Err: cause}
			fake := &fakeService{reconnectErr: fmt.Errorf("reconnect: %w", partial)}
			m, cmd := reconnectDetail(t, fake)
			runOperation(t, m, cmd)
			if m.screen != recoveryScreen || !strings.Contains(content(m), "Retry sync") {
				t.Fatalf("incorrect partial-success recovery: %s", content(m))
			}
			cmd = runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
			runOperation(t, m, cmd)
			if len(fake.reconnectCalls) != 1 || len(fake.syncCalls) != 1 || fake.syncCalls[0] != "item-1" {
				t.Fatal("repeated sign-in or lost target after confirmed authentication")
			}
		})
	}
}

func TestReconnectSyncActionableErrorDoesNotOfferBlindSyncRetry(t *testing.T) {
	for _, cause := range []error{app.ErrLoginRequired, app.ErrAdditionalConsentRequired} {
		t.Run(cause.Error(), func(t *testing.T) {
			fake := &fakeService{syncErrs: []error{cause}}
			m, cmd := reconnectDetail(t, fake)
			cmd = runOperation(t, m, cmd)
			runOperation(t, m, cmd)
			if strings.Contains(content(m), "Retry sync") {
				t.Fatalf("ineffective retry: %s", content(m))
			}
			if cause == app.ErrLoginRequired {
				if !strings.Contains(content(m), "Reconnect institution") {
					t.Fatal("missing reconnect action")
				}
				runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
				if len(fake.reconnectCalls) != 2 {
					t.Fatal("recovery did not repeat authentication")
				}
			} else if !strings.Contains(strings.Join(strings.Fields(content(m)), " "), "Enable statement data") {
				t.Fatalf("missing consent instructions: %s", content(m))
			}
		})
	}
}

func TestDropReconnectedResultDoesNotMutateOperationResult(t *testing.T) {
	results := []app.SyncResult{{ItemID: "item-1"}, {ItemID: "item-2"}, {ItemID: "item-1"}, {ItemID: "item-3"}}
	m := New(&fakeService{})
	m.syncResults = results
	m.reconnected = app.LinkedItem{ItemID: "item-1"}
	m.dropReconnectedResult()
	if len(m.syncResults) != 2 || m.syncResults[0].ItemID != "item-2" || m.syncResults[1].ItemID != "item-3" {
		t.Fatalf("filtered results = %+v", m.syncResults)
	}
	for i, id := range []string{"item-1", "item-2", "item-1", "item-3"} {
		if results[i].ItemID != id {
			t.Fatal("mutated the operation's backing array")
		}
	}
}
