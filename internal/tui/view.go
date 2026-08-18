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

// detailActions is the fixed order of the account detail screen.
var detailActions = []string{"Rename", "Unlink institution"}

// Positions inside detailActions.
const (
	detailRename = iota
	detailUnlink
)

// unlinkActions is the fixed order of the removal confirmation. Cancel is
// first, so the cursor never starts on the action that deletes data.
var unlinkActions = []string{"Cancel", "Unlink"}

// Positions inside unlinkActions.
const (
	unlinkCancel = iota
	unlinkConfirm
)

// historyDays is how much history each fixed choice of the history screen asks
// for. The first is the most a link may ask for, and is the default.
var historyDays = []int{app.MaxLinkDays, 365, 90}

// historyChoices is the order of the history screen, with the free choice
// last.
var historyChoices = historyMenu(historyDays)

// historyMenu writes one choice for every fixed history length, then the free
// choice.
func historyMenu(days []int) []string {
	choices := make([]string, 0, len(days)+1)
	for _, count := range days {
		choices = append(choices, strconv.Itoa(count)+" days")
	}
	return append(choices, "Custom")
}

// errHistoryRange is what a history length the provider would refuse shows on
// the prompt.
var errHistoryRange = errors.New("history must be a whole number of days from " +
	strconv.Itoa(app.MinLinkDays) + " through " + strconv.Itoa(app.MaxLinkDays))

// The ways out of a failure.
const (
	recoveryRetry = "Retry"
	recoveryMain  = "Main"
)

// choices are the ways out of this failure. Retry is offered only when the
// failed operation can be run again as it was.
func (r recoveryState) choices() []string {
	if r.retry != nil {
		return []string{recoveryRetry, recoveryMain}
	}
	return []string{recoveryMain}
}

// defaultWidth is the width used before the first resize message arrives.
const defaultWidth = 80

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
	case historyScreen:
		lines = m.historyLines()
	case customDaysScreen, nicknameScreen:
		lines = m.promptLines()
	case unlinkScreen:
		lines = m.unlinkLines()
	case recoveryScreen:
		lines = m.recoveryLines()
	default:
		lines = append(m.header("Accounts"), m.footer("esc back · ctrl+c quit")...)
	}
	// A progress line carries a whole provider error behind a padded label, so
	// it is the longest string the interface ever shows. Left whole it wraps
	// and pushes the footer out of the screen.
	switch {
	case m.running:
		lines = append(lines, "", truncate(blankMark+m.spinner.View()+" "+
			mutedStyle.Render(m.runningLine()), m.contentWidth()))
	case m.status != "":
		lines = append(lines, "", truncate(blankMark+mutedStyle.Render(m.status), m.contentWidth()))
	}
	return strings.Join(lines, "\n")
}

// runningLine says what the interface is waiting for: the newest step of the
// operation, or the name of the operation while it has reported no step. Every
// operation takes long enough to look frozen, so each one has a label.
func (m *Model) runningLine() string {
	if m.status != "" {
		return m.status
	}
	switch m.runningKind {
	case linkOperation:
		return "Linking"
	case syncItemOperation, syncAllOperation:
		return "Syncing"
	case nicknameOperation:
		return "Saving"
	case unlinkPreviewOperation:
		return "Reading"
	case unlinkOperation:
		return "Removing"
	}
	return "Loading"
}

// successLines is the outcome of the last finished flow. It sits above the
// rule of the footer, so the confirmation is the last thing the user reads on
// the screen the flow returned to.
func (m *Model) successLines() []string {
	if m.success == "" {
		return nil
	}
	return []string{"", truncate(blankMark+successStyle.Render(successMark+" "+m.success),
		m.contentWidth())}
}

// mainLines is the main menu, with the account count on the first choice and
// the sync status on the third.
func (m *Model) mainLines() []string {
	status, tone := syncStatus(m.syncStates, m.clock())
	notes := map[int]string{
		choiceAccounts: mutedStyle.Render(plural(len(m.accounts.rows), "account")),
		choiceSync:     tone.Render(status),
	}

	lines := m.header("Main menu")
	for i, choice := range mainChoices {
		lines = append(lines, m.menuRow(i == m.main.cursor, choice, notes[i]))
	}
	lines = append(lines, m.syncSummary()...)
	lines = append(lines, m.successLines()...)
	return append(lines, m.footer("↑/↓ move · enter select · q quit")...)
}

// syncSummary is one line for every institution of the last sync, including
// the ones that were skipped and the ones that failed. It is empty until a
// sync has run.
func (m *Model) syncSummary() []string {
	if len(m.syncResults) == 0 {
		return nil
	}
	lines := []string{"", truncate(blankMark+headingStyle.Render("Last sync"), m.contentWidth())}
	for _, result := range m.syncResults {
		label, note, tone := syncResultParts(result)
		lines = append(lines, columns(blankMark+label, tone.Render(note), m.contentWidth()))
	}
	return lines
}

