package main

import "testing"

func TestParseLinkOptions(t *testing.T) {
	tests := []struct {
		name    string
		options []string
		want    int
	}{
		{"no options asks for everything Plaid permits", nil, 730},
		{"days with a space", []string{"--days", "180"}, 180},
		{"days with an equals sign", []string{"--days=90"}, 90},
		{"the smallest amount Plaid honours", []string{"--days", "30"}, 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLinkOptions(tt.options)
			if err != nil {
				t.Fatalf("parseLinkOptions(%q) error = %v", tt.options, err)
			}
			if got != tt.want {
				t.Errorf("parseLinkOptions(%q) = %d, want %d", tt.options, got, tt.want)
			}
		})
	}
}

func TestParseLinkOptionsRejectsBadInput(t *testing.T) {
	for _, options := range [][]string{
		{"--days"},
		// Plaid raises anything under 30 to 30, so asking for less is refused
		// instead of silently changed.
		{"--days", "29"},
		{"--days", "731"},
		{"--days", "many"},
		{"--fx"},
	} {
		if _, err := parseLinkOptions(options); err == nil {
			t.Errorf("parseLinkOptions(%q) error = nil, want an error", options)
		}
	}
}
