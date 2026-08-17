package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/shopspring/decimal"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// fakeService is a stand-in for *app.App. It records what the model asked for
// and returns prepared answers, so no store or provider is opened in a test.
type fakeService struct {
	data        app.AccountData
	accountsErr error
	// accountsCalls holds the item id of every Accounts call, in order.
	accountsCalls []string
	// accountsCtx is the context of the last Accounts call, so a test can see
	// whether the model cancelled it.
	accountsCtx context.Context
}

func (f *fakeService) Accounts(ctx context.Context, itemID string) (app.AccountData, error) {
	f.accountsCalls = append(f.accountsCalls, itemID)
	f.accountsCtx = ctx
	return f.data, f.accountsErr
}

func (f *fakeService) Link(context.Context, int, app.Progress) (app.LinkedItem, error) {
	return app.LinkedItem{}, nil
}

func (f *fakeService) SyncItem(context.Context, string, app.Progress) ([]model.AccountView, error) {
	return nil, nil
}

func (f *fakeService) SyncAll(context.Context, app.Progress) ([]app.SyncResult, error) {
	return nil, nil
}

func (f *fakeService) SetNickname(context.Context, string, string) error { return nil }

func (f *fakeService) UnlinkPreview(context.Context, string) (app.UnlinkData, error) {
	return app.UnlinkData{}, nil
}

func (f *fakeService) Unlink(context.Context, string, app.Progress) (app.UnlinkResult, error) {
	return app.UnlinkResult{}, nil
}

// press sends one key to the model and checks the model identity stays the
// same, because every Bubble Tea method uses a pointer receiver.
func press(t *testing.T, m *Model, key tea.KeyPressMsg) tea.Cmd {
	t.Helper()
	next, cmd := m.Update(key)
	if next != tea.Model(m) {
		t.Fatalf("Update returned a different model for key %q", key.String())
	}
	return cmd
}

// runes builds a printable key press.
func runeKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func codeKey(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func ctrlC() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }

// quits says whether the command ends the program.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// ready builds a model whose first account refresh has already completed.
func ready(t *testing.T, fake *fakeService) *Model {
	t.Helper()
	m := New(fake)
	m.width, m.height = 80, 24
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no command")
	}
	m.Update(cmd())
	if m.running {
		t.Fatal("model is still running after the first refresh")
	}
	return m
}

// account builds one displayable account.
func account(id, nickname, name, mask, institution string, balance float64) model.AccountView {
	return model.AccountView{
		Account: model.Account{
			Provider:       "plaid",
			AccountID:      id,
			ItemID:         "item-1",
			Name:           name,
			Mask:           mask,
			Currency:       "USD",
			Nickname:       nickname,
			BalanceCurrent: decimal.NewNullDecimal(decimal.NewFromFloat(balance)),
		},
		InstitutionName: institution,
	}
}

func TestMainNavigationAndQuit(t *testing.T) {
	fake := &fakeService{data: app.AccountData{
		Accounts: []model.AccountView{
			account("acc-1", "", "Everyday Checking", "1234", "Chase", 10),
			account("acc-2", "Travel card", "Sapphire", "9876", "Chase", 20),
		},
	}}
	m := ready(t, fake)

	if m.screen != mainScreen {
		t.Fatalf("screen = %v, want mainScreen", m.screen)
	}
	body := m.View().Content
	for _, want := range []string{"Accounts", "Add account", "Sync"} {
		if !strings.Contains(body, want) {
			t.Errorf("main view = %q, want it to contain %q", body, want)
		}
	}
	if !strings.Contains(body, "2") {
		t.Errorf("main view = %q, want the account count", body)
	}

	press(t, m, codeKey(tea.KeyDown))
	if m.main.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", m.main.cursor)
	}
	press(t, m, codeKey(tea.KeyDown))
	press(t, m, codeKey(tea.KeyDown))
	if m.main.cursor != 2 {
		t.Errorf("cursor after three downs = %d, want it clamped to 2", m.main.cursor)
	}
	press(t, m, codeKey(tea.KeyUp))
	press(t, m, codeKey(tea.KeyUp))
	press(t, m, codeKey(tea.KeyUp))
	if m.main.cursor != 0 {
		t.Errorf("cursor after three ups = %d, want it clamped to 0", m.main.cursor)
	}

	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != accountsScreen {
		t.Fatalf("screen after enter = %v, want accountsScreen", m.screen)
	}
	list := m.View().Content
	if !strings.Contains(list, "Travel card") {
		t.Errorf("account list = %q, want the nickname of the second account", list)
	}

	press(t, m, codeKey(tea.KeyDown))
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != detailScreen {
		t.Fatalf("screen after enter on a row = %v, want detailScreen", m.screen)
	}
	if m.detail.account.AccountID != "acc-2" {
		t.Errorf("detail account = %q, want acc-2", m.detail.account.AccountID)
	}

	press(t, m, codeKey(tea.KeyEsc))
	if m.screen != accountsScreen {
		t.Errorf("screen after esc = %v, want accountsScreen", m.screen)
	}
	press(t, m, codeKey(tea.KeyEsc))
	if m.screen != mainScreen {
		t.Errorf("screen after a second esc = %v, want mainScreen", m.screen)
	}

	if !quits(press(t, m, runeKey('q'))) {
		t.Error("q on the main screen did not quit")
	}
	if !quits(press(t, m, ctrlC())) {
		t.Error("ctrl+c with no running operation did not quit")
	}
}

