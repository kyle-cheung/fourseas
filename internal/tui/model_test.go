package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

	// linkItem is what a successful Link returns, and linkErr replaces it.
	linkItem app.LinkedItem
	linkErr  error
	// linkDays holds the history length of every Link call, in order.
	linkDays []int
	// links holds every item a successful Link saved, as App.Link saves the
	// token before any sync runs.
	links map[string]bool

	// syncCalls holds the item id of every SyncItem call, in order.
	syncCalls []string
	// syncErrs is consumed one entry per SyncItem call. A nil entry, or an
	// empty list, is a success that returns syncAccounts.
	syncErrs     []error
	syncAccounts []model.AccountView

	// nicknameCalls holds the account id and the name of every SetNickname
	// call, in order.
	nicknameCalls [][2]string
	// nicknameErrs is consumed one entry per SetNickname call.
	nicknameErrs []error

	// syncAllCalls counts every SyncAll call, so a test can see that the
	// interface never syncs twice on its own.
	syncAllCalls   int
	syncAllResults []app.SyncResult
	syncAllErr     error

	// preview is what UnlinkPreview returns, and previewErr replaces it.
	preview    app.UnlinkData
	previewErr error
	// previewCalls holds the item id of every UnlinkPreview call, in order.
	previewCalls []string

	// unlinkCalls holds the item id of every Unlink call, in order.
	unlinkCalls  []string
	unlinkResult app.UnlinkResult
	unlinkErr    error
}

// takeError takes the first prepared error off a queue.
func takeError(queue *[]error) error {
	if len(*queue) == 0 {
		return nil
	}
	err := (*queue)[0]
	*queue = (*queue)[1:]
	return err
}

func (f *fakeService) Accounts(ctx context.Context, itemID string) (app.AccountData, error) {
	f.accountsCalls = append(f.accountsCalls, itemID)
	f.accountsCtx = ctx
	return f.data, f.accountsErr
}

func (f *fakeService) Link(_ context.Context, days int, _ app.Progress) (app.LinkedItem, error) {
	f.linkDays = append(f.linkDays, days)
	if f.linkErr != nil {
		return app.LinkedItem{}, f.linkErr
	}
	if f.links == nil {
		f.links = map[string]bool{}
	}
	f.links[f.linkItem.ItemID] = true
	return f.linkItem, nil
}

func (f *fakeService) SyncItem(_ context.Context, itemID string, _ app.Progress) ([]model.AccountView, error) {
	f.syncCalls = append(f.syncCalls, itemID)
	if err := takeError(&f.syncErrs); err != nil {
		return nil, err
	}
	return f.syncAccounts, nil
}

func (f *fakeService) SyncAll(context.Context, app.Progress) ([]app.SyncResult, error) {
	f.syncAllCalls++
	if f.syncAllErr != nil {
		return nil, f.syncAllErr
	}
	return f.syncAllResults, nil
}

func (f *fakeService) SetNickname(_ context.Context, accountID, nickname string) error {
	f.nicknameCalls = append(f.nicknameCalls, [2]string{accountID, nickname})
	return takeError(&f.nicknameErrs)
}

func (f *fakeService) UnlinkPreview(_ context.Context, itemID string) (app.UnlinkData, error) {
	f.previewCalls = append(f.previewCalls, itemID)
	if f.previewErr != nil {
		return app.UnlinkData{}, f.previewErr
	}
	return f.preview, nil
}

func (f *fakeService) Unlink(_ context.Context, itemID string, _ app.Progress) (app.UnlinkResult, error) {
	f.unlinkCalls = append(f.unlinkCalls, itemID)
	if f.unlinkErr != nil {
		return app.UnlinkResult{}, f.unlinkErr
	}
	return f.unlinkResult, nil
}

// content is the active screen as plain text. The interface writes styled
// lines, so a test reads the printable text with the escape sequences removed.
func content(m *Model) string { return ansi.Strip(m.View().Content) }

