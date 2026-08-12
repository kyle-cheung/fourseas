# Fourseas build 1 — accounts, nicknames, and incremental sync

Date: 2026-08-12
Status: approved, not yet implemented
Supersedes the probe spec: [2026-08-12-plaid-duckdb-probe-design.md](2026-08-12-plaid-duckdb-probe-design.md)

## Purpose

The probe proved that Plaid can put real credit card transactions into a local
DuckDB file. Build 1 makes that useful.

**Deliverable.** Link any number of cards, give each account a nickname, sync
only what changed, and explore the result in the DuckDB command line tool.

No web interface. That is build 3.

## What the probe got right, and what it did not

Kept: the provider interface, cursor-based incremental sync, the local one-shot
Link server, and the token file.

Replaced: one denormalized `transactions` table, `DOUBLE` amounts, no account
records, no nicknames, and a sync that commits rows and cursors separately.

## Verified facts this design depends on

These were tested, not assumed.

| Fact | Evidence |
| ---- | -------- |
| `marcboeker/go-duckdb` is deprecated | The repository redirects to `duckdb/duckdb-go` from v2.5.0 |
| `duckdb/duckdb-go/v2` v2.10505.0 bundles DuckDB v1.5.5 | `SELECT version()` returned `v1.5.5 d8cdaa33fd` |
| `DECIMAL(18,4)` round trips through the driver | Scans to `duckdb.Decimal`; `0.1 + 0.2` gives `0.3`, while `DOUBLE` gives `0.30000000000000004` |
| Quack needs DuckDB v1.5.3 or later | DuckDB release notes, 2026-05-12 |
| Scotiabank OAuth completes | A live link succeeded with the localhost redirect URI |
| Plaid returns the account list on every sync call | The probe receives it and discards it |

## Decisions

**Driver: `github.com/duckdb/duckdb-go/v2`.** The old driver is deprecated. The
new one is maintained by the DuckDB team and bundles DuckDB v1.5.5.

**Command name: `fourseas`.** `cmd/probe` becomes `cmd/fourseas`.

**Money is `DECIMAL(18,4)`, never `DOUBLE`.** Floating point sums are wrong for
money, and this is a financial planning application.

**Base currency is USD.** Rows already in USD get `base_amount` with an
`fx_rate` of 1.0. Rows in CAD get **null**. A query that sums `base_amount`
across a Scotiabank card returns null instead of a plausible wrong number.
Exchange rates are build 2.

**One cursor for each institution, not for each account.** Plaid's
`/transactions/sync` works on an access token, and one access token covers every
account in that institution. A single card cannot be synced alone. The `tracked`
column on `accounts` filters what is shown, not what is fetched.

**No migration framework.** A `schema_version` table records the version. When
it does not match, the command stops and tells the user to run
`fourseas reset`, which drops everything and syncs again. All of this data can
be fetched again from Plaid, so migrations would be ceremony.

**Quack is not used in build 1, and is the expected answer for build 3.** The
single-writer limit only matters when a long-lived process holds the file open.
Build 1 writes in short bursts, so nothing blocks the DuckDB command line tool.
When build 3 adds a resident service, Quack solves it. It is beta now, and its
protocol and defaults are still changing.

## Schema

Version 1. All money columns are `DECIMAL(18,4)`.

### `institutions`

One row for each linked Plaid Item.

| Column | Type | Notes |
| ------ | ---- | ----- |
| `provider` | VARCHAR | Part of the primary key |
| `item_id` | VARCHAR | Part of the primary key |
| `institution_id` | VARCHAR | Plaid's identifier |
| `institution_name` | VARCHAR | For example "American Express" |
| `env` | VARCHAR | `sandbox` or `production` |
| `linked_at` | TIMESTAMP | |

### `accounts`

One row for each card or account.

| Column | Type | Notes |
| ------ | ---- | ----- |
| `provider` | VARCHAR | Part of the primary key |
| `account_id` | VARCHAR | Part of the primary key |
| `item_id` | VARCHAR | Refers to `institutions` |
| `name` | VARCHAR | Plaid's official name, or the short name when there is no official name |
| `mask` | VARCHAR | The last four digits |
| `type` | VARCHAR | For example `credit` |
| `subtype` | VARCHAR | For example `credit card` |
| `currency` | VARCHAR | |
| `nickname` | VARCHAR | Set by the user. Null until set |
| `tracked` | BOOLEAN | Default true. Controls display, not fetching |
| `balance_current` | DECIMAL(18,4) | |
| `balance_available` | DECIMAL(18,4) | |
| `balance_limit` | DECIMAL(18,4) | |
| `balance_updated_at` | TIMESTAMP | |
| `first_seen_at` | TIMESTAMP | |
| `last_seen_at` | TIMESTAMP | |

