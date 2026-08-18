// Package tui is the account management terminal interface.
//
// It holds one root model. Every screen is a state of that model, and every
// long call to the application façade is one cancellable operation. The
// package renders text and reads keys: it holds no store, provider, or token
// code.
package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// service is everything the interface asks the application façade to do.
// *app.App satisfies it.
type service interface {
	Accounts(context.Context, string) (app.AccountData, error)
	Link(context.Context, int, app.Progress) (app.LinkedItem, error)
	CompleteLinkSave(app.PendingSave) (app.LinkedItem, error)
	SyncItem(context.Context, string, app.Progress) ([]model.AccountView, error)
	SyncAll(context.Context, app.Progress) ([]app.SyncResult, error)
	SetNickname(context.Context, string, string) error
	UnlinkPreview(context.Context, string) (app.UnlinkData, error)
	Unlink(context.Context, string, app.Progress) (app.UnlinkResult, error)
}

type screen uint8

const (
	mainScreen screen = iota
	accountsScreen
	detailScreen
	historyScreen
	customDaysScreen
	nicknameScreen
	unlinkScreen
	recoveryScreen
)

type menuState struct{ cursor int }

type accountsState struct {
	cursor   int
	rows     []model.AccountView
	newItems map[string]bool
}

type detailState struct {
	account model.AccountView
	cursor  int
}

type promptState struct {
	input textinput.Model
	err   error
}

type unlinkState struct {
	preview app.UnlinkData
	cursor  int
}

type recoveryState struct {
	message string
	// notes are the extra lines under the message. A failure the user has to
	// understand before choosing needs more than one line.
	notes  []string
	cursor int
	retry  func() tea.Cmd
}

// Model is the one owner of every screen's state.
type Model struct {
	app           service
	screen        screen
	width, height int
	running       bool
	runningKind   operation
	spinner       spinner.Model
	cancel        context.CancelFunc
	returnTo      screen
	status        string
	// success is the confirmed outcome of the last finished flow. It stays on
	// the screen until the next navigation or the next operation.
	success       string
	main          menuState
	history       menuState
	syncStates    []app.SyncState
	syncResults   []app.SyncResult
	accounts      accountsState
	detail        detailState
	prompt        promptState
	unlink        unlinkState
	recovery      recoveryState
	linked        app.LinkedItem
	nicknameQueue []model.AccountView
	nicknameIndex int
	send          func(tea.Msg)
	now           func() time.Time
}

// New builds the root model over one application façade.
func New(client service) *Model {
	return &Model{
		app:      client,
		accounts: accountsState{newItems: map[string]bool{}},
		spinner:  spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(spinnerStyle)),
		now:      time.Now,
	}
}

// Run opens the account management interface over one application façade and
// blocks until the user quits.
func Run(client service) error {
	m := New(client)
	program := tea.NewProgram(m)
	m.send = program.Send
	_, err := program.Run()
	return err
}

// operation says which call returned, so one message type carries every
// result.
type operation uint8

const (
	accountsOperation operation = iota
	linkOperation
	linkSaveOperation
	syncItemOperation
	syncAllOperation
	nicknameOperation
	unlinkPreviewOperation
	unlinkOperation
)

// progressMsg is one step of a long operation, sent from the operation's own
// goroutine through the program.
type progressMsg string

// operationMsg is the single result of one operation.
type operationMsg struct {
	kind  operation
	value any
	err   error
}

// start marks the model busy and returns the command that runs the operation.
// Only one operation runs at a time, so one cancel function is enough. The
// recovery state of the previous operation is dropped here, so a failure never
// offers the retry of an older operation. An operation that can be run again
// sets its own retry after this call.
func (m *Model) start(kind operation, run func(context.Context) (any, error)) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.returnTo = m.screen
	m.cancel = cancel
	m.running = true
	m.runningKind = kind
	m.recovery = recoveryState{}
	// The outcome of the last flow describes a run that is now over.
	m.success = ""
	// The first frame of the spinner runs beside the operation. The program
	// never gives a tea.BatchMsg to Update: it runs each command of the batch
	// and delivers each message on its own, so the operation result arrives as
	// it would from a single command.
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		value, err := run(ctx)
		return operationMsg{kind: kind, value: value, err: err}
	})
}

