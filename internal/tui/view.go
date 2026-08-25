package tui

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	accountformat "github.com/kyle-cheung/fourseas/providence/internal/format"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

// mainChoices is the fixed order of the main screen.
var mainChoices = []string{"Accounts", "Add account", "Sync"}

const noAccountsMessage = "No accounts are linked yet."

// Positions inside mainChoices.
const (
	choiceAccounts = iota
	choiceAdd
	choiceSync
)

// addSetupChoices is the fixed order of the new-account setup screen.
var addSetupChoices = []string{"Transaction history", "Enable Liabilities API?", "Continue"}

// Positions inside addSetupChoices.
const (
	addHistory = iota
	addLiabilities
	addContinue
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

// body is the text of the active screen.
func (m *Model) body() string {
	var lines []string
	switch m.screen {
	case mainScreen:
		lines = m.mainLines()
	case accountsScreen:
		lines = m.accountLines()
	case detailScreen:
		lines = m.detailLines()
	case addSetupScreen:
		lines = m.addSetupLines()
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
	return strings.Join(lines, "\n")
}

// stateLines is the one slot every screen keeps for what the interface is
// doing now, or for what it has just done. The three are exclusive: an
// operation clears the outcome of the last flow when it starts, and clears the
// progress line when it ends.
//
// A progress line carries a whole provider error behind a padded label, so it
// is the longest string the interface ever shows. Left whole it wraps and
// pushes the footer out of the screen.
func (m *Model) stateLines() []string {
	width := m.contentWidth()
	switch {
	case m.running:
		return []string{"", truncate(blankMark+m.spinner.View()+" "+
			mutedStyle.Render(m.runningLine()), width)}
	case m.status != "":
		return []string{"", truncate(blankMark+mutedStyle.Render(m.status), width)}
	case m.success != "":
		return []string{"", truncate(blankMark+successStyle.Render(successMark+" "+m.success), width)}
	}
	return nil
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
	case linkSaveOperation:
		return "Saving the token"
	case syncItemOperation, syncAllOperation:
		return "Syncing"
	case nicknameOperation:
		return "Saving"
	case unlinkPreviewOperation:
		return "Reading"
	case unlinkOperation:
		return "Removing"
	case enableLiabilitiesOperation:
		return "Enabling statement data"
	case postEnableRefreshOperation:
		return "Refreshing accounts"
	}
	return "Loading"
}

// mainLines is the main menu, with the account count on the first choice and
// the sync status on the third.
func (m *Model) mainLines() []string {
	now := m.clock()
	status, tone := syncStatus(m.syncStates, now)
	notes := map[int]string{
		choiceAccounts: mutedStyle.Render(plural(len(m.accounts.rows), "account")),
		choiceSync:     tone.Render(status),
	}

	width := m.contentWidth()
	lines := []string{
		truncate(blankMark+brandStyle.Render(brandName)+" "+brandMarkStyle.Render(brandMark), width),
		"",
	}
	lines = append(lines, m.accountSummaryLines(now)...)
	lines = append(lines,
		"",
		truncate(blankMark+headingStyle.Render("Main menu"), width),
		"",
	)
	for i, choice := range mainChoices {
		lines = append(lines, m.menuRow(i == m.main.cursor, choice, notes[i]))
	}
	lines = append(lines, m.syncSummary()...)
	return append(lines, m.footer("↑/↓ move · enter select · q quit")...)
}

// accountSummaryLines is one content-sized table row per stored account.
func (m *Model) accountSummaryLines(now time.Time) []string {
	if len(m.accounts.rows) == 0 {
		return []string{truncate(blankMark+mutedStyle.Render(noAccountsMessage), m.contentWidth())}
	}

	t := table.New().
		Headers("Account", "Balance", "Due", "Last payment").
		Wrap(false).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderHeader(false).
		BorderColumn(false).
		BorderRow(false).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := itemStyle
			if row == table.HeaderRow {
				style = headingStyle
			}
			if col < 3 {
				style = style.PaddingRight(2)
			}
			return style
		})

	for _, account := range m.accounts.rows {
		var due, payment string
		if account.Liability == nil {
			due, payment = "—", "—"
		} else {
			due = accountformat.Date(account.Liability.PaymentDueDate, now)
			payment = accountformat.LatestPayment(
				account.Liability.LastPaymentDate,
				account.Liability.LastPaymentAmount,
				account.Currency,
				now,
			)
		}
		t.Row(accountName(account), accountformat.Money(account.BalanceCurrent, account.Currency), due, payment)
	}

	lines := strings.Split(t.String(), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		lines[i] = truncate(blankMark+lines[i], m.contentWidth())
	}
	return lines
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
		lines = append(lines, truncate(blankMark+mutedStyle.Render(noAccountsMessage),
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
		{"Balance", accountformat.Money(account.BalanceCurrent, account.Currency)},
		{"Account id", account.AccountID},
	} {
		if field[1] == "" {
			continue
		}
		lines = append(lines, m.fieldRow(field[0], field[1]))
	}
	lines = append(lines, "")
	for i, action := range m.detailActions() {
		lines = append(lines, m.menuRow(i == m.detail.cursor, detailActionLabel(action), ""))
		if action == detailEnableLiabilities {
			lines = append(lines, m.choiceNoteLines(
				"This enables statement data for the whole institution.")...)
		}
	}
	return append(lines, m.footer("↑/↓ move · enter select · esc back · q quit")...)
}

