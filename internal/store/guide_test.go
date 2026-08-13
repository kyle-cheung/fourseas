package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

// The queries in this file are the worked examples in README.md. They are held
// here so that the guide is proven, not asserted: if a column is renamed, the
// README stops being a promise nobody checks.

const (
	// queryOneAccountSinceADate is the target experience of the design.
	queryOneAccountSinceADate = `
SELECT date, account, description, amount, currency
FROM v_transactions
WHERE nickname = 'Amex Daily' AND date >= '2026-08-01'
ORDER BY date DESC;`

	// querySpendByMonth groups by currency as well as by month, because a sum
	// across currencies is not a number anybody should trust.
	querySpendByMonth = `
SELECT strftime(date, '%Y-%m') AS month, currency, sum(amount) AS spend
FROM v_transactions
GROUP BY month, currency
ORDER BY month, currency;`

	querySpendByCategory = `
SELECT category, currency, sum(amount) AS spend
FROM v_transactions
WHERE date >= '2026-08-01'
GROUP BY category, currency
ORDER BY spend DESC;`

	querySpendByAccount = `
SELECT account, institution_name, currency, sum(amount) AS spend, count(*) AS rows
FROM v_transactions
GROUP BY account, institution_name, currency
ORDER BY spend DESC;`

	queryLargestCharges = `
SELECT date, account, description, amount, currency
FROM v_transactions
WHERE amount > 0
ORDER BY amount DESC
LIMIT 20;`

	queryPending = `
SELECT date, account, description, amount
FROM v_transactions
WHERE pending
ORDER BY date DESC;`

	queryMerchantOverTime = `
SELECT strftime(date, '%Y-%m') AS month, count(*) AS visits, sum(amount) AS spend
FROM v_transactions
WHERE description ILIKE '%coffee%' AND currency = 'USD'
GROUP BY month
ORDER BY month;`

	// queryCurrencyCoverage shows what a base currency total leaves out. CAD
	// rows carry a null base_amount until build 2 adds exchange rates.
	queryCurrencyCoverage = `
SELECT currency, count(*) AS rows, count(base_amount) AS convertible
FROM v_transactions
GROUP BY currency
ORDER BY currency;`

	queryDescribe = `DESCRIBE v_transactions;`
)

// guideStore holds the shape the README describes: two institutions, one of
// them in another currency, and a pending row that a posted row replaced.
func guideStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)

	amex := sampleAccount()
	amex.Nickname = "Amex Daily"

	scotia := sampleAccount()
	scotia.AccountID = "acct-scotia"
	scotia.ItemID = "item-2"
	scotia.Name = "Momentum Visa"
	scotia.Nickname = "Scotia Visa"
	scotia.Currency = "CAD"

	if err := s.UpsertAccounts(ctx, []model.Account{amex, scotia}); err != nil {
		t.Fatalf("upsert accounts: %v", err)
	}

	scotiabank := sampleInstitution()
	scotiabank.ItemID = "item-2"
	scotiabank.InstitutionID = "ins_20"
	scotiabank.InstitutionName = "Scotiabank"
	for _, inst := range []model.Institution{sampleInstitution(), scotiabank} {
		if err := s.UpsertInstitution(ctx, inst); err != nil {
			t.Fatalf("upsert institution: %v", err)
		}
	}

	july := sample()
	july.ExternalID = "txn-july"
	july.Date = day("2026-07-15")
	july.Pending = false
	july.PendingTransactionID = ""
	july.Amount, july.BaseAmount = dec("100.00"), nullDec("100.00")
	july.Category = "GENERAL_MERCHANDISE"

	coffee := sample()
	coffee.ExternalID = "txn-coffee"
	coffee.Date = day("2026-08-03")
	coffee.Pending = false
	coffee.PendingTransactionID = ""
	coffee.Amount, coffee.BaseAmount = dec("24.75"), nullDec("24.75")

	pending := sample()
	pending.ExternalID = "txn-pending"
	pending.Date = day("2026-08-10")
	pending.Name = "Pacific Gas Electric Company"
	pending.MerchantName = "" // Plaid supplies no clean name for this one.
	pending.Pending = true
	pending.PendingTransactionID = ""
	pending.SupersededBy = "txn-posted"
	pending.Amount, pending.BaseAmount = dec("138.98"), nullDec("138.98")
	pending.Category = "GENERAL_SERVICES"

	posted := pending
	posted.ExternalID = "txn-posted"
	posted.Date = day("2026-08-11")
	posted.Name = "Pacific Gas And Elecwest"
	posted.Pending = false
	posted.PendingTransactionID = "txn-pending"
	posted.SupersededBy = ""

	// A Scotiabank row: CAD, so base_amount is null until build 2.
	canadian := sample()
	canadian.ExternalID = "txn-scotia"
	canadian.ItemID = "item-2"
	canadian.AccountID = "acct-scotia"
	canadian.Date = day("2026-08-05")
	canadian.Name = "TIM HORTONS"
	canadian.MerchantName = "Tim Hortons"
	canadian.Pending = false
	canadian.PendingTransactionID = ""
	canadian.Amount, canadian.Currency = dec("42.00"), "CAD"
	canadian.BaseAmount, canadian.BaseCurrency = decimal.NullDecimal{}, ""
	canadian.FXRate, canadian.FXDate = decimal.NullDecimal{}, nil

	rows := []model.Transaction{july, coffee, pending, posted, canadian}
	if err := s.Upsert(ctx, rows); err != nil {
		t.Fatalf("upsert transactions: %v", err)
	}
	return s, ctx
}