// report sends one progress line to the program. Tests drive Update directly
// and leave send nil, so the guard is required.
func (m *Model) report(line string) {
	if m.send != nil {
		m.send(progressMsg(line))
	}
}

// Init starts the first account refresh as an operation, so the model is busy
// before the store is opened and a key cannot open it a second time.
func (m *Model) Init() tea.Cmd {
	return m.refreshAccounts()
}

// refreshAccounts reads every account and sync state in one call.
func (m *Model) refreshAccounts() tea.Cmd {
	return m.start(accountsOperation, func(ctx context.Context) (any, error) {
		return m.app.Accounts(ctx, "")
	})
}

// startLink opens the provider's link flow for one history length. A failure
// can be run again with the same length.
func (m *Model) startLink(days int) tea.Cmd {
	cmd := m.start(linkOperation, func(ctx context.Context) (any, error) {
		return m.app.Link(ctx, days, m.report)
	})
	m.recovery.retry = func() tea.Cmd { return m.startLink(days) }
	return cmd
}

// startLinkSave writes the access token of an item Plaid has already created.
// It is the only retry a failed save may offer: the browser step must not run
// again, because it would create a second billed item at the same bank.
func (m *Model) startLinkSave(pending app.PendingSave) tea.Cmd {
	cmd := m.start(linkSaveOperation, func(context.Context) (any, error) {
		return m.app.CompleteLinkSave(pending)
	})
	m.recovery.retry = func() tea.Cmd { return m.startLinkSave(pending) }
	return cmd
}

// startSyncItem fetches the first data of one linked institution. The item is
// already saved, so a failure can be run again with the same item.
func (m *Model) startSyncItem(itemID string) tea.Cmd {
	cmd := m.start(syncItemOperation, func(ctx context.Context) (any, error) {
		return m.app.SyncItem(ctx, itemID, m.report)
	})
	m.recovery.retry = func() tea.Cmd { return m.startSyncItem(itemID) }
	return cmd
}

// startNickname gives one account the name the user typed.
func (m *Model) startNickname(accountID, nickname string) tea.Cmd {
	return m.start(nicknameOperation, func(ctx context.Context) (any, error) {
		return nil, m.app.SetNickname(ctx, accountID, nickname)
	})
}

// startSyncAll fetches new data for every linked institution in one call. The
// summary of the previous run is dropped here, so a result line always belongs
// to the run the user just asked for.
func (m *Model) startSyncAll() tea.Cmd {
	m.syncResults = nil
	cmd := m.start(syncAllOperation, func(ctx context.Context) (any, error) {
		return m.app.SyncAll(ctx, m.report)
	})
	m.recovery.retry = func() tea.Cmd { return m.startSyncAll() }
	return cmd
}

// startUnlinkPreview reads what a removal would delete. Nothing is changed yet,
// so a failure can be run again with the same item.
func (m *Model) startUnlinkPreview(itemID string) tea.Cmd {
	cmd := m.start(unlinkPreviewOperation, func(ctx context.Context) (any, error) {
		return m.app.UnlinkPreview(ctx, itemID)
	})
	m.recovery.retry = func() tea.Cmd { return m.startUnlinkPreview(itemID) }
	return cmd
}

// startUnlink removes one institution and everything stored for it. App.Unlink
// stops at the first step that fails and leaves the rest in place, so a failure
// can be run again with the same item.
func (m *Model) startUnlink(itemID string) tea.Cmd {
	cmd := m.start(unlinkOperation, func(ctx context.Context) (any, error) {
		return m.app.Unlink(ctx, itemID, m.report)
	})
	m.recovery.retry = func() tea.Cmd { return m.startUnlink(itemID) }
	return cmd
}

// Update applies one message. While an operation runs, only progress, the
// operation result, a spinner frame, a resize, and ctrl+c are accepted.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// An open prompt has to scroll inside the new width, or a long value
		// runs past the cut with its caret.
		m.resizePrompt()
		return m, nil
	case progressMsg:
		m.status = string(msg)
		return m, nil
	case operationMsg:
		return m, m.finish(msg)
	case spinner.TickMsg:
		return m, m.advanceSpinner(msg)
	case tea.KeyPressMsg:
		return m, m.keyPress(msg)
	}
	return m, nil
}

