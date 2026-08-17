# Fourseas FX rates

Date: 2026-08-16
Status: approved design

## Purpose

After a transaction sync, fetch the exchange rates that are necessary to show
all transaction amounts in USD. Store the rates once and calculate converted
amounts in `v_transactions`.

The raw transaction remains the provider record. It keeps its original
`amount` and `currency`. Derived conversion values do not belong in the
`transactions` table because a rate correction would make them stale.

## Decisions

### Base currency

The base currency remains the existing `model.BaseCurrency` value, `USD`.
Account linking does not ask the user to select a base currency.

One store query lists each non-USD transaction currency and its oldest
transaction date. A non-empty result both triggers FX sync and defines its
work. Do not add a separate trigger query. This rule also converts a database
that contains only one CAD account.

### Rate source

Use the public Frankfurter v2 API at `https://api.frankfurter.dev`. It does not
need an API key. Use Frankfurter's default blend of providers instead of
pinning one provider.

Request each source currency directly against USD. For example, a CAD request
uses CAD as the API base and USD as the quote. The stored rate therefore has
this meaning:

```text
amount in CAD * CAD-to-USD rate = amount in USD
```

Frankfurter supports base and quote filters, historical dates, and date
ranges. Its v2 documentation is at <https://frankfurter.dev/>.

Frankfurter's default mode blends rates from many providers. It is not limited
to ECB publication dates. However, the design must also work when a provider
does not publish on a weekend, holiday, or the current date.

### Currency normalization

Store `transactions.currency`, `fx_rates.currency`, and
`fx_rates.base_currency` as uppercase ISO codes. Normalize at both store write
boundaries so a provider-specific mapper cannot bypass the rule. Compare the
base currency with `upper(currency)` in the work query. Validate Frankfurter
response currencies after normalizing them to uppercase.

### Derived values

Remove `base_amount`, `base_currency`, `fx_rate`, and `fx_date` from the raw
`transactions` table and from the raw transaction model. Remove `WithBase`.
Move the three computed conversion fields to `TransactionView`, which models
the view result instead of the stored provider record.

Keep these computed columns in `v_transactions`:

- `base_amount`
- `base_currency`
- `fx_rate`

Do not expose `fx_date`.

For a USD transaction, the view uses rate `1`, copies `amount` to
`base_amount`, and sets `base_currency` to `USD`. It does not need an
`fx_rates` row for USD.

For a non-USD transaction, the view uses an `ASOF LEFT JOIN` to select the
newest rate on or before the transaction date:

```sql
ASOF LEFT JOIN fx_rates r
  ON r.currency = t.currency
 AND r.base_currency = 'USD'
 AND r.date <= t.date
```

