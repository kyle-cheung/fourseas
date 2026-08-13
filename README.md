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
| `fourseas sync` | Fetch what changed, store it, and print the newest rows |
| `fourseas accounts` | List accounts with ids, balances, and nicknames |
| `fourseas accounts nickname <id> "<name>"` | Name an account. An empty name clears it |
| `fourseas show` | Print the newest stored rows without calling Plaid |
| `fourseas reset` | Drop all data and build the schema again. Asks first |

Settings come from `.env`. The database is `data/fourseas.duckdb` unless you set
`FOURSEAS_DB_PATH`.

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

`v_transactions` is the view to explore. It does two things the raw table does
not:

1. It joins each transaction to its account and institution, so a row carries
   `nickname` and `institution_name` instead of only Plaid's identifiers.
2. It hides superseded rows. Plaid gives the pending and the posted version of
   one charge different transaction ids. Both are stored. Without the view, one
   charge is counted two times.

The `account` column is the name to group by. It is the nickname, or the
provider's name when there is no nickname, or the raw account id when there is
no account row yet.

The raw tables stay available. Query `transactions` when you want the superseded
rows, for example to see what a charge looked like while it was pending.

### Worked examples

Every query below is run by a test in
[`internal/store/guide_test.go`](internal/store/guide_test.go), so the guide
cannot go stale without the tests failing.

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

**Cross-currency totals do not work yet.** `base_amount` holds the amount in
USD, the base currency. USD rows carry it with an `fx_rate` of 1.0. Rows in
another currency, such as a Canadian card, carry **null**, because no exchange
rate is stored.

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

Until exchange rates arrive, group by `currency` and read one currency at a
time.

**About 90 days of history.** That is Plaid's default window for a newly linked
Item. More history needs an explicit historical request.

**Losing the file forces a full sync again.** The cursor lives with the data, so
the two can never disagree. Deleting the file fetches everything again, which is
cheap and safe.

**Access tokens sit in a plain file.** `.secrets/tokens.json`, mode 0600, not in
git. This is good enough for one local user, and not for anything shared.

**One user, one machine.** No accounts, no sharing, no sync between machines.

## Schema

Version 1. Money columns are `DECIMAL(18,4)`, never `DOUBLE`, because a float
sum of money is wrong.

| Table | Holds |
| ----- | ----- |
| `institutions` | One row for each linked Plaid Item |
| `accounts` | One row for each card, with balances and the nickname |
| `transactions` | Every transaction, including the superseded ones |
| `sync_state` | The cursor for each institution |
| `schema_version` | One integer |
| `v_transactions` | The view above: joined, and without superseded rows |

There is no migration framework. When the file on disk has another version, the
command stops and tells you to run `fourseas reset`. All of this data can be
fetched again from Plaid, so migrations would be ceremony.

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
