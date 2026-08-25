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
	// linkCalls holds both choices of every Link call, in order.
	linkCalls []linkCall
	// links holds every item a successful Link saved, as App.Link saves the
	// token before any sync runs.
	links map[string]bool

	// completeCalls holds the handle of every CompleteLinkSave call, in order.
	// A failed save must be completed from the handle and never by linking
	// again, so a test reads this list beside linkCalls.
	completeCalls []app.PendingSave
	// completeItem is what a successful CompleteLinkSave returns, and
	// completeErr replaces it.
	completeItem app.LinkedItem
	completeErr  error

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

	// enableCalls holds the account id of every EnableLiabilities call, in
	// order. enableEffect models a state change that happens before the call
	// returns, including a partial change followed by cancellation.
	enableCalls   []string
	enableEffect  func(*fakeService)
	enableErr     error
	consentSaved  bool
	endpointCalls int

	// liabilityRefreshCalls holds each snapshot-only retry. The operation must
	// never repeat update consent or transaction sync.
	liabilityRefreshCalls  []string
	liabilityRefreshErrs   []error
	liabilityRefreshEffect func(*fakeService)
}

type linkCall struct {
	days        int
	liabilities bool
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

func (f *fakeService) Link(_ context.Context, days int, liabilities bool, _ app.Progress) (app.LinkedItem, error) {
	f.linkCalls = append(f.linkCalls, linkCall{days: days, liabilities: liabilities})
	if f.linkErr != nil {
		return app.LinkedItem{}, f.linkErr
	}
	if f.links == nil {
		f.links = map[string]bool{}
	}
	f.links[f.linkItem.ItemID] = true
	return f.linkItem, nil
}

func (f *fakeService) EnableLiabilities(_ context.Context, accountID string, _ app.Progress) error {
	f.enableCalls = append(f.enableCalls, accountID)
	if f.enableEffect != nil {
		f.enableEffect(f)
	}
	return f.enableErr
}

func (f *fakeService) RefreshLiabilities(_ context.Context, accountID string) error {
	f.liabilityRefreshCalls = append(f.liabilityRefreshCalls, accountID)
	if err := takeError(&f.liabilityRefreshErrs); err != nil {
		return err
	}
	if f.liabilityRefreshEffect != nil {
		f.liabilityRefreshEffect(f)
	}
	return nil
}

func (f *fakeService) CompleteLinkSave(pending app.PendingSave) (app.LinkedItem, error) {
	f.completeCalls = append(f.completeCalls, pending)
	if f.completeErr != nil {
		return app.LinkedItem{}, f.completeErr
	}
	if f.links == nil {
		f.links = map[string]bool{}
	}
	f.links[f.completeItem.ItemID] = true
	return f.completeItem, nil
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

// hostileDisplayName wraps visible text in terminal controls that stored
// account names must never write back to the terminal.
func hostileDisplayName(visible string) string {
	return "\x1b[31m" + visible + "\x1b[0m" +
		"\x1b]52;c;c2VjcmV0\x07" +
		"\r\n\b\x00\x01\x1f\x7f\u0080\u0085"
}

func assertSafeAccountNames(t *testing.T, raw string, names ...string) {
	t.Helper()
	for _, sequence := range []string{
		"\x1b[31m", "\x1b]52;", "\x07", "\r", "\b", "\x00", "\x01", "\x1f", "\x7f", "\u0080", "\u0085",
	} {
		if strings.Contains(raw, sequence) {
			t.Errorf("raw view contains unsafe terminal sequence %q: %q", sequence, raw)
		}
	}
	for _, r := range raw {
		if r == '\n' || r == '\x1b' {
			continue
		}
		if r < ' ' || r >= '\x7f' && r <= '\u009f' {
			t.Errorf("raw view contains control character %U: %q", r, raw)
		}
	}
	for _, name := range names {
		lines := 0
		for _, line := range strings.Split(raw, "\n") {
			if strings.Contains(line, name) {
				lines++
			}
		}
		if lines != 1 {
			t.Errorf("safe account name %q appears on %d physical lines, want 1: %q", name, lines, raw)
		}
	}
}

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

// hasLine says whether one whole line of a screen is the given text, with the
// gutter and the column padding removed. It is exact about the line, so a
// label that also appears inside another line does not match.
func hasLine(body, want string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.Join(strings.Fields(line), " ") == want {
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
	m.Update(operationResult(t, cmd))
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

func TestMainAccountSummaryTable(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	due := time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)
	paid := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	partialDue := time.Date(2025, time.December, 5, 0, 0, 0, 0, time.UTC)
	partialPaid := time.Date(2026, time.August, 18, 0, 0, 0, 0, time.UTC)

	accounts := []model.AccountView{
		account("acc-1", "Amex Daily", "Blue Cash", "1001", "American Express", 1284.21),
		account("acc-2", "", "Scotia Visa", "2002", "Scotiabank", 320.1),
		account("acc-3", "", "", "3003", "Wealthsimple", 2400),
		account("acc-4", "Cash reserve", "Cash", "4004", "Wealthsimple", -810),
	}
	accounts[0].Currency = "usd"
	accounts[0].Liability = &model.CreditLiability{
		PaymentDueDate:       &due,
		LastPaymentDate:      &paid,
		LastPaymentAmount:    decimal.NewNullDecimal(decimal.NewFromInt(500)),
		LastStatementBalance: decimal.NewNullDecimal(decimal.NewFromInt(1000)),
	}
	accounts[1].Currency = "cad"
	accounts[1].Liability = &model.CreditLiability{
		PaymentDueDate:    &partialDue,
		LastPaymentDate:   &partialPaid,
		LastPaymentAmount: decimal.NullDecimal{},
	}
	accounts[2].Currency = "jpy"
	accounts[3].Currency = "CAD"
	accounts[3].Liability = &model.CreditLiability{}

	m := ready(t, &fakeService{data: app.AccountData{Accounts: accounts}})
	m.now = func() time.Time { return now }
	m.width = 100
	body := content(m)

	for _, want := range []string{
		"Account", "Cur. balance", "Stmt balance", "Due", "Last payment",
		"Amex Daily", "1,284.21 USD", "1,000.00 USD", "Sep 12", "Aug 20 · 500.00 USD",
		"Scotia Visa", "320.10 CAD", "Dec 05, 2025",
		"acc-3", "2,400.00 JPY", "Cash reserve", "-810.00 CAD",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("main view = %q, want it to contain %q", body, want)
		}
	}
	if got := strings.Count(body, "—"); got != 8 {
		t.Errorf("main view has %d missing values, want 8 for absent liability data: %q", got, body)
	}

	wordmark := strings.Index(body, brandName+" "+brandMark)
	header := strings.Index(body, "Account")
	menu := strings.Index(body, "Main menu")
	if wordmark < 0 || header < 0 || menu < 0 || !(wordmark < header && header < menu) {
		t.Fatalf("main view order = %q, want wordmark, table, then Main menu", body)
	}
	if !strings.Contains(body, "\n  Main menu\n\n› Accounts") {
		t.Errorf("main view = %q, want Main menu directly above the choices", body)
	}

	last := header
	for _, name := range []string{"Amex Daily", "Scotia Visa", "acc-3", "Cash reserve"} {
		position := strings.Index(body, name)
		if position <= last {
			t.Errorf("account %q is at %d after prior position %d; want stored row order", name, position, last)
		}
		last = position
	}

	lines := strings.Split(body[header-len(blankMark):menu], "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, blankMark) {
			t.Errorf("table line %q does not start with blankMark", line)
		}
		if width := lipgloss.Width(line); width >= m.contentWidth() {
			t.Errorf("table line %q is %d cells wide, want natural width below terminal width %d",
				line, width, m.contentWidth())
		}
	}
}

func TestMainAccountSummarySanitizesAccountNames(t *testing.T) {
	accounts := []model.AccountView{
		account("acc-1", hostileDisplayName("Nick\r\n safe"), "Provider one", "1001", "Bank", 10),
		account("acc-2", hostileDisplayName(""), hostileDisplayName("Provider\t safe"), "2002", "Bank", 20),
		account(hostileDisplayName("acc-\n safe"), hostileDisplayName(""), hostileDisplayName(""), "3003", "Bank", 30),
	}
	m := ready(t, &fakeService{data: app.AccountData{Accounts: accounts}})

	assertSafeAccountNames(t, m.View().Content, "Nick safe", "Provider safe", "acc- safe")
}

func TestAccountListAndDetailSanitizeAccountNames(t *testing.T) {
	view := account("acc-1", hostileDisplayName("Nick\r\n safe"), "Provider", "1001", "Bank", 10)
	m := ready(t, &fakeService{data: app.AccountData{Accounts: []model.AccountView{view}}})

	press(t, m, codeKey(tea.KeyEnter))
	assertSafeAccountNames(t, m.View().Content, "Nick safe")
	press(t, m, codeKey(tea.KeyEnter))
	assertSafeAccountNames(t, m.View().Content, "Nick safe")
}

func TestMainRenderUsesOneCapturedTime(t *testing.T) {
	captured := time.Date(2025, time.December, 31, 23, 59, 45, 0, time.UTC)
	later := time.Date(2026, time.January, 1, 0, 0, 45, 0, time.UTC)
	lastSync := captured.Add(-30 * time.Second)
	due := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	view := account("acc-1", "Card", "Credit card", "1001", "Bank", 100)
	view.Liability = &model.CreditLiability{PaymentDueDate: &due}
	m := ready(t, &fakeService{data: app.AccountData{
		Accounts: []model.AccountView{view},
		States: []app.SyncState{{
			ItemID:       "item-1",
			Institution:  "Bank",
			LastSyncedAt: &lastSync,
			LastStatus:   "ok",
		}},
	}})

	clockCalls := 0
	m.now = func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return captured
		}
		return later
	}
	body := content(m)

	if clockCalls != 1 {
		t.Fatalf("one main render called the clock %d times, want 1", clockCalls)
	}
	if !hasRow(body, "Sync", okMark+" Last sync: just now") {
		t.Errorf("main view = %q, want sync status formatted with the captured time", body)
	}
	if !strings.Contains(body, "Jan 02, 2026") {
		t.Errorf("main view = %q, want the due date formatted with the same captured time", body)
	}
}