// advanceSpinner moves the running indicator on one frame and asks for the
// next frame. Each frame schedules the one after it, so the chain has to end
// where the operation ends: a frame that arrives with nothing running returns
// no command, and the interface stops ticking.
//
// One operation may start the next one, as a sync starts the closing account
// refresh, so a second chain can begin while a frame of the first is still in
// flight. Two chains would run the spinner at twice its speed. spinner.Model
// prevents that itself: it counts every frame it accepts and rejects a frame
// that carries an older count. Keep the frame message it returns, and give it
// back unchanged.
func (m *Model) advanceSpinner(msg spinner.TickMsg) tea.Cmd {
	if !m.running {
		return nil
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return cmd
}

// keyPress applies one key and drops the outcome of the last flow when the key
// moved the user to another screen. The outcome describes the screen it was
// written on, so it must not follow the user.
func (m *Model) keyPress(msg tea.KeyPressMsg) tea.Cmd {
	from := m.screen
	cmd := m.key(msg)
	if m.screen != from {
		m.success = ""
	}
	return cmd
}

// finish applies one operation result.
func (m *Model) finish(msg operationMsg) tea.Cmd {
	m.running = false
	m.cancel = nil
	m.status = ""

	// A cancellation is what the user asked for, not a failure. A destructive
	// operation is the exception: it changes data at Plaid before it changes
	// anything here, so whatever it reports has to be shown.
	if errors.Is(msg.err, context.Canceled) && !destructive(msg.kind) {
		return m.cancelled(msg.kind)
	}
	if msg.err != nil {
		return m.failed(msg)
	}

	switch msg.kind {
	case accountsOperation:
		data, _ := msg.value.(app.AccountData)
		m.accounts.rows = data.Accounts
		m.accounts.cursor = clampCursor(m.accounts.cursor, len(data.Accounts))
		m.syncStates = data.States
		m.refreshDetail()
	case linkOperation, linkSaveOperation:
		// A completed save ends where a successful link ends, because both
		// leave the same saved item behind.
		// Only the non-secret metadata of the new item is kept.
		item, _ := msg.value.(app.LinkedItem)
		m.linked = app.LinkedItem{ItemID: item.ItemID, Institution: item.Institution}
		return m.startSyncItem(m.linked.ItemID)
	case syncItemOperation:
		accounts, _ := msg.value.([]model.AccountView)
		return m.askNicknames(accounts)
	case syncAllOperation:
		results, _ := msg.value.([]app.SyncResult)
		return m.finishSyncAll(results)
	case nicknameOperation:
		if m.naming() {
			return m.nextNickname()
		}
		return m.finishRename()
	case unlinkPreviewOperation:
		preview, _ := msg.value.(app.UnlinkData)
		m.unlink = unlinkState{preview: preview}
		m.screen = unlinkScreen
	case unlinkOperation:
		return m.finishUnlink()
	}
	return nil
}

// destructive says whether the operation changes stored or billed state. Such
// an operation cannot be treated as if nothing happened.
func destructive(kind operation) bool { return kind == unlinkOperation }

// cancelled leaves one cancelled operation behind.
//
// The screen the operation started from is the right place to land, except
// after the first sync of a just-linked item: that screen is the history menu,
// where enter would create a second billed item at the same bank. The account
// list is shown instead, with a line saying the link did succeed.
func (m *Model) cancelled(kind operation) tea.Cmd {
	switch kind {
	case linkOperation:
		m.linked = app.LinkedItem{}
	case syncItemOperation:
		if m.linked.ItemID != "" {
			m.status = "Linked " + linkedName(m.linked) + ". Choose Sync to fetch its data."
			m.linked = app.LinkedItem{}
			m.screen = accountsScreen
			return nil
		}
	}
	m.screen = m.returnTo
	return nil
}

// linkedName is the institution of a new link, or its item id while no name is
// known.
func linkedName(linked app.LinkedItem) string {
	if linked.Institution != "" {
		return linked.Institution
	}
	return linked.ItemID
}

// finishSyncAll keeps the summary of every item and reports a failure once.
// SyncAll carries on after one item fails, so a failed item arrives as a result
// and not as the error of the operation.
//
// A run where every institution returned data is reported as one success line
// instead. The per-item summary of such a run says the same thing a second
// time, so it is dropped and the user reads one outcome.
func (m *Model) finishSyncAll(results []app.SyncResult) tea.Cmd {
	m.syncResults = results
	for _, result := range results {
		if result.Err != nil {
			// The retry the operation left is still the whole sync, which is
			// what Retry must run.
			return m.showFailure(result.Err)
		}
	}
	synced, clean := syncedLabels(results)
	if clean {
		m.syncResults = nil
	}
	// The refresh clears the outcome of the last flow, so this one is written
	// after the call that starts it.
	cmd := m.refreshAccounts()
	if clean {
		m.success = "Synced " + strings.Join(synced, ", ")
	}
	return cmd
}

// syncedLabels is the institution of every item that returned data, and
// whether the run was clean: at least one item synced and no item was skipped.
// A skipped item is not a success, so it keeps the per-item summary.
func syncedLabels(results []app.SyncResult) ([]string, bool) {
	labels := make([]string, 0, len(results))
	for _, result := range results {
		if result.Skipped {
			return nil, false
		}
		labels = append(labels, result.Label)
	}
	return labels, len(labels) > 0
}

// finishRename closes the rename prompt and reads the stored account again, so
// the detail screen shows the saved name and not the typed one.
func (m *Model) finishRename() tea.Cmd {
	m.prompt.input.Blur()
	m.prompt = promptState{}
	m.screen = detailScreen
	return m.refreshAccounts()
}

// finishUnlink drops every trace of the removed institution from this session
// and shows the account list with the fresh data.
func (m *Model) finishUnlink() tea.Cmd {
	for _, view := range m.unlink.preview.Accounts {
		delete(m.accounts.newItems, view.AccountID)
	}
	m.unlink = unlinkState{}
	m.detail = detailState{}
	m.screen = accountsScreen
	return m.refreshAccounts()
}

// refreshDetail points the detail screen at the row the last refresh returned,
// so a saved change is shown instead of the copy the screen was opened with.
func (m *Model) refreshDetail() {
	if m.detail.account.AccountID == "" {
		return
	}
	for _, row := range m.accounts.rows {
		if row.AccountID == m.detail.account.AccountID {
			m.detail.account = row
			return
		}
	}
}

// failed shows one failure. A name the store refused stays on its own prompt,
// because the user can correct the text and try again. Every other failure
// opens the recovery screen, which offers the retry the operation left.
func (m *Model) failed(msg operationMsg) tea.Cmd {
	if msg.kind == nicknameOperation {
		m.prompt.err = msg.err
		m.screen = nicknameScreen
		return m.prompt.input.Focus()
	}
	// The summary of an older sync does not describe this failure.
	m.syncResults = nil
	var notSaved *app.TokenNotSavedError
	if errors.As(msg.err, &notSaved) {
		return m.showTokenNotSaved(notSaved)
	}
	return m.showFailure(msg.err)
}

// showTokenNotSaved opens the recovery screen of a link whose item Plaid has
// already created and already bills. The retry saves the token again, and the
// notes say that the bank part is done, so the user does not read Retry as
// "try the bank again".
func (m *Model) showTokenNotSaved(err *app.TokenNotSavedError) tea.Cmd {
	pending := err.Pending
	m.showFailure(err)
	m.recovery.message = tokenNotSavedMessage
	m.recovery.notes = tokenNotSavedNotes(pending.Institution(), pending.ItemID(), err.Err)
	m.recovery.retry = func() tea.Cmd { return m.startLinkSave(pending) }
	return nil
}

// showFailure opens the recovery screen for one error, with the ways out the
// failed operation left.
func (m *Model) showFailure(err error) tea.Cmd {
	m.recovery.message = displayError(err)
	m.recovery.cursor = 0
	m.screen = recoveryScreen
	return nil
}

// key applies the common key rules in one order.
func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		if !m.running {
			return tea.Quit
		}
		// Keep the interface alive until the operation returns its result.
		if m.cancel != nil {
			m.cancel()
		}
		m.status = "Cancelling"
		return nil
	}
	if m.running {
		return nil
	}
	if isPrompt(m.screen) {
		return m.promptKey(msg)
	}
	return m.menuKey(msg)
}

