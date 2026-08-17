package model

import (
	"testing"
)

func TestNormalizeCurrency(t *testing.T) {
	for input, want := range map[string]string{
		"cad":   "CAD",
		" Usd ": "USD",
		"":      "",
	} {
		if got := NormalizeCurrency(input); got != want {
			t.Errorf("NormalizeCurrency(%q) = %q, want %q", input, got, want)
		}
	}
}
