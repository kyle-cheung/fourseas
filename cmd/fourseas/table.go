package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
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

// truncate shortens a value that is too wide for a column.
func truncate(s string) string {
	if len(s) <= maxCellWidth {
		return s
	}
	return strings.TrimSpace(s[:maxCellWidth-1]) + "…"
}