// hasRow says whether one line of a screen holds the label at the left and the
// value at the right. The cursor mark and the column padding are removed, so
// the check stays exact about the pair while the width of the columns changes.
func hasRow(body, label, value string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), strings.TrimSpace(cursorMark))
		if strings.Join(strings.Fields(line), " ") == label+" "+value {
			return true
		}
	}
	return false
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
	body := content(m)
	for _, want := range []string{"Accounts", "Add account", "Sync"} {
		if !strings.Contains(body, want) {
			t.Errorf("main view = %q, want it to contain %q", body, want)
		}
	}
	if !hasRow(body, "Accounts", "2 accounts") {
		t.Errorf("main view = %q, want the account count in the right column", body)
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
	list := content(m)
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

// promptLine is the line of the open prompt, as it is written and as plain
// text.
func promptLine(t *testing.T, m *Model) (string, string) {
	t.Helper()
	for _, line := range strings.Split(m.View().Content, "\n") {
		if plain := ansi.Strip(line); strings.Contains(plain, m.prompt.input.Prompt) {
			return line, plain
		}
	}
	t.Fatal("no prompt line on the screen")
	return "", ""
}

// caretPattern matches the caret of the text input: one cell of reverse video
// with a character inside it. A cut that leaves the escape sequence but takes
// the cell away leaves the user typing into a screen that does not change.
var caretPattern = regexp.MustCompile("\x1b\\[7[0-9;]*m[^\x1b]")

// A value longer than the terminal has to scroll under the caret. The input
// renders its whole value while it has no width, so the line was cut and both
// the newest characters and the caret went with the cut.
func TestPromptKeepsTheCaretOnAValueLongerThanTheTerminal(t *testing.T) {
	const narrow = 40
	value := "Everyday Checking at the " + strings.Repeat("very ", 6) + "long Bank of Nowhere"
	head, tail := value[:16], value[len(value)-16:]

	for _, tc := range []struct {
		name string
		open func(*Model)
	}{
		{"typed while the terminal is narrow", func(m *Model) {
			m.Update(tea.WindowSizeMsg{Width: narrow, Height: 24})
			m.openPrompt(nicknameScreen, "name", "")
			for _, r := range value {
				m.Update(runeKey(r))
			}
		}},
		{"the terminal narrows while the prompt is open", func(m *Model) {
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			m.openPrompt(nicknameScreen, "name", value)
			m.Update(tea.WindowSizeMsg{Width: narrow, Height: 24})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(&fakeService{})
			tc.open(m)

			if got := m.prompt.input.Value(); got != value {
				t.Fatalf("prompt value = %q, want the whole value", got)
			}
			raw, plain := promptLine(t, m)
			if !strings.Contains(plain, tail) {
				t.Errorf("prompt line = %q, want the newest characters %q", plain, tail)
			}
			if strings.Contains(plain, head) {
				t.Errorf("prompt line = %q, want it scrolled past the start %q", plain, head)
			}
			if got := lipgloss.Width(plain); got > narrow {
				t.Errorf("prompt line is %d columns wide, want at most %d", got, narrow)
			}
			if !caretPattern.MatchString(raw) {
				t.Errorf("prompt line = %q, want the caret cell kept", raw)
			}
		})
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
	if body := content(m); strings.Contains(strings.ToLower(body), "context canceled") {
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
	if body := content(m); !strings.Contains(body, "Link completed") {
		t.Errorf("view = %q, want the progress line", body)
	}
}

func TestAccountRowKeepsNameAtNarrowWidths(t *testing.T) {
	view := account("acc-1", "Café Ünicode", "Everyday Checking", "1234", "Chase", 1234.56)

	wide := accountRow(view, true, itemStyle, 80)
	for _, want := range []string{"Café Ünicode", "••1234", "Chase", "USD", "1234.56", "NEW"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide row = %q, want it to contain %q", wide, want)
		}
	}

	for _, width := range []int{12, 16, 20, 30} {
		narrow := accountRow(view, true, itemStyle, width)
		if got := lipgloss.Width(narrow); got > width {
			t.Errorf("row at width %d measured %d cells", width, got)
		}
		if !strings.Contains(narrow, "Café Ünicode") {
			t.Errorf("row at width %d = %q, want the whole account name", width, narrow)
		}
	}

	if got := accountRow(view, false, itemStyle, 80); strings.Contains(got, "NEW") {
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

	if got, _ := syncStatus(nil, now); got != noneMark+" Not synced" {
		t.Errorf("syncStatus of no states = %q, want %q", got, noneMark+" Not synced")
	}

	allOK := []app.SyncState{
		{ItemID: "item-1", Institution: "Chase", LastStatus: "ok", LastSyncedAt: &older},
		{ItemID: "item-2", Institution: "Amex", LastStatus: "ok", LastSyncedAt: &recent},
	}
	success, _ := syncStatus(allOK, now)
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
	failed, _ := syncStatus(mixed, now)
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
	if got, _ := syncStatus(neverRan, now); got != noneMark+" Not synced" {
		t.Errorf("syncStatus of a state with no time = %q, want %q", got, noneMark+" Not synced")
	}

	if got := displayError(app.ErrProductNotReady); got != "The bank is still preparing the data" {
		t.Errorf("displayError = %q, want the short bank message", got)
	}
	if got := displayError(errors.New("boom")); got != "boom" {
		t.Errorf("displayError = %q, want the raw message", got)
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// runOperation executes one operation command, applies its result, and returns
// whatever command the result started next.
func runOperation(t *testing.T, m *Model, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected an operation command, got none")
	}
	msg := cmd()
	if _, ok := msg.(operationMsg); !ok {
		t.Fatalf("command returned %T, want operationMsg", msg)
	}
	_, next := m.Update(msg)
	return next
}

// typeText sends one key press per character.
func typeText(t *testing.T, m *Model, text string) {
	t.Helper()
	for _, r := range text {
		press(t, m, runeKey(r))
	}
}

// openHistory walks the main menu to the history length choice.
func openHistory(t *testing.T, m *Model) {
	t.Helper()
	press(t, m, codeKey(tea.KeyDown))
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != historyScreen {
		t.Fatalf("screen after choosing Add account = %v, want historyScreen", m.screen)
	}
}

// addUntilFirstNickname runs the add flow from the main menu to the first
// nickname prompt.
func addUntilFirstNickname(t *testing.T, m *Model) {
	t.Helper()
	openHistory(t, m)
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Link
	runOperation(t, m, cmd)                                       // SyncItem
	if m.screen != nicknameScreen {
		t.Fatalf("screen after the first sync = %v, want nicknameScreen", m.screen)
	}
}

func TestAddFlowLinksSyncsNamesAndMarksNewAccounts(t *testing.T) {
	first := account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)
	second := account("acc-2", "", "Sapphire", "9876", "Chase", 20)
	fake := &fakeService{
		data:         app.AccountData{Accounts: []model.AccountView{first, second}},
		linkItem:     app.LinkedItem{ItemID: "item-new", Institution: "Chase"},
		syncAccounts: []model.AccountView{first, second},
	}
	m := ready(t, fake)

	openHistory(t, m)
	choices := content(m)
	for _, want := range []string{"730 days", "365 days", "90 days", "Custom"} {
		if !strings.Contains(choices, want) {
			t.Errorf("history view = %q, want it to contain %q", choices, want)
		}
	}
	if m.history.cursor != 0 {
		t.Errorf("history cursor = %d, want the longest history as the default", m.history.cursor)
	}

	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Link
	if len(fake.linkDays) != 1 || fake.linkDays[0] != 730 {
		t.Fatalf("Link days = %v, want one call for 730 days", fake.linkDays)
	}
	if m.linked.ItemID != "item-new" || m.linked.Institution != "Chase" {
		t.Fatalf("linked item = %+v, want the returned metadata", m.linked)
	}

	cmd = runOperation(t, m, cmd) // SyncItem
	if len(fake.syncCalls) != 1 || fake.syncCalls[0] != "item-new" {
		t.Fatalf("SyncItem calls = %v, want one call for item-new", fake.syncCalls)
	}
	if m.screen != nicknameScreen || m.nicknameIndex != 0 {
		t.Fatalf("screen = %v, index = %d, want the first nickname prompt", m.screen, m.nicknameIndex)
	}

	typeText(t, m, "Travel card")
	cmd = runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // SetNickname
	if m.screen != nicknameScreen || m.nicknameIndex != 1 {
		t.Fatalf("screen = %v, index = %d, want the second nickname prompt", m.screen, m.nicknameIndex)
	}
	if got := m.prompt.input.Value(); got != "" {
		t.Errorf("second prompt value = %q, want an empty prompt", got)
	}

	cmd = press(t, m, codeKey(tea.KeyEsc)) // skip the second name
	runOperation(t, m, cmd)                // the closing account refresh

	want := [][2]string{{"acc-1", "Travel card"}}
	if len(fake.nicknameCalls) != 1 || fake.nicknameCalls[0] != want[0] {
		t.Fatalf("SetNickname calls = %v, want %v", fake.nicknameCalls, want)
	}
	if m.screen != mainScreen {
		t.Fatalf("screen after the last name = %v, want mainScreen", m.screen)
	}
	for _, id := range []string{"acc-1", "acc-2"} {
		if !m.accounts.newItems[id] {
			t.Errorf("account %q is not marked as new", id)
		}
	}

	press(t, m, codeKey(tea.KeyUp)) // back to the account list choice
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != accountsScreen {
		t.Fatalf("screen after opening the account list = %v, want accountsScreen", m.screen)
	}
	if got := strings.Count(content(m), "NEW"); got != 2 {
		t.Errorf("account list has %d NEW marks, want 2:\n%s", got, content(m))
	}
}

func TestCustomHistoryKeepsAnUnusableValueOnThePrompt(t *testing.T) {
	fake := &fakeService{linkItem: app.LinkedItem{ItemID: "item-new", Institution: "Chase"}}
	m := ready(t, fake)

	openHistory(t, m)
	for range historyDays { // move to the free choice
		press(t, m, codeKey(tea.KeyDown))
	}
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != customDaysScreen {
		t.Fatalf("screen after choosing Custom = %v, want customDaysScreen", m.screen)
	}

	for _, value := range []string{"ten", strconv.Itoa(app.MinLinkDays - 1), strconv.Itoa(app.MaxLinkDays + 1)} {
		m.prompt.input.SetValue(value)
		if cmd := press(t, m, codeKey(tea.KeyEnter)); cmd != nil {
			t.Fatalf("value %q started an operation", value)
		}
		if m.screen != customDaysScreen {
			t.Fatalf("screen after the value %q = %v, want the prompt to stay open", value, m.screen)
		}
		if m.prompt.err == nil {
			t.Fatalf("value %q left no reason on the prompt", value)
		}
		if body := content(m); !strings.Contains(body, errHistoryRange.Error()) {
			t.Errorf("prompt view = %q, want %q", body, errHistoryRange.Error())
		}
	}
	if len(fake.linkDays) != 0 {
		t.Fatalf("Link days = %v, want no link for an unusable value", fake.linkDays)
	}

	m.prompt.input.SetValue(" 120 ")
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	if len(fake.linkDays) != 1 || fake.linkDays[0] != 120 {
		t.Fatalf("Link days = %v, want one call for 120 days", fake.linkDays)
	}
}

func TestAddFlowEscSkipsNickname(t *testing.T) {
	only := account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)
	fake := &fakeService{
		data:         app.AccountData{Accounts: []model.AccountView{only}},
		linkItem:     app.LinkedItem{ItemID: "item-new", Institution: "Chase"},
		syncAccounts: []model.AccountView{only},
	}
	m := ready(t, fake)
	addUntilFirstNickname(t, m)

	typeText(t, m, "Never saved")
	runOperation(t, m, press(t, m, codeKey(tea.KeyEsc)))

	if len(fake.nicknameCalls) != 0 {
		t.Errorf("SetNickname calls = %v, want none after esc", fake.nicknameCalls)
	}
	if m.screen != mainScreen {
		t.Fatalf("screen after skipping the only name = %v, want mainScreen", m.screen)
	}
	if !m.accounts.newItems["acc-1"] {
		t.Error("a skipped account is not marked as new")
	}
}

func TestAddFlowSyncFailureKeepsLinkedItemForRetry(t *testing.T) {
	only := account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)
	fake := &fakeService{
		data:         app.AccountData{Accounts: []model.AccountView{only}},
		linkItem:     app.LinkedItem{ItemID: "item-new", Institution: "Chase"},
		syncErrs:     []error{fmt.Errorf("plaid: %w", app.ErrProductNotReady)},
		syncAccounts: []model.AccountView{only},
	}
	m := ready(t, fake)

	openHistory(t, m)
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Link
	cmd = runOperation(t, m, cmd)                                 // the failing SyncItem
	if cmd != nil {
		t.Fatal("the failed sync started another command; the interface must not retry on its own")
	}
	if m.screen != recoveryScreen {
		t.Fatalf("screen after a failed sync = %v, want recoveryScreen", m.screen)
	}

	body := content(m)
	for _, want := range []string{"Retry", "Main", "The bank is still preparing the data"} {
		if !strings.Contains(body, want) {
			t.Errorf("recovery view = %q, want it to contain %q", body, want)
		}
	}
	if m.linked.ItemID != "item-new" {
		t.Errorf("linked item = %+v, want item-new kept for the retry", m.linked)
	}
	if !fake.links["item-new"] {
		t.Error("the fake no longer holds item-new as a saved link")
	}
	if len(fake.syncCalls) != 1 {
		t.Fatalf("SyncItem calls = %v, want no automatic retry", fake.syncCalls)
	}

	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry
	if len(fake.syncCalls) != 2 || fake.syncCalls[1] != "item-new" {
		t.Fatalf("SyncItem calls = %v, want a second call for item-new", fake.syncCalls)
	}
	if m.screen != nicknameScreen {
		t.Fatalf("screen after a successful retry = %v, want nicknameScreen", m.screen)
	}
}

func TestRetryLeavesTheRecoveryScreenBehind(t *testing.T) {
	fake := &fakeService{linkErr: errors.New("plaid is unavailable")}
	m := ready(t, fake)

	openHistory(t, m)
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // the failing Link
	if m.screen != recoveryScreen {
		t.Fatalf("screen after a failed link = %v, want recoveryScreen", m.screen)
	}

	cmd := press(t, m, codeKey(tea.KeyEnter)) // Retry
	if cmd == nil {
		t.Fatal("Retry started no operation")
	}
	if m.screen != historyScreen {
		t.Fatalf("screen while the retry runs = %v, want historyScreen", m.screen)
	}
	if body := content(m); strings.Contains(body, "Something went wrong") {
		t.Errorf("view while the retry runs = %q, want the retried screen, not the cleared failure", body)
	}

	// The user gives up on the retry.
	fake.linkErr = context.Canceled
	press(t, m, ctrlC())
	if !m.running {
		t.Fatal("ctrl+c cleared the running state before the retry returned")
	}
	runOperation(t, m, cmd)

	if m.screen != historyScreen {
		t.Fatalf("screen after a cancelled retry = %v, want historyScreen", m.screen)
	}
	if m.linked != (app.LinkedItem{}) {
		t.Errorf("linked item = %+v, want it cleared", m.linked)
	}
	if body := content(m); strings.Contains(body, "Something went wrong") {
		t.Errorf("view after a cancelled retry = %q, want the history choice", body)
	}
	if len(fake.linkDays) != 2 {
		t.Errorf("Link calls = %v, want the first call and the retry", fake.linkDays)
	}
}

func TestLinkCancellationReturnsToHistoryChoice(t *testing.T) {
	fake := &fakeService{
		linkItem: app.LinkedItem{ItemID: "item-new", Institution: "Chase"},
		linkErr:  context.Canceled,
	}
	m := ready(t, fake)

	openHistory(t, m)
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	if cmd != nil {
		t.Fatal("a cancelled link started another command")
	}
	if m.screen != historyScreen {
		t.Fatalf("screen after a cancelled link = %v, want historyScreen", m.screen)
	}
	if m.linked != (app.LinkedItem{}) {
		t.Errorf("linked item = %+v, want it cleared", m.linked)
	}
	if body := content(m); strings.Contains(strings.ToLower(body), "went wrong") {
		t.Errorf("view = %q, want cancellation not shown as a failure", body)
	}
}

func TestNicknameFailureKeepsPromptOpen(t *testing.T) {
	only := account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)
	fake := &fakeService{
		data:         app.AccountData{Accounts: []model.AccountView{only}},
		linkItem:     app.LinkedItem{ItemID: "item-new", Institution: "Chase"},
		syncAccounts: []model.AccountView{only},
		nicknameErrs: []error{errors.New("the account is unknown")},
	}
	m := ready(t, fake)
	addUntilFirstNickname(t, m)

	typeText(t, m, "Travel card")
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))

	if m.screen != nicknameScreen {
		t.Fatalf("screen after a failed name = %v, want the prompt to stay open", m.screen)
	}
	if m.nicknameIndex != 0 {
		t.Errorf("index after a failed name = %d, want the same account", m.nicknameIndex)
	}
	if got := m.prompt.input.Value(); got != "Travel card" {
		t.Errorf("prompt value = %q, want the typed text kept", got)
	}
	if !m.prompt.input.Focused() {
		t.Error("the prompt lost focus after a failed name")
	}
	if body := content(m); !strings.Contains(body, "the account is unknown") {
		t.Errorf("view = %q, want the failure under the prompt", body)
	}

	// A second attempt succeeds and moves on.
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	want := [][2]string{{"acc-1", "Travel card"}, {"acc-1", "Travel card"}}
	if len(fake.nicknameCalls) != 2 || fake.nicknameCalls[1] != want[1] {
		t.Fatalf("SetNickname calls = %v, want %v", fake.nicknameCalls, want)
	}
}