func TestMainAccountSummaryTableAtNarrowWidth(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	due := time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)
	paid := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	accounts := []model.AccountView{
		account("acc-1", "Amex Daily", "Blue Cash", "1001", "American Express", 1284.21),
		account("acc-2", "", "Scotia Visa", "2002", "Scotiabank", 320.1),
		account("acc-3", "", "", "3003", "Wealthsimple", 2400),
		account("acc-4", "Cash reserve", "Cash", "4004", "Wealthsimple", -810),
	}
	accounts[0].Liability = &model.CreditLiability{
		PaymentDueDate:       &due,
		LastPaymentDate:      &paid,
		LastPaymentAmount:    decimal.NewNullDecimal(decimal.NewFromInt(500)),
		LastStatementBalance: decimal.NewNullDecimal(decimal.NewFromInt(1000)),
	}

	m := ready(t, &fakeService{data: app.AccountData{Accounts: accounts}})
	m.now = func() time.Time { return now }
	const narrow = 50
	m.width = narrow
	body := content(m)
	for _, name := range []string{"Amex Daily", "Scotia Visa", "acc-3", "Cash reserve"} {
		lines := 0
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, name) {
				lines++
			}
		}
		if lines != 1 {
			t.Errorf("account %q appears on %d physical lines, want exactly 1: %q", name, lines, body)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if width := lipgloss.Width(line); width > narrow {
			t.Errorf("main line %q is %d cells wide, want at most %d", line, width, narrow)
		}
	}
}

func TestMainEmptyAccountSummary(t *testing.T) {
	m := ready(t, &fakeService{})
	body := content(m)
	if !hasLine(body, "No accounts are linked yet.") {
		t.Errorf("empty main view = %q, want exact empty-account message", body)
	}
	if !strings.Contains(body, "No accounts are linked yet.\n\n  Main menu\n\n› Accounts") {
		t.Errorf("empty main view = %q, want the message above the usable menu", body)
	}

	press(t, m, codeKey(tea.KeyDown))
	if m.main.cursor != choiceAdd {
		t.Fatalf("cursor after down = %d, want Add account", m.main.cursor)
	}
	press(t, m, codeKey(tea.KeyUp))
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != accountsScreen {
		t.Fatalf("screen after selecting Accounts = %v, want accountsScreen", m.screen)
	}
}

func TestAccountRowUsesOneGroupedCurrencyBearingBalance(t *testing.T) {
	view := account("acc-1", "", "Everyday Checking", "1234", "Chase", 1284.21)
	row := ansi.Strip(accountRow(view, false, lipgloss.NewStyle(), 80))
	if !strings.Contains(row, "1,284.21 USD") {
		t.Errorf("account row = %q, want the shared money format", row)
	}
	if got := strings.Count(row, "USD"); got != 1 {
		t.Errorf("account row = %q, want one currency value, got %d", row, got)
	}
}

func TestDetailUsesOneGroupedCurrencyBearingBalance(t *testing.T) {
	view := account("acc-1", "", "Everyday Checking", "1234", "Chase", 1284.21)
	m := ready(t, &fakeService{data: app.AccountData{Accounts: []model.AccountView{view}}})
	openDetail(t, m, 0)
	body := content(m)
	if !strings.Contains(body, "1,284.21 USD") {
		t.Errorf("detail view = %q, want the shared money format", body)
	}
	if got := strings.Count(body, "USD"); got != 1 {
		t.Errorf("detail view = %q, want one currency value, got %d", body, got)
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
	runCmd(cmd) // let the fake capture the context

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

	msg := operationResult(t, cmd)
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
	for _, want := range []string{"Café Ünicode", "••1234", "Chase", "1,234.56 USD", "NEW"} {
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

// runCmd runs one command and returns every message it produced. A command
// that batches produces one message per batched command: the program runs a
// tea.BatchMsg itself and delivers each message on its own, so Update never
// sees the batch.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var msgs []tea.Msg
	for _, batched := range batch {
		msgs = append(msgs, runCmd(batched)...)
	}
	return msgs
}

// operationResult runs one command and returns the single operation result it
// produced. Starting an operation also starts the spinner, so the command
// carries a frame message beside the result.
func operationResult(t *testing.T, cmd tea.Cmd) operationMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected an operation command, got none")
	}
	return operationOf(t, runCmd(cmd))
}

// operationOf is the one operation result among the messages of one command.
func operationOf(t *testing.T, msgs []tea.Msg) operationMsg {
	t.Helper()
	var result *operationMsg
	for _, msg := range msgs {
		if got, ok := msg.(operationMsg); ok {
			result = &got
		}
	}
	if result == nil {
		t.Fatalf("command produced %v, want an operationMsg", msgs)
	}
	return *result
}

// firstTick is the first spinner frame among the messages of one command.
func firstTick(msgs []tea.Msg) (spinner.TickMsg, bool) {
	for _, msg := range msgs {
		if tick, ok := msg.(spinner.TickMsg); ok {
			return tick, true
		}
	}
	return spinner.TickMsg{}, false
}

// runOperation executes one operation command, applies its result, and returns
// whatever command the result started next.
func runOperation(t *testing.T, m *Model, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	_, next := m.Update(operationResult(t, cmd))
	return next
}

// typeText sends one key press per character.
func typeText(t *testing.T, m *Model, text string) {
	t.Helper()
	for _, r := range text {
		press(t, m, runeKey(r))
	}
}