// promptKey gives every character key to the text input, and keeps enter and
// esc for the prompt itself.
func (m *Model) promptKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEsc:
		// Esc on a queued name skips that one account instead of leaving the
		// add flow. Every other prompt closes and returns one screen.
		if m.screen == nicknameScreen && m.naming() {
			return m.nextNickname()
		}
		m.closePrompt()
		return nil
	case tea.KeyEnter:
		return m.submitPrompt()
	}
	var cmd tea.Cmd
	m.prompt.input, cmd = m.prompt.input.Update(msg)
	return cmd
}

// submitPrompt runs the action behind the active prompt. Each flow that opens
// a prompt adds its own case.
func (m *Model) submitPrompt() tea.Cmd {
	switch m.screen {
	case customDaysScreen:
		return m.submitCustomDays()
	case nicknameScreen:
		if m.naming() {
			return m.startNickname(m.nicknameQueue[m.nicknameIndex].AccountID, m.prompt.input.Value())
		}
		// An empty queue means the prompt was opened to rename one account.
		return m.startNickname(m.detail.account.AccountID, m.prompt.input.Value())
	}
	return nil
}

// submitCustomDays reads a free history length. A value the provider would
// refuse keeps the prompt open with the reason, so the user can correct it.
func (m *Model) submitCustomDays() tea.Cmd {
	days, err := strconv.Atoi(strings.TrimSpace(m.prompt.input.Value()))
	if err != nil || days < app.MinLinkDays || days > app.MaxLinkDays {
		m.prompt.err = errHistoryRange
		return nil
	}
	m.prompt.input.Blur()
	m.prompt = promptState{}
	m.screen = historyScreen
	return m.startLink(days)
}