// openDetail walks the main menu to the detail screen of one account row.
func openDetail(t *testing.T, m *Model, row int) {
	t.Helper()
	press(t, m, codeKey(tea.KeyEnter)) // Accounts
	for i := 0; i < row; i++ {
		press(t, m, codeKey(tea.KeyDown))
	}
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != detailScreen {
		t.Fatalf("screen after opening a row = %v, want detailScreen", m.screen)
	}
}

// openUnlink walks from the main menu to the unlink confirmation of the first
// account.
func openUnlink(t *testing.T, m *Model) {
	t.Helper()
	openDetail(t, m, 0)
	press(t, m, codeKey(tea.KeyDown)) // Unlink institution
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	if m.screen != unlinkScreen {
		t.Fatalf("screen after the preview = %v, want unlinkScreen", m.screen)
	}
}

// openSync walks the main menu to the sync action and runs it.
func openSync(t *testing.T, m *Model) tea.Cmd {
	t.Helper()
	press(t, m, codeKey(tea.KeyDown))
	press(t, m, codeKey(tea.KeyDown))
	if m.main.cursor != choiceSync {
		t.Fatalf("main cursor = %d, want the sync choice", m.main.cursor)
	}
	return runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
}

func TestDetailRenameSetsAndClearsNickname(t *testing.T) {
	fake := &fakeService{data: app.AccountData{
		Accounts: []model.AccountView{account("acc-1", "Travel card", "Sapphire", "9876", "Chase", 20)},
	}}
	m := ready(t, fake)
	openDetail(t, m, 0)

	body := content(m)
	for _, want := range []string{"Rename", "Unlink institution"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail view = %q, want it to contain %q", body, want)
		}
	}

	press(t, m, codeKey(tea.KeyEnter)) // Rename
	if m.screen != nicknameScreen {
		t.Fatalf("screen after choosing Rename = %v, want nicknameScreen", m.screen)
	}
	if got := m.prompt.input.Value(); got != "Travel card" {
		t.Errorf("prompt value = %q, want the current nickname", got)
	}

	// A refused name keeps the prompt open with the typed text.
	m.prompt.input.SetValue("Weekend card")
	fake.nicknameErrs = []error{errors.New("the account is unknown")}
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	if m.screen != nicknameScreen {
		t.Fatalf("screen after a refused rename = %v, want the prompt to stay open", m.screen)
	}
	if got := m.prompt.input.Value(); got != "Weekend card" {
		t.Errorf("prompt value after a refused rename = %q, want the typed text kept", got)
	}

	fake.data = app.AccountData{
		Accounts: []model.AccountView{account("acc-1", "Weekend card", "Sapphire", "9876", "Chase", 20)},
	}
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // SetNickname
	runOperation(t, m, cmd)                                       // the account refresh
	if m.screen != detailScreen {
		t.Fatalf("screen after a saved rename = %v, want detailScreen", m.screen)
	}
	if m.detail.account.AccountID != "acc-1" || m.detail.account.Nickname != "Weekend card" {
		t.Fatalf("detail account = %+v, want the refreshed row of acc-1", m.detail.account)
	}
	if got := content(m); !strings.Contains(got, "Weekend card") {
		t.Errorf("detail view = %q, want the new nickname", got)
	}

	// A blank value clears the nickname.
	press(t, m, codeKey(tea.KeyEnter)) // Rename again
	if got := m.prompt.input.Value(); got != "Weekend card" {
		t.Errorf("prompt value = %q, want the nickname it starts from", got)
	}
	m.prompt.input.SetValue("")
	fake.data = app.AccountData{
		Accounts: []model.AccountView{account("acc-1", "", "Sapphire", "9876", "Chase", 20)},
	}
	cmd = runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	runOperation(t, m, cmd)

	want := [][2]string{{"acc-1", "Weekend card"}, {"acc-1", "Weekend card"}, {"acc-1", ""}}
	if len(fake.nicknameCalls) != len(want) {
		t.Fatalf("SetNickname calls = %v, want %v", fake.nicknameCalls, want)
	}
	for i, call := range want {
		if fake.nicknameCalls[i] != call {
			t.Fatalf("SetNickname calls = %v, want %v", fake.nicknameCalls, want)
		}
	}
	if m.screen != detailScreen || m.detail.account.Nickname != "" {
		t.Fatalf("screen = %v, detail account = %+v, want the cleared name on the detail screen",
			m.screen, m.detail.account)
	}
}

