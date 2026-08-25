# Account management terminal interface

## What it is

`fourseas` with no argument opens the terminal interface (`cmd/fourseas/main.go`).
Each subcommand keeps its command line behaviour. The interface shows the stored
accounts, adds a link, syncs, renames an account, and removes an institution.

## The layers

```
cmd/fourseas ──┐
               ├──> internal/app ──> internal/store, internal/tokens, internal/provider
internal/tui ──┘
```

`cmd/fourseas` and `internal/tui` are two presenters over one façade.
`internal/tui` imports `internal/app` and `internal/model` only. It holds no
store, no token, and no provider code. `internal/app` holds no user wording:
`displayError` in `internal/tui/view.go` is the only place an error becomes
text.

## The internal/app contract

`*app.App` has nine operations: `Accounts`, `Link`, `CompleteLinkSave`,
`EnableLiabilities`, `SyncItem`, `SyncAll`, `SetNickname`, `UnlinkPreview`, and
`Unlink`. The `service` interface in `internal/tui/model.go` lists the same
nine. The TUI operation enum has ten values. It gives the account refresh after
Liabilities consent its own `postEnableRefreshOperation` state.

Rules that the code depends on:

- Each operation reads the token file and opens DuckDB only when it needs them.
  `App.start` in `internal/app/app.go` does both. The operation closes the store
  before it returns, so two operations never hold the same database file.
- The order of the checks is behaviour, not style. `SyncAll` validates the Plaid
  settings and refuses an empty token file before it opens DuckDB.
  `UnlinkPreview` does not call `start`, because `start` would open DuckDB
  before the environment check. Keep this order when you change an operation.
- `Progress` is how a long operation reports a step. It may be nil, and
  `app.progress` accepts nil.
- Cancellation crosses the boundary as the returned error. The caller passes a
  context and reads `context.Canceled` from the error.
- A failed token save is the one failure a presenter must classify.
  `App.Link` saves the access token before it returns, because Plaid bills the
  item it has created and the token is the only handle to it. When that save
  fails, `Link` returns a `*app.TokenNotSavedError`, which a caller reads with
  `errors.As`. Its `Pending` field is an opaque `app.PendingSave`: every field
  is unexported, so the token stays inside `internal/app`. A presenter reads
  `Pending.Institution()` and `Pending.ItemID()` only, and `PendingSave.String`
  redacts the token so a stray `%v` cannot print it.
- `App.CompleteLinkSave(pending)` is the retry of that save. It calls no
  provider and returns the same `LinkedItem` a successful `Link` returns. It
  reads the token file again first and falls back to the snapshot of the
  failure only when that read fails, so a file the user repaired in another
  terminal is not overwritten. A save that fails again returns another
  `*app.TokenNotSavedError`, so the retry is never a one-shot.
- **Never run `Link` again after a `*app.TokenNotSavedError`.** Plaid has an
  item and bills it. A second link creates a second billed item and leaves the
  first one unreachable. `internal/tui` retries with `startLinkSave`, and
  `cmd/fourseas` retries in `linkOnce`.
- The local half of `Unlink` runs on `context.WithoutCancel` with the bound
  `localCleanupTimeout`, after Plaid agrees to the removal. The user cannot stop
  the local deletes, because Plaid no longer bills the item.
- `App.EnableLiabilities(accountID)` resolves a credit account to its Item. It
  opens Plaid Link in update mode for that whole institution. Update mode keeps
  the access token and common Link and OAuth redirect fields. It omits products
  and transaction history, and it does not exchange a token.
- The update completion endpoint accepts a local JSON `POST` only. It validates
  the Host and Origin headers and a per-session nonce. After success, the app
  reloads and changes the current token file under the write lock. This keeps
  concurrent Items and rejects a changed target token or environment. Token
  files use owner-only, atomic writes under a cross-process lock.
- `SyncItem` calls Liabilities once, after transaction pagination, only when the
  Item setting is enabled. `ErrProductNotReady` preserves the old snapshot and
  returns success. Missing consent preserves the snapshot and records an
  actionable state. Other errors preserve the snapshot and return the account
  views for data that already committed.

## The TUI model

The interface uses Bubble Tea v2, at the module path `charm.land/bubbletea/v2`.
The version 1 examples at `github.com/charmbracelet/bubbletea` do not apply:
version 2 returns `tea.View` from `View`, and sends `tea.KeyPressMsg`. Read the
module source when you are not sure of an interface.

One root `*Model` in `internal/tui/model.go` owns every screen's state. All
Bubble Tea methods use pointer receivers, so `Update` returns the same model.

- `screen` is an enum. Each screen is a state of the one model, not a separate
  model. `parent` gives the screen that esc returns to.
- One operation runs at a time. `start` marks the model busy, stores the
  `context.CancelFunc`, and returns the command. The command sends one
  `operationMsg`. A long operation sends `progressMsg` values through
  `m.report`.
