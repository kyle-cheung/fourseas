# Plaid account discovery during initial sync

## Problem

Plaid can return `transactions_update_status: NOT_READY` from the first
`/transactions/sync` call. That response can have no transactions or accounts.
Fourseas treats it as a successful empty sync. The TUI then skips the
nickname prompts and returns to the main menu even though the access token was
saved.

If the user starts Add again, Plaid creates another billed Item for the same
login. The later Sync action works after Plaid prepares the transaction data,
which makes all duplicate Items visible.

## Design

`plaid.Source.Sync` will inspect `transactions_update_status`. When the status
is `NOT_READY` and the response has no accounts, it will call `/accounts/get`
with the same access token. If `/transactions/sync` returned accounts, the
source will use them without making the extra call. `/accounts/get` does not
wait for Transactions, and it returns the active accounts of a linked Item from
Plaid's cache.

The source will convert those accounts with the existing `toAccounts` function
and return them in the same `provider.Batch`. The batch will keep
`resp.NextCursor`, as it does for every other successful response. The existing
page transaction will store the accounts and cursor together. `App.SyncItem`
will then return the new account views, and the existing TUI flow will open one
nickname prompt for each account.

The add flow will not wait. A later Sync call will use the saved cursor and
fetch the transactions when Plaid has prepared them.

If `/accounts/get` fails, the source will return that API error. If Plaid reports
`NOT_READY` and `/accounts/get` returns no accounts, the source will return the
existing `provider.ErrProductNotReady` error. Both cases use the existing TUI
recovery screen. Retry runs `SyncItem`; it never starts Link again.

## Accepted limitation

The fallback page records a successful sync with zero transactions. The account
list does not show a persistent warning. The existing progress line reports
zero added, modified, and removed transactions. The next Sync call fills them.
This change does not add polling, a timer, or a new TUI state.

## Duplicate Items

Existing duplicate Items need manual removal by exact Item ID. Automatic
duplicate prevention is a separate change. It must compare Plaid Link
`onSuccess` account metadata before Fourseas exchanges the public token.

## Tests

- A Plaid source test will return `NOT_READY` from `/transactions/sync` and
  accounts from `/accounts/get`. It will verify the fallback request, mapped
  accounts, empty transaction changes, and unchanged `next_cursor`.
- Existing TUI tests already verify that returned accounts open the nickname
  flow and that `ErrProductNotReady` retries `SyncItem` instead of Link.

## Out of scope

- Automatic polling for transaction readiness.
- Automatic duplicate detection or removal.
- The local socket error during public-token exchange. GitHub issue #22 tracks
  that failure, and #21 stays open until both paths are resolved.
