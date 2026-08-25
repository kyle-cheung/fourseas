# TUI Statement Balance Column Design

Date: 2026-08-25

Status: Approved

## Purpose

Show the latest credit-card statement balance in the main TUI account summary.
This lets a user compare the current balance with the balance on the last
statement without opening account details.

## Scope

Change only the main TUI account-summary table. Its headers, in this order,
will be:

```text
Account | Cur. balance | Stmt balance | Due | Last payment
```

The table keeps the existing account order, styles, natural column widths, and
one-line rows. It keeps the current whole-line truncation behavior on narrow
terminals. No new layout rule, width calculation, wrapping rule, or terminal
fallback is needed.

## Data Flow

The summary renderer already receives account views with current account and
liability data. For each row it will:

1. Render `BalanceCurrent` as the current balance, with the account currency,
   by using the existing money formatter.
2. Render the stored liability `LastStatementBalance` as the statement balance,
   with that same account currency and formatter.
3. Render `—` when `LastStatementBalance` is absent or invalid.

The summary does not fetch, calculate, convert, or persist a statement balance.
It reads only the liability value that the application already stores and makes
available in the account view.

## Display and Error Behavior

`Cur. balance` replaces the current `Balance` header. `Stmt balance` is placed
immediately after it. Each present amount has two decimal places and includes
the account currency, matching the existing summary presentation.

An absent statement balance is expected data, not an error. The cell displays
`—`. The screen does not add an alert, status message, retry, or error state.
Existing due-date and last-payment behavior remains unchanged.

## Non-goals

This change does not modify Plaid calls, API responses, schemas, models,
storage, CLI output, account-detail views, sync behavior, or liability error
handling. It does not add a statement date, payment calculation, currency
conversion, or a new summary table.

## Verification

Before changing production code, update existing summary-table expectations and
fixtures for the renamed current-balance header and the new statement-balance
column. Do not add test cases. Then run the focused TUI tests and the full test
suite. Confirm present values use the account currency, missing values show
`—`, and narrow-terminal rendering still truncates complete rows as it does
today.