- The `running` gate is in `key`. While an operation runs, the model accepts
  `tea.WindowSizeMsg`, `progressMsg`, `operationMsg`, `spinner.TickMsg`, and
  ctrl+c only. Ctrl+c cancels the operation and keeps the interface alive until
  the result arrives.
- A running operation shows a spinner with the newest progress line, or with
  the label of the operation from `runningLine` while it has reported no step.
  `start` returns `tea.Batch(m.spinner.Tick, …)`, which starts the animation
  beside the operation. The program runs a `tea.BatchMsg` itself and delivers
  each message on its own, so `Update` never sees the batch and the
  `operationMsg` arrives as it would from a single command.
- Each frame schedules the frame after it, so the chain has to be stopped:
  `advanceSpinner` returns no command when nothing is running. One operation
  may start the next one, so two chains can overlap for one frame.
  `spinner.Model` rejects a frame that carries an older count of its own, which
  is what keeps the speed right; give its frame message back unchanged.
- `m.success` is the outcome of the last finished flow, written with `✓` in the
  healthy colour. It has no timer: `start` clears it, and `keyPress` clears it
  when a key moved the user to another screen. A flow writes it **after** the
  call that starts its closing refresh, because `start` clears it. `finishAdd`
  and a clean `finishSyncAll` are the two flows that write one, and both return
  to the main menu. A clean sync drops the per-item `Last sync` block, so one
  run reads as one outcome; a run with a skipped or a failed item keeps that
  block.
- `failed` classifies one failure. A `*app.TokenNotSavedError` opens the
  recovery screen with `tokenNotSavedMessage`, the notes of
  `tokenNotSavedNotes`, and a retry that runs `startLinkSave` instead of
  `startLink`. The wording says that the bank connected and that only the save
  failed, so `Retry` never reads as "try the bank again", and it says what
  `Main` costs: an item that stays billed with no saved token. Every other link
  failure keeps its own retry.
- `m.recovery.notes` are the lines under the failure message. They are written
  in the muted style and **wrapped**, never cut: `recoveryLines` runs both the
  message and every note through `wrapText` at `m.noteWidth()`. The notes carry
  the consequence of each choice, and `truncate` is a hard cut with no
  ellipsis, so it would turn "It does not open the bank" into "It does n" on a
  narrow terminal, which says the opposite. `wrapText` breaks at a space and
  cuts a word that is longer than the whole width, because a file path holds no
  space to break at.
- `start` writes `returnTo` with the screen the operation started from.
  `cancelled` and `recoverWith` return to that screen. The exception is a
  cancelled first sync of a new link: the model shows the account list, because
  enter on the history screen would create a second billed item.
- `Add account` opens `addSetupScreen`. It starts with 730 history days and
  Liabilities set to `Yes`. Enter on the history row opens `historyScreen`.
  Enter on the Liabilities row toggles the value. Enter on `Continue` passes
  both values to `startLink`.
- A credit account detail shows `Enable statement data` when its Item is off or
  the last sync recorded missing consent. The action calls
  `EnableLiabilities` for the account. A success marks the Item enabled before
  it starts `postEnableRefreshOperation`, so the stale action cannot appear
  during the refresh.
- `EnableLiabilities` can return a cancellation after Plaid consent succeeds,
  `Liabilities=true` is saved, and the first `/liabilities/get` call begins.
  This cancellation can follow a stored or billed state change. The TUI shows
  recovery instead of hiding it. Retry repeats the enable and consent action
  for the same account.
- A nil `EnableLiabilities` result starts `postEnableRefreshOperation`. If that
  account reload fails or is canceled, recovery says that consent succeeded.
  Retry reloads `Accounts` only. It does not open Link or call the Liabilities
  endpoint again.
- `Main` from recovery refreshes account state. Do not hide cancellation after
  an operation that can have changed billed or stored state. The recovery text
  must state which change completed and what Retry does.
- `m.send` is `program.Send`, and `Run` sets it. It stays nil in a test, so
  `report` checks it before use.
- `m.now` is the clock. A test replaces it.

## How a screen is written

`internal/tui/view.go` builds every screen as a list of lines.
`internal/tui/style.go` holds the whole visual language: the palette, the
marks, and the column rules. Change a colour in that one file and every screen
follows.

- Each colour is a 4-bit ANSI index, so the terminal theme chooses the shade
  and both a light and a dark background stay readable. Bubble Tea downsamples
  the colours at its renderer and honours `NO_COLOR`, so no code here detects
  the colour profile.
- The styles are named for their role: `brandStyle`, `brandMarkStyle`,
  `headingStyle`, `itemStyle`, `selectedStyle`, `mutedStyle`, `tagStyle`,
  `healthyStyle`, `warnStyle`, `successStyle`, and `spinnerStyle`. Cyan is the
  one accent and marks the selection and the spinner. Green is a state that is in order, red is a state that needs the
  user, and everything secondary is dim. The brand holds the accent on its `≋`
  glyph only, so the wordmark never reads as a selected row.