// openAddSetup walks the main menu to the choices for one new account.
func openAddSetup(t *testing.T, m *Model) {
	t.Helper()
	for m.main.cursor < choiceAdd {
		press(t, m, codeKey(tea.KeyDown))
	}
	for m.main.cursor > choiceAdd {
		press(t, m, codeKey(tea.KeyUp))
	}
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != addSetupScreen {
		t.Fatalf("screen after choosing Add account = %v, want addSetupScreen", m.screen)
	}
}

// openHistory walks the main menu and add setup to the history length choice.
func openHistory(t *testing.T, m *Model) {
	t.Helper()
	openAddSetup(t, m)
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != historyScreen {
		t.Fatalf("screen after choosing Transaction history = %v, want historyScreen", m.screen)
	}
}

// startDefaultLink selects the default history choice, then starts Link from
// Continue on the setup screen.
func startDefaultLink(t *testing.T, m *Model) tea.Cmd {
	t.Helper()
	openHistory(t, m)
	if cmd := press(t, m, codeKey(tea.KeyEnter)); cmd != nil {
		t.Fatal("choosing the default history started Link")
	}
	press(t, m, codeKey(tea.KeyDown))
	press(t, m, codeKey(tea.KeyDown))
	return press(t, m, codeKey(tea.KeyEnter))
}

// addUntilFirstNickname runs the add flow from the main menu to the first
// nickname prompt.
func addUntilFirstNickname(t *testing.T, m *Model) {
	t.Helper()
	cmd := runOperation(t, m, startDefaultLink(t, m)) // Link
	runOperation(t, m, cmd)                           // SyncItem
	if m.screen != nicknameScreen {
		t.Fatalf("screen after the first sync = %v, want nicknameScreen", m.screen)
	}
}

