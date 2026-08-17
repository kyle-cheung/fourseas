package tui

import (
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// mainChoices is the fixed order of the main screen.
var mainChoices = []string{"Accounts", "Add account", "Sync"}

// Positions inside mainChoices.
const (
	choiceAccounts = iota
	choiceAdd
	choiceSync
)

// defaultWidth is the width used before the first resize message arrives.
const defaultWidth = 80

// cursorMark and blankMark keep every row of a list in the same column.
const (
	cursorMark = "> "
	blankMark  = "  "
)

// View renders the active screen. The interface owns the whole terminal, so it
// runs in the alternate screen buffer.
func (m *Model) View() tea.View {
	view := tea.NewView(m.body())
	view.AltScreen = true
	return view
}

// body is the text of the active screen, with the progress line last.
func (m *Model) body() string {
	var lines []string
	switch m.screen {
	case mainScreen:
		lines = m.mainLines()
	case accountsScreen:
		lines = m.accountLines()
	case detailScreen:
		lines = m.detailLines()
	case customDaysScreen, nicknameScreen:
		lines = m.promptLines()
	case recoveryScreen:
		lines = m.recoveryLines()
	default:
		lines = []string{header("Accounts"), "", footer("esc back · ctrl+c quit")}
	}
	if m.status != "" {
		lines = append(lines, "", blankMark+m.status)
	}
	return strings.Join(lines, "\n")
}

// mainLines is the main menu, with the account count on the first choice and
// the sync status on the third.
func (m *Model) mainLines() []string {
	notes := map[int]string{
		choiceAccounts: "(" + strconv.Itoa(len(m.accounts.rows)) + ")",
		choiceSync:     syncStatus(m.syncStates, m.clock()),
	}

	lines := []string{header("Main menu"), ""}
	for i, choice := range mainChoices {
		row := mark(i == m.main.cursor) + choice
		if note := notes[i]; note != "" {
			row += "  " + note
		}
		lines = append(lines, truncate(row, m.contentWidth()))
	}
	return append(lines, "", footer("↑/↓ move · enter select · q quit"))
}

// accountLines is every stored account, one row each.
func (m *Model) accountLines() []string {
	lines := []string{header("Accounts"), ""}
	if len(m.accounts.rows) == 0 {
		lines = append(lines, blankMark+"No accounts are linked yet.")
		return append(lines, "", footer("esc back · q quit"))
	}
	for i, row := range m.accounts.rows {
		lines = append(lines, mark(i == m.accounts.cursor)+
			accountRow(row, m.accounts.newItems[row.AccountID], m.contentWidth()-lipgloss.Width(blankMark)))
	}
	return append(lines, "", footer("↑/↓ move · enter open · esc back · q quit"))
}

// detailLines is everything stored about one account.
func (m *Model) detailLines() []string {
	account := m.detail.account
	lines := []string{header(accountName(account)), ""}
	for _, field := range [][2]string{
		{"Institution", account.InstitutionName},
		{"Account", account.Name},
		{"Mask", maskText(account)},
		{"Type", strings.TrimSpace(account.Type + " " + account.Subtype)},
		{"Balance", strings.TrimSpace(balanceText(account) + " " + account.Currency)},
		{"Account id", account.AccountID},
	} {
		if field[1] == "" {
			continue
		}
		lines = append(lines, truncate(blankMark+field[0]+": "+field[1], m.contentWidth()))
	}
	return append(lines, "", footer("esc back · q quit"))
}

// promptLines is one text prompt with its own error, if it has one.
func (m *Model) promptLines() []string {
	lines := []string{header(promptTitle(m.screen)), "", blankMark + m.prompt.input.View()}
	if m.prompt.err != nil {
		lines = append(lines, "", blankMark+displayError(m.prompt.err))
	}
	return append(lines, "", footer("enter accept · esc cancel"))
}

// recoveryLines shows what failed.
func (m *Model) recoveryLines() []string {
	return []string{header("Something went wrong"), "", blankMark + m.recovery.message,
		"", footer("esc back · ctrl+c quit")}
}

// promptTitle names the prompt of one screen.
func promptTitle(s screen) string {
	if s == nicknameScreen {
		return "Name this account"
	}
	return "How many days of history"
}

// header is the title line of one screen.
func header(title string) string { return blankMark + "fourseas — " + title }

// footer is the key help line of one screen.
func footer(keys string) string { return blankMark + keys }

// accountName is the nickname, then the provider's name, then the raw account
// id when neither is known yet.
func accountName(view model.AccountView) string {
	if view.Nickname != "" {
		return view.Nickname
	}
	if view.Name != "" {
		return view.Name
	}
	return view.AccountID
}

// accountRow is one line of the account list. The name comes first and is
// never dropped: every other detail is added only while the width allows it.
func accountRow(view model.AccountView, isNew bool, width int) string {
	row := accountName(view)
	extras := []string{maskText(view), view.InstitutionName, view.Currency, balanceText(view)}
	if isNew {
		extras = append(extras, "NEW")
	}
	for _, extra := range extras {
		if extra == "" {
			continue
		}
		candidate := row + "  " + extra
		if lipgloss.Width(candidate) > width {
			break
		}
		row = candidate
	}
	return truncate(row, width)
}

// maskText is the last digits of the account number, marked as a partial
// number, or empty when the provider sent none.
func maskText(view model.AccountView) string {
	if view.Mask == "" {
		return ""
	}
	return "••" + view.Mask
}

// balanceText is the current balance, or empty when no sync has returned one.
func balanceText(view model.AccountView) string {
	if !view.BalanceCurrent.Valid {
		return ""
	}
	return view.BalanceCurrent.Decimal.StringFixed(2)
}

// syncStatus is one line about the last sync of every linked institution. A
// failure of any institution is reported even when a later sync succeeded,
// because the failed institution still holds stale data.
func syncStatus(states []app.SyncState, now time.Time) string {
	var failed, ok *app.SyncState
	for i := range states {
		state := &states[i]
		if state.LastStatus == "ok" {
			if state.LastSyncedAt != nil && (ok == nil || state.LastSyncedAt.After(*ok.LastSyncedAt)) {
				ok = state
			}
			continue
		}
		if failed == nil || newer(state.LastSyncedAt, failed.LastSyncedAt) {
			failed = state
		}
	}

	switch {
	case failed != nil && failed.LastSyncedAt != nil:
		return "Sync failed: " + failed.Institution + " (" + since(*failed.LastSyncedAt, now) + ")"
	case failed != nil:
		return "Sync failed: " + failed.Institution
	case ok != nil:
		return "Last synced " + since(*ok.LastSyncedAt, now)
	}
	return "Never synced"
}

// newer says whether a is after b. A missing time is the oldest.
func newer(a, b *time.Time) bool {
	if a == nil {
		return false
	}
	return b == nil || a.After(*b)
}

// since is how long ago a time was, in whole units.
func since(then, now time.Time) string {
	elapsed := now.Sub(then)
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return count(int(elapsed.Minutes()), "minute")
	case elapsed < 24*time.Hour:
		return count(int(elapsed.Hours()), "hour")
	}
	return count(int(elapsed.Hours()/24), "day")
}

// count is a whole number of units, written as a time in the past.
func count(n int, unit string) string {
	if n == 1 {
		return "1 " + unit + " ago"
	}
	return strconv.Itoa(n) + " " + unit + "s ago"
}

// displayError is the only place a stored error becomes user wording.
func displayError(err error) string {
	if errors.Is(err, app.ErrProductNotReady) {
		return "The bank is still preparing the data"
	}
	return err.Error()
}

// mark is the cursor column of one list row.
func mark(selected bool) string {
	if selected {
		return cursorMark
	}
	return blankMark
}

// truncate cuts one line to the width of the terminal without splitting a
// character.
func truncate(line string, width int) string {
	if width <= 0 {
		return line
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

// contentWidth is the width of the terminal, or a usable default before the
// first resize message.
func (m *Model) contentWidth() int {
	if m.width > 0 {
		return m.width
	}
	return defaultWidth
}

// clock is the current time, which tests replace.
func (m *Model) clock() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}
