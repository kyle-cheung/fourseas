package plaid

import (
	"errors"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/provider"
)

func TestCodedErrorClassifiesActionableCodes(t *testing.T) {
	tests := []struct {
		code          string
		restart, gone bool
	}{
		{mutationDuringPagination, true, false},
		{itemNotFound, false, true},
		{"ITEM_LOGIN_REQUIRED", false, false},
	}

	for _, tt := range tests {
		err := codedError("Plaid operation", tt.code, "API_ERROR", "details")
		if got := errors.Is(err, provider.ErrRestartPagination); got != tt.restart {
			t.Errorf("code %s: restart = %v, want %v", tt.code, got, tt.restart)
		}
		if got := errors.Is(err, provider.ErrItemGone); got != tt.gone {
			t.Errorf("code %s: gone = %v, want %v", tt.code, got, tt.gone)
		}
		if !strings.Contains(err.Error(), tt.code) {
			t.Errorf("error = %q, want code %q", err, tt.code)
		}
	}
}