func TestAddSetupDefaultsTogglesAndPassesLiabilitiesToLink(t *testing.T) {
	fake := &fakeService{linkItem: app.LinkedItem{ItemID: "item-new", Institution: "Chase"}}
	m := ready(t, fake)

	openAddSetup(t, m)
	if m.add != (addState{days: app.MaxLinkDays, liabilities: true}) {
		t.Fatalf("add state = %+v, want 730 days, Liabilities on, and the first row", m.add)
	}
	body := content(m)
	for _, row := range [][2]string{
		{"Transaction history", "730 days"},
		{"Enable Liabilities API?", "Yes"},
	} {
		if !hasRow(body, row[0], row[1]) {
			t.Errorf("add setup = %q, want row %q %q", body, row[0], row[1])
		}
	}
	for _, want := range []string{
		"Continue",
		"Fetch credit card statement, payment, and interest details.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("add setup = %q, want %q", body, want)
		}
	}

	press(t, m, codeKey(tea.KeyDown)) // Enable Liabilities API?
	press(t, m, codeKey(tea.KeyEnter))
	if m.add.liabilities || !hasRow(content(m), "Enable Liabilities API?", "No") {
		t.Fatalf("Liabilities after the first toggle = %v, want No", m.add.liabilities)
	}
	press(t, m, codeKey(tea.KeyEnter))
	if !m.add.liabilities || !hasRow(content(m), "Enable Liabilities API?", "Yes") {
		t.Fatalf("Liabilities after the second toggle = %v, want Yes", m.add.liabilities)
	}

	press(t, m, codeKey(tea.KeyUp)) // Transaction history
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != historyScreen {
		t.Fatalf("screen after opening history = %v, want historyScreen", m.screen)
	}
	if len(fake.linkCalls) != 0 {
		t.Fatalf("Link calls after opening history = %v, want none", fake.linkCalls)
	}
	press(t, m, codeKey(tea.KeyEsc))
	if m.screen != addSetupScreen {
		t.Fatalf("screen after history esc = %v, want addSetupScreen", m.screen)
	}
	press(t, m, codeKey(tea.KeyEsc))
	if m.screen != mainScreen {
		t.Fatalf("screen after setup esc = %v, want mainScreen", m.screen)
	}

	openHistory(t, m)
	press(t, m, codeKey(tea.KeyDown)) // 365 days
	if cmd := press(t, m, codeKey(tea.KeyEnter)); cmd != nil {
		t.Fatal("choosing fixed history started Link")
	}
	if m.screen != addSetupScreen || m.add.days != 365 {
		t.Fatalf("screen = %v, days = %d, want setup with 365 days", m.screen, m.add.days)
	}
	if len(fake.linkCalls) != 0 {
		t.Fatalf("Link calls after fixed history = %v, want none", fake.linkCalls)
	}

	press(t, m, codeKey(tea.KeyEnter)) // open history again
	for m.history.cursor < len(historyDays) {
		press(t, m, codeKey(tea.KeyDown))
	}
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != customDaysScreen {
		t.Fatalf("screen after choosing Custom = %v, want customDaysScreen", m.screen)
	}
	m.prompt.input.SetValue("120")
	if cmd := press(t, m, codeKey(tea.KeyEnter)); cmd != nil {
		t.Fatal("choosing custom history started Link")
	}
	if m.screen != addSetupScreen || m.add.days != 120 {
		t.Fatalf("screen = %v, days = %d, want setup with 120 days", m.screen, m.add.days)
	}
	if len(fake.linkCalls) != 0 {
		t.Fatalf("Link calls after custom history = %v, want none", fake.linkCalls)
	}

	press(t, m, codeKey(tea.KeyDown))  // Liabilities
	press(t, m, codeKey(tea.KeyEnter)) // No
	press(t, m, codeKey(tea.KeyDown))  // Continue
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	if cmd == nil {
		t.Fatal("a successful Link did not start the first sync")
	}
	want := []linkCall{{days: 120, liabilities: false}}
	if len(fake.linkCalls) != 1 || fake.linkCalls[0] != want[0] {
		t.Fatalf("Link calls = %+v, want %+v", fake.linkCalls, want)
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

	if cmd := press(t, m, codeKey(tea.KeyEnter)); cmd != nil { // choose 730 days
		t.Fatal("choosing history started Link")
	}
	press(t, m, codeKey(tea.KeyDown))
	press(t, m, codeKey(tea.KeyDown))
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Link
	if len(fake.linkCalls) != 1 || fake.linkCalls[0] != (linkCall{days: 730, liabilities: true}) {
		t.Fatalf("Link calls = %v, want one call for 730 days with Liabilities", fake.linkCalls)
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
	if len(fake.linkCalls) != 0 {
		t.Fatalf("Link calls = %v, want no link for an unusable value", fake.linkCalls)
	}

	m.prompt.input.SetValue(" 120 ")
	if cmd := press(t, m, codeKey(tea.KeyEnter)); cmd != nil {
		t.Fatal("a valid custom value started Link")
	}
	if m.screen != addSetupScreen || m.add.days != 120 {
		t.Fatalf("screen = %v, days = %d, want setup with 120 days", m.screen, m.add.days)
	}
	if len(fake.linkCalls) != 0 {
		t.Fatalf("Link calls = %v, want none before Continue", fake.linkCalls)
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

	cmd := runOperation(t, m, startDefaultLink(t, m)) // Link
	cmd = runOperation(t, m, cmd)                     // the failing SyncItem
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

	runOperation(t, m, startDefaultLink(t, m)) // the failing Link
	if m.screen != recoveryScreen {
		t.Fatalf("screen after a failed link = %v, want recoveryScreen", m.screen)
	}

	cmd := press(t, m, codeKey(tea.KeyEnter)) // Retry
	if cmd == nil {
		t.Fatal("Retry started no operation")
	}
	if m.screen != addSetupScreen {
		t.Fatalf("screen while the retry runs = %v, want addSetupScreen", m.screen)
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

	if m.screen != addSetupScreen {
		t.Fatalf("screen after a cancelled retry = %v, want addSetupScreen", m.screen)
	}
	if m.linked != (app.LinkedItem{}) {
		t.Errorf("linked item = %+v, want it cleared", m.linked)
	}
	if body := content(m); strings.Contains(body, "Something went wrong") {
		t.Errorf("view after a cancelled retry = %q, want the history choice", body)
	}
	if len(fake.linkCalls) != 2 {
		t.Errorf("Link calls = %v, want the first call and the retry", fake.linkCalls)
	}
}

func TestLinkCancellationReturnsToAddSetup(t *testing.T) {
	fake := &fakeService{
		linkItem: app.LinkedItem{ItemID: "item-new", Institution: "Chase"},
		linkErr:  context.Canceled,
	}
	m := ready(t, fake)

	cmd := runOperation(t, m, startDefaultLink(t, m))
	if cmd != nil {
		t.Fatal("a cancelled link started another command")
	}
	if m.screen != addSetupScreen {
		t.Fatalf("screen after a cancelled link = %v, want addSetupScreen", m.screen)
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
	for m.main.cursor > choiceAccounts {
		press(t, m, codeKey(tea.KeyUp))
	}
	press(t, m, codeKey(tea.KeyEnter)) // Accounts
	for i := 0; i < row; i++ {
		press(t, m, codeKey(tea.KeyDown))
	}
	press(t, m, codeKey(tea.KeyEnter))
	if m.screen != detailScreen {
		t.Fatalf("screen after opening a row = %v, want detailScreen", m.screen)
	}
}

func creditAccount(id, institution string) model.AccountView {
	view := account(id, "", "Credit card", "1234", institution, 20)
	view.Type = "credit"
	return view
}

func TestAccountRefreshCopiesLiabilitiesEnabled(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	source := map[string]bool{"item-1": false}
	state := app.SyncState{ItemID: "item-1", Institution: "Chase", LastStatus: "ok"}
	m := ready(t, &fakeService{data: app.AccountData{
		Accounts:           []model.AccountView{row},
		States:             []app.SyncState{state},
		LiabilitiesEnabled: source,
	}})

	if got, found := m.liabilitiesEnabled["item-1"]; !found || got {
		t.Fatalf("copied Liabilities flag = %v, found = %v, want stored false", got, found)
	}
	if len(m.accounts.rows) != 1 || m.accounts.rows[0].AccountID != "acc-1" {
		t.Fatalf("account rows = %+v, want acc-1 preserved", m.accounts.rows)
	}
	if len(m.syncStates) != 1 || m.syncStates[0] != state {
		t.Fatalf("sync states = %+v, want %+v", m.syncStates, state)
	}

	source["item-1"] = true
	if m.liabilitiesEnabled["item-1"] {
		t.Fatal("the Model aliases the AccountData Liabilities map")
	}
}

func TestAccountRefreshLeavesDetailWhenTheSelectedAccountDisappears(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	fake := &fakeService{data: app.AccountData{
		Accounts:           []model.AccountView{row},
		LiabilitiesEnabled: map[string]bool{"item-1": false},
	}}
	m := ready(t, fake)
	openDetail(t, m, 0)
	m.detail.cursor = 2

	fake.data = app.AccountData{LiabilitiesEnabled: map[string]bool{}}
	runOperation(t, m, m.refreshAccounts())

	if m.screen != accountsScreen {
		t.Fatalf("screen after the selected account disappeared = %v, want accountsScreen", m.screen)
	}
	if m.detail != (detailState{}) {
		t.Fatalf("detail after the selected account disappeared = %+v, want empty state", m.detail)
	}
	if m.accounts.cursor != 0 {
		t.Fatalf("account cursor = %d, want reset for the empty account list", m.accounts.cursor)
	}
	if cmd := press(t, m, codeKey(tea.KeyEnter)); cmd != nil || m.screen != accountsScreen {
		t.Fatalf("enter on the empty account list returned cmd %v and screen %v", cmd, m.screen)
	}
}

func TestDetailDisabledCreditShowsStatementActionAndRefreshesAfterSuccess(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	fake := &fakeService{data: app.AccountData{
		Accounts:           []model.AccountView{row},
		LiabilitiesEnabled: map[string]bool{"item-1": false},
	}}
	fake.enableEffect = func(f *fakeService) {
		f.data.LiabilitiesEnabled["item-1"] = true
	}
	m := ready(t, fake)
	openDetail(t, m, 0)

	body := content(m)
	for _, want := range []string{
		"Enable statement data",
		"This enables statement data for the whole institution.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail view = %q, want %q", body, want)
		}
	}
	actions := m.detailActions()
	if len(actions) != 3 || actions[0] != detailRename ||
		actions[1] != detailEnableLiabilities || actions[2] != detailUnlink {
		t.Fatalf("detail actions = %v, want Rename, Enable, Unlink", actions)
	}

	press(t, m, codeKey(tea.KeyDown)) // Enable statement data
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	if len(fake.enableCalls) != 1 || fake.enableCalls[0] != "acc-1" {
		t.Fatalf("EnableLiabilities calls = %v, want one call for acc-1", fake.enableCalls)
	}
	runOperation(t, m, cmd) // account refresh

	if m.screen != detailScreen || m.detail.account.AccountID != "acc-1" {
		t.Fatalf("screen = %v, detail = %+v, want refreshed acc-1 detail", m.screen, m.detail.account)
	}
	if !m.liabilitiesEnabled["item-1"] {
		t.Fatal("refreshed Model does not show the enabled Item flag")
	}
	actions = m.detailActions()
	if len(actions) != 2 || actions[0] != detailRename || actions[1] != detailUnlink {
		t.Fatalf("detail actions after enable = %v, want Rename and Unlink", actions)
	}
	if m.detail.cursor != 1 || detailActionLabel(actions[m.detail.cursor]) != "Unlink institution" {
		t.Fatalf("detail cursor = %d, action = %q, want the valid Unlink index",
			m.detail.cursor, detailActionLabel(actions[m.detail.cursor]))
	}
}

func TestPostEnableAccountRefreshFailureIsTruthfulAndRetriesOnlyRefresh(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantMessage string
	}{
		{
			name:        "failure",
			err:         errors.New("read accounts: database is unavailable"),
			wantMessage: "Statement data was enabled, but the account refresh failed.",
		},
		{
			name:        "cancellation",
			err:         context.Canceled,
			wantMessage: "Statement data was enabled, but the account refresh was canceled.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := creditAccount("acc-1", "Chase")
			fake := &fakeService{data: app.AccountData{
				Accounts:           []model.AccountView{row},
				LiabilitiesEnabled: map[string]bool{"item-1": false},
			}}
			m := ready(t, fake)
			fake.accountsErr = tt.err
			openDetail(t, m, 0)
			press(t, m, codeKey(tea.KeyDown)) // Enable statement data

			refreshCmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
			if !m.liabilitiesEnabled["item-1"] {
				t.Fatal("successful consent did not mark the Item enabled before refresh")
			}
			if m.runningKind == accountsOperation {
				t.Fatal("post-enable refresh uses the generic cancellable account operation")
			}
			if body := content(m); strings.Contains(body, "Enable statement data") {
				t.Errorf("detail during refresh = %q, want no stale enable action", body)
			}

			if cmd := runOperation(t, m, refreshCmd); cmd != nil {
				t.Fatal("a failed post-enable refresh started another operation")
			}
			if m.screen != recoveryScreen {
				t.Fatalf("screen after refresh %s = %v, want recoveryScreen", tt.name, m.screen)
			}
			body := content(m)
			for _, want := range []string{
				tt.wantMessage,
				displayError(tt.err),
				"Retry refreshes accounts. It does not request consent again.",
				"Retry",
				"Main",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("recovery view = %q, want %q", body, want)
				}
			}
			if !m.liabilitiesEnabled["item-1"] {
				t.Fatal("failed refresh replaced the locally enabled Item flag")
			}
			if len(fake.enableCalls) != 1 {
				t.Fatalf("EnableLiabilities calls = %v, want one", fake.enableCalls)
			}

			fake.accountsErr = nil
			fake.data.LiabilitiesEnabled["item-1"] = true
			runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry refresh
			if len(fake.enableCalls) != 1 {
				t.Fatalf("EnableLiabilities calls after Retry = %v, want no second consent", fake.enableCalls)
			}
			if len(fake.accountsCalls) != 3 {
				t.Fatalf("Accounts calls = %v, want initial, failed, and retried refresh", fake.accountsCalls)
			}
			if m.screen != detailScreen || m.detail.account.AccountID != "acc-1" {
				t.Fatalf("screen = %v, detail = %+v, want refreshed acc-1 detail",
					m.screen, m.detail.account)
			}
		})
	}
}