// unlinkFake builds a service whose item-1 preview holds two accounts and a
// nonzero count of every kind of row.
func unlinkFake() *fakeService {
	first := account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)
	second := account("acc-2", "", "Sapphire", "9876", "Chase", 20)
	return &fakeService{
		data: app.AccountData{Accounts: []model.AccountView{first, second}},
		preview: app.UnlinkData{
			ItemID:      "item-1",
			Institution: "Chase",
			Accounts:    []model.AccountView{first, second},
			Rows:        app.RowCounts{Transactions: 412, Accounts: 3, SyncState: 7, Institutions: 5},
		},
	}
}

func TestUnlinkPreviewListsEveryAffectedAccountAndRowCount(t *testing.T) {
	fake := unlinkFake()
	m := ready(t, fake)
	openUnlink(t, m)

	if len(fake.previewCalls) != 1 || fake.previewCalls[0] != "item-1" {
		t.Fatalf("UnlinkPreview calls = %v, want one call for item-1", fake.previewCalls)
	}
	body := content(m)
	for _, want := range []string{"Chase", "Everyday Checking", "Sapphire", "Cancel", "Unlink"} {
		if !strings.Contains(body, want) {
			t.Errorf("unlink view = %q, want it to contain %q", body, want)
		}
	}
	for _, want := range [][2]string{
		{"Transactions", "412"}, {"Accounts", "3"}, {"Sync state", "7"}, {"Institutions", "5"},
	} {
		if !hasRow(body, want[0], want[1]) {
			t.Errorf("unlink view = %q, want the row %q %q", body, want[0], want[1])
		}
	}
	if m.unlink.cursor != 0 {
		t.Errorf("unlink cursor = %d, want Cancel as the default", m.unlink.cursor)
	}
	if len(fake.unlinkCalls) != 0 {
		t.Errorf("Unlink calls = %v, want none before the user confirms", fake.unlinkCalls)
	}
}