There is no separate `official_name` column. Plaid's `official_name` goes into
`name`, and `name` is used when `official_name` is empty.

### `transactions`

| Column | Type | Notes |
| ------ | ---- | ----- |
| `provider` | VARCHAR | Part of the primary key |
| `external_id` | VARCHAR | Part of the primary key. Plaid's `transaction_id` |
| `item_id` | VARCHAR | |
| `account_id` | VARCHAR | Refers to `accounts` |
| `date` | DATE | |
| `authorized_date` | DATE | Null when Plaid does not supply one |
| `name` | VARCHAR | The raw description |
| `merchant_name` | VARCHAR | Cleaned by Plaid. Often null |
| `amount` | DECIMAL(18,4) | Positive means money leaving the account |
| `currency` | VARCHAR | |
| `base_amount` | DECIMAL(18,4) | Null when the currency is not the base currency |
| `base_currency` | VARCHAR | `USD` when `base_amount` is set |
| `fx_rate` | DECIMAL(18,8) | 1.0 for USD rows in build 1 |
| `fx_date` | DATE | |
| `pending` | BOOLEAN | |
| `pending_transaction_id` | VARCHAR | The pending row this posted row replaces |
| `superseded_by` | VARCHAR | Set on a pending row when its posted row arrives |
| `category` | VARCHAR | Plaid's primary personal finance category |
| `synced_at` | TIMESTAMP | |

### `sync_state`

| Column | Type | Notes |
| ------ | ---- | ----- |
| `provider` | VARCHAR | Part of the primary key |
| `item_id` | VARCHAR | Part of the primary key |
| `cursor` | VARCHAR | |
| `last_synced_at` | TIMESTAMP | |
| `last_status` | VARCHAR | `ok` or a short failure reason |

### `schema_version`

A single row holding an integer version.

## Views

`v_transactions` is the surface for exploration. It joins transactions to
accounts and institutions, shows `nickname` and `institution_name`, and excludes
rows where `superseded_by` is set.

```sql
SELECT * FROM v_transactions WHERE nickname = 'Amex Daily' AND date >= '2026-08-01';
```

Raw tables stay available for anyone who wants superseded rows.

## The pending duplicate

Plaid gives the pending and the posted version of one charge different
transaction identifiers. Both are stored, so a naive sum counts the charge two
times. Real data already shows this:

```
2026-08-11  Pacific Gas And Elecwest      138.98  posted
2026-08-10  Pacific Gas Electric Company  138.98  pending
```

Rule: when a posted transaction arrives with a `pending_transaction_id`, set
`superseded_by` on the row it refers to. Keep both rows. `v_transactions` hides
the superseded one.

The posted row can arrive in the same page as the pending row or in a later
sync, so the rule runs after every page is written.

## Sync

The behavior does not change: read the saved cursor, ask Plaid for changes,
write them, save the new cursor, and repeat while `has_more`. A second run with
nothing new reports `0 added, 0 modified, 0 removed` and makes one API call.

One correctness fix: **each page's rows and its cursor commit in one database
transaction.** Today they are two, so a failure between them either loses a page
or writes it two times.

Each page, inside one transaction:

1. Upsert added and modified transactions.
2. Delete removed transactions.
3. Upsert accounts and their balances from the same response.
4. Resolve supersedes.
5. Save `next_cursor` and `last_synced_at`.

A failure on one institution reports and continues to the next. This already
works and must be kept.

## Commands

```
fourseas link                                 Link an institution in the browser
fourseas accounts                             List accounts with ids, balances, nicknames
fourseas accounts nickname <id> "Amex Daily"  Name an account
fourseas sync                                 Fetch what changed
fourseas sync --reset                         Discard cursors and fetch again
fourseas show                                 Print the newest rows
fourseas reset                                Drop all data. Asks for confirmation
```

## Package layout

```
cmd/fourseas/          commands and terminal output
internal/model/        canonical types. Imports nothing
internal/provider/     the source interface
internal/provider/plaid/  link, sync, and mapping
internal/store/        DuckDB, one file for each table
internal/tokens/       the access token file
```

`model` and `store` still do not import Plaid.

`store` is split into `schema.go`, `accounts.go`, `transactions.go`,
`sync_state.go`, and `views.go`. This is deliberate: issues 3, 4, 5, and 6 run
in parallel worktrees, and one large file would make them conflict.

## Testing

No test uses the network.

- Mapping from Plaid JSON to the model, using recorded responses.
- Store round trips for each table, including `DECIMAL` exactness.
- The upsert is idempotent.
- Paging follows every page and resumes from the saved cursor.
- A page commits rows and its cursor together, and a failure leaves both unchanged.
- Supersede resolution, using the real PG&E case, in one page and across two syncs.
- `base_amount` is set for USD and null for CAD.
- A schema version mismatch stops the command.