func TestPartialLiabilitiesEnableRetriesSnapshotThenAccounts(t *testing.T) {
	tests := []struct {
		name        string
		cause       error
		wantMessage string
	}{
		{
			name:        "temporary failure",
			cause:       errors.New("liabilities are temporarily unavailable"),
			wantMessage: "Statement data is enabled for the whole institution, but the first refresh failed.",
		},
		{
			name:        "cancellation",
			cause:       context.Canceled,
			wantMessage: "Statement data is enabled for the whole institution, but the first refresh was canceled.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := creditAccount("acc-1", "Chase")
			partial := app.NewLiabilitiesEnabledError(tt.cause, "")
			fake := &fakeService{
				data: app.AccountData{
					Accounts:           []model.AccountView{row},
					LiabilitiesEnabled: map[string]bool{"item-1": false},
					States: []app.SyncState{{
						ItemID:                     "item-1",
						Institution:                "Chase",
						LiabilitiesConsentRequired: true,
					}},
				},
				enableErr: partial,
			}
			fake.liabilityRefreshEffect = func(f *fakeService) {
				f.data.LiabilitiesEnabled["item-1"] = true
				f.data.States[0].LiabilitiesConsentRequired = false
			}
			m := ready(t, fake)
			openDetail(t, m, 0)
			press(t, m, codeKey(tea.KeyDown)) // Enable statement data

			msg := operationResult(t, press(t, m, codeKey(tea.KeyEnter)))
			if !errors.Is(msg.err, tt.cause) {
				t.Fatalf("operation error = %v, want cause %v", msg.err, tt.cause)
			}
			if _, cmd := m.Update(msg); cmd != nil {
				t.Fatal("a partial enable started another operation")
			}

			if !m.liabilitiesEnabled["item-1"] {
				t.Fatal("partial enable did not set the local Item flag")
			}
			for _, action := range m.detailActions() {
				if action == detailEnableLiabilities {
					t.Fatal("partial enable left the Enable action visible")
				}
			}
			if m.screen != recoveryScreen {
				t.Fatalf("screen after partial enable = %v, want recoveryScreen", m.screen)
			}
			body := content(m)
			normalizedBody := strings.Join(strings.Fields(body), " ")
			for _, want := range []string{
				tt.wantMessage,
				"Reason: " + displayError(tt.cause),
				"Retry requests the statement snapshot. It does not request consent again.",
				"Retry",
				"Main",
			} {
				if !strings.Contains(normalizedBody, strings.Join(strings.Fields(want), " ")) {
					t.Errorf("recovery view = %q, want %q", body, want)
				}
			}
			if len(fake.enableCalls) != 1 || len(fake.liabilityRefreshCalls) != 0 || len(fake.accountsCalls) != 1 {
				t.Fatalf("calls after partial enable = Enable %v, Refresh %v, Accounts %v; want 1, 0, 1",
					fake.enableCalls, fake.liabilityRefreshCalls, fake.accountsCalls)
			}

			accountsCmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry snapshot
			if len(fake.liabilityRefreshCalls) != 1 || len(fake.accountsCalls) != 1 {
				t.Fatalf("calls after snapshot Retry = Refresh %v, Accounts %v; want 1 and 1",
					fake.liabilityRefreshCalls, fake.accountsCalls)
			}
			runOperation(t, m, accountsCmd)
			if len(fake.enableCalls) != 1 {
				t.Fatalf("EnableLiabilities calls after Retry = %v, want one", fake.enableCalls)
			}
			if len(fake.accountsCalls) != 2 {
				t.Fatalf("Accounts calls after snapshot success = %v, want initial and refresh", fake.accountsCalls)
			}
			if m.screen != detailScreen || m.detail.account.AccountID != "acc-1" {
				t.Fatalf("screen = %v, detail = %+v, want refreshed acc-1 detail",
					m.screen, m.detail.account)
			}
		})
	}
}

func TestPartialLiabilitiesSnapshotFailureRetriesTheSnapshotAgain(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	firstErr := errors.New("first snapshot failed")
	retryErr := errors.New("snapshot retry failed")
	fake := &fakeService{
		data: app.AccountData{
			Accounts:           []model.AccountView{row},
			LiabilitiesEnabled: map[string]bool{"item-1": false},
		},
		enableErr:            app.NewLiabilitiesEnabledError(firstErr, ""),
		liabilityRefreshErrs: []error{retryErr, nil},
	}
	fake.liabilityRefreshEffect = func(f *fakeService) {
		f.data.LiabilitiesEnabled["item-1"] = true
	}
	m := ready(t, fake)
	openDetail(t, m, 0)
	press(t, m, codeKey(tea.KeyDown))
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))

	if cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))); cmd != nil {
		t.Fatal("failed snapshot retry started account refresh")
	}
	if m.screen != recoveryScreen || !strings.Contains(content(m), retryErr.Error()) {
		t.Fatalf("screen = %v, view = %q; want snapshot retry recovery", m.screen, content(m))
	}
	if len(fake.enableCalls) != 1 || len(fake.liabilityRefreshCalls) != 1 || len(fake.accountsCalls) != 1 {
		t.Fatalf("calls after failed retry = Enable %v, Refresh %v, Accounts %v; want 1 each",
			fake.enableCalls, fake.liabilityRefreshCalls, fake.accountsCalls)
	}

	accountsCmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	runOperation(t, m, accountsCmd)
	if len(fake.enableCalls) != 1 || len(fake.liabilityRefreshCalls) != 2 || len(fake.accountsCalls) != 2 {
		t.Fatalf("final calls = Enable %v, Refresh %v, Accounts %v; want 1, 2, 2",
			fake.enableCalls, fake.liabilityRefreshCalls, fake.accountsCalls)
	}
}

func TestLiabilitiesStatusCleanupFailureRetriesSnapshotWithoutConsent(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	cleanupErr := errors.New("save consent status: database is busy")
	fake := &fakeService{
		data: app.AccountData{
			Accounts:           []model.AccountView{row},
			LiabilitiesEnabled: map[string]bool{"item-1": false},
		},
		enableErr: app.NewLiabilitiesStatusError(cleanupErr, "", true),
	}
	fake.liabilityRefreshEffect = func(f *fakeService) {
		f.data.LiabilitiesEnabled["item-1"] = true
	}
	m := ready(t, fake)
	openDetail(t, m, 0)
	press(t, m, codeKey(tea.KeyDown))
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))

	body := strings.Join(strings.Fields(content(m)), " ")
	for _, want := range []string{
		"Statement data is enabled for the whole institution, and the first snapshot was stored, but its consent status could not be updated.",
		"Retry requests the statement snapshot. It does not request consent again.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("recovery view = %q, want %q", body, want)
		}
	}
	if !m.liabilitiesEnabled["item-1"] {
		t.Fatal("cleanup failure did not mark the Item enabled")
	}
	for _, action := range m.detailActions() {
		if action == detailEnableLiabilities {
			t.Fatal("cleanup failure left the Enable action visible")
		}
	}

	accountsCmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	runOperation(t, m, accountsCmd)
	if len(fake.enableCalls) != 1 || len(fake.liabilityRefreshCalls) != 1 || len(fake.accountsCalls) != 2 {
		t.Fatalf("calls = Enable %v, Refresh %v, Accounts %v; want 1, 1, 2",
			fake.enableCalls, fake.liabilityRefreshCalls, fake.accountsCalls)
	}
}