// naming says whether the add flow is still offering a name for a queued
// account. A rename from the account detail leaves the queue empty.
func (m *Model) naming() bool { return m.nicknameIndex < len(m.nicknameQueue) }

// askNicknames offers a name for every account the first sync returned, one
// account at a time.
func (m *Model) askNicknames(accounts []model.AccountView) tea.Cmd {
	m.nicknameQueue = accounts
	m.nicknameIndex = 0
	return m.askNickname()
}

// askNickname opens the prompt of the account at the current index, or closes
// the add flow once every account has been offered a name.
func (m *Model) askNickname() tea.Cmd {
	if !m.naming() {
		return m.finishAdd()
	}
	return m.openPrompt(nicknameScreen, accountName(m.nicknameQueue[m.nicknameIndex]), "")
}

// nextNickname moves to the next account. A saved name and a skipped name both
// advance by one.
func (m *Model) nextNickname() tea.Cmd {
	m.nicknameIndex++
	return m.askNickname()
}

// finishAdd marks every account of the finished add flow as new for the rest
// of this session, and returns to the main menu with the outcome of the flow.
// The user started the flow there, so the confirmation belongs there, on a
// screen whose account count and sync state the closing refresh makes current.
func (m *Model) finishAdd() tea.Cmd {
	m.prompt.input.Blur()
	m.prompt = promptState{}
	for _, view := range m.nicknameQueue {
		m.accounts.newItems[view.AccountID] = true
	}
	added := len(m.nicknameQueue)
	m.nicknameQueue, m.nicknameIndex = nil, 0
	m.screen = mainScreen
	// The refresh clears the outcome of the last flow, so this one is written
	// after the call that starts it.
	cmd := m.refreshAccounts()
	m.success = "Added " + addedName(m.linked, added)
	return cmd
}

// addedName is what the finished add flow names: the institution of the new
// link, or the number of accounts it returned while no name is known.
func addedName(linked app.LinkedItem, accounts int) string {
	if name := linkedName(linked); name != "" {
		return name
	}
	return plural(accounts, "account")
}

// menuKey moves the cursor of a list, opens the selection, and leaves.
func (m *Model) menuKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyUp:
		m.moveCursor(-1)
	case tea.KeyDown:
		m.moveCursor(1)
	case tea.KeyEnter:
		return m.activate()
	case tea.KeyEsc:
		if m.screen == unlinkScreen {
			// Leaving the confirmation is a cancellation, so the preview of a
			// removal that never ran is dropped.
			m.unlink = unlinkState{}
		}
		m.screen = parent(m.screen)
	default:
		if msg.String() == "q" && quittable(m.screen) {
			return tea.Quit
		}
	}
	return nil
}