## Issues

Scotiabank OAuth was verified before this build started, so it is not an issue. A live link succeeded.

The issues are on GitHub, numbered in execution order. The numbers below are the
GitHub numbers.

| # | Issue | Depends on | Worktree |
| - | ----- | ---------- | -------- |
| 1 | Driver swap and rename | — | Alone |
| 2 | Schema, versioning, store rewrite | 1 | Alone |
| 3 | Accounts and balances | 2 | Yes |
| 4 | Base currency | 2 | Yes, beside 3 |
| 5 | Atomic page commit | 2 | Yes |
| 6 | Pending supersede | 2 | Yes, beside 5 |
| 7 | Nicknames | 3 | Yes |
| 8 | Views and documentation | 3, 4, 6 | Yes |

Two worktrees at a time is the ceiling. Issues 5 and 6 both rewrite the sync
write path, and issues 3 and 6 both touch transaction upserts. More than two in
parallel produces conflicts that cost more than the time saved.

### Issue 1 — driver swap and renames

Three mechanical renames that each touch every file, done together as one churn.

Replace `github.com/marcboeker/go-duckdb/v2` with
`github.com/duckdb/duckdb-go/v2`. Rename `cmd/probe` to `cmd/fourseas` and the
binary to `bin/fourseas`. Change the module path from
`github.com/kyle-cheung/fourseas2/providence` to
`github.com/kyle-cheung/fourseas/providence`, because the repository was renamed
from `fourseas2` to `fourseas`. Rename the three `PROBE_` environment variables
to `FOURSEAS_`: `PROBE_DB_PATH`, `PROBE_TOKENS_PATH`, and `PROBE_LINK_PORT`.
Update `.env.example` and `docs/plaid-setup.md` where they name the `probe`
command.

Done when: the module builds, `go vet` is clean, `grep -ri "probe\|fourseas2"`
finds nothing outside the two spec files, all existing tests pass with no change
to their logic or assertions, and `fourseas sync` still works against the live
Amex link.

The default database file is renamed to `data/fourseas.duckdb`. Anyone with the
old file either renames it or runs a full sync again, which is cheap.

### Issue 2 — schema, versioning, store rewrite

Create all four tables and `schema_version`. Add **every** column, including
`pending_transaction_id`, `superseded_by`, and the four currency columns, even
though issues 4 and 6 populate them. Split `store` into one file for each table.
Add `fourseas reset`.

Done when: the schema is created from nothing, a version mismatch stops the
command with a clear message, `DECIMAL` values round trip exactly, and the
store tests pass.

### Issue 3 — accounts and balances

Write the accounts from every sync response into `accounts`, with balances.
Add `fourseas accounts`.

Done when: after a sync, every account Plaid returned appears with its balance,
and `fourseas accounts` shows ids, names, masks, types, balances, and nicknames.

### Issue 4 — base currency

Populate `base_amount`, `base_currency`, and `fx_rate` for USD rows. Leave CAD
rows null.

Done when: a USD row has `base_amount` equal to `amount` with an `fx_rate` of
1.0, and a CAD row has null, proven by a test.

### Issue 5 — atomic page commit

Move the row writes and the cursor save into one database transaction.

Done when: an injected failure part way through a page leaves the rows and the
cursor unchanged, proven by a test.

### Issue 6 — pending supersede

Store `pending_transaction_id` and set `superseded_by`.

Done when: the PG&E pair produces one row in `v_transactions` and two rows in
`transactions`, both when the pair arrives in one page and when it arrives
across two syncs.

### Issue 7 — nicknames

Add `fourseas accounts nickname <account-id> "<name>"`.

Done when: a nickname is stored, shown by `fourseas accounts`, can be changed
and cleared, and an unknown id gives a clear error.

### Issue 8 — views and documentation

Create `v_transactions`. Write the exploration guide with example queries.

Done when: the view hides superseded rows and exposes nicknames, and the README
shows how to open the database and answer normal questions with SQL.

## Out of scope

No web interface. No exchange rates. No categorization beyond what Plaid
supplies. No budgets or forecasting. No scheduled or background sync. No more
than one user. No historical backfill past Plaid's default window. No Quack.

## Known limitations after build 1

**Cross-currency totals do not work.** Scotiabank rows have a null
`base_amount`, so any USD total excludes them. This is deliberate and visible
rather than silent. Exchange rates should be the first item in build 2.

**Losing the database forces a full resync.** The cursor lives with the data on
purpose, so the two can never disagree. The cost is that deleting the file
re-fetches everything, which is cheap and safe.

**Only about 90 days of history.** That is Plaid's default window for a new
Item. Pulling further back needs an explicit historical request.

**Access tokens sit in a plain file.** `.secrets/tokens.json`, mode 0600, not in
git. Good enough for one local user, not for anything shared.