func TestProductNotReadyStatusCleanupFailureDoesNotClaimSnapshotStored(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	cleanupErr := errors.New("save consent status: database is busy")
	fake := &fakeService{
		data: app.AccountData{
			Accounts:           []model.AccountView{row},
			LiabilitiesEnabled: map[string]bool{"item-1": false},
		},
		enableErr: app.NewLiabilitiesStatusError(cleanupErr, "", false),
	}
	m := ready(t, fake)
	openDetail(t, m, 0)
	press(t, m, codeKey(tea.KeyDown))
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))

	body := strings.Join(strings.Fields(content(m)), " ")
	for _, want := range []string{
		"Statement data is enabled for the whole institution, but the first snapshot is not ready, and its consent status could not be updated.",
		"Retry requests the statement snapshot. It does not request consent again.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("recovery view = %q, want %q", body, want)
		}
	}
	if strings.Contains(body, "snapshot was stored") {
		t.Errorf("recovery view = %q, want no stored-snapshot claim", body)
	}

	accountsCmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	runOperation(t, m, accountsCmd)
	if len(fake.enableCalls) != 1 || len(fake.liabilityRefreshCalls) != 1 || len(fake.accountsCalls) != 2 {
		t.Fatalf("calls = Enable %v, Refresh %v, Accounts %v; want 1, 1, 2",
			fake.enableCalls, fake.liabilityRefreshCalls, fake.accountsCalls)
	}
}

func TestPartialLiabilitiesConsentRequiredRetriesConsent(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	consentErr := fmt.Errorf("Plaid response: %w", app.ErrAdditionalConsentRequired)
	fake := &fakeService{
		data: app.AccountData{
			Accounts:           []model.AccountView{row},
			LiabilitiesEnabled: map[string]bool{"item-1": false},
		},
		enableErr: app.NewLiabilitiesEnabledError(consentErr, ""),
	}
	m := ready(t, fake)
	openDetail(t, m, 0)
	press(t, m, codeKey(tea.KeyDown))
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))

	body := strings.Join(strings.Fields(content(m)), " ")
	for _, want := range []string{
		"The statement data request was saved, but Plaid still requires consent.",
		"Retry requests consent again.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("recovery view = %q, want %q", body, want)
		}
	}
	if strings.Contains(body, "Statement data is enabled") {
		t.Errorf("recovery view = %q, want no enabled claim", body)
	}
	if !m.liabilitiesEnabled["item-1"] {
		t.Fatal("saved request did not set the local Item flag")
	}
	foundEnable := false
	for _, action := range m.detailActions() {
		foundEnable = foundEnable || action == detailEnableLiabilities
	}
	if !foundEnable {
		t.Fatal("consent-required outcome removed the Enable action")
	}

	fake.enableErr = nil
	fake.enableEffect = func(f *fakeService) {
		f.data.LiabilitiesEnabled["item-1"] = true
		f.data.States = []app.SyncState{{ItemID: "item-1", Institution: "Chase"}}
	}
	accountsCmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	runOperation(t, m, accountsCmd)
	if len(fake.enableCalls) != 2 || len(fake.liabilityRefreshCalls) != 0 || len(fake.accountsCalls) != 2 {
		t.Fatalf("calls = Enable %v, Refresh %v, Accounts %v; want 2, 0, 2",
			fake.enableCalls, fake.liabilityRefreshCalls, fake.accountsCalls)
	}
}

func TestLiabilitiesExplanationsRemainWholeAtNarrowWidth(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	m := ready(t, &fakeService{data: app.AccountData{
		Accounts:           []model.AccountView{row},
		LiabilitiesEnabled: map[string]bool{"item-1": false},
	}})
	m.width = 40

	openAddSetup(t, m)
	setup := strings.Join(strings.Fields(content(m)), " ")
	if want := "Fetch credit card statement, payment, and interest details."; !strings.Contains(setup, want) {
		t.Errorf("narrow setup = %q, want the whole explanation %q", setup, want)
	}
	press(t, m, codeKey(tea.KeyEsc))
	openDetail(t, m, 0)
	detail := strings.Join(strings.Fields(content(m)), " ")
	if want := "This enables statement data for the whole institution."; !strings.Contains(detail, want) {
		t.Errorf("narrow detail = %q, want the whole scope note %q", detail, want)
	}
}

func TestDetailNonCreditOmitsStatementActionAndKeepsUnlinkIndex(t *testing.T) {
	row := account("acc-1", "", "Everyday Checking", "1234", "Chase", 20)
	row.Type = "depository"
	fake := &fakeService{
		data: app.AccountData{
			Accounts:           []model.AccountView{row},
			LiabilitiesEnabled: map[string]bool{"item-1": false},
		},
		preview: app.UnlinkData{ItemID: "item-1", Institution: "Chase"},
	}
	m := ready(t, fake)
	openDetail(t, m, 0)

	if body := content(m); strings.Contains(body, "Enable statement data") {
		t.Errorf("non-credit detail = %q, want no statement action", body)
	}
	actions := m.detailActions()
	if len(actions) != 2 || actions[1] != detailUnlink {
		t.Fatalf("non-credit actions = %v, want Rename and Unlink", actions)
	}
	press(t, m, codeKey(tea.KeyDown))
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	if len(fake.previewCalls) != 1 || fake.previewCalls[0] != "item-1" {
		t.Fatalf("UnlinkPreview calls = %v, want item-1 from the dynamic index", fake.previewCalls)
	}
}

func TestDetailConsentRequiredShowsStatementActionWhenItemIsEnabled(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	m := ready(t, &fakeService{data: app.AccountData{
		Accounts:           []model.AccountView{row},
		LiabilitiesEnabled: map[string]bool{"item-1": true},
		States: []app.SyncState{{
			ItemID:                     "item-1",
			Institution:                "Chase",
			LiabilitiesConsentRequired: true,
		}},
	}})
	openDetail(t, m, 0)

	if body := content(m); !strings.Contains(body, "Enable statement data") {
		t.Errorf("consent-required detail = %q, want the statement action", body)
	}
}

func TestDetailStatementRetryUsesTheSameAccount(t *testing.T) {
	row := creditAccount("acc-1", "Chase")
	fake := &fakeService{
		data: app.AccountData{
			Accounts:           []model.AccountView{row},
			LiabilitiesEnabled: map[string]bool{"item-1": false},
		},
		enableErr: errors.New("consent did not finish"),
	}
	m := ready(t, fake)
	openDetail(t, m, 0)
	press(t, m, codeKey(tea.KeyDown))
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
	if m.screen != recoveryScreen {
		t.Fatalf("screen after failed enable = %v, want recoveryScreen", m.screen)
	}

	fake.enableErr = nil
	fake.enableEffect = func(f *fakeService) {
		f.data.LiabilitiesEnabled["item-1"] = true
	}
	cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry
	runOperation(t, m, cmd)                                       // account refresh
	want := []string{"acc-1", "acc-1"}
	if len(fake.enableCalls) != 2 || fake.enableCalls[0] != want[0] || fake.enableCalls[1] != want[1] {
		t.Fatalf("EnableLiabilities calls = %v, want %v", fake.enableCalls, want)
	}
	if m.screen != detailScreen || m.detail.account.AccountID != "acc-1" {
		t.Fatalf("screen = %v, detail = %+v, want acc-1 detail", m.screen, m.detail.account)
	}
}