func detailActionLabel(action detailAction) string {
	switch action {
	case detailRename:
		return "Rename"
	case detailEnableLiabilities:
		return "Enable statement data"
	case detailUnlink:
		return "Unlink institution"
	}
	return ""
}

// addSetupLines shows the settings that apply when the new item is created.
func (m *Model) addSetupLines() []string {
	liabilities := "No"
	if m.add.liabilities {
		liabilities = "Yes"
	}
	notes := map[int]string{
		addHistory:     strconv.Itoa(m.add.days) + " days",
		addLiabilities: liabilities,
	}
	lines := m.header("Add account")
	for i, choice := range addSetupChoices {
		lines = append(lines, m.menuRow(i == m.add.cursor, choice, notes[i]))
		if i == addLiabilities {
			lines = append(lines, m.choiceNoteLines(
				"Fetch credit card statement, payment, and interest details.")...)
		}
	}
	return append(lines, m.footer("↑/↓ move · enter select · esc back")...)
}

// choiceNoteLines keeps the explanation under a menu choice complete at every
// terminal width. Repeating the indent makes wrapped lines part of the same
// choice.
func (m *Model) choiceNoteLines(note string) []string {
	prefix := blankMark + "  "
	width := max(m.contentWidth()-lipgloss.Width(prefix)-rightPad, 1)
	wrapped := wrapText(note, width)
	lines := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		lines = append(lines, truncate(prefix+mutedStyle.Render(line), m.contentWidth()))
	}
	return lines
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

// tokenNotSavedMessage is the first line of the one failure where the provider
// part succeeded. It says so first, because the choices under it read
// differently once the user knows that the bank is linked.
const tokenNotSavedMessage = "The bank connected. Only the save of the access token failed."

// tokenNotSavedNotes explains what the user now owns and what each choice
// does. The item is billed from now on, and the access token is the only way to
// reach it, so both choices have a cost the user must read before choosing.
func tokenNotSavedNotes(institution, itemID string, cause error) []string {
	name := institution
	if name == "" {
		name = itemID
	}
	if name == "" {
		name = "The new item"
	}
	notes := []string{name + " is linked at Plaid. Plaid bills this item each month."}
	if itemID != "" {
		notes = append(notes, "Item id: "+itemID)
	}
	return append(notes,
		"Reason: "+displayError(cause),
		"Retry saves the token again. It does not open the bank a second time.",
		"Main leaves this item billed with no saved token.",
		"Without the token you cannot sync this item or remove it.",
	)
}

