# TUI Statement Balance Column Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist Plaid credit-card statement balances and show them in the main TUI account summary.

**Architecture:** Add `LastStatementBalance decimal.NullDecimal` to the existing liability value and carry it through the Plaid mapper and DuckDB liability table into `model.AccountView`. Extend only `accountSummaryLines`; it will pass the nullable value and the account currency to the existing formatter, which returns `—` for an invalid value. The schema keeps `SchemaVersion` at 2 and uses one idempotent `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` after table creation so existing version-2 branch databases retain their rows and gain a NULL statement-balance column.

**Tech Stack:** Go, shopspring/decimal, Plaid Go SDK, DuckDB, Bubble Tea, Lipgloss.

---

### Task 1: Carry and render the credit-card statement balance

**Files:**

- Modify: `internal/model/liability.go:10-20`
- Modify: `internal/provider/plaid/map.go:156-174`
- Modify: `internal/provider/plaid/liabilities_test.go:102-129`
- Modify: `internal/store/schema.go:71-82`
- Modify: `internal/store/schema_test.go:14-31`
- Modify: `internal/store/liabilities.go:10-49`
- Modify: `internal/store/accounts.go:20-27,127-192`
- Modify: `internal/store/liabilities_test.go:31-68,96-132,207-219`
- Modify: `internal/tui/view.go:213-247`
- Modify: `internal/tui/model_test.go:408-480,542-572`

- [ ] **Step 1: Update the existing fixtures and assertions before production code.**

  Do not add a test function or a test case. Extend the current successful Plaid fixture assertion, store round-trip fixture/assertions, schema assertion, and both existing summary-table fixtures. Use one present value (`456.78` from the existing Plaid response, `50.2500` in the raw store fixture, and `1000` in the TUI fixture) and existing zero-value `decimal.NullDecimal{}` fields for absent values.

  ```go
  // internal/provider/plaid/liabilities_test.go
  if !full.LastStatementBalance.Valid || full.LastStatementBalance.Decimal.String() != "456.78" {
      t.Errorf("last statement balance = %v, want exact decimal 456.78", full.LastStatementBalance)
  }
  if nullFields.PaymentDueDate != nil || nullFields.LastPaymentDate != nil ||
      nullFields.LastPaymentAmount.Valid || nullFields.LastStatementBalance.Valid {
      t.Errorf("null fields = %+v, want absent dates and amounts", nullFields)
  }

  // internal/store/liabilities_test.go: add the field to this existing assertion.
  if got.PaymentDueDate != nil || got.LastPaymentDate != nil ||
      got.LastPaymentAmount.Valid || got.LastStatementBalance.Valid {
      t.Errorf("nullable liability fields = %+v, want all fields null", got)
  }
  wantStatement := decimal.RequireFromString("50.2500")
  if !got.LastStatementBalance.Valid || !got.LastStatementBalance.Decimal.Equal(wantStatement) {
      t.Errorf("LastStatementBalance = %+v, want 50.2500", got.LastStatementBalance)
  }
  ```

  ```go
  // internal/store/liabilities_test.go: extend insertRawLiability.
  INSERT INTO account_liabilities (
      provider, item_id, account_id,
      payment_due_date, last_payment_date, last_payment_amount,
      last_statement_balance, fetched_at
  ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
  // arguments end with:
  decimal.RequireFromString("50.2500").String(),
  decimal.RequireFromString("50.2500").String(), fetchedAt)

  // internal/tui/model_test.go
  accounts[0].Liability = &model.CreditLiability{
      PaymentDueDate:       &due,
      LastPaymentDate:      &paid,
      LastPaymentAmount:    decimal.NewNullDecimal(decimal.NewFromInt(500)),
      LastStatementBalance: decimal.NewNullDecimal(decimal.NewFromInt(1000)),
  }
  // Use the same LastStatementBalance in the existing narrow-width fixture.

  for _, want := range []string{
      "Account", "Cur. balance", "Stmt balance", "Due", "Last payment",
      "Amex Daily", "1,284.21 USD", "1,000.00 USD", "Sep 12", "Aug 20 · 500.00 USD",
      "Scotia Visa", "320.10 CAD", "Dec 05, 2025",
      "acc-3", "2,400.00 JPY", "Cash reserve", "-810.00 CAD",
  } {
      if !strings.Contains(body, want) {
          t.Errorf("main view = %q, want it to contain %q", body, want)
      }
  }
  if got := strings.Count(body, "—"); got != 8 {
      t.Errorf("main view has %d missing values, want 8 for absent liability data: %q", got, body)
  }
  ```

  In `TestSchemaIsCreatedFromNothingAndStamped`, add this existing-test assertion after the table loop; retain the existing version assertion that compares against `SchemaVersion`.

  ```go
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
  ```

- [ ] **Step 2: Run the focused tests and record the RED result.**

  Run:

  ```bash
  go test ./internal/provider/plaid ./internal/store ./internal/tui
  ```

  Expected: FAIL at build time because the changed existing tests refer to `model.CreditLiability.LastStatementBalance`, which does not exist yet. This proves the fixtures and assertions demand the new end-to-end value before production code changes.