- `m.header(title)` writes the brand `fourseas ≋` and the name of the screen.
  `m.footer(keys)` writes `m.stateLines()`, then the faint rule and the key
  help. Both return lines, so a screen builder appends them.
- The main screen puts the account summary above the `Main menu` heading. The
  summary has `Account`, `Balance`, `Due`, and `Last payment` columns. Money
  includes its currency. Missing values use the shared `format.missing` value.
  The table has no border, does not wrap cells, and keeps one account on one
  line. Lipgloss chooses the natural table width. Each rendered line then gets
  the normal left gutter and is cut to the TUI content width. Do not force the
  table to fill the terminal.
- `m.stateLines()` is the one slot every screen keeps for what the interface is
  doing now, or for what it has just done: the running line, then a leftover
  progress line, then the outcome of the last flow. It sits above the rule,
  because the rule closes the screen and the line that changes must not follow
  the key help that never changes. The three cannot appear together, so the
  slot holds one line that changes in place.
- `columns(left, right, width)` writes one row as a label at the left and a
  note at the right. `m.menuRow` and `m.fieldRow` call it. A terminal too
  narrow for both keeps the whole label and cuts the note.
- `cursorMark` (`› `) and `blankMark` are the same printable width, so a row
  never moves when the cursor does.
- Every line is cut with `truncate`, which uses `MaxWidth`. A cut that would
  change the meaning of a line is a defect: wrap that line with `wrapText`
  first, as the recovery screen does. Use
  `lipgloss.Width` and never `len` for layout, because a line holds escape
  sequences and wide characters.
- `m.resizePrompt` gives the text input the width it may use. `openPrompt` and
  the `tea.WindowSizeMsg` case both call it. Without a width the input renders
  its whole value, so a value longer than the terminal loses its newest
  characters and its caret to the cut. This is the only rendering value that
  `internal/tui/model.go` holds.

## How to add a screen

1. Add the constant to the `screen` enum in `internal/tui/model.go`.
2. Add its state struct as a field of `Model`.
3. Add the case to `parent`, and to `quittable` or `isPrompt` if it applies.
4. Add the case to `moveCursor` and to `activate`, or to `submitPrompt` for a
   text prompt.
5. Add the case to `body` in `internal/tui/view.go` and write the line builder.
   Start it with `m.header` and end it with `m.footer`, and write every row
   with `m.menuRow` or `m.fieldRow`.

## How to add an operation

1. Add the method to `*app.App`, in its own file of `internal/app`.
2. Add the method to the `service` interface in `internal/tui/model.go`.
3. Add the constant to the `operation` enum.
4. Write a `startX` method that calls `m.start`.
5. Add the case to `finish`.
6. Set `m.recovery.retry` on the line immediately after the `start` call it
   retries. `start` clears `recovery` and writes `returnTo`, so a retry that is
   set before the call is lost. Set a retry only when the operation can run
   again as it was.
7. Add the operation to `destructive` if it changes stored or billed state.
   A destructive cancellation must open truthful recovery instead of returning
   silently to the old screen.

## How to test

- `internal/tui/model_test.go` drives `Update` directly with `tea.KeyPressMsg`.
  The `press` helper sends one key and checks the model identity. Never start a
  real terminal program in a test.
- `fakeService` satisfies `service`. It records the arguments of each call and
  returns prepared answers, so no store and no provider are opened. `ready`
  builds a model whose first refresh is complete: it calls `Init`, runs the
  command, and passes the message to `Update`.
- `fakeService` records `completeCalls` beside `linkDays`. A test of a failed
  save asserts both: the retry must call `CompleteLinkSave` once and must not
  call `Link` a second time.
- Enable-flow tests must check each state boundary. A post-consent refresh retry
  must increase the `Accounts` call count without increasing the
  `EnableLiabilities` call count.
- To read a screen, call the `content` helper. It returns `m.View().Content`
  with the escape sequences removed, so an assertion compares printable text.
  The `hasRow` helper checks that one line holds a label and its value in the
  two columns, and `hasLine` checks a whole line.
- Starting an operation returns a batch: the operation and the first spinner
  frame. `runCmd` runs one command and returns every message it produced, the
  way the program does, and `operationResult` picks the one result out of them.
  A test that needs the frame reads it with `firstTick`.
- The tests of `internal/app` use a real temporary DuckDB file and real token
  files (`tempConfig` and `seedTokens` in `internal/app/app_test.go`). Only
  Plaid is injected, through `newWith` or `WithRemove`.

## Known gaps

- No viewport and no scrolling. `m.height` is stored but never read, so many
  accounts overflow a short terminal.
- The default SIGTERM handling of Bubble Tea sends `QuitMsg` and ends the
  process. The active operation is not cancelled.
- The implementation is designed to support Windows token locking and
  replacement. The runtime path still needs verification on a Windows host.