This rule carries the most recent published rate across weekends, holidays,
publication lag, and future-dated transactions. It multiplies `amount` by
`rate` and casts `base_amount` to `DECIMAL(18,4)`. If no rate exists on or
before the transaction date, `base_amount`, `base_currency`, and `fx_rate` are
null. This keeps a real coverage gap visible and avoids a plausible but wrong
total. DuckDB documents this lookup behavior in its
[ASOF JOIN reference](https://duckdb.org/docs/stable/sql/query_syntax/from.html#as-of-joins).

## Schema

Raise `SchemaVersion` from 1 to 2. The application has no migration framework.
An existing version 1 database stops with the current instruction to run
`fourseas reset`, then the next sync fetches the source data again.

Add this table:

### `fx_rates`

| Column | Type | Notes |
| ------ | ---- | ----- |
| `date` | DATE | Rate date and part of the primary key |
| `currency` | VARCHAR | Source currency and part of the primary key |
| `base_currency` | VARCHAR | Destination currency and part of the primary key; `USD` in this build |
| `rate` | DECIMAL(18,8) | Multiply a source amount by this value |

The primary key is `(date, currency, base_currency)`. An upsert replaces the
rate when the same key is fetched again. The table does not store a provider
column because this build always uses the Frankfurter default blend.

Add `fx_rates` to the schema reset list. Remove the four derived columns from
the `transactions` DDL and its read and write paths.

## Components

### Frankfurter client

Add a small client for the Frankfurter rate-range endpoint. Its input is a
source currency, destination currency, start date, and end date. Its output is
a list of typed rate rows.

The client:

- uses the caller's context;
- uses JSON numbers without a binary floating-point conversion;
- rejects non-success HTTP status codes;
- rejects malformed JSON, invalid dates, non-positive rates, and response rows
  with unexpected currencies;
- allows the HTTP base URL and client to be replaced in tests.

No production code or test depends on a Frankfurter API key.

### Store

Add focused store operations that:

- list each non-USD transaction currency and its oldest transaction date;
- read the oldest and newest stored rate date for one currency pair;
- upsert one returned range in a database transaction;
- read rates through `v_transactions`.

Split the raw and view read paths. `transactionColumns` and `scanTransaction`
continue to read only the raw transaction fields. Add a separate view column
list and `scanTransactionView` for the raw fields plus computed conversion and
display fields. Remove `viewScanner`; the view scanner reads its own explicit
shape.

### FX sync service

Keep orchestration separate from the Frankfurter HTTP client and the store.
For each required currency, the FX sync service makes one range request through
the current UTC date. Choose the start date as follows:

- If no rates are stored, start at the oldest transaction date.
- If the oldest transaction is before the oldest stored rate, start at the
  oldest transaction date.
- Otherwise, start at the newest stored rate date.

If the selected start date is after today, clamp it to today. This lets a
future-dated transaction acquire the latest available rate for its ASOF
lookup without sending an inverted date range to Frankfurter.

Starting again at the newest stored rate refreshes the latest published value.
The idempotent upsert replaces that row and inserts any newer rows. A newly
discovered older transaction causes a complete backfill for the expanded
span. Do not detect individual missing calendar dates.

Process currencies sequentially. The expected data set is small, and
parallel requests add no useful behavior in this build.

The sync result reports inserted or updated row counts for each currency and
collects currency-specific failures.

## Command behavior

### `fourseas sync`

Keep the current Plaid behavior. Run FX sync after the Plaid loop unless every
attempted item failed. Also run FX sync when every saved item was skipped,
because the database can still hold transactions from an earlier environment.

Print one concise FX count for each successful currency. If a currency fails,
print a warning and continue with the remaining currencies. An FX failure does
not change the successful exit status of the account and transaction sync.
Plaid data and cursors remain committed.

The existing rule for Plaid failures does not change. If every attempted Plaid
item fails, `fourseas sync` returns its existing error instead of starting the
post-sync FX step.

### `fourseas sync --fx`

Add `--fx` as the only sync option. It runs FX sync only. It must:

- skip Plaid configuration validation;
- skip token-file loading;
- open the existing DuckDB database;
- run the same FX sync service used by normal sync;
- return a nonzero exit status if any required currency fails.

An FX-only run with no non-USD transaction currencies succeeds and reports
that no rates are required. Unknown sync options return a usage error.

## Data flow

Normal sync:

1. Validate Plaid configuration and load linked items.
2. Sync each usable Plaid item with the existing page and cursor rules.
3. Stop with the existing error if every attempted Plaid item failed.
4. Query stored transactions for non-USD currencies and their oldest dates.
5. For each currency, choose one start date from its transaction and stored
   rate bounds.
6. Fetch through the current UTC date and atomically upsert the response.
7. Print FX warnings without failing the Plaid sync.
8. Print the newest transactions.

FX-only sync starts at step 4. It returns an error after processing all
currencies when one or more currencies failed.

## Error handling

A response is one storage unit. If validation or an upsert fails, do not store
a partial response. A failure for one currency does not stop the next
currency.

Normal sync prints FX failures as warnings. FX-only sync prints the same
currency context and returns an aggregate error so shell scripts can detect an
incomplete run.

Context cancellation stops further HTTP and database work and is returned to
the command.

## Testing

No test uses the network.

- Frankfurter client tests use an HTTP test server for the request path,
  query parameters, decimal parsing, bad status, bad JSON, and invalid rows.
- Store tests cover exact `DECIMAL(18,8)` round trips and idempotent upserts.
- Range-bound tests cover an empty rate table, an incremental refresh, and a
  newly discovered older transaction.
- View tests cover USD rate `1`, an exact-date conversion, a weekend or holiday
  conversion from the prior rate, a future-dated conversion, and null values
  for a transaction older than the first rate.
- Store tests prove transaction and rate currencies are normalized to
  uppercase.
- Transaction store tests prove raw rows no longer contain derived FX values.
- Command tests prove normal sync warns without failing, while `sync --fx`
  returns an error on an FX failure and bypasses Plaid requirements.
- Schema tests require version 2, include `fx_rates`, and confirm reset drops
  and rebuilds it.
- Existing sync pagination, cursor atomicity, account, supersede, and view
  behavior tests continue to pass.

## Documentation

Update the README to:

- document `fourseas sync --fx`;
- add `fx_rates` to the schema table list;
- explain that `v_transactions.base_amount` is computed from daily rates;
- replace the current statement that cross-currency totals do not work;
- retain the warning that missing rates produce null converted values.

## Out of scope

- User-selectable base currency
- A pinned central-bank provider
- Intraday or market-trading rates
- A schema migration framework
- Rate editing in the CLI
- Concurrent Frankfurter requests
