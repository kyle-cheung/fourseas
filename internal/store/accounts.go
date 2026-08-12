package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

const accountColumns = `
	provider, account_id, item_id, name, mask, type, subtype, currency,
	nickname, tracked,
	balance_current, balance_available, balance_limit, balance_updated_at,
	first_seen_at, last_seen_at`

// upsertAccountSQL keeps what the user owns and takes what the provider owns.
// A sync knows nothing about nicknames or tracking, so it must not erase them.
const upsertAccountSQL = `
INSERT INTO accounts (
	provider, account_id, item_id, name, mask, type, subtype, currency,
	nickname, tracked,
	balance_current, balance_available, balance_limit, balance_updated_at,
	first_seen_at, last_seen_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, account_id) DO UPDATE SET
	item_id            = excluded.item_id,
	name               = excluded.name,
	mask               = excluded.mask,
	type               = excluded.type,
	subtype            = excluded.subtype,
	currency           = excluded.currency,
	nickname           = coalesce(excluded.nickname, accounts.nickname),
	balance_current    = excluded.balance_current,
	balance_available  = excluded.balance_available,
	balance_limit      = excluded.balance_limit,
	balance_updated_at = excluded.balance_updated_at,
	last_seen_at       = excluded.last_seen_at
`

// UpsertAccounts writes the accounts a sync returned.
//
// Two columns are never overwritten here: nickname keeps its stored value
// unless a new one is given, and tracked and first_seen_at keep the values
// they were created with.
func (s *Store) UpsertAccounts(ctx context.Context, accounts []model.Account) error {
	if len(accounts) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, upsertAccountSQL)
	if err != nil {
		return fmt.Errorf("prepare account upsert: %w", err)
	}
	defer stmt.Close()

	for _, a := range accounts {
		_, err := stmt.ExecContext(ctx,
			a.Provider, a.AccountID, textArg(a.ItemID), textArg(a.Name), textArg(a.Mask),
			textArg(a.Type), textArg(a.Subtype), textArg(a.Currency),
			textArg(a.Nickname), a.Tracked,
			nullDecimalArg(a.BalanceCurrent), nullDecimalArg(a.BalanceAvailable),
			nullDecimalArg(a.BalanceLimit), timeArg(a.BalanceUpdatedAt),
			a.FirstSeenAt, a.LastSeenAt,
		)
		if err != nil {
			return fmt.Errorf("upsert account %s/%s: %w", a.Provider, a.AccountID, err)
		}
	}
	return tx.Commit()
}

// Accounts returns every stored account, newest institution activity first.
func (s *Store) Accounts(ctx context.Context) ([]model.Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts ORDER BY item_id, name, account_id`)
	if err != nil {
		return nil, fmt.Errorf("query accounts: %w", err)
	}
	defer rows.Close()

	var out []model.Account
	for rows.Next() {
		var (
			a                                         model.Account
			itemID, name, mask, kind, subtype         sql.NullString
			currency, nickname                        sql.NullString
			current, available, limit                 any
			balanceUpdatedAt, firstSeenAt, lastSeenAt sql.NullTime
		)
		err := rows.Scan(
			&a.Provider, &a.AccountID, &itemID, &name, &mask, &kind, &subtype, &currency,
			&nickname, &a.Tracked,
			&current, &available, &limit, &balanceUpdatedAt,
			&firstSeenAt, &lastSeenAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}

		if a.BalanceCurrent, err = toNullDecimal(current); err != nil {
			return nil, fmt.Errorf("balance_current: %w", err)
		}
		if a.BalanceAvailable, err = toNullDecimal(available); err != nil {
			return nil, fmt.Errorf("balance_available: %w", err)
		}
		if a.BalanceLimit, err = toNullDecimal(limit); err != nil {
			return nil, fmt.Errorf("balance_limit: %w", err)
		}

		a.ItemID = text(itemID)
		a.Name = text(name)
		a.Mask = text(mask)
		a.Type = text(kind)
		a.Subtype = text(subtype)
		a.Currency = text(currency)
		a.Nickname = text(nickname)
		a.BalanceUpdatedAt = timePtr(balanceUpdatedAt)
		if when := timePtr(firstSeenAt); when != nil {
			a.FirstSeenAt = *when
		}
		if when := timePtr(lastSeenAt); when != nil {
			a.LastSeenAt = *when
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const upsertInstitutionSQL = `
INSERT INTO institutions (
	provider, item_id, institution_id, institution_name, env, linked_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, item_id) DO UPDATE SET
	institution_id   = excluded.institution_id,
	institution_name = excluded.institution_name,
	env              = excluded.env
`

// UpsertInstitution writes one linked item. The time it was first linked is
// kept, because a later sync is not a new link.
func (s *Store) UpsertInstitution(ctx context.Context, i model.Institution) error {
	_, err := s.db.ExecContext(ctx, upsertInstitutionSQL,
		i.Provider, i.ItemID, textArg(i.InstitutionID), textArg(i.InstitutionName),
		textArg(i.Env), i.LinkedAt)
	if err != nil {
		return fmt.Errorf("upsert institution %s/%s: %w", i.Provider, i.ItemID, err)
	}
	return nil
}

// Institutions returns every linked item.
func (s *Store) Institutions(ctx context.Context) ([]model.Institution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, item_id, institution_id, institution_name, env, linked_at
		FROM institutions
		ORDER BY institution_name, item_id`)
	if err != nil {
		return nil, fmt.Errorf("query institutions: %w", err)
	}
	defer rows.Close()

	var out []model.Institution
	for rows.Next() {
		var (
			i             model.Institution
			id, name, env sql.NullString
			linkedAt      sql.NullTime
		)
		if err := rows.Scan(&i.Provider, &i.ItemID, &id, &name, &env, &linkedAt); err != nil {
			return nil, fmt.Errorf("scan institution: %w", err)
		}
		i.InstitutionID = text(id)
		i.InstitutionName = text(name)
		i.Env = text(env)
		if when := timePtr(linkedAt); when != nil {
			i.LinkedAt = *when
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
