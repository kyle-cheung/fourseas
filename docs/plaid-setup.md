# Plaid setup

What you must configure in the Plaid dashboard before the CLI can link a card.
These are facts about Plaid, not about this application, so they stay true as
the application changes.

Every step here was found by hitting the failure it prevents.

## 1. Account and keys

Make an account at <https://dashboard.plaid.com>. The entry tier is free and
gives live production data at a small scale. You do not talk to sales for it.

Get the client id and the secrets from **Developers > Keys**. The sandbox secret
and the production secret are different values. Using the sandbox secret against
production gives `INVALID_API_KEYS`.

Put them in `.env`. See `.env.example`.

## 2. Data Transparency Messaging — required

Plaid refuses to create a link token until the Link customization has at least
one use case. Without it you get:

```
INVALID_LINK_CUSTOMIZATION (INVALID_INPUT): At least one Data Transparency
Messaging use case is required to be configured.
```

This applies to **sandbox as well as production**, and no API parameter avoids
it.

1. Go to <https://dashboard.plaid.com/link/data-transparency-v5>.
2. Open the Data Transparency section.
3. Select at least one use case. "Personal finance management" fits this
   application.
4. Click **Publish**. Selecting is not enough.

## 3. Redirect URI — required for OAuth banks

Scotiabank uses OAuth, so Plaid needs a redirect URI that you registered first.
American Express does not need this.

1. Go to **Developers > API > Allowed redirect URIs**. It is under Developers,
   not under Team Settings.
2. Add `http://localhost:8080/oauth`.
3. Click **Save changes**.
4. Remove the `#` from the `PLAID_REDIRECT_URI` line in `.env`.

The port must match `FOURSEAS_LINK_PORT` if you change it from 8080.

Link a bank that does not use OAuth first. It proves the rest of the path with
fewer moving parts.

## 4. Sandbox test values

Sandbox refuses real values. It is seeded with test users.

| Prompt         | Value          |
| -------------- | -------------- |
| Phone number   | `415-555-0011` |
| One-time code  | `123456`       |
| Bank user name | `user_good`    |
| Bank password  | `pass_good`    |

Every sandbox phone number uses the same one-time code. In production your own
phone number and your real bank sign-in work as normal.

Sandbox data comes from a fake institution called **Tartan Bank**, with accounts
named "Plaid Checking", "Plaid Saving", and so on. If you see those in your data
after moving to production, they are left over from a sandbox run.

## 5. Moving from sandbox to production

1. Change `PLAID_ENV` to `production` in `.env`.
2. Replace the secret with the production secret.
3. Link each card again. A sandbox access token does not work in production, and
   the CLI skips tokens that were linked in a different environment.

## Limits worth knowing

**History is chosen once, at link time.** `fourseas link` sends
`transactions.days_requested` in the link token request and asks for 730 days,
the most Plaid permits. Plaid would request 90 days if nothing were asked for.

Plaid fixes this amount while it initializes the transactions product, and it
cannot be raised for the life of the item: `days_requested` in a later
`/transactions/sync` call is ignored. To get more history for a card that is
already linked, remove the item and link the card again. Use
`fourseas link --days <n>` (30 to 730) to ask for less.

**One cursor for each institution, not for each account.** `/transactions/sync`
works on an access token, and one access token covers every account in that
institution. A single card cannot be synced alone.

**Pending and posted are separate transactions.** Plaid gives them different
transaction ids, and the posted one carries `pending_transaction_id` pointing at
the pending one. Anything that sums amounts must handle this or it double counts.

**Amounts are positive when money leaves the account.** On a credit card a
purchase is positive and a payment or refund is negative.
