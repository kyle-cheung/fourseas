package main

import (
	"io"
	"os"
	"testing"
)

// captureStdout returns everything print writes while fn runs.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = write
	fn()
	os.Stdout = saved
	write.Close()

	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatalf("read the captured output: %v", err)
	}
	return string(out)
}

// runLink now gets the sign-in URL from app.Link, which reports it just before
// the browser opens. The line the user reads must not change because of that.
func TestSignInLineKeepsTheWording(t *testing.T) {
	const reported = "Open http://localhost:8080/link in your browser"
	const want = "Open http://localhost:8080/link in your browser to sign in to the bank.\n"

	if got := captureStdout(t, func() { signInLine(reported) }); got != want {
		t.Errorf("signInLine printed %q, want %q", got, want)
	}
	// Every other line app.Link reports belongs to the terminal interface.
	if got := captureStdout(t, func() { signInLine("Link completed") }); got != "" {
		t.Errorf("signInLine printed %q for a line the command line does not show", got)
	}
}

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
