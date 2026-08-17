# Account management TUI

Status: Approved design. Keep this document current when the TUI changes.

## Goal

Make account setup and management easy in a terminal. Keep all current CLI
commands for scripts and advanced use.

Running `fourseas` with no arguments opens the TUI. Existing subcommands keep
their current contracts.

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
session. At narrow widths, the TUI truncates status, institution, and balance,
in that order. The account name stays visible.

Selecting an account opens its details. The user can rename the account or
unlink its institution. Unlink confirmation shows every affected account and
the number of rows that the operation will delete.

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

Nickname entry shows one new account at a time. A blank nickname is valid and
keeps the provider name.

The automatic sync affects only the new item. A sync from the main screen
affects all usable linked items.

## Architecture

```text
cmd/fourseas
|- no arguments -----------------> internal/tui
`- existing subcommands ---------> CLI adapters
                                      |
internal/tui -------------------------|
                                      v
                                 internal/app
                         link | sync | rename | unlink
                                      |
                   +------------------+------------------+
                   v                  v                  v
                provider            store              tokens
```

`internal/app` owns account workflows. The TUI and CLI adapters use the same
application services. Services return data and errors. They do not print or
prompt.

Long operations emit typed progress events. The TUI renders the events. CLI
adapters convert them to text. Network and database work runs asynchronously
in the TUI.

The application layer provides these operations:

- `Dashboard`: Return the account count and latest sync state.
- `Accounts`: Return the flat account list, optionally for one item.
- `Link`: Run Plaid Link and save the new item.
- `SyncItem`: Sync one item.
- `SyncAll`: Sync all usable items.
- `SetNickname`: Set or clear one account nickname.
- `UnlinkPreview`: Return affected accounts and row counts.
- `Unlink`: Remove an item at Plaid, then remove its local data and token.

The dashboard reads the current `sync_state` records. The TUI does not require
a schema change.

## TUI structure

Use Bubble Tea v2, Bubbles v2, and Lip Gloss v2.

A root model owns navigation, terminal size, and active operation
cancellation. Each screen owns its local state. Shared components provide the
header, footer keys, prompts, status, and errors. The TUI uses keyboard input.

New screens call `internal/app`. They must not call provider, store, or token
packages directly. New CLI commands use the same application services.

## Errors and cancellation

- `Ctrl+C` cancels a long operation.
- Plaid Link cancellation saves nothing and returns to the add screen.
- If link succeeds but sync fails, the item stays linked. The TUI offers retry
  or return to the main screen.
- A nickname write failure keeps the nickname prompt open.
- Sync-all continues after one item fails and reports each item result.
- Unlink removes the item at Plaid, deletes local data, then deletes its token.
- If local unlink cleanup fails, the token stays available for a retry.
- Recoverable errors stay in the TUI and show a recovery action.
- Normal exit, cancellation, and fatal errors restore the terminal state.

## Verification

Automated tests cover current CLI behavior and the complete add flow. Add-flow
tests cover success and a sync failure after a successful link. The failure
test confirms that the saved item remains recoverable.

A manual smoke check covers navigation, resizing, cancellation, and terminal
restoration. Run `go test ./...` for full automated verification.