func TestEnableLiabilitiesCancellationIsShownAtEachStateBoundary(t *testing.T) {
	tests := []struct {
		name          string
		effect        func(*fakeService)
		wantSaved     bool
		wantEndpoints int
	}{
		{name: "before consent"},
		{
			name: "after saved flag and initial endpoint call",
			effect: func(f *fakeService) {
				f.consentSaved = true
				f.endpointCalls++
				f.data.LiabilitiesEnabled["item-1"] = true
			},
			wantSaved:     true,
			wantEndpoints: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := creditAccount("acc-1", "Chase")
			fake := &fakeService{
				data: app.AccountData{
					Accounts:           []model.AccountView{row},
					LiabilitiesEnabled: map[string]bool{"item-1": false},
				},
				enableEffect: tt.effect,
				enableErr:    context.Canceled,
			}
			m := ready(t, fake)
			openDetail(t, m, 0)
			press(t, m, codeKey(tea.KeyDown))
			if cmd := runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))); cmd != nil {
				t.Fatal("a cancelled enable started another operation")
			}

			if m.screen != recoveryScreen {
				t.Fatalf("screen after cancellation = %v, want recoveryScreen", m.screen)
			}
			body := content(m)
			for _, want := range []string{"context canceled", "Retry", "Main"} {
				if !strings.Contains(body, want) {
					t.Errorf("recovery view = %q, want %q", body, want)
				}
			}
			if fake.consentSaved != tt.wantSaved || fake.endpointCalls != tt.wantEndpoints {
				t.Fatalf("saved = %v, endpoint calls = %d, want %v and %d",
					fake.consentSaved, fake.endpointCalls, tt.wantSaved, tt.wantEndpoints)
			}

			press(t, m, codeKey(tea.KeyDown)) // Main
			runOperation(t, m, press(t, m, codeKey(tea.KeyEnter)))
			if got := m.liabilitiesEnabled["item-1"]; got != tt.wantSaved {
				t.Fatalf("flag after recovery refresh = %v, want %v", got, tt.wantSaved)
			}
		})
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