// syncResultParts is how one institution's sync ended: the label at the left,
// the marked result at the right, and the style of that result. The label is
// the stored institution name, or the item id while no name is known.
func syncResultParts(result app.SyncResult) (string, string, lipgloss.Style) {
	switch {
	case result.Skipped:
		return result.Label, noneMark + " skipped", mutedStyle
	case result.Err != nil:
		return result.Label, failedMark + " failed — " + displayError(result.Err), warnStyle
	}
	return result.Label, okMark + " " + plural(len(result.Accounts), "account"), healthyStyle
}

// accountLines is every stored account, one row each.
func (m *Model) accountLines() []string {
	lines := m.header("Accounts")
	if len(m.accounts.rows) == 0 {
		lines = append(lines, truncate(blankMark+mutedStyle.Render("No accounts are linked yet."),
			m.contentWidth()))
		return append(lines, m.footer("esc back · q quit")...)
	}
	for i, row := range m.accounts.rows {
		selected := i == m.accounts.cursor
		lines = append(lines, truncate(mark(selected)+accountRow(row, m.accounts.newItems[row.AccountID],
			choiceStyle(selected), m.contentWidth()-lipgloss.Width(blankMark)), m.contentWidth()))
	}
	return append(lines, m.footer("↑/↓ move · enter open · esc back · q quit")...)
}

// detailLines is everything stored about one account.
func (m *Model) detailLines() []string {
	account := m.detail.account
	lines := m.header(accountName(account))
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
		lines = append(lines, m.fieldRow(field[0], field[1]))
	}
	lines = append(lines, "")
	for i, choice := range detailActions {
		lines = append(lines, m.menuRow(i == m.detail.cursor, choice, ""))
	}
	return append(lines, m.footer("↑/↓ move · enter select · esc back · q quit")...)
}

// unlinkLines is everything one removal would delete, and the two ways out of
// the confirmation.
func (m *Model) unlinkLines() []string {
	preview := m.unlink.preview
	lines := append(m.header("Unlink "+preview.Institution),
		truncate(blankMark+warnStyle.Render("This deletes every stored row of this institution:"),
			m.contentWidth()), "")
	for _, view := range preview.Accounts {
		lines = append(lines, m.fieldRow("Account", accountName(view)))
	}
	for _, row := range [][2]string{
		{"Transactions", strconv.FormatInt(preview.Rows.Transactions, 10)},
		{"Accounts", strconv.FormatInt(preview.Rows.Accounts, 10)},
		{"Sync state", strconv.FormatInt(preview.Rows.SyncState, 10)},
		{"Institutions", strconv.FormatInt(preview.Rows.Institutions, 10)},
	} {
		lines = append(lines, m.fieldRow(row[0], row[1]))
	}
	lines = append(lines, "")
	for i, choice := range unlinkActions {
		lines = append(lines, m.menuRow(i == m.unlink.cursor, choice, ""))
	}
	return append(lines, m.footer("↑/↓ move · enter select · esc cancel")...)
}

// promptLines is one text prompt with its own error, if it has one.
func (m *Model) promptLines() []string {
	lines := append(m.header(promptTitle(m.screen)),
		truncate(blankMark+m.prompt.input.View(), m.contentWidth()))
	if m.prompt.err != nil {
		lines = append(lines, "", truncate(blankMark+
			warnStyle.Render(failedMark+" "+displayError(m.prompt.err)), m.contentWidth()))
	}
	return append(lines, m.footer("enter accept · esc cancel")...)
}

// historyLines is how much history a new link may ask for.
func (m *Model) historyLines() []string {
	lines := m.header(promptTitle(historyScreen))
	for i, choice := range historyChoices {
		lines = append(lines, m.menuRow(i == m.history.cursor, choice, ""))
	}
	return append(lines, m.footer("↑/↓ move · enter select · esc back")...)
}

// recoveryLines shows what failed and what the user can do about it.
func (m *Model) recoveryLines() []string {
	lines := append(m.header("Something went wrong"),
		truncate(blankMark+warnStyle.Render(failedMark+" "+m.recovery.message), m.contentWidth()), "")
	for i, choice := range m.recovery.choices() {
		lines = append(lines, m.menuRow(i == m.recovery.cursor, choice, ""))
	}
	lines = append(lines, m.syncSummary()...)
	return append(lines, m.footer("↑/↓ move · enter select · esc back · ctrl+c quit")...)
}

