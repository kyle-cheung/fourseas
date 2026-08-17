# fourseas

Pull your credit card transactions from Plaid into a local DuckDB file, then
answer your own questions in SQL.

There is no web interface and no server. `fourseas` writes one file. You read it
with the DuckDB command line tool.

## Quick start

```sh
cp .env.example .env          # then put your Plaid keys in it
go build -o bin/fourseas ./cmd/fourseas

bin/fourseas link             # link one card in the browser. Repeat for each card
bin/fourseas sync             # fetch transactions into data/fourseas.duckdb
bin/fourseas accounts         # list the accounts, with their ids
bin/fourseas accounts nickname <account-id> "Amex Daily"
```

Plaid needs setup in its dashboard before `link` works. See
[docs/plaid-setup.md](docs/plaid-setup.md).

## Commands

| Command | What it does |
| ------- | ------------ |
| `fourseas link` | Link one institution through Plaid Link in the browser |
| `fourseas sync` | Fetch what changed from Plaid, refresh FX rates, and print the newest rows |
| `fourseas sync --fx` | Refresh FX rates only |
| `fourseas accounts` | List accounts with ids, balances, and nicknames |
| `fourseas accounts nickname <id> "<name>"` | Name an account. An empty name clears it |
| `fourseas show` | Print the newest stored rows without calling Plaid |
| `fourseas reset` | Drop all data and build the schema again. Asks first |

Settings come from `.env`. The database is `data/fourseas.duckdb` unless you set
`FOURSEAS_DB_PATH`.

### Refresh FX rates

Normal `fourseas sync` refreshes FX rates after it fetches transactions from
Plaid. An FX failure prints a warning and does not fail the transaction sync.

Use `fourseas sync --fx` when the stored non-USD transactions need fresh rates
without a Plaid sync. It refreshes FX rates only. This mode is strict: an FX
failure makes the command fail.

## Exploring the data

### Open the file

Install the DuckDB command line tool (`brew install duckdb`), then:

```sh
duckdb -readonly data/fourseas.duckdb
```

Two things to know before you start.

**Use `-readonly`.** You are reading, not writing, and read-only mode cannot
damage the file.

**Close the shell before you sync.** DuckDB permits one process at a time on a
file, and this includes a read-only shell. If the shell is open, `fourseas sync`
stops with a lock error. If a sync is running, the shell cannot open the file.
Sync writes in short bursts, so this is a wait of seconds.

`fourseas` writes with DuckDB v1.5.5, the version its driver bundles. An older
command line tool can refuse the file. `duckdb --version` reports yours.

### Read `v_transactions`, not `transactions`

`v_transactions` is the view to explore. It does three things the raw table does
not:

1. It joins each transaction to its account and institution, so a row carries
   `nickname` and `institution_name` instead of only Plaid's identifiers.
2. It hides superseded rows. Plaid gives the pending and the posted version of
   one charge different transaction ids. Both are stored. Without the view, one
   charge is counted two times.
3. It adds USD conversion fields: `base_amount`, `base_currency`, and
   `fx_rate`.

The `account` column is the name to group by. It is the nickname, or the
provider's name when there is no nickname, or the raw account id when there is
no account row yet.

`base_amount` is the transaction amount in USD. `base_currency` is `USD` when a
conversion is available. `fx_rate` is the rate used for that conversion. USD
transactions have an `fx_rate` of 1.

For a non-USD transaction, the view uses an ASOF join to select the latest rate
on or before the transaction date. A weekend or holiday uses the prior available
rate. A transaction before the first stored rate has null conversion fields.

The raw tables stay available. `transactions` keeps the provider fields, such as
the original `amount` and `currency`; conversion fields exist in the view only.
Query `transactions` when you want the superseded rows, for example to see what
a charge looked like while it was pending.

### Worked examples

The output below comes from real runs against a small database. Your own
numbers will differ. The column names these queries use are held by a test, so
a rename to the view fails the build rather than the README.

**One card, since a date.** This is the question this application was built for.

```sql
SELECT date, account, description, amount, currency
FROM v_transactions
WHERE nickname = 'Amex Daily' AND date >= '2026-08-01'
ORDER BY date DESC;
```

```
┌────────────┬────────────┬──────────────────────────┬───────────────┬──────────┐
│    date    │  account   │       description        │    amount     │ currency │
├────────────┼────────────┼──────────────────────────┼───────────────┼──────────┤
│ 2026-08-11 │ Amex Daily │ Pacific Gas And Elecwest │      138.9800 │ USD      │
│ 2026-08-03 │ Amex Daily │ Blue Bottle Coffee       │       24.7500 │ USD      │
└────────────┴────────────┴──────────────────────────┴───────────────┴──────────┘
```

**Spend by month.**

