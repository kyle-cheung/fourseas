# Providence probe

A test bench, not a product. It answers one question:

> Can we pull real credit card transactions automatically into a local DuckDB
> file and read them back?

The target cards are a Scotiabank credit card (Canada) and an American Express
credit card (United States). The source is Plaid. We expect to discard this code.

The design is in
[`docs/superpowers/specs/2026-08-12-plaid-duckdb-probe-design.md`](docs/superpowers/specs/2026-08-12-plaid-duckdb-probe-design.md).

## Setup

1. Make a Plaid account at <https://dashboard.plaid.com>. The entry tier is free.
2. Copy `.env.example` to `.env` and add your client id and secret.
3. Build the probe:

   ```sh
   go build -o bin/probe ./cmd/probe
   ```

Start with `PLAID_ENV=sandbox`. Sandbox needs no approval and proves the whole
path works. Then change to `PLAID_ENV=production` and use the production secret
for your real cards.

## Use

```sh
./bin/probe link    # link one card in the browser; repeat for the next card
./bin/probe sync    # fetch new transactions, store them, print the newest rows
./bin/probe show    # print the stored rows again without calling Plaid
```

In sandbox, sign in with the user name `user_good` and the password `pass_good`.

`sync` is safe to run more than one time. Rows are keyed on the provider and the
transaction id, so a repeated run updates rows instead of duplicating them.

## Scotiabank and OAuth

Scotiabank uses OAuth, so Plaid needs a redirect URI that you registered first.

1. In the Plaid dashboard, go to Developers > API > Allowed redirect URIs.
2. Add `http://localhost:8080/oauth` and click Save changes.
3. Remove the `#` from the `PLAID_REDIRECT_URI` line in `.env`.

American Express does not need this. If `probe link` stops with a redirect URI
error, this step is missing.

## Data Transparency Messaging

Plaid refuses to create a link token until the Link customization has at least
one use case. The error is `INVALID_LINK_CUSTOMIZATION`. It applies to sandbox
as well as production, and no API setting avoids it.

1. Go to <https://dashboard.plaid.com/link/data-transparency-v5>.
2. Open the Data Transparency section.
3. Select at least one use case. "Personal finance management" fits this probe.
4. Click Publish.

## Files it writes

| Path                     | Contents                       |
| ------------------------ | ------------------------------ |
| `.secrets/tokens.json`   | Plaid access tokens, mode 0600 |
| `data/providence.duckdb` | The transactions               |

Both are in `.gitignore`. The database holds no credentials.

## Layout

| Package                   | Purpose                                    |
| ------------------------- | ------------------------------------------ |
| `internal/model`          | The canonical `Transaction` type           |
| `internal/provider`       | The source interface                       |
| `internal/provider/plaid` | Link flow, sync, and Plaid to model mapping |
| `internal/store`          | DuckDB: schema, upsert, read               |
| `internal/tokens`         | The access token file                      |
| `cmd/probe`               | Commands and terminal output               |

`model` and `store` do not import Plaid, so a second source is a new package.

## Tests

```sh
go test ./...
```

No test uses the network. The tests cover the Plaid to model mapping, the
DuckDB round trip and upsert, the paging loop, and the token file.

## Amounts

A positive amount is money leaving the account. On a credit card, a purchase is
positive and a payment or a refund is negative. This is the Plaid convention,
kept unchanged.