// recoveryLines shows what failed and what the user can do about it.
func (m *Model) recoveryLines() []string {
	lines := m.header("Something went wrong")
	// The message is wrapped for the same reason the notes are, and the lines
	// under the first one keep the width of the mark, so the sentence reads as
	// one block.
	mark := failedMark + " "
	for i, line := range wrapText(m.recovery.message, max(m.noteWidth()-lipgloss.Width(mark), 1)) {
		if i > 0 {
			mark = strings.Repeat(" ", lipgloss.Width(failedMark)+1)
		}
		lines = append(lines, truncate(blankMark+warnStyle.Render(mark+line), m.contentWidth()))
	}
	// A note is wrapped and never cut. A note carries the consequence of each
	// choice, and truncate is a hard cut: it would turn "It does not open the
	// bank" into "It does n", which says the opposite of what the user must
	// read here.
	for _, note := range m.recovery.notes {
		for _, line := range wrapText(note, m.noteWidth()) {
			lines = append(lines, truncate(blankMark+mutedStyle.Render(line), m.contentWidth()))
		}
	}
	lines = append(lines, "")
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

// footer is what the interface is doing now, then the faint rule that closes
// the screen and the key help under it. The rule closes the screen, so the one
// line that changes while the user waits belongs above it and not after the
// key help, which never changes.
func (m *Model) footer(keys string) []string {
	width := m.contentWidth()
	return append(m.stateLines(),
		"",
		truncate(blankMark+mutedStyle.Render(strings.Repeat(dividerRune, max(width-2*rightPad, 0))), width),
		truncate(blankMark+mutedStyle.Render(keys), width),
	)
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

// accountName is a safe nickname, provider name, or account id. A candidate
// that has no text after sanitizing does not block the next fallback.
func accountName(view model.AccountView) string {
	for _, candidate := range []string{view.Nickname, view.Name, view.AccountID} {
		if name := displayText(candidate); name != "" {
			return name
		}
	}
	return ""
}

// displayText makes stored provider text safe for one terminal line. It
// removes terminal commands and controls, then reduces whitespace to one
// printable space.
func displayText(value string) string {
	value = ansi.Strip(value)
	var clean strings.Builder
	space := false
	for _, r := range value {
		switch {
		case unicode.IsSpace(r):
			space = clean.Len() > 0
		case r < ' ' || r >= '\x7f' && r <= '\u009f':
			continue
		default:
			if space {
				clean.WriteByte(' ')
				space = false
			}
			clean.WriteRune(r)
		}
	}
	return clean.String()
}

// accountRow is one line of the account list, written in the two columns of
// every other screen. The name is the left column and is never dropped: every
// other detail joins the right column only while the width allows it. label is
// how the name is written, which says whether the cursor is on this row.
func accountRow(view model.AccountView, isNew bool, label lipgloss.Style, width int) string {
	name := label.Render(accountName(view))
	extras := []string{}
	for _, extra := range []string{maskText(view), view.InstitutionName,
		accountformat.Money(view.BalanceCurrent, view.Currency)} {
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

// noteWidth is the room one wrapped note has: the content width, less the left
// gutter and the same right pad the two columns keep.
func (m *Model) noteWidth() int {
	return max(m.contentWidth()-lipgloss.Width(blankMark)-rightPad, 1)
}

// wrapText breaks plain text into lines that fit width cells. It breaks at a
// space, and it cuts a word that is longer than the whole width, because a file
// path has no space to break at. The text it takes holds no escape sequence, so
// the styles are applied to the lines it returns.
func wrapText(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		for lipgloss.Width(word) > width {
			if line != "" {
				lines, line = append(lines, line), ""
			}
			head, tail := cutCells(word, width)
			lines, word = append(lines, head), tail
		}
		switch {
		case word == "":
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
			line += " " + word
		default:
			lines, line = append(lines, line), word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// cutCells splits text after the given number of cells, without splitting a
// character. It always takes at least one character, so a caller that cuts a
// long word makes progress even on a terminal narrower than one wide glyph.
func cutCells(text string, cells int) (string, string) {
	used := 0
	for i, r := range text {
		cell := lipgloss.Width(string(r))
		if i > 0 && used+cell > cells {
			return text[:i], text[i:]
		}
		used += cell
	}
	return text, ""
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
