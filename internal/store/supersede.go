package store

import (
	"context"
	"fmt"
)

// Plaid gives the pending and the posted version of one charge different
// identifiers, so both are stored and a naive sum counts the charge two times:
//
//	2026-08-11  Pacific Gas And Elecwest      138.98  posted
//	2026-08-10  Pacific Gas Electric Company  138.98  pending
//
// The posted row names the pending row it replaces. This sets superseded_by on
// the row it names. Both rows stay on disk; v_transactions hides the
// superseded one.

// resolveSupersedesSQL rewrites superseded_by for one provider from what the
// table holds. It is a recomputation, not an addition, which buys three things
// for one statement:
//
//   - The posted row may arrive in the same page as its pending row or in any
//     later sync, so the answer cannot be read from one page alone.
//   - An upsert writes the provider's empty superseded_by over a resolved one
//     every time a row is modified. Recomputing puts it back.
//   - A posted row Plaid takes back releases the pending row it was hiding,
//     instead of hiding a charge behind a row that is no longer stored.
const resolveSupersedesSQL = `
UPDATE transactions AS pending
SET superseded_by = (
	SELECT min(posted.external_id)
	FROM transactions AS posted
	WHERE posted.provider = pending.provider
	  AND posted.pending_transaction_id = pending.external_id
)
WHERE pending.provider = ?
`

// resolveSupersedes runs the rule through the caller's handle, so it commits
// with the page that caused it.
func resolveSupersedes(ctx context.Context, db execer, provider string) error {
	if _, err := db.ExecContext(ctx, resolveSupersedesSQL, provider); err != nil {
		return fmt.Errorf("resolve supersedes for %s: %w", provider, err)
	}
	return nil
}
