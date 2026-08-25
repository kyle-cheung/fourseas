# TUI Statement Balance Column Design

Date: 2026-08-25

Status: Approved

## Purpose

Show the latest credit-card statement balance in the main TUI account summary.
This lets a user compare the current balance with the balance on the last
statement without opening account details.

## Scope

Add the statement-balance data path and show it only in the main TUI
account-summary table. Its headers, in this order, will be:

```text
Account | Cur. balance | Stmt balance | Due | Last payment
```

The table keeps the existing account order, styles, natural column widths, and
one-line rows. It keeps the current whole-line truncation behavior on narrow
terminals. No new layout rule, width calculation, wrapping rule, or terminal
fallback is needed.

## Data Flow

Plaid already returns `last_statement_balance`, but the current mapper discards
it. Add `LastStatementBalance decimal.NullDecimal` to `model.CreditLiability`.
Map `GetLastStatementBalanceOk()` with the existing `optionalAmount` helper.

Persist the value in the branch-new `account_liabilities` table as
`last_statement_balance DECIMAL(18,4)`. Include the field in the liability
insert, `AccountViews` select and scan, and the reconstructed liability.
The main summary then reads `LastStatementBalance` from its account view.

For each row, render `BalanceCurrent` as the current balance and
`LastStatementBalance` as the statement balance. Use the existing money
formatter and the account currency for both values. Render `—` when the
statement balance is absent or invalid. The summary does not calculate or
convert a statement balance.

The schema version stays at 2. The table is new relative to `origin/main`, so
the existing additive DDL creates the column for a new database. Development
databases created from this unmerged branch need `fourseas reset` to get the
new column.

## Display and Error Behavior

`Cur. balance` replaces the current `Balance` header. `Stmt balance` is placed
immediately after it. Each present amount has two decimal places and includes
the account currency, matching the existing summary presentation.

An absent statement balance is expected data, not an error. The cell displays
`—`. The screen does not add an alert, status message, retry, or error state.
Existing due-date and last-payment behavior remains unchanged.

## Non-goals

This change does not change Plaid requests or API responses, CLI output,
account-detail views, sync control flow, liability error handling, or schema
version. It does not add a statement date, payment calculation, currency
conversion, or a new summary table.

## Verification

Before changing production code, update existing model, Plaid mapping, store,
and summary-table expectations and fixtures. Do not add test cases. Then run
the focused tests and the full test suite. Confirm the data reaches the account
view, present values use the account currency, missing values show `—`, and
narrow-terminal rendering still truncates complete rows as it does today.
