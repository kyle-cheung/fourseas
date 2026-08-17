package plaid

import (
	"errors"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/provider"
)

// The caller must be able to act on the code without reading the message, so
// the mutation code carries the sentinel that means "start the item again".
func TestCodedErrorMarksAMutationDuringPagination(t *testing.T) {
	err := codedError("sync item item-1", mutationDuringPagination, "TRANSACTIONS_ERROR",
		"Underlying transaction data changed since last page was fetched.")

	if !errors.Is(err, provider.ErrRestartPagination) {
		t.Errorf("errors.Is(err, ErrRestartPagination) = false, want true (err = %v)", err)
	}
	// The operator still needs to read what Plaid said.
	for _, want := range []string{"sync item item-1", mutationDuringPagination, "TRANSACTIONS_ERROR",
		"Underlying transaction data changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// Every other code is an ordinary failure, so a sync must not start again.
func TestCodedErrorLeavesOtherCodesAlone(t *testing.T) {
	for _, code := range []string{"ITEM_LOGIN_REQUIRED", "RATE_LIMIT", "PRODUCT_NOT_READY"} {
		err := codedError("sync item item-1", code, "ITEM_ERROR", "something else")
		if errors.Is(err, provider.ErrRestartPagination) {
			t.Errorf("code %s carries ErrRestartPagination, want an ordinary error", code)
		}
		if !strings.Contains(err.Error(), code) {
			t.Errorf("error = %q, want it to contain %q", err, code)
		}
	}
}