func TestUnlinkEscCancelsWithoutChanges(t *testing.T) {
	fake := unlinkFake()
	m := ready(t, fake)

	openUnlink(t, m)
	press(t, m, codeKey(tea.KeyEsc))
	if m.screen != accountsScreen {
		t.Fatalf("screen after esc = %v, want accountsScreen", m.screen)
	}

	press(t, m, codeKey(tea.KeyEsc)) // back to the main menu
	openUnlink(t, m)
	if cmd := press(t, m, codeKey(tea.KeyEnter)); cmd != nil { // Cancel
		t.Fatal("Cancel started an operation")
	}
	if m.screen != accountsScreen {
		t.Fatalf("screen after Cancel = %v, want accountsScreen", m.screen)
	}

	if len(fake.unlinkCalls) != 0 {
		t.Errorf("Unlink calls = %v, want none after a cancellation", fake.unlinkCalls)
	}
	if len(m.accounts.rows) != 2 {
		t.Errorf("stored rows = %d, want both accounts kept", len(m.accounts.rows))
	}
}

func TestUnlinkSuccessRefreshesAccounts(t *testing.T) {
	fake := unlinkFake()
	fake.unlinkResult = app.UnlinkResult{Rows: app.RowCounts{Transactions: 412, Accounts: 3}}
	m := ready(t, fake)
	m.accounts.newItems["acc-1"] = true
	m.accounts.newItems["acc-2"] = true

	openUnlink(t, m)
	press(t, m, codeKey(tea.KeyDown)) // Unlink
	if m.unlink.cursor != 1 {
		t.Fatalf("unlink cursor = %d, want the Unlink action", m.unlink.cursor)
	}

	remaining := account("acc-3", "", "Gold card", "4321", "Amex", 30)
	fake.data = app.AccountData{Accounts: []model.AccountView{remaining}}
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Unlink
	if len(fake.unlinkCalls) != 1 || fake.unlinkCalls[0] != "item-1" {
		t.Fatalf("Unlink calls = %v, want one call for item-1", fake.unlinkCalls)
	}
	runOperation(t, m, cmd) // the account refresh

	if m.screen != accountsScreen {
		t.Fatalf("screen after a removal = %v, want accountsScreen", m.screen)
	}
	for _, id := range []string{"acc-1", "acc-2"} {
		if m.accounts.newItems[id] {
			t.Errorf("account %q is still marked as new after its item was removed", id)
		}
	}
	if len(m.accounts.rows) != 1 || m.accounts.rows[0].AccountID != "acc-3" {
		t.Fatalf("stored rows = %+v, want only the remaining account", m.accounts.rows)
	}
	if body := content(m); strings.Contains(body, "Everyday Checking") {
		t.Errorf("account list = %q, want the removed accounts gone", body)
	}
}