// promptTitle names the prompt of one screen.
func promptTitle(s screen) string {
	if s == nicknameScreen {
		return "Name this account"
	}
	return "How many days of history"
}

// header is the brand of the application and the name of one screen, with a
// blank line around the name. A nickname or an institution name has no length
// limit, so the name is cut like every other line.
func (m *Model) header(title string) []string {
	width := m.contentWidth()
	return []string{
		truncate(blankMark+brandStyle.Render(brandName)+" "+brandMarkStyle.Render(brandMark), width),
		"",
		truncate(blankMark+headingStyle.Render(title), width),
		"",
	}
}

// footer is the faint rule that closes the screen and the key help under it.
func (m *Model) footer(keys string) []string {
	width := m.contentWidth()
	return []string{
		"",
		truncate(blankMark+mutedStyle.Render(strings.Repeat(dividerRune, max(width-2*rightPad, 0))), width),
		truncate(blankMark+mutedStyle.Render(keys), width),
	}
}

// resizePrompt gives the text input the width it may use: the content width,
// less the left gutter and the prompt of the input itself. Without a width the
// input renders the whole value, and a value longer than the terminal loses
// its caret and its newest characters to the cut. With a width the input
// scrolls under the caret instead.
func (m *Model) resizePrompt() {
	room := m.contentWidth() - lipgloss.Width(blankMark) -
		lipgloss.Width(m.prompt.input.Prompt) - caretCell
	m.prompt.input.SetWidth(max(room, 1))
	// SetWidth alone keeps the scroll offset of the width before, which can
	// hold the caret outside the new window. Setting the cursor where it
	// already is makes the input compute that offset again.
	m.prompt.input.SetCursor(m.prompt.input.Position())
}

// columns writes one row as a label at the left and a note at the right. A
// terminal too narrow for both keeps the whole label and cuts the note, so a
// label is never wrapped and never pushed off the screen.
func columns(left, right string, width int) string {
	if right == "" {
		return truncate(left, width)
	}
	gap := width - rightPad - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < minGap {
		return truncate(left+strings.Repeat(" ", minGap)+right, width)
	}
	return truncate(left+strings.Repeat(" ", gap)+right, width)
}

// menuRow is one choice of a list, with its note at the right.
func (m *Model) menuRow(selected bool, label, note string) string {
	return columns(mark(selected)+choiceStyle(selected).Render(label), note, m.contentWidth())
}

// fieldRow is one stored value, with the name of the field at the left.
func (m *Model) fieldRow(name, value string) string {
	return columns(blankMark+mutedStyle.Render(name), value, m.contentWidth())
}

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

// accountRow is one line of the account list, written in the two columns of
// every other screen. The name is the left column and is never dropped: every
// other detail joins the right column only while the width allows it. label is
// how the name is written, which says whether the cursor is on this row.
func accountRow(view model.AccountView, isNew bool, label lipgloss.Style, width int) string {
	name := label.Render(accountName(view))
	extras := []string{}
	for _, extra := range []string{maskText(view), view.InstitutionName, view.Currency, balanceText(view)} {
		if extra != "" {
			extras = append(extras, mutedStyle.Render(extra))
		}
	}

	right := ""
	for _, extra := range append(extras, newExtra(isNew)) {
		if extra == "" {
			continue
		}
		candidate := extra
		if right != "" {
			candidate = right + "  " + extra
		}
		if lipgloss.Width(name)+minGap+lipgloss.Width(candidate)+rightPad > width {
			break
		}
		right = candidate
	}
	return columns(name, right, width)
}

// newExtra marks an account that this session has just linked.
func newExtra(isNew bool) string {
	if !isNew {
		return ""
	}
	return tagStyle.Render(newMark)
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

// syncStatus is the marked state of the last sync of every linked
// institution, and the style that state is written in. A failure of any
// institution is reported even when a later sync succeeded, because the failed
// institution still holds stale data.
func syncStatus(states []app.SyncState, now time.Time) (string, lipgloss.Style) {
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
		return failedMark + " Sync failed: " + failed.Institution +
			" (" + since(*failed.LastSyncedAt, now) + ")", warnStyle
	case failed != nil:
		return failedMark + " Sync failed: " + failed.Institution, warnStyle
	case ok != nil:
		return okMark + " Last sync: " + since(*ok.LastSyncedAt, now), healthyStyle
	}
	return noneMark + " Not synced", mutedStyle
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
func count(n int, unit string) string { return plural(n, unit) + " ago" }

// plural is a whole number of units, with the unit written for that number.
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// displayError is the only place a stored error becomes user wording.
func displayError(err error) string {
	if errors.Is(err, app.ErrProductNotReady) {
		return "The bank is still preparing the data"
	}
	return err.Error()
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