func TestGuideQueryFindsOneAccountSinceADate(t *testing.T) {
	s, ctx := guideStore(t)

	rows, err := s.db.QueryContext(ctx, queryOneAccountSinceADate)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var date, account, description, currency string
		var amount any
		if err := rows.Scan(&date, &account, &description, &amount, &currency); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, description)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	// The pending PG&E row is hidden, so its charge is counted one time.
	want := []string{"Pacific Gas And Elecwest", "Blue Bottle Coffee"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGuideQuerySpendByMonth(t *testing.T) {
	s, ctx := guideStore(t)

	got := sums(t, s, ctx, querySpendByMonth, 2)
	want := map[string]string{
		"2026-07|USD": "100.00",
		"2026-08|USD": "163.73", // 24.75 + 138.98, with the pending copy left out
		"2026-08|CAD": "42.00",
	}
	assertSums(t, got, want)
}

func TestGuideQuerySpendByCategory(t *testing.T) {
	s, ctx := guideStore(t)

	got := sums(t, s, ctx, querySpendByCategory, 2)
	want := map[string]string{
		"GENERAL_SERVICES|USD": "138.98",
		"FOOD_AND_DRINK|USD":   "24.75",
		"FOOD_AND_DRINK|CAD":   "42.00",
	}
	assertSums(t, got, want)
}

func TestGuideQuerySpendByAccount(t *testing.T) {
	s, ctx := guideStore(t)

	rows, err := s.db.QueryContext(ctx, querySpendByAccount)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var account, institution, currency string
		var spend any
		var count int
		if err := rows.Scan(&account, &institution, &currency, &spend, &count); err != nil {
			t.Fatalf("scan: %v", err)
		}
		total, err := toDecimal(spend)
		if err != nil {
			t.Fatalf("read sum: %v", err)
		}
		got[account+"|"+institution+"|"+currency] = total.StringFixed(2)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	// The account column carries the nickname, so a person reads their own
	// name for the card instead of Plaid's.
	want := map[string]string{
		"Amex Daily|American Express|USD": "263.73",
		"Scotia Visa|Scotiabank|CAD":      "42.00",
	}
	assertSums(t, got, want)
}

// TestGuideQueryShowsWhatABaseTotalLeavesOut is the honest limitation: a sum of
// base_amount skips CAD rows silently, so the guide shows the coverage query
// beside it.
func TestGuideQueryShowsWhatABaseTotalLeavesOut(t *testing.T) {
	s, ctx := guideStore(t)

	rows, err := s.db.QueryContext(ctx, queryCurrencyCoverage)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	got := map[string][2]int{}
	for rows.Next() {
		var currency string
		var total, convertible int
		if err := rows.Scan(&currency, &total, &convertible); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[currency] = [2]int{total, convertible}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if got["USD"] != [2]int{3, 3} {
		t.Errorf("USD coverage = %v, want 3 rows and 3 convertible", got["USD"])
	}
	if got["CAD"] != [2]int{1, 0} {
		t.Errorf("CAD coverage = %v, want 1 row and none convertible", got["CAD"])
	}
}

// TestGuideQueryLargestCharges proves the list of purchases leaves out
// payments and refunds, which carry a negative amount.
func TestGuideQueryLargestCharges(t *testing.T) {
	s, ctx := guideStore(t)

	refund := sample()
	refund.ExternalID = "txn-refund"
	refund.Date = day("2026-08-06")
	refund.Pending = false
	refund.PendingTransactionID = ""
	refund.Name, refund.MerchantName = "PAYMENT THANK YOU", ""
	refund.Amount, refund.BaseAmount = dec("-500.00"), nullDec("-500.00")
	if err := s.Upsert(ctx, []model.Transaction{refund}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rows, err := s.db.QueryContext(ctx, queryLargestCharges)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var descriptions []string
	for rows.Next() {
		var date, account, description, currency string
		var amount any
		if err := rows.Scan(&date, &account, &description, &amount, &currency); err != nil {
			t.Fatalf("scan: %v", err)
		}
		descriptions = append(descriptions, description)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	want := []string{"Pacific Gas And Elecwest", "Blue Bottle Coffee", "Tim Hortons", "Blue Bottle Coffee"}
	if len(descriptions) != len(want) {
		t.Fatalf("got %v, want %v", descriptions, want)
	}
	for i := range want {
		if descriptions[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, descriptions[i], want[i])
		}
	}
}

func TestGuideQueryPending(t *testing.T) {
	s, ctx := guideStore(t)

	rows, err := s.db.QueryContext(ctx, queryPending)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	// Every pending row in the fixture was superseded by a posted row, so the
	// view shows nothing waiting.
	for rows.Next() {
		var date, account, description string
		var amount any
		if err := rows.Scan(&date, &account, &description, &amount); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Errorf("pending shows %s %s, want nothing: its posted row has arrived", date, description)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
}

func TestGuideQueryMerchantOverTime(t *testing.T) {
	s, ctx := guideStore(t)

	got := sums(t, s, ctx, `
SELECT strftime(date, '%Y-%m') AS month, sum(amount) AS spend
FROM v_transactions
WHERE description ILIKE '%coffee%' AND currency = 'USD'
GROUP BY month
ORDER BY month;`, 1)
	assertSums(t, got, map[string]string{"2026-07": "100.00", "2026-08": "24.75"})

	// The documented query counts visits beside the sum. Run it as written.
	rows, err := s.db.QueryContext(ctx, queryMerchantOverTime)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	months := 0
	for rows.Next() {
		months++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if months != 2 {
		t.Errorf("got %d months, want 2", months)
	}
}

// TestGuideDescribeNamesTheDocumentedColumns keeps the guide's column names
// true: a rename in the view fails here.
func TestGuideDescribeNamesTheDocumentedColumns(t *testing.T) {
	s, ctx := guideStore(t)

	rows, err := s.db.QueryContext(ctx, queryDescribe)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	defer rows.Close()

	found := map[string]bool{}
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	for rows.Next() {
		cells := make([]any, len(cols))
		holders := make([]sql.NullString, len(cols))
		for i := range cells {
			cells[i] = &holders[i]
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found[holders[0].String] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, name := range []string{
		"date", "account", "nickname", "institution_name", "description",
		"amount", "currency", "base_amount", "category", "pending",
	} {
		if !found[name] {
			t.Errorf("v_transactions has no column %q, and the README names it", name)
		}
	}
}

// sums reads a grouped query whose first keyCols columns are text keys and
// whose next column is a decimal sum.
func sums(t *testing.T, s *Store, ctx context.Context, query string, keyCols int) map[string]string {
	t.Helper()

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		keys := make([]string, keyCols)
		dest := make([]any, 0, keyCols+1)
		for i := range keys {
			dest = append(dest, &keys[i])
		}
		var spend any
		dest = append(dest, &spend)
		if err := rows.Scan(dest...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		total, err := toDecimal(spend)
		if err != nil {
			t.Fatalf("read sum: %v", err)
		}
		key := keys[0]
		for _, k := range keys[1:] {
			key += "|" + k
		}
		out[key] = total.StringFixed(2)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func assertSums(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("got %d groups %v, want %d %v", len(got), got, len(want), want)
	}
	for key, w := range want {
		if got[key] != w {
			t.Errorf("%s = %q, want %q", key, got[key], w)
		}
	}
}