func TestTextPromptAcceptsQAndEscCancels(t *testing.T) {
	m := ready(t, &fakeService{})

	m.openPrompt(customDaysScreen, "days", "")
	if m.screen != customDaysScreen {
		t.Fatalf("screen = %v, want customDaysScreen", m.screen)
	}

	for _, r := range "q90" {
		if quits(press(t, m, runeKey(r))) {
			t.Fatalf("key %q quit the program while a prompt was open", r)
		}
	}
	if got := m.prompt.input.Value(); got != "q90" {
		t.Errorf("prompt value = %q, want %q", got, "q90")
	}
	if m.screen != customDaysScreen {
		t.Errorf("screen = %v, want the prompt to stay open", m.screen)
	}

	press(t, m, codeKey(tea.KeyEsc))
	if m.screen != historyScreen {
		t.Errorf("screen after esc = %v, want historyScreen", m.screen)
	}
	if got := m.prompt.input.Value(); got != "" {
		t.Errorf("prompt value after esc = %q, want it cleared", got)
	}
}

func TestRunningOperationIgnoresKeysAndCtrlCCancels(t *testing.T) {
	fake := &fakeService{}
	m := ready(t, fake)
	press(t, m, codeKey(tea.KeyEnter)) // open the account list, so returnTo is not main
	if m.screen != accountsScreen {
		t.Fatalf("screen = %v, want accountsScreen", m.screen)
	}

	cmd := m.refreshAccounts()
	if !m.running {
		t.Fatal("start did not mark the model as running")
	}
	if m.returnTo != accountsScreen {
		t.Fatalf("returnTo = %v, want accountsScreen", m.returnTo)
	}
	cmd() // let the fake capture the context

	for _, key := range []tea.KeyPressMsg{runeKey('q'), codeKey(tea.KeyDown), codeKey(tea.KeyEnter), codeKey(tea.KeyEsc)} {
		if got := press(t, m, key); got != nil {
			t.Errorf("key %q produced a command while an operation was running", key.String())
		}
	}
	if m.screen != accountsScreen {
		t.Errorf("screen = %v, want keys to be ignored while running", m.screen)
	}

	if quits(press(t, m, ctrlC())) {
		t.Fatal("ctrl+c quit while an operation was running")
	}
	if !m.running {
		t.Error("ctrl+c cleared the running state before the operation returned")
	}
	if err := fake.accountsCtx.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("operation context error = %v, want context.Canceled", err)
	}

	m.Update(operationMsg{kind: accountsOperation, err: context.Canceled})
	if m.running {
		t.Error("the model is still running after the cancelled operation returned")
	}
	if m.screen != accountsScreen {
		t.Errorf("screen after cancellation = %v, want returnTo (accountsScreen)", m.screen)
	}
	if body := m.View().Content; strings.Contains(strings.ToLower(body), "context canceled") {
		t.Errorf("view = %q, want cancellation not shown as a failure", body)
	}

	// A window resize is accepted while running.
	m.running = true
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if m.width != 100 || m.height != 40 {
		t.Errorf("size = %dx%d, want 100x40", m.width, m.height)
	}
}

func TestInitialRefreshRunsAsAnOperation(t *testing.T) {
	fake := &fakeService{data: app.AccountData{
		Accounts: []model.AccountView{account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)},
		States: []app.SyncState{
			{ItemID: "item-1", Institution: "Chase", LastStatus: "ok", LastSyncedAt: timePtr(time.Now())},
		},
	}}
	m := New(fake)

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no command")
	}
	if !m.running {
		t.Fatal("Init did not mark the model as running before the refresh ran")
	}
	if quits(press(t, m, runeKey('q'))) {
		t.Error("a key before the first refresh quit the program")
	}

	msg, ok := cmd().(operationMsg)
	if !ok {
		t.Fatalf("Init command returned %T, want operationMsg", msg)
	}
	if msg.kind != accountsOperation {
		t.Fatalf("operation kind = %v, want accountsOperation", msg.kind)
	}

	m.Update(msg)
	if m.running {
		t.Error("the model is still running after the refresh returned")
	}
	if len(m.accounts.rows) != 1 {
		t.Fatalf("stored rows = %d, want 1", len(m.accounts.rows))
	}
	if len(m.syncStates) != 1 {
		t.Fatalf("stored sync states = %d, want 1", len(m.syncStates))
	}
	if len(fake.accountsCalls) != 1 || fake.accountsCalls[0] != "" {
		t.Errorf("Accounts calls = %q, want one call with an empty item id", fake.accountsCalls)
	}
}

