# Plaid → DuckDB probe

Date: 2026-08-12
Status: approved, not yet implemented

## Purpose

Test one assumption before we build a financial planning app: can we pull real
credit card transactions automatically, put them in a local DuckDB file, and
read them back?

The target accounts are a Scotiabank credit card (Canada) and an American
Express credit card (United States).

This is a probe. We expect to discard the code. Keep it small.

## What we decided, and why

**Aggregator: Plaid.** Teller and SimpleFIN Bridge are less expensive, but both
supply United States institutions only. Neither can read the Scotiabank card.
Plaid supplies both countries with one API.

**Cost: none.** Plaid has a free entry tier that uses live production data. The
public documents describe it in two ways: a *Limited Production* service (about
200 API calls for each product with live data), and a free *Trial* plan with a
limit of 10 Production Items for teams in the United States and Canada. Two
cards is two Items. We confirm the exact terms when we register.

**Provider interface.** The transaction source is behind a small Go interface.
Today there is one implementation, Plaid. If the Plaid limits or terms become a
problem, a different provider is a new folder, not a rewrite.

**Authorization: local one-shot web server.** Plaid gives an `access_token` only
after Plaid Link completes in a browser. A command-line program cannot do this
alone. The program starts a temporary `localhost` server, serves Link, and
catches the result. This is the method in Plaid's own Go quickstart.

Scotiabank uses OAuth. Plaid needs a registered redirect URI for OAuth
institutions. We register the localhost URI in the Plaid dashboard.

## Design

### Commands

One binary, two commands.

- `probe link` — starts the temporary server, opens the browser to Plaid Link.
  The user signs in to the bank. The program exchanges the `public_token` for an
  `access_token`, writes it to a local file, and stops. Run once for each card.
- `probe sync` — reads the saved tokens, calls Plaid `/transactions/sync`,
  writes the rows to DuckDB, then prints the 10 newest rows as a table.

### Packages

| Package                 | Purpose                                                             | Imports              |
| ----------------------- | ------------------------------------------------------------------- | -------------------- |
| `internal/model`        | The canonical `Transaction` type. Nothing else.                     | —                    |
| `internal/provider`     | `Provider` interface: `Sync(ctx, cursor) ([]model.Transaction, string, error)` | `model`   |
| `internal/provider/plaid` | The only implementation. Link flow and sync.                      | `plaid-go`, `model`  |
| `internal/store`        | DuckDB. Create the table, upsert rows, read the newest N.           | `go-duckdb`, `model` |
| `cmd/probe`             | Wiring and terminal output.                                         | all                  |

`model` and `store` do not import Plaid.

### Data flow

Plaid JSON → the `plaid` package maps it to `model.Transaction` → `store`
upserts on `(provider, external_id)` → `store` reads the newest 10 → the
terminal prints them.

The upsert makes `probe sync` safe to run more than one time.

### Dependencies

- `github.com/plaid/plaid-go/v40` v40.1.0
- `github.com/marcboeker/go-duckdb/v2` v2.4.3 (uses cgo)
- Go 1.25

### Configuration and secrets

`.env` holds `PLAID_CLIENT_ID`, `PLAID_SECRET`, and `PLAID_ENV`. The user
supplies these values. `.env` is already in `.gitignore`.

Access tokens go to `.secrets/tokens.json`. Add `.secrets/` to `.gitignore`.

No secrets in the source code. No secrets in the DuckDB file.

### Files on disk

- `data/providence.duckdb` — the database. Add `data/` to `.gitignore`.
- `.secrets/tokens.json` — the access tokens.

### Errors

Fail immediately and show the cause. A missing key, a Plaid API error, or an
unsupported institution prints the true error and exits with a non-zero status.
For a probe, a clear failure is a useful result.

### Tests

Two tests. Neither uses the network.

1. **Mapping test.** Plaid JSON to `model.Transaction`, table-driven, with a
   recorded sample response. Wrong amount signs, wrong dates, and pending flags
   are the failures that stay hidden. This test finds them.
2. **Store round trip.** Write to a real DuckDB and read back. Includes a second
   insert of the same row to prove the upsert.

Do not mock DuckDB. Do not mock the Plaid HTTP layer.

## Out of scope

No categorization. No budgets. No scheduler. No web interface. No more than one
user. No migration framework. No historical backfill beyond the default Plaid
window.

## Risk we cannot remove with code

Whether the Plaid free tier and Scotiabank OAuth both work for this account. The
first run of `probe link` answers this.

## Success criteria

`probe sync` prints 10 real transaction rows from a DuckDB file, and those rows
came from the Scotiabank card and the Amex card.