- [ ] **Step 3: Add the minimal model, mapping, persistence, and rendering code.**

  Add the nullable decimal alongside the existing payment amount, map Plaid's optional field with the existing helper, and keep all other liability behavior unchanged.

  ```go
  // internal/model/liability.go
  LastPaymentAmount    decimal.NullDecimal
  LastStatementBalance decimal.NullDecimal
  FetchedAt            time.Time

  // internal/provider/plaid/map.go, inside toCreditLiabilities
  LastPaymentAmount:    optionalAmount(row.GetLastPaymentAmountOk()),
  LastStatementBalance: optionalAmount(row.GetLastStatementBalanceOk()),
  FetchedAt:            fetchedAt,
  ```

  Keep `SchemaVersion` at 2. After `CREATE TABLE account_liabilities`, add one idempotent statement that ensures the nullable column on existing version-2 databases without changing existing rows.

  ```sql
  -- internal/store/schema.go, account_liabilities
  last_payment_amount    DECIMAL(18,4),
  last_statement_balance DECIMAL(18,4),
  fetched_at             TIMESTAMP NOT NULL,
  ```

  ```sql
  ALTER TABLE account_liabilities
      ADD COLUMN IF NOT EXISTS last_statement_balance DECIMAL(18,4);
  ```

  Add a regression test that drops this column from a seeded version-2 temp
  database, reopens it, and verifies that the column is restored and the
  account and liability rows remain.

  Include the field in each store boundary in its table order.

  ```go
  // internal/store/liabilities.go
  payment_due_date, last_payment_date, last_payment_amount,
  last_statement_balance, fetched_at
  ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)

  timeArg(row.PaymentDueDate), timeArg(row.LastPaymentDate),
  nullDecimalArg(row.LastPaymentAmount), nullDecimalArg(row.LastStatementBalance), row.FetchedAt)

  // internal/store/accounts.go
  l.item_id, l.payment_due_date, l.last_payment_date, l.last_payment_amount,
  l.last_statement_balance, l.fetched_at

  current, available, limit, paymentAmount, statementBalance any

  &liabilityItemID, &paymentDueDate, &lastPaymentDate, &paymentAmount,
  &statementBalance, &liabilityFetchedAt,

  statement, err := toNullDecimal(statementBalance)
  if err != nil {
      return model.AccountView{}, fmt.Errorf("last_statement_balance: %w", err)
  }

  view.Liability = &model.CreditLiability{
      Provider:             a.Provider,
      ItemID:               text(liabilityItemID),
      AccountID:            a.AccountID,
      PaymentDueDate:       timePtr(paymentDueDate),
      LastPaymentDate:      timePtr(lastPaymentDate),
      LastPaymentAmount:    amount,
      LastStatementBalance: statement,
      FetchedAt:            liabilityFetchedAt.Time,
  }
  ```

  Change only the main summary table. Keep `Wrap(false)`, the current style function, and the existing final whole-line `truncate` loop unchanged.

  ```go
  // internal/tui/view.go
  Headers("Account", "Cur. balance", "Stmt balance", "Due", "Last payment").

  for _, account := range m.accounts.rows {
      statement, due, payment := "—", "—", "—"
      if account.Liability != nil {
          statement = accountformat.Money(account.Liability.LastStatementBalance, account.Currency)
          due = accountformat.Date(account.Liability.PaymentDueDate, now)
          payment = accountformat.LatestPayment(
              account.Liability.LastPaymentDate,
              account.Liability.LastPaymentAmount,
              account.Currency,
              now,
          )
      }
      t.Row(accountName(account), accountformat.Money(account.BalanceCurrent, account.Currency), statement, due, payment)
  }
  ```

- [ ] **Step 4: Run focused tests and then the full suite for GREEN.**

  Run:

  ```bash
  go test ./internal/provider/plaid ./internal/store ./internal/tui
  go test ./...
  ```

  Expected: PASS. The mapping test verifies `456.78`; store tests verify a valid value and SQL NULL reconstruction; the summary test verifies `1,000.00 USD`, `Cur. balance`, `Stmt balance`, and eight `—` cells; the existing narrow test still verifies one physical row per account and line widths at most 50.

- [ ] **Step 5: Format, inspect scope, and commit one focused implementation change.**

  Existing version-2 development databases gain the column when opened. Do not change CLI behavior or schema version.

  Run:

  ```bash
  gofmt -w internal/model/liability.go internal/provider/plaid/map.go internal/provider/plaid/liabilities_test.go internal/store/schema.go internal/store/schema_test.go internal/store/liabilities.go internal/store/accounts.go internal/store/liabilities_test.go internal/tui/view.go internal/tui/model_test.go
  git diff --check
  git diff -- internal/model/liability.go internal/provider/plaid/map.go internal/provider/plaid/liabilities_test.go internal/store/schema.go internal/store/schema_test.go internal/store/liabilities.go internal/store/accounts.go internal/store/liabilities_test.go internal/tui/view.go internal/tui/model_test.go
  git status --short
  git add internal/model/liability.go internal/provider/plaid/map.go internal/provider/plaid/liabilities_test.go internal/store/schema.go internal/store/schema_test.go internal/store/liabilities.go internal/store/accounts.go internal/store/liabilities_test.go internal/tui/view.go internal/tui/model_test.go
  git commit -m "feat: show statement balance in TUI summary"
  ```

  Expected: `git diff --check` has no output; the staged paths are only the listed implementation and regression-test files; the commit contains no CLI, detail-view, request, schema-version, or layout-rule change.
