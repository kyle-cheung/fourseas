package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	accountformat "github.com/kyle-cheung/fourseas/providence/internal/format"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

// maxCellWidth keeps long merchant names from breaking the layout.
const maxCellWidth = 30

// printTable writes the rows to standard output in aligned columns.
func printTable(rows []model.TransactionView) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DATE\tACCOUNT\tDESCRIPTION\tAMOUNT\tCCY\tCATEGORY\tSTATUS")

	for _, r := range rows {
		description := r.MerchantName
		if description == "" {
			description = r.Name
		}
		status := "posted"
		if r.Pending {
			status = "pending"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%10s\t%s\t%s\t%s\n",
			r.Date.Format("2006-01-02"),
			truncate(r.AccountLabel),
			truncate(description),
			r.Amount.StringFixed(2),
			r.Currency,
			truncate(r.Category),
			status,
		)
	}
	w.Flush()

	fmt.Println("\nA positive amount is money leaving the account. A payment or refund is negative.")
}

// printAccounts writes the account list in aligned columns.
//
// The account id is printed in full and last, because it is the value the user
// copies into `fourseas accounts nickname <id>`.
func printAccounts(rows []model.AccountView) {
	printAccountsAt(rows, time.Now())
}

func printAccountsAt(rows []model.AccountView, now time.Time) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "INSTITUTION\tNAME\tMASK\tTYPE\tBALANCE\tLIMIT\tDUE\tLAST PAYMENT\tUPDATED\tNICKNAME\tACCOUNT ID")

	for _, r := range rows {
		var due, lastDate *time.Time
		var lastAmount decimal.NullDecimal
		if r.Liability != nil {
			due = r.Liability.PaymentDueDate
			lastDate = r.Liability.LastPaymentDate
			lastAmount = r.Liability.LastPaymentAmount
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			truncate(r.InstitutionName),
			truncate(r.Name),
			r.Mask,
			accountKind(r.Account),
			accountformat.Money(r.BalanceCurrent, r.Currency),
			accountformat.Money(r.BalanceLimit, r.Currency),
			accountformat.Date(due, now),
			accountformat.LatestPayment(lastDate, lastAmount, r.Currency, now),
			accountUpdated(r.Account),
			truncate(r.Nickname),
			r.AccountID,
		)
	}
	w.Flush()

	fmt.Println("\nOn a credit card a positive balance is money owed.")
	fmt.Println("Balances come from the last sync. Run `fourseas sync` for newer ones.")
}

// accountKind is the subtype, which says "credit card" where the type only
// says "credit". It falls back to the type when there is no subtype.
func accountKind(a model.Account) string {
	if a.Subtype != "" {
		return a.Subtype
	}
	return a.Type
}

// truncate shortens a value that is too wide for a column.
func truncate(s string) string {
	runes := []rune(s)
	if len(runes) <= maxCellWidth {
		return s
	}
	return strings.TrimSpace(string(runes[:maxCellWidth-1])) + "…"
}