// Ctrl+c during a sync is a deliberate stop. Showing "Something went wrong"
// with the context error is the failure screen the cancellation branch exists
// to prevent.
func TestSyncAllCancellationShowsNoFailure(t *testing.T) {
	fake := &fakeService{
		data:       app.AccountData{Accounts: []model.AccountView{account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)}},
		syncAllErr: context.Canceled,
	}
	m := ready(t, fake)

	if cmd := openSync(t, m); cmd != nil {
		t.Fatal("a cancelled sync started another command")
	}
	if m.screen != mainScreen {
		t.Fatalf("screen after a cancelled sync = %v, want the main menu it started from", m.screen)
	}
	body := content(m)
	for _, unwanted := range []string{"Something went wrong", "context canceled"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("view = %q, want it not to contain %q", body, unwanted)
		}
	}
}

// A removal changes what Plaid bills before it changes anything here, so its
// error must be shown even when the user cancelled. Swallowing it would leave
// the item gone at Plaid, the local data here, and the user never told.
func TestUnlinkErrorIsShownEvenWhenCancelled(t *testing.T) {
	fake := unlinkFake()
	fake.unlinkErr = fmt.Errorf("the item is gone at Plaid but the local data is not: %w", context.Canceled)
	m := ready(t, fake)

	openUnlink(t, m)
	press(t, m, codeKey(tea.KeyDown)) // Unlink
	if cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))); cmd != nil {
		t.Fatal("a failed removal started another command")
	}

	if m.screen != recoveryScreen {
		t.Fatalf("screen after a cancelled removal = %v, want recoveryScreen", m.screen)
	}
	if body := content(m); !strings.Contains(body, "the item is gone at Plaid") {
		t.Errorf("view = %q, want the removal failure shown", body)
	}
}

// The first sync of a just-linked item runs from the history menu. Landing
// back there hides the finished link and turns enter into a second billed
// link at the same bank.
func TestCancelledFirstSyncLandsOnTheAccountsScreen(t *testing.T) {
	fake := &fakeService{
		data:     app.AccountData{Accounts: []model.AccountView{account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)}},
		linkItem: app.LinkedItem{ItemID: "item-new", Institution: "Bank of Nowhere"},
		syncErrs: []error{context.Canceled},
	}
	m := ready(t, fake)

	openHistory(t, m)
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Link
	if cmd = runOperation(t, m, cmd); cmd != nil {                // the cancelled first sync
		t.Fatal("a cancelled first sync started another command")
	}

	if m.screen == historyScreen {
		t.Fatal("a cancelled first sync left the user on the history menu, where enter links again")
	}
	if m.screen != accountsScreen {
		t.Fatalf("screen after a cancelled first sync = %v, want accountsScreen", m.screen)
	}
	body := content(m)
	if !strings.Contains(body, "Bank of Nowhere") {
		t.Errorf("view = %q, want it to name the institution that was linked", body)
	}
	if strings.Contains(body, "Something went wrong") {
		t.Errorf("view = %q, want cancellation not shown as a failure", body)
	}
	if len(fake.linkDays) != 1 {
		t.Errorf("Link calls = %v, want only the one the user asked for", fake.linkDays)
	}
}

