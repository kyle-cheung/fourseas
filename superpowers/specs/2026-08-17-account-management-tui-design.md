# Account management TUI design

Status: Approved design. Delete this spec after implementation.

## Goal

Make account setup and management easy in a terminal. Keep all current CLI
commands for scripts and advanced use.

Running `fourseas` with no arguments opens the TUI. Existing subcommands keep
their current behavior and structure.

## Scope

The TUI supports these tasks:

- List accounts.
- Link an institution.
- Sync linked institutions.
- Set or clear an account nickname.
- Unlink an institution.

The TUI does not replace transaction queries, FX-only sync, reset, or other
current CLI commands.

## Navigation

The main screen has three choices:

```text
FOURSEAS

> Accounts                              3 accounts
  Add account
  Sync                                  Last sync: 2 min ago

enter select   q quit
```

`Accounts` opens one flat account list. `Sync` is a main-screen task. It is not
an account-list action.

```text
ACCOUNTS

> Daily Visa        TD Canada Trust      CAD 1,204.18  NEW
  Chequing ••5678   TD Canada Trust        CAD 842.11
  Travel Amex       American Express       USD 319.40

enter details   esc back
```

The list shows the nickname or provider name, institution, masked number,
currency, balance, and status. A `NEW` marker lasts for the current TUI
session. At narrow widths, truncate the row and keep the account name visible.

Selecting an account opens its details. The user can rename the account or
unlink its institution. Unlink confirmation shows every affected account and
the number of rows that the operation will delete.

## Key map

- All menus and lists: Arrow keys move, `Enter` selects, and `Esc` goes back.
- Main, list, and detail screens: `q` quits.
- Text prompts: Character keys enter text, including `q`. `Enter` submits and
  `Esc` cancels the prompt.
- Confirmation screens: Arrow keys select an action. `Enter` applies it and
  `Esc` cancels it.
- Running operation: `Ctrl+C` requests cancellation. Other keys have no effect.
- No running operation: `Ctrl+C` quits and restores the terminal.

## Add account flow

The flow is:

```text
Select history window
  -> Complete Plaid Link in the browser
  -> Save linked item
  -> Sync the new item
  -> Enter each new account nickname
  -> Show all accounts with NEW markers
```

The history choices are 730, 365, 90, or a custom value from 30 through 730
days. The default is 730 days.

New accounts are the accounts that `SyncItem` wrote for the new item. Nickname
entry shows these accounts one at a time. A blank nickname is valid and keeps
the provider name.

The automatic sync affects only the new item. A sync from the main screen
affects all usable linked items.

## Sync status

The main screen reads `last_synced_at` and `last_status` from `sync_state`.

- If all item statuses are `ok`, show the latest successful sync time.
- If an item has a failure status, show the institution and failure time.
- Do not label a failed attempt as a successful sync.

## Architecture

```text
fourseas with no arguments
            |
            v
       internal/tui
            |
            v
       internal/app
  accounts | link | sync | rename | unlink
            |
   +--------+--------+
   v        v        v
provider  store    tokens

existing subcommands -> current command functions
```

`internal/app` contains only the operations that the TUI needs. Existing CLI
commands do not move into this layer.

Application operations return data and errors. Long operations accept a
`func(string)` progress callback. The TUI shows the latest string in its status
line. Current CLI functions can print the strings when they share an operation.

The application layer provides these operations:

- `Accounts`: Return the flat account list, optionally for one item.
- `Link`: Run Plaid Link and save the new item.
- `SyncItem`: Sync one item and return the accounts that it wrote.
- `SyncAll`: Sync all usable items.
- `SetNickname`: Set or clear one account nickname.
- `UnlinkPreview`: Validate the item and return affected accounts and row
  counts.
- `Unlink`: Remove an item at Plaid, then remove its local data and token.

Each operation loads the token file when it starts and saves changes
immediately. The TUI model does not cache token data.

Each operation opens the DuckDB store when it starts and closes it before it
returns. The TUI does not hold the database lock while the user reads a screen.
A second process can use the database when no TUI operation is active.

## TUI structure

Use Bubble Tea v2, Bubbles v2, and Lip Gloss v2.

A root model owns navigation, terminal size, and active operation
cancellation. Each screen owns its local state. Shared components provide the
header, footer keys, prompts, status, and errors. The TUI uses keyboard input.

New screens call `internal/app`. They do not call provider, store, or token
packages directly.

## Link and unlink safety

The Plaid provider must not print. `cmd/fourseas/link.go` prints the local Link
URL, and the TUI shows the same URL on its link screen.

On cancellation, Plaid Link stops accepting new requests and waits for an
active `/exchange` handler. It then checks for a completed result before it
returns a cancellation error. This prevents fourseas from losing the access
token for a live, billed Plaid item.

If bank sign-in completes before cancellation finishes, `Link` saves the item.
The TUI reports that the link completed and offers to continue setup or unlink
the institution.

`UnlinkPreview` checks that the item belongs to the active `PLAID_ENV` before
the TUI shows its confirmation screen. `Unlink` checks the environment again
before it calls Plaid.

Unlink keeps this order:

1. Remove the item at Plaid.
2. Delete local data.
3. Delete the token.

If local cleanup fails, the token stays available for a retry.

## Other errors

- Plaid Link cancellation before bank sign-in completes saves nothing and
  returns to the add screen.
- If link succeeds but sync fails, the item stays linked. The TUI offers retry
  or return to the main screen.
- A nickname write failure keeps the nickname prompt open.
- Sync-all continues after one item fails and reports each item result.
- Recoverable errors stay in the TUI and show a recovery action.
- Normal exit, cancellation, and fatal errors restore the terminal state.

## Verification

Automated tests cover current CLI behavior and the complete add flow. Add-flow
tests cover success and a sync failure after a successful link. The failure
test confirms that the saved item remains recoverable.

A manual smoke check covers navigation, resizing, cancellation, and terminal
restoration. Run `go test -race ./...` for full automated verification.