```sql
SELECT strftime(date, '%Y-%m') AS month, currency, sum(amount) AS spend
FROM v_transactions
GROUP BY month, currency
ORDER BY month, currency;
```

Group by `currency` as well as by month. A sum across currencies is not a
number you can trust. See [Limits](#limits) below.

**Spend in USD.** `base_amount` uses the rate that applied on each transaction
date. Check the conversion coverage before you use this total.

```sql
SELECT strftime(date, '%Y-%m') AS month, sum(base_amount) AS spend_usd
FROM v_transactions
WHERE date >= '2026-08-01'
GROUP BY month
ORDER BY month;
```

**Spend by category.** The category is Plaid's primary personal finance
category, such as `FOOD_AND_DRINK`.

```sql
SELECT category, currency, sum(amount) AS spend
FROM v_transactions
WHERE date >= '2026-08-01'
GROUP BY category, currency
ORDER BY spend DESC;
```

**Spend by account.**

```sql
SELECT account, institution_name, currency, sum(amount) AS spend, count(*) AS rows
FROM v_transactions
GROUP BY account, institution_name, currency
ORDER BY spend DESC;
```

**The largest charges.** A positive amount is money leaving the account, so a
payment or a refund is negative. `WHERE amount > 0` keeps payments out of a
list of purchases.

```sql
SELECT date, account, description, amount, currency
FROM v_transactions
WHERE amount > 0
ORDER BY amount DESC
LIMIT 20;
```

**What has not posted yet.**

```sql
SELECT date, account, description, amount
FROM v_transactions
WHERE pending
ORDER BY date DESC;
```

**A merchant over time.**

```sql
SELECT strftime(date, '%Y-%m') AS month, count(*) AS visits, sum(amount) AS spend
FROM v_transactions
WHERE description ILIKE '%coffee%' AND currency = 'USD'
GROUP BY month
ORDER BY month;
```

**See the columns.**

```sql
DESCRIBE v_transactions;
```

## Limits

**Cross-currency totals need a coverage check.** `base_amount` holds the amount
in USD, the base currency. USD rows carry it with an `fx_rate` of 1.0. A
non-USD row has a value only after a rate is stored for it.

This is deliberate. A wrong total is worse than a missing one. But note what
`sum` does with nulls: it skips them silently. So a sum of `base_amount` across
a USD card and a Canadian card is a USD-only total that looks complete.

Check the coverage before you trust a base currency total:

```sql
SELECT currency, count(*) AS rows, count(base_amount) AS convertible
FROM v_transactions
GROUP BY currency
ORDER BY currency;
```

```
┌──────────┬───────┬─────────────┐
│ currency │ rows  │ convertible │
├──────────┼───────┼─────────────┤
│ CAD      │     1 │           0 │
│ USD      │     3 │           3 │
└──────────┴───────┴─────────────┘
```

Use `fourseas sync --fx` to refresh missing rates, or group by `currency` and
read one currency at a time.

**About 90 days of history.** That is Plaid's default window for a newly linked
Item. More history needs an explicit historical request.

**Losing the file forces a full sync again.** The cursor lives with the data, so
the two can never disagree. Deleting the file fetches everything again, which is
cheap and safe.

**Access tokens sit in a plain file.** `.secrets/tokens.json`, mode 0600, not in
git. This is good enough for one local user, and not for anything shared.

**One user, one machine.** No accounts, no sharing, no sync between machines.

## Schema

Version 2. Money columns are `DECIMAL(18,4)`, never `DOUBLE`, because a float
sum of money is wrong.

| Table | Holds |
| ----- | ----- |
| `institutions` | One row for each linked Plaid Item |
| `accounts` | One row for each card, with balances and the nickname |
| `transactions` | Every transaction, including the superseded ones |
| `fx_rates` | Daily exchange rates from each currency to USD |
| `sync_state` | The cursor for each institution |
| `schema_version` | One integer |
| `v_transactions` | The view above: joined, and without superseded rows |

There is no migration framework. When the file on disk has another version, the
command stops and tells you to run `fourseas reset`. All of this data can be
fetched again from Plaid, so migrations would be ceremony.

Upgrading from schema version 1 requires `fourseas reset`, followed by a full
`fourseas sync`. The reset removes the local database, and the full sync fetches
the source data again.

The full design is in
[docs/superpowers/specs/2026-08-12-fourseas-build1-design.md](docs/superpowers/specs/2026-08-12-fourseas-build1-design.md).

## Development

```sh
go build ./...
go test ./...
go vet ./...
```

No test uses the network.

```
cmd/fourseas/            commands and terminal output
internal/model/          canonical types. Imports nothing
internal/provider/       the source interface
internal/provider/plaid/ link, sync, and mapping
internal/store/          DuckDB, one file for each table
internal/tokens/         the access token file
```