// Every line is cut to the terminal width. The status line and the title were
// the only two that were not, and a progress line carrying a whole provider
// error is the longest string the interface shows.
func TestNarrowWidthCutsTheStatusLineAndTheTitle(t *testing.T) {
	// The width is the narrowest that still holds the fixed key help line, so
	// the test is about the two lines that grow with the stored data.
	const narrow = 50
	long := strings.Repeat("Institution of Considerable Length ", 4)
	fake := &fakeService{data: app.AccountData{
		Accounts: []model.AccountView{account("acc-1", long, "Everyday Checking", "1234", long, 10)},
	}}
	m := ready(t, fake)
	m.width = narrow

	openDetail(t, m, 0) // the title is the long nickname
	m.status = strings.Repeat("x", 28) + " failed: " + long

	for _, line := range strings.Split(content(m), "\n") {
		if got := lipgloss.Width(line); got > narrow {
			t.Errorf("line %q is %d columns wide, want at most %d", line, got, narrow)
		}
	}
}

func TestSyncAllShowsEveryResultAndRefreshesStatus(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	synced := now.Add(-5 * time.Minute)
	first := account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)
	fake := &fakeService{
		data: app.AccountData{Accounts: []model.AccountView{first}},
		syncAllResults: []app.SyncResult{
			{ItemID: "item-1", Label: "Chase", Accounts: []model.AccountView{first}},
			{ItemID: "item-2", Label: "item-2", Skipped: true},
		},
	}
	m := ready(t, fake)
	m.now = func() time.Time { return now }

	fake.data = app.AccountData{
		Accounts: []model.AccountView{first, account("acc-2", "", "Sapphire", "9876", "Chase", 20)},
		States: []app.SyncState{
			{ItemID: "item-1", Institution: "Chase", LastStatus: "ok", LastSyncedAt: &synced},
		},
	}
	runOperation(t, m, openSync(t, m)) // SyncAll, then the account refresh

	if fake.syncAllCalls != 1 {
		t.Fatalf("SyncAll calls = %d, want exactly one", fake.syncAllCalls)
	}
	if m.screen != mainScreen {
		t.Fatalf("screen after a sync = %v, want mainScreen", m.screen)
	}
	body := content(m)
	for _, want := range []string{"Chase", "item-2", "skipped", okMark + " Last sync: 5 minutes ago"} {
		if !strings.Contains(body, want) {
			t.Errorf("main view = %q, want it to contain %q", body, want)
		}
	}
	if !hasRow(body, "Accounts", "2 accounts") {
		t.Errorf("main view = %q, want the refreshed account count", body)
	}
}

// The add flow starts on the main menu, so it has to confirm its outcome
// there. Landing on the account list left the user to work out whether the
// link had worked.
func TestAddFlowEndsOnTheMainMenuWithTheOutcome(t *testing.T) {
	only := account("acc-1", "", "Blue Cash", "1234", "American Express", 10)
	fake := &fakeService{
		data:         app.AccountData{Accounts: []model.AccountView{only}},
		linkItem:     app.LinkedItem{ItemID: "item-new", Institution: "American Express"},
		syncAccounts: []model.AccountView{only},
	}
	m := ready(t, fake)
	addUntilFirstNickname(t, m)

	typeText(t, m, "Everyday card")
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // SetNickname
	runOperation(t, m, cmd)                                       // the closing account refresh

	if m.screen != mainScreen {
		t.Fatalf("screen after the add flow = %v, want mainScreen", m.screen)
	}
	body := content(m)
	if !strings.Contains(body, successMark+" Added American Express") {
		t.Errorf("main view = %q, want the outcome of the add flow", body)
	}
	if !hasRow(body, "Accounts", "1 account") {
		t.Errorf("main view = %q, want the refreshed account count", body)
	}
	if !aboveTheFooterRule(body, successMark+" Added American Express") {
		t.Errorf("main view = %q, want the outcome above the rule of the footer", body)
	}

	// The outcome stays until the user leaves the screen.
	press(t, m, codeKey(tea.KeyDown))
	if !strings.Contains(content(m), successMark+" Added American Express") {
		t.Error("the outcome was dropped by a cursor move on the same screen")
	}
	press(t, m, codeKey(tea.KeyUp))
	press(t, m, codeKey(tea.KeyUp))
	press(t, m, codeKey(tea.KeyEnter)) // open the account list
	if m.screen != accountsScreen {
		t.Fatalf("screen = %v, want accountsScreen", m.screen)
	}
	press(t, m, codeKey(tea.KeyEsc))
	if body := content(m); strings.Contains(body, "Added American Express") {
		t.Errorf("main view = %q, want the outcome dropped by the navigation", body)
	}
}

// aboveTheFooterRule says whether one line is written before the faint rule
// that closes the screen.
func aboveTheFooterRule(body, want string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, want) {
			return true
		}
		if strings.Contains(line, strings.Repeat(dividerRune, 3)) {
			return false
		}
	}
	return false
}

