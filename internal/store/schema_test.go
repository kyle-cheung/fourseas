package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

func TestSchemaIsCreatedFromNothingAndStamped(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("version = %d, want %d", version, SchemaVersion)
	}

	for _, table := range []string{"institutions", "accounts", "transactions", "fx_rates", "account_liabilities", "sync_state", "schema_version"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Errorf("table %s is missing: %v", table, err)
		}
	}

	var column string
	err := s.db.QueryRowContext(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'main'
		  AND table_name = 'account_liabilities'
		  AND column_name = 'last_statement_balance'`).Scan(&column)
	if err != nil {
		t.Fatalf("read statement-balance column: %v", err)
	}
	if column != "last_statement_balance" {
		t.Errorf("liability column = %q, want last_statement_balance", column)
	}
}

func TestTransactionsTableDoesNotPersistConversionFields(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	rows, err := s.db.QueryContext(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'main' AND table_name = 'transactions'`)
	if err != nil {
		t.Fatalf("list transaction columns: %v", err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatalf("scan transaction column: %v", err)
		}
		columns[column] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list transaction columns: %v", err)
	}
	for _, column := range []string{"base_amount", "base_currency", "fx_rate", "fx_date"} {
		if columns[column] {
			t.Errorf("transactions has computed column %q, want raw provider fields only", column)
		}
	}
}

func TestReopeningTheSameFileKeepsOneVersionRow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	first := openAt(t, path)
	if err := first.Upsert(ctx, []model.Transaction{sample()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := openAt(t, path)
	var rows int
	if err := second.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_version`).Scan(&rows); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if rows != 1 {
		t.Errorf("schema_version holds %d rows, want 1", rows)
	}

	n, err := second.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("stored rows = %d, want the row written before the reopen", n)
	}
}

func TestVersionTwoDatabaseGainsAccountLiabilitiesOnOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.UpsertAccounts(ctx, []model.Account{sampleAccount()}); err != nil {
		t.Fatalf("upsert account: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	raw, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `DROP TABLE IF EXISTS account_liabilities`); err != nil {
		t.Fatalf("drop account liabilities: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()

	var tableExists bool
	if err := second.db.QueryRowContext(ctx, `
		SELECT count(*) > 0
		FROM information_schema.tables
		WHERE table_schema = 'main' AND table_name = 'account_liabilities'`).Scan(&tableExists); err != nil {
		t.Fatalf("look for account liabilities: %v", err)
	}
	if !tableExists {
		t.Error("account_liabilities is missing after reopening a version 2 database")
	}

	accounts, err := second.Accounts(ctx)
	if err != nil {
		t.Fatalf("read accounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0].AccountID != "acct-amex" {
		t.Errorf("accounts = %+v, want the stored account", accounts)
	}

	var version int
	if err := second.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 2 {
		t.Errorf("schema version = %d, want 2", version)
	}
}

func TestVersionTwoDatabaseAddsStatementBalanceColumnOnOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	first := openAt(t, path)
	account := sampleAccount()
	if err := first.UpsertAccounts(ctx, []model.Account{account}); err != nil {
		t.Fatalf("upsert account: %v", err)
	}
	if err := first.ReplaceLiabilities(ctx, "plaid", "item-1", []model.CreditLiability{{
		AccountID:         account.AccountID,
		LastPaymentAmount: nullDec("50.2500"),
		FetchedAt:         account.LastSeenAt,
	}}); err != nil {
		t.Fatalf("replace liabilities: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	raw, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, `ALTER TABLE account_liabilities DROP COLUMN last_statement_balance`); err != nil {
		t.Fatalf("drop statement-balance column: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}

	second := openAt(t, path)

	views, err := second.AccountViews(ctx)
	if err != nil {
		t.Fatalf("account views: %v", err)
	}
	if len(views) != 1 || views[0].AccountID != account.AccountID {
		t.Fatalf("account views = %+v, want the stored account", views)
	}
	liability := views[0].Liability
	if liability == nil {
		t.Fatal("liability is nil, want the stored liability")
	}
	if !liability.LastPaymentAmount.Valid || !liability.LastPaymentAmount.Decimal.Equal(dec("50.2500")) {
		t.Errorf("LastPaymentAmount = %+v, want 50.2500", liability.LastPaymentAmount)
	}
	if liability.LastStatementBalance.Valid {
		t.Errorf("LastStatementBalance = %+v, want NULL", liability.LastStatementBalance)
	}

	var dataType, nullable string
	var precision, scale int
	if err := second.db.QueryRowContext(ctx, `
		SELECT data_type, numeric_precision, numeric_scale, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'main'
		  AND table_name = 'account_liabilities'
		  AND column_name = 'last_statement_balance'`).Scan(&dataType, &precision, &scale, &nullable); err != nil {
		t.Fatalf("read statement-balance column: %v", err)
	}
	kind := strings.ToUpper(dataType)
	if !(strings.HasPrefix(kind, "DECIMAL") || strings.HasPrefix(kind, "NUMERIC")) ||
		precision != 18 || scale != 4 || nullable != "YES" {
		t.Errorf("statement-balance metadata = (%q, %d, %d, %q), want decimal/numeric, 18, 4, YES",
			dataType, precision, scale, nullable)
	}
}

func TestAVersionMismatchStopsTheCommand(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	s := openAt(t, path)
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET version = 99`); err != nil {
		t.Fatalf("change version: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err := Open(path)
	if err == nil {
		t.Fatal("want an error opening a database from another schema version, got nil")
	}
	var mismatch *VersionError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %T (%v), want a *VersionError", err, err)
	}
	if mismatch.Found != 99 || mismatch.Want != SchemaVersion {
		t.Errorf("error = %+v, want found 99 and want %d", mismatch, SchemaVersion)
	}
	if !strings.Contains(err.Error(), "fourseas reset") {
		t.Errorf("message %q does not name `fourseas reset`", err.Error())
	}
}

// TestAnUnversionedDatabaseStopsTheCommand covers a database written by the
// probe, before schema_version existed.
func TestAnUnversionedDatabaseStopsTheCommand(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	raw, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE transactions (provider VARCHAR, amount DOUBLE)`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	_, err = Open(path)
	var mismatch *VersionError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want a *VersionError", err)
	}
	if mismatch.Found != 0 {
		t.Errorf("found version = %d, want 0 for a database with no schema_version table", mismatch.Found)
	}
}

func TestResetDropsEverythingAndRebuilds(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fourseas.duckdb")

	s := openAt(t, path)
	if err := s.Upsert(ctx, []model.Transaction{sample()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.SetCursor(ctx, "plaid", "item-1", "cursor-abc"); err != nil {
		t.Fatalf("set cursor: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET version = 99`); err != nil {
		t.Fatalf("change version: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reset must work on a database this build refuses to open.
	fresh, err := Reset(path)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	defer fresh.Close()

	n, err := fresh.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("stored rows = %d after a reset, want 0", n)
	}
	cursor, err := fresh.Cursor(ctx, "plaid", "item-1")
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor != "" {
		t.Errorf("cursor = %q after a reset, want an empty string", cursor)
	}

	var version int
	if err := fresh.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("version = %d after a reset, want %d", version, SchemaVersion)
	}
}
