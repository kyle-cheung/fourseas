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

`*app.App` has seven operations: `Accounts`, `Link`, `SyncItem`, `SyncAll`,
`SetNickname`, `UnlinkPreview`, and `Unlink`. The `service` interface in
`internal/tui/model.go` lists the same seven.

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
- The local half of `Unlink` runs on `context.WithoutCancel` with the bound
  `localCleanupTimeout`, after Plaid agrees to the removal. The user cannot stop
  the local deletes, because Plaid no longer bills the item.

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
  `tea.WindowSizeMsg`, `progressMsg`, `operationMsg`, and ctrl+c only. Ctrl+c
  cancels the operation and keeps the interface alive until the result arrives.
- `start` writes `returnTo` with the screen the operation started from.
  `cancelled` and `recoverWith` return to that screen. The exception is a
  cancelled first sync of a new link: the model shows the account list, because
  enter on the history screen would create a second billed item.
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
  `healthyStyle`, and `warnStyle`. Cyan is the one accent and marks the
  selection. Green is a state that is in order, red is a state that needs the
  user, and everything secondary is dim. The brand holds the accent on its `≋`
  glyph only, so the wordmark never reads as a selected row.
- `m.header(title)` writes the brand `fourseas ≋` and the name of the screen.
  `m.footer(keys)` writes the faint rule and the key help. Both return lines,
  so a screen builder appends them.
- `columns(left, right, width)` writes one row as a label at the left and a
  note at the right. `m.menuRow` and `m.fieldRow` call it. A terminal too
  narrow for both keeps the whole label and cuts the note.
- `cursorMark` (`› `) and `blankMark` are the same printable width, so a row
  never moves when the cursor does.
- Every line is cut with `truncate`, which uses `MaxWidth`. Use
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

## How to test

- `internal/tui/model_test.go` drives `Update` directly with `tea.KeyPressMsg`.
  The `press` helper sends one key and checks the model identity. Never start a
  real terminal program in a test.
- `fakeService` satisfies `service`. It records the arguments of each call and
  returns prepared answers, so no store and no provider are opened. `ready`
  builds a model whose first refresh is complete: it calls `Init`, runs the
  command, and passes the message to `Update`.
- To read a screen, call the `content` helper. It returns `m.View().Content`
  with the escape sequences removed, so an assertion compares printable text.
  The `hasRow` helper checks that one line holds a label and its value in the
  two columns.
- The tests of `internal/app` use a real temporary DuckDB file and real token
  files (`tempConfig` and `seedTokens` in `internal/app/app_test.go`). Only
  Plaid is injected, through `newWith` or `WithRemove`.

## Known gaps

- No viewport and no scrolling. `m.height` is stored but never read, so many
  accounts overflow a short terminal.
- The default SIGTERM handling of Bubble Tea sends `QuitMsg` and ends the
  process. The active operation is not cancelled.
- `internal/tokens` writes the token file with `os.WriteFile`, without a
  temporary file and a rename.
