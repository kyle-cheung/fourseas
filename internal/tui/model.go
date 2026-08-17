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
	"time"

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
	cursor  int
	retry   func() tea.Cmd
}

// Model is the one owner of every screen's state.
type Model struct {
	app           service
	screen        screen
	width, height int
	running       bool
	cancel        context.CancelFunc
	returnTo      screen
	status        string
	main          menuState
	history       menuState
	syncStates    []app.SyncState
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
		now:      time.Now,
	}
}

// operation says which call returned, so one message type carries every
// result.
type operation uint8

const (
	accountsOperation operation = iota
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
// Only one operation runs at a time, so one cancel function is enough.
func (m *Model) start(kind operation, run func(context.Context) (any, error)) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.returnTo = m.screen
	m.cancel = cancel
	m.running = true
	return func() tea.Msg {
		value, err := run(ctx)
		return operationMsg{kind: kind, value: value, err: err}
	}
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

// Update applies one message. While an operation runs, only progress, the
// operation result, a resize, and ctrl+c are accepted.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case progressMsg:
		m.status = string(msg)
		return m, nil
	case operationMsg:
		return m, m.finish(msg)
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

// finish applies one operation result.
func (m *Model) finish(msg operationMsg) tea.Cmd {
	m.running = false
	m.cancel = nil
	m.status = ""

	// A cancellation is what the user asked for, not a failure.
	if errors.Is(msg.err, context.Canceled) {
		m.screen = m.returnTo
		return nil
	}
	if msg.err != nil {
		m.recovery = recoveryState{message: displayError(msg.err)}
		m.screen = recoveryScreen
		return nil
	}

	switch msg.kind {
	case accountsOperation:
		data, _ := msg.value.(app.AccountData)
		m.accounts.rows = data.Accounts
		m.accounts.cursor = clampCursor(m.accounts.cursor, len(data.Accounts))
		m.syncStates = data.States
	}
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
func (m *Model) submitPrompt() tea.Cmd { return nil }

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
	}
}

// activate opens what the cursor points at.
func (m *Model) activate() tea.Cmd {
	switch m.screen {
	case mainScreen:
		if m.main.cursor == choiceAccounts {
			m.accounts.cursor = clampCursor(m.accounts.cursor, len(m.accounts.rows))
			m.screen = accountsScreen
		}
	case accountsScreen:
		if m.accounts.cursor < len(m.accounts.rows) {
			m.detail = detailState{account: m.accounts.rows[m.accounts.cursor]}
			m.screen = detailScreen
		}
	}
	return nil
}

// openPrompt shows one text prompt with the value it starts from.
func (m *Model) openPrompt(target screen, placeholder, value string) tea.Cmd {
	input := textinput.New()
	input.Placeholder = placeholder
	input.SetValue(value)
	m.prompt = promptState{input: input}
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