// A running operation used to change nothing on the screen: the interface
// looked frozen until the result arrived.
func TestRunningOperationShowsTheSpinnerAndStopsTickingWhenItEnds(t *testing.T) {
	first := account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)
	fake := &fakeService{
		data:           app.AccountData{Accounts: []model.AccountView{first}},
		syncAllResults: []app.SyncResult{{ItemID: "item-1", Label: "Chase", Accounts: []model.AccountView{first}}},
	}
	m := ready(t, fake)

	press(t, m, codeKey(tea.KeyDown))
	press(t, m, codeKey(tea.KeyDown))
	cmd := press(t, m, codeKey(tea.KeyEnter)) // Sync, without running the operation yet
	if !m.running {
		t.Fatal("the sync did not mark the model as running")
	}

	frame := m.spinner.View()
	body := content(m)
	if !strings.Contains(body, "Syncing") {
		t.Errorf("view while syncing = %q, want the label of the running operation", body)
	}
	if !strings.Contains(body, ansi.Strip(frame)) {
		t.Errorf("view while syncing = %q, want the spinner frame %q", body, ansi.Strip(frame))
	}

	// A frame advances the spinner and asks for the next one.
	next := apply(m, spinnerTick(m))
	if m.spinner.View() == frame {
		t.Error("the spinner did not advance on a tick")
	}
	if next == nil {
		t.Fatal("a tick while the operation runs asked for no next frame")
	}

	// A progress line replaces the label and keeps the spinner.
	m.Update(progressMsg("Fetching Chase"))
	if body := content(m); !strings.Contains(body, "Fetching Chase") ||
		!strings.Contains(body, ansi.Strip(m.spinner.View())) {
		t.Errorf("view = %q, want the spinner beside the progress line", body)
	}

	cmd = runOperation(t, m, cmd) // SyncAll returns
	runOperation(t, m, cmd)       // the closing account refresh
	if m.running {
		t.Fatal("the model is still running after the sync returned")
	}
	if got := apply(m, spinnerTick(m)); got != nil {
		t.Error("a tick after the operation ended asked for another frame; the chain never stops")
	}
	if body := content(m); strings.Contains(body, "Syncing") {
		t.Errorf("view after the sync = %q, want the running indicator gone", body)
	}
}

// spinnerTick is one frame message for the spinner of this model, as the
// command chain would send it. Tests drive Update directly, so no program
// runs.
func spinnerTick(m *Model) spinner.TickMsg {
	return spinner.TickMsg{Time: time.Now(), ID: m.spinner.ID()}
}

// apply gives one message that is not a key press to the model and returns the
// command it asked for.
func apply(m *Model, msg tea.Msg) tea.Cmd {
	_, cmd := m.Update(msg)
	return cmd
}

// A run where every institution returned data is one outcome, not a table the
// user has to read.
func TestSyncAllSuccessNamesEveryInstitutionItSynced(t *testing.T) {
	first := account("acc-1", "", "Blue Cash", "1234", "American Express", 10)
	second := account("acc-2", "", "Everyday Checking", "9876", "Chase", 20)
	fake := &fakeService{
		data: app.AccountData{Accounts: []model.AccountView{first, second}},
		syncAllResults: []app.SyncResult{
			{ItemID: "item-1", Label: "American Express", Accounts: []model.AccountView{first}},
			{ItemID: "item-2", Label: "Chase", Accounts: []model.AccountView{second}},
		},
	}
	m := ready(t, fake)

	runOperation(t, m, openSync(t, m)) // SyncAll, then the account refresh
	if m.screen != mainScreen {
		t.Fatalf("screen after a clean sync = %v, want mainScreen", m.screen)
	}
	body := content(m)
	if !strings.Contains(body, successMark+" Synced American Express, Chase") {
		t.Errorf("main view = %q, want the outcome naming both institutions", body)
	}
	if strings.Contains(body, "Last sync") {
		t.Errorf("main view = %q, want one outcome and not a second summary of the same run", body)
	}
	if !aboveTheFooterRule(body, successMark+" Synced") {
		t.Errorf("main view = %q, want the outcome above the rule of the footer", body)
	}

	// A run that fails reports the failure, not an outcome.
	fake.syncAllResults = []app.SyncResult{
		{ItemID: "item-1", Label: "American Express", Accounts: []model.AccountView{first}},
		{ItemID: "item-2", Label: "Chase", Err: errors.New("plaid is unavailable")},
	}
	if cmd := openSync(t, m); cmd != nil {
		t.Fatal("a failed sync started another command")
	}
	if m.screen != recoveryScreen {
		t.Fatalf("screen after a failed sync = %v, want recoveryScreen", m.screen)
	}
	if body := content(m); strings.Contains(body, successMark+" Synced") {
		t.Errorf("recovery view = %q, want no outcome of a run that failed", body)
	}
}

func TestSyncAllFailureOffersRetryWithoutAutomaticRetry(t *testing.T) {
	first := account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)
	fake := &fakeService{
		data: app.AccountData{Accounts: []model.AccountView{first}},
		syncAllResults: []app.SyncResult{
			{ItemID: "item-1", Label: "Chase", Accounts: []model.AccountView{first}},
			{ItemID: "item-2", Label: "Amex", Err: fmt.Errorf("plaid: %w", app.ErrProductNotReady)},
		},
	}
	m := ready(t, fake)

	if cmd := openSync(t, m); cmd != nil {
		t.Fatal("a failed sync started another command; the interface must not retry on its own")
	}
	if fake.syncAllCalls != 1 {
		t.Fatalf("SyncAll calls = %d, want no automatic retry", fake.syncAllCalls)
	}
	if m.screen != recoveryScreen {
		t.Fatalf("screen after a failed sync = %v, want recoveryScreen", m.screen)
	}
	body := content(m)
	for _, want := range []string{"Retry", "Main", "Chase", "Amex", "The bank is still preparing the data"} {
		if !strings.Contains(body, want) {
			t.Errorf("recovery view = %q, want it to contain %q", body, want)
		}
	}

	fake.syncAllResults = []app.SyncResult{
		{ItemID: "item-1", Label: "Chase", Accounts: []model.AccountView{first}},
		{ItemID: "item-2", Label: "Amex", Accounts: []model.AccountView{first}},
	}
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry
	if fake.syncAllCalls != 2 {
		t.Fatalf("SyncAll calls = %d, want the first call and the retry", fake.syncAllCalls)
	}
	runOperation(t, m, cmd) // the account refresh
	if m.screen != mainScreen {
		t.Fatalf("screen after a successful retry = %v, want mainScreen", m.screen)
	}
	if body := content(m); strings.Contains(body, "Something went wrong") {
		t.Errorf("main view = %q, want the failure left behind", body)
	}
}
