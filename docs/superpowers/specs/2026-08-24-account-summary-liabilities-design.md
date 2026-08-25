# Account Summary and Plaid Liabilities Design

Date: 2026-08-24

Status: Approved

## Purpose

Show the information needed to check each card from the main menu:

- Account nickname
- Current balance
- Current payment due date when Plaid supplies one
- Most recent payment date and amount

Plaid Transactions does not supply payment due dates. Plaid Liabilities supplies
the due date and recent payment fields. Liabilities is a separate Plaid product
and can have separate billing.

## Scope

This change will:

- Enable Liabilities by default for a new link.
- Let the user turn Liabilities off before a new link starts.
- Let an existing credit account request Liabilities consent.
- Fetch and store liability data during sync.
- Show an account summary table above the main menu.
- Add matching CLI flags, commands, and output.

This change will not disable Liabilities after Plaid activates it. Plaid requires
the whole Item to be unlinked to stop that subscription. Issue
[#24](https://github.com/kyle-cheung/fourseas/issues/24) tracks a future
replace-Item workflow.

## Main menu

The wordmark stays at the top. The account table follows it. The `Main menu`
heading moves below the table and stays directly above the menu choices.

```text
  fourseas ≋



  Account          Balance         Due       Last payment
  Amex Daily       1,284.21 USD    Sep 12    Aug 20 · 500.00 USD
  Scotia Visa        320.10 CAD    Sep 04    Aug 27 · 320.10 CAD
  Wealthsimple     2,400.00 CAD    —         —
  Wealthsimple       810.00 CAD    —         —

  Main menu

› Accounts                                      4 accounts
  Add account
  Sync                              ● Last sync: 1 day ago
```

The first column uses the nickname. It falls back to the provider account name,
then the account ID, as it does today. The existing account order stays in use.

Money has two decimal places and includes the account currency. A date in the
current year uses `Mon DD`. A date from another year includes the year. Missing
liability data uses `—`. The latest-payment cell needs both its date and amount;
if Plaid omits either value, the cell uses `—`.

The table uses `charm.land/lipgloss/v2/table`. It has no outer border and uses
the current heading and muted styles. Its width is the TUI content width. Cells
do not wrap, so one account always uses one terminal line. Lipgloss reduces and
clips columns when the terminal is narrow.

When there are no accounts, the table area says `No accounts are linked yet.`
The menu stays usable.

## New link setup

`Add account` opens one setup screen before Plaid Link starts:

```text
  fourseas ≋

  Add account

  Transaction history       730 days
› Enable Liabilities API?    Yes
    Fetch credit card statement, payment, and interest details.
  Continue
```

Liabilities defaults to `Yes`. Enter toggles it between `Yes` and `No`.
Selecting the history row opens the existing history choices. Selecting
`Continue` starts Plaid Link with both settings.

When Liabilities is on, the Link token puts `liabilities` in
`additional_consented_products`. It stays out of the required `products` list.
This does not restrict the institution list and does not activate billing until
fourseas calls `/liabilities/get`.

When Liabilities is off, the Link token does not request Liabilities consent.
Later syncs do not call `/liabilities/get` for that Item.

## Existing account consent

A credit account whose Item does not have Liabilities enabled shows an
`Enable statement data` action in account details. The action opens Plaid Link
in update mode for the whole institution Item. The screen will make the
institution scope clear.

Update mode sends the existing access token and `liabilities` in
`additional_consented_products`. Its Link token request omits `products` and all
new-Item product settings. Plaid rejects a request that combines an access token
with the normal Transactions product list.

Update mode also uses its own browser success path. A successful callback ends
the flow without calling `/item/public_token/exchange`. Plaid keeps the existing
access token and Item.

After Link succeeds, fourseas records that Liabilities is enabled and fetches
liability data immediately. A canceled or failed consent flow leaves the local
setting unchanged.

## CLI interface

The link command gains one Boolean flag:

```sh
fourseas link
fourseas link --liabilities=false
fourseas link --days 365 --liabilities=false
```

The flag defaults to true. Help text explains that Liabilities supplies
statement, payment, and interest details and can have separate Plaid billing.

An existing account can request consent with:

```sh
fourseas accounts liabilities enable <account-id>
```

The command resolves the account ID to its institution Item, opens update mode,
records the enabled setting, and fetches the first liability snapshot.

`fourseas accounts` adds due date and latest payment columns. `fourseas sync`
refreshes enabled liability data without a new flag.

## Local configuration

Each saved token Item gains a Boolean `liabilities` field.

- New Items store the choice from link setup.
- Old token files read a missing field as false.
- A successful existing-Item consent changes it to true.
- A failed or canceled consent attempt does not change it.

This field controls whether sync calls the Liabilities endpoint. It does not
claim that the institution will return liability data.

## Provider boundary

Add a provider-neutral `CreditLiability` model with provider, Item ID, account
ID, payment due date, last payment date, last payment amount, and fetch time.
Money uses `decimal.NullDecimal`.

Plaid mapping reads `next_payment_due_date`, `last_payment_date`, and
`last_payment_amount`. It validates provider dates and converts the one API
floating-point amount to a decimal at the provider boundary. Plaid models
`account_id` as nullable. A liability with a null or empty account ID is skipped
instead of being written with an empty primary-key value.

Liability retrieval stays separate from `Provider.Sync`, which paginates
transactions. A small liability-capable provider interface adds one
`Liabilities(context.Context)` operation. The application calls it at most once
for each enabled Item in one sync, after transaction pagination completes.

## Storage

Add an `account_liabilities` table with these columns:

```text
provider
item_id
account_id
payment_due_date
last_payment_date
last_payment_amount
fetched_at
```

The primary key is `(provider, account_id)`. Money uses `DECIMAL(18,4)`. Dates
use `DATE`.

Every successful fetch uses a plain replace-all upsert. Null due-date and payment
fields clear their stored values. This prevents a past due date from remaining
on screen after Plaid reports that no payment is expected. Fourseas does not
infer a payment from negative transactions because it could mislabel a refund.

`AccountViews` left-joins liability data. Accounts remain visible when no
liability row exists. Unlink cleanup deletes liability rows for the Item.

No separate liability state table is needed. `tokens.Item.Liabilities` says
whether fourseas must fetch the product. The existing `sync_state.last_status`
records sync errors, including missing consent.

The new table uses `CREATE TABLE IF NOT EXISTS`. Store startup already reapplies
additive DDL, so existing version 2 databases gain it without a reset or schema
version change. The reset table list includes the table.

## Sync behavior

For each Item, sync does this work in order:

1. Sync and commit every transaction page as it does today.
2. Stop if the token Item has Liabilities off.
3. Call `/liabilities/get` once.
4. Classify the result.
5. Store liability rows in one local transaction.
6. Return refreshed account views.

Result rules:

- A successful fetch replaces all liability fields for each returned account.
- `NO_LIABILITY_ACCOUNTS` shows `—` and does not fail transaction sync.
- `PRODUCT_NOT_READY` shows `—`, does not fail transaction sync, and retries on
  the next sync. This is the expected result for a fresh Item while Plaid
  prepares Liabilities.
- `ADDITIONAL_CONSENT_REQUIRED` shows `—`, stays in
  `sync_state.last_status`, and makes the enable action available.
- A temporary Plaid or mapping error keeps stored liability data and marks the
  institution sync as failed.
- Context cancellation follows the existing behavior and records no new status.

The current balance still comes from the account data returned by Transactions.
The latest payment amount uses the account currency because Plaid does not put a
separate currency on that liability field.

## Error presentation

Unsupported data is an expected absence, not an error. The table shows `—`.

Liabilities data that is not ready is also an expected temporary absence. The
table shows `—`, the Item sync succeeds, and the next sync retries it.

Missing consent gives an actionable account-detail option. The CLI error names
the `accounts liabilities enable` command.

A temporary liability failure uses the existing per-institution sync failure
presentation. It does not roll back transaction pages that already committed.
The next sync retries the liability call.

User-facing errors never print access tokens or Item IDs.

## Tests

Tests protect the policy boundaries with a small number of broad cases:

- One Link request test covers the default-on setting and explicit opt-out.
- One TUI setup test covers the default, toggle, and value passed to Link.
- One Plaid mapping test covers values, nulls, a nullable account ID, decimal
  conversion, and dates.
- One store test covers replace-all upserts, including null fields.
- One application test proves that pagination causes one liability call per Item
  and covers unsupported, not-ready, missing-consent, and temporary errors.
- One update-mode test proves that the request omits `products`, keeps the access
  token, and skips token exchange.
- Focused CLI and TUI assertions cover commands, main-menu placement, normal
  width, and narrow width.
- One store startup test proves that a version 2 database gains the additive
  table without a reset.

Provider tests use fake HTTP servers. Application and TUI tests use existing fake
boundaries. Automated tests do not call production Plaid.