// The first sync of a just-linked item runs from the add setup. Landing back
// there hides the finished link and leaves a second billed link one action
// away.
func TestCancelledFirstSyncLandsOnTheAccountsScreen(t *testing.T) {
	fake := &fakeService{
		data:     app.AccountData{Accounts: []model.AccountView{account("acc-1", "", "Everyday Checking", "1234", "Chase", 10)}},
		linkItem: app.LinkedItem{ItemID: "item-new", Institution: "Bank of Nowhere"},
		syncErrs: []error{context.Canceled},
	}
	m := ready(t, fake)

	cmd := runOperation(t, m, startDefaultLink(t, m)) // Link
	if cmd = runOperation(t, m, cmd); cmd != nil {    // the cancelled first sync
		t.Fatal("a cancelled first sync started another command")
	}

	if m.screen == addSetupScreen {
		t.Fatal("a cancelled first sync left the user on add setup, where Continue links again")
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
	if len(fake.linkCalls) != 1 {
		t.Errorf("Link calls = %v, want only the one the user asked for", fake.linkCalls)
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
	if !aboveTheFooterRule(body, "Syncing") {
		t.Errorf("view while syncing = %q, want the running line above the rule of the footer", body)
	}

	// The command that starts the operation also starts the animation. Without
	// its first frame the glyph never moves, which is the frozen screen this
	// indicator exists to replace.
	msgs := runCmd(cmd)
	frameMsg, ok := firstTick(msgs)
	if !ok {
		t.Fatalf("starting the sync produced %v, want a spinner frame beside the result", msgs)
	}

	// A frame advances the spinner and asks for the next one.
	next := apply(m, frameMsg)
	if m.spinner.View() == frame {
		t.Error("the spinner did not advance on the first frame")
	}
	if next == nil {
		t.Fatal("a frame while the operation runs asked for no next frame")
	}
	frame = m.spinner.View()
	if apply(m, spinnerTick(m)) == nil || m.spinner.View() == frame {
		t.Error("the spinner did not advance on a later frame")
	}

	// A progress line replaces the label and keeps the spinner.
	m.Update(progressMsg("Fetching Chase"))
	if body := content(m); !strings.Contains(body, "Fetching Chase") ||
		!strings.Contains(body, ansi.Strip(m.spinner.View())) {
		t.Errorf("view = %q, want the spinner beside the progress line", body)
	}

	// The result of the run that already produced the frames above, so the
	// fake syncs once.
	_, next = m.Update(operationOf(t, msgs)) // SyncAll returns
	runOperation(t, m, next)                 // the closing account refresh
	if fake.syncAllCalls != 1 {
		t.Fatalf("SyncAll calls = %d, want exactly one", fake.syncAllCalls)
	}
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
	// The Sync row of the main menu carries its own "Last sync: …" note, so
	// the check is on the heading of the per-item summary, which is a line of
	// its own.
	if hasLine(body, "Last sync") {
		t.Errorf("main view = %q, want one outcome and not a second summary of the same run", body)
	}
	for _, label := range []string{"American Express", "Chase"} {
		if hasLine(body, label+" "+okMark+" 1 account") {
			t.Errorf("main view = %q, want no summary row for %q", body, label)
		}
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

// tokenNotSaved is the failure of a link whose item Plaid has already created.
func tokenNotSaved() error {
	return &app.TokenNotSavedError{Err: errors.New("write tokens.json: permission denied")}
}

// Plaid bills the item it created, and the token is the only handle to it. The
// retry must therefore save the token again. A retry that links again would
// create a second billed item and leave the first one unreachable.
func TestLinkSaveFailureRetriesTheSaveAndNotTheBank(t *testing.T) {
	fake := &fakeService{
		linkErr:      tokenNotSaved(),
		completeItem: app.LinkedItem{ItemID: "item-new", Institution: "TD Canada Trust"},
		syncAccounts: []model.AccountView{account("acc-1", "", "Everyday", "1234", "TD Canada Trust", 10)},
	}
	m := ready(t, fake)
	runOperation(t, m, startDefaultLink(t, m)) // the link, which fails at the save

	if m.screen != recoveryScreen {
		t.Fatalf("screen after the failed save = %v, want recoveryScreen", m.screen)
	}
	body := content(m)
	for _, want := range []string{tokenNotSavedMessage, recoveryRetry, recoveryMain} {
		if !strings.Contains(body, want) {
			t.Errorf("the recovery screen does not hold %q:\n%s", want, body)
		}
	}

	cmd := press(t, m, codeKey(tea.KeyEnter)) // Retry
	next := runOperation(t, m, cmd)
	if len(fake.linkCalls) != 1 {
		t.Fatalf("Link ran %d times, want the one link Plaid already billed", len(fake.linkCalls))
	}
	if len(fake.completeCalls) != 1 {
		t.Fatalf("CompleteLinkSave ran %d times, want once", len(fake.completeCalls))
	}
	if m.linked.ItemID != "item-new" || m.linked.Institution != "TD Canada Trust" {
		t.Errorf("linked = %+v, want the item the completed save returned", m.linked)
	}
	runOperation(t, m, next) // the first sync of the saved item
	if len(fake.syncCalls) != 1 || fake.syncCalls[0] != "item-new" {
		t.Errorf("sync calls = %v, want one sync of item-new", fake.syncCalls)
	}
	if m.screen != nicknameScreen {
		t.Errorf("screen after the completed save and its sync = %v, want nicknameScreen", m.screen)
	}
}

// A save that fails again keeps the token, so Retry stays a save.
func TestASecondSaveFailureStillRetriesTheSave(t *testing.T) {
	fake := &fakeService{linkErr: tokenNotSaved(), completeErr: tokenNotSaved()}
	m := ready(t, fake)
	runOperation(t, m, startDefaultLink(t, m)) // the failed link

	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry, which fails again
	if m.screen != recoveryScreen {
		t.Fatalf("screen after the second failure = %v, want recoveryScreen", m.screen)
	}
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry again
	if len(fake.completeCalls) != 2 {
		t.Errorf("CompleteLinkSave ran %d times, want twice", len(fake.completeCalls))
	}
	if len(fake.linkCalls) != 1 {
		t.Errorf("Link ran %d times, want the one link Plaid already billed", len(fake.linkCalls))
	}
}

// A failed save that reports a plain error is still a failed save: the token is
// still unsaved and Plaid still bills the item, so Retry must stay the save.
// This is the one case where the retry inside startLinkSave is load-bearing,
// because a *TokenNotSavedError would set that retry again from failed.
func TestASaveFailingWithAnOrdinaryErrorStillRetriesTheSave(t *testing.T) {
	fake := &fakeService{
		linkErr:     tokenNotSaved(),
		completeErr: errors.New("no unsaved link to complete"),
	}
	m := ready(t, fake)
	runOperation(t, m, startDefaultLink(t, m))             // the failed link
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry, which fails plainly
	if m.screen != recoveryScreen {
		t.Fatalf("screen after the plain failure = %v, want recoveryScreen", m.screen)
	}
	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry again

	if len(fake.linkCalls) != 1 {
		t.Errorf("Link ran %d times, want the one link Plaid already billed", len(fake.linkCalls))
	}
	if len(fake.completeCalls) != 2 {
		t.Errorf("CompleteLinkSave ran %d times, want twice", len(fake.completeCalls))
	}
}

// Every other link failure keeps the behaviour it has today: Retry links again,
// because Plaid created no item.
func TestAnOrdinaryLinkFailureStillRetriesTheLink(t *testing.T) {
	fake := &fakeService{linkErr: errors.New("plaid is unavailable")}
	m := ready(t, fake)
	runOperation(t, m, startDefaultLink(t, m))

	runOperation(t, m, press(t, m, codeKey(tea.KeyEnter))) // Retry
	if len(fake.linkCalls) != 2 {
		t.Errorf("Link ran %d times, want the retry to link again", len(fake.linkCalls))
	}
	if len(fake.completeCalls) != 0 {
		t.Errorf("CompleteLinkSave ran %d times, want none", len(fake.completeCalls))
	}
}

// The wording must say that the bank part succeeded, so the user does not read
// Retry as "try the bank again", and it must qualify possible charges.
func TestTokenNotSavedWordingQualifiesPossibleCharges(t *testing.T) {
	notes := tokenNotSavedNotes("TD Canada Trust", "item-new",
		errors.New("write tokens.json: permission denied"))
	joined := strings.Join(notes, "\n")
	flatNotes := strings.Join(notes, " ")

	for _, want := range []string{
		"TD Canada Trust is active at Plaid.",
		"On paid Production plans, subscription products can incur monthly charges under your Plaid agreement.",
		"Item id: item-new",
		"Reason: write tokens.json: permission denied",
		"Retry saves the token again. It does not open the bank a second time.",
		"Main leaves the active Item without a saved token.",
		"Without the token, fourseas cannot sync or remove the Item.",
		"Contact Plaid Support to remove an Item whose token was lost.",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the notes do not hold %q:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{
		"Plaid bills every live card each month",
		"Plaid bills this item each month",
		"Plaid created this item and bills it each month",
		"A second link creates another Item that can also incur charges.",
	} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("the notes contain %q:\n%s", unwanted, joined)
		}
	}
	if !strings.Contains(flatNotes, "A second link creates a second Item. On a paid Production plan, its subscription products can also incur charges under your Plaid agreement.") {
		t.Errorf("the second-Item warning is not qualified:\n%s", joined)
	}
}

// The failure screen carries the notes at every width. The notes hold the whole
// mitigation, so a narrow terminal must wrap them and must never cut them: a
// cut turns "It does not open the bank" into "It does n", which says the
// opposite.
func TestTokenNotSavedScreenKeepsTheMeaningAtEveryWidth(t *testing.T) {
	for _, width := range []int{40, 80, 100} {
		fake := &fakeService{linkErr: tokenNotSaved()}
		m := ready(t, fake)
		m.width = width
		m.height = 60
		runOperation(t, m, startDefaultLink(t, m))

		body := content(m)
		flat := strings.Join(strings.Fields(body), " ")
		for _, want := range []string{
			tokenNotSavedMessage,
			"Retry saves the token again. It does not open the bank a second time.",
			"Main leaves the active Item without a saved token.",
			"Without the token, fourseas cannot sync or remove the Item.",
			"Contact Plaid Support to remove an Item whose token was lost.",
			"A second link creates a second Item. On a paid Production plan, its subscription products can also incur charges under your Plaid agreement.",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("at width %d the recovery screen does not say %q:\n%s", width, want, body)
			}
		}
		for _, line := range strings.Split(body, "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("at width %d the line %q is %d cells wide", width, line, lipgloss.Width(line))
			}
		}
	}
}

func TestTokenNotSavedRecoveryFitsA40By24Terminal(t *testing.T) {
	m := ready(t, &fakeService{})
	m.width, m.height = 40, 24
	m.screen = recoveryScreen
	m.recovery = recoveryState{
		message:        tokenNotSavedMessage,
		compactMessage: tokenNotSavedCompactMessage,
		notes: tokenNotSavedNotes("TD Canada Trust", "item-new",
			errors.New("write tokens.json: permission denied")),
		compactNotes: tokenNotSavedCompactNotes("item-new"),
		retry:        func() tea.Cmd { return nil },
	}

	plain := ansi.Strip(m.View().Content)
	lines := strings.Split(plain, "\n")
	if len(lines) > m.height {
		t.Errorf("rendered lines = %d, want at most %d:\n%s", len(lines), m.height, plain)
	}
	visible := strings.Join(lines[:min(len(lines), m.height)], "\n")
	visibleFlat := strings.Join(strings.Fields(visible), " ")
	for _, want := range []string{
		"Item linked; token not saved.",
		"Item id: item-new",
		"No token: fourseas cannot sync/remove.",
		"Retry saves token; it does not relink.",
		"Main leaves the Item active.",
		"Cannot recover token? Contact Plaid Support.",
		"On paid Production plans, subscription products can incur charges under your Plaid agreement; a second link creates another Item whose products can incur them too.",
		"enter select",
	} {
		if !strings.Contains(visibleFlat, want) {
			t.Errorf("first %d lines do not hold %q:\n%s", m.height, want, visible)
		}
	}
	for _, choice := range []string{"› Retry", "Main"} {
		found := false
		for _, line := range lines[:min(len(lines), m.height)] {
			if strings.TrimSpace(line) == choice {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("first %d lines do not show the %q action:\n%s", m.height, choice, visible)
		}
	}
	for _, line := range lines {
		if width := lipgloss.Width(line); width > m.width {
			t.Errorf("line %q is %d cells wide, want at most %d", line, width, m.width)
		}
	}
}

// A note longer than the screen is wrapped word by word, and a word longer than
// the screen, such as a file path, is cut into whole lines instead of being
// dropped.
func TestWrapTextKeepsEveryWord(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{"a short note is one line", "Main leaves this item billed.", 40,
			[]string{"Main leaves this item billed."}},
		{"a long note breaks at a space", "It does not open the bank a second time.", 20,
			[]string{"It does not open the", "bank a second time."}},
		{"a word longer than the width is cut", "Reason: /a/very/long/path/tokens.json", 12,
			[]string{"Reason:", "/a/very/long", "/path/tokens", ".json"}},
		{"nothing is nothing", "", 10, []string{""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapText(tt.text, tt.width)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("wrapText(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
			}
			for _, line := range got {
				if lipgloss.Width(line) > tt.width {
					t.Errorf("line %q is wider than %d", line, tt.width)
				}
			}
		})
	}
}