func TestProgressCallbackIsSafeWithoutProgramSend(t *testing.T) {
	m := New(&fakeService{})
	if m.send != nil {
		t.Fatal("New set a send function; unit tests need it nil")
	}
	m.report("Opening the browser") // must not panic

	var got []tea.Msg
	m.send = func(msg tea.Msg) { got = append(got, msg) }
	m.report("Link completed")
	if len(got) != 1 || got[0] != tea.Msg(progressMsg("Link completed")) {
		t.Fatalf("sent messages = %v, want one progressMsg", got)
	}

	m.running = true
	m.Update(progressMsg("Link completed"))
	if m.status != "Link completed" {
		t.Errorf("status = %q, want the progress line", m.status)
	}
	if body := m.View().Content; !strings.Contains(body, "Link completed") {
		t.Errorf("view = %q, want the progress line", body)
	}
}

func TestAccountRowKeepsNameAtNarrowWidths(t *testing.T) {
	view := account("acc-1", "Café Ünicode", "Everyday Checking", "1234", "Chase", 1234.56)

	wide := accountRow(view, true, 80)
	for _, want := range []string{"Café Ünicode", "••1234", "Chase", "USD", "1234.56", "NEW"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide row = %q, want it to contain %q", wide, want)
		}
	}

	for _, width := range []int{12, 16, 20, 30} {
		narrow := accountRow(view, true, width)
		if got := lipgloss.Width(narrow); got > width {
			t.Errorf("row at width %d measured %d cells", width, got)
		}
		if !strings.Contains(narrow, "Café Ünicode") {
			t.Errorf("row at width %d = %q, want the whole account name", width, narrow)
		}
	}

	if got := accountRow(view, false, 80); strings.Contains(got, "NEW") {
		t.Errorf("row = %q, want no NEW mark on an old account", got)
	}

	noName := model.AccountView{Account: model.Account{AccountID: "acc-9"}}
	if got := accountName(noName); got != "acc-9" {
		t.Errorf("accountName without a nickname or name = %q, want the account id", got)
	}
	onlyName := model.AccountView{Account: model.Account{AccountID: "acc-9", Name: "Checking"}}
	if got := accountName(onlyName); got != "Checking" {
		t.Errorf("accountName without a nickname = %q, want the provider name", got)
	}
}

func TestSyncStatusNeverCallsFailureASuccess(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-5 * time.Minute)
	older := now.Add(-3 * time.Hour)

	if got := syncStatus(nil, now); got != "Never synced" {
		t.Errorf("syncStatus of no states = %q, want %q", got, "Never synced")
	}

	allOK := []app.SyncState{
		{ItemID: "item-1", Institution: "Chase", LastStatus: "ok", LastSyncedAt: &older},
		{ItemID: "item-2", Institution: "Amex", LastStatus: "ok", LastSyncedAt: &recent},
	}
	success := syncStatus(allOK, now)
	if strings.Contains(strings.ToLower(success), "fail") {
		t.Errorf("syncStatus of all-ok states = %q, want no failure", success)
	}
	if !strings.Contains(success, "5 minutes ago") {
		t.Errorf("syncStatus of all-ok states = %q, want the newest success time", success)
	}

	// The newest state succeeded, but an older one failed. The status must not
	// report success.
	mixed := []app.SyncState{
		{ItemID: "item-1", Institution: "Chase", LastStatus: "ITEM_LOGIN_REQUIRED", LastSyncedAt: &older},
		{ItemID: "item-2", Institution: "Amex", LastStatus: "ok", LastSyncedAt: &recent},
	}
	failed := syncStatus(mixed, now)
	if !strings.Contains(failed, "Chase") {
		t.Errorf("syncStatus of a mixed set = %q, want the failed institution", failed)
	}
	if !strings.Contains(strings.ToLower(failed), "fail") {
		t.Errorf("syncStatus of a mixed set = %q, want it to report a failure", failed)
	}
	if strings.Contains(failed, "5 minutes ago") {
		t.Errorf("syncStatus of a mixed set = %q, want the failure time, not the newest success", failed)
	}

	neverRan := []app.SyncState{{ItemID: "item-1", Institution: "Chase", LastStatus: "ok"}}
	if got := syncStatus(neverRan, now); got != "Never synced" {
		t.Errorf("syncStatus of a state with no time = %q, want %q", got, "Never synced")
	}

	if got := displayError(app.ErrProductNotReady); got != "The bank is still preparing the data" {
		t.Errorf("displayError = %q, want the short bank message", got)
	}
	if got := displayError(errors.New("boom")); got != "boom" {
		t.Errorf("displayError = %q, want the raw message", got)
	}
}

func timePtr(t time.Time) *time.Time { return &t }