// moveCursor moves the cursor of the active list and keeps it inside the list.
func (m *Model) moveCursor(delta int) {
	switch m.screen {
	case mainScreen:
		m.main.cursor = clampCursor(m.main.cursor+delta, len(mainChoices))
	case accountsScreen:
		m.accounts.cursor = clampCursor(m.accounts.cursor+delta, len(m.accounts.rows))
	case detailScreen:
		m.detail.cursor = clampCursor(m.detail.cursor+delta, len(detailActions))
	case unlinkScreen:
		m.unlink.cursor = clampCursor(m.unlink.cursor+delta, len(unlinkActions))
	case historyScreen:
		m.history.cursor = clampCursor(m.history.cursor+delta, len(historyChoices))
	case recoveryScreen:
		m.recovery.cursor = clampCursor(m.recovery.cursor+delta, len(m.recovery.choices()))
	}
}

// activate opens what the cursor points at.
func (m *Model) activate() tea.Cmd {
	switch m.screen {
	case mainScreen:
		switch m.main.cursor {
		case choiceAccounts:
			m.accounts.cursor = clampCursor(m.accounts.cursor, len(m.accounts.rows))
			m.screen = accountsScreen
		case choiceAdd:
			// The longest history is the default, because Plaid fixes the
			// amount when the item is created.
			m.history = menuState{}
			m.screen = historyScreen
		case choiceSync:
			return m.startSyncAll()
		}
	case accountsScreen:
		if m.accounts.cursor < len(m.accounts.rows) {
			m.detail = detailState{account: m.accounts.rows[m.accounts.cursor]}
			m.screen = detailScreen
		}
	case detailScreen:
		return m.activateDetail()
	case unlinkScreen:
		if m.unlink.cursor == unlinkConfirm {
			return m.startUnlink(m.unlink.preview.ItemID)
		}
		m.unlink = unlinkState{}
		m.screen = parent(unlinkScreen)
	case historyScreen:
		if m.history.cursor >= len(historyDays) {
			return m.openPrompt(customDaysScreen, "days", "")
		}
		return m.startLink(historyDays[m.history.cursor])
	case recoveryScreen:
		return m.recoverWith(m.recovery.choices()[m.recovery.cursor])
	}
	return nil
}

// activateDetail runs the action the user chose for one account. The rename
// prompt starts from the name the account holds now, and an empty value clears
// that name.
func (m *Model) activateDetail() tea.Cmd {
	account := m.detail.account
	if m.detail.cursor == detailUnlink {
		return m.startUnlinkPreview(account.ItemID)
	}
	return m.openPrompt(nicknameScreen, accountName(account), account.Nickname)
}

// recoverWith runs the way out of a failure the user chose. Main always leaves
// the failure behind with a fresh account list, because the failed operation
// may still have changed the stored data.
func (m *Model) recoverWith(choice string) tea.Cmd {
	if choice == recoveryRetry && m.recovery.retry != nil {
		// The retry belongs to the screen the failed operation ran on, which
		// returnTo still holds. Leaving the recovery screen up would show a
		// cleared failure while the retry runs, and would make a cancelled
		// retry return to the failure instead of to that screen.
		m.screen = m.returnTo
		return m.recovery.retry()
	}
	m.screen = mainScreen
	return m.refreshAccounts()
}

// openPrompt shows one text prompt with the value it starts from.
func (m *Model) openPrompt(target screen, placeholder, value string) tea.Cmd {
	input := textinput.New()
	input.Placeholder = placeholder
	input.SetValue(value)
	m.prompt = promptState{input: input}
	m.resizePrompt()
	m.screen = target
	return m.prompt.input.Focus()
}

// closePrompt drops the text and returns one screen.
func (m *Model) closePrompt() {
	m.prompt.input.Blur()
	m.prompt = promptState{}
	m.screen = parent(m.screen)
}

// isPrompt says whether the screen reads free text.
func isPrompt(s screen) bool {
	return s == customDaysScreen || s == nicknameScreen
}

// quittable says whether q leaves the program from this screen. It must not
// quit from a prompt, where q is a character.
func quittable(s screen) bool {
	return s == mainScreen || s == accountsScreen || s == detailScreen
}

// parent is the screen that esc returns to.
func parent(s screen) screen {
	switch s {
	case detailScreen, unlinkScreen:
		return accountsScreen
	case customDaysScreen:
		return historyScreen
	case nicknameScreen:
		return detailScreen
	}
	return mainScreen
}

// clampCursor keeps a cursor inside a list of the given length.
func clampCursor(cursor, length int) int {
	if cursor < 0 || length == 0 {
		return 0
	}
	if cursor >= length {
		return length - 1
	}
	return cursor
}
