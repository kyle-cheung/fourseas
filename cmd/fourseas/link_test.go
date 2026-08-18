package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
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

// fakeLinker records what the link command asked of the façade.
type fakeLinker struct {
	linkCalls     int
	linkItem      app.LinkedItem
	linkErr       error
	completeCalls int
	completeItem  app.LinkedItem
	completeErr   error
}

func (f *fakeLinker) Link(context.Context, int, app.Progress) (app.LinkedItem, error) {
	f.linkCalls++
	return f.linkItem, f.linkErr
}

func (f *fakeLinker) CompleteLinkSave(app.PendingSave) (app.LinkedItem, error) {
	f.completeCalls++
	return f.completeItem, f.completeErr
}

// tokenNotSaved is the failure of a link whose item Plaid has already created.
func tokenNotSaved() error {
	return &app.TokenNotSavedError{Err: errors.New("write tokens.json: permission denied")}
}

// A failed save is saved again. Plaid bills the item it has already created,
// and a second link would create a second billed item.
func TestLinkOnceSavesAgainInsteadOfLinkingAgain(t *testing.T) {
	fake := &fakeLinker{
		linkErr:      tokenNotSaved(),
		completeItem: app.LinkedItem{ItemID: "item-new", Institution: "TD Canada Trust"},
	}

	linked, err := linkOnce(context.Background(), fake, 730, nil)
	if err != nil {
		t.Fatalf("linkOnce: %v", err)
	}
	if linked.ItemID != "item-new" {
		t.Errorf("linked = %+v, want the item the completed save returned", linked)
	}
	if fake.linkCalls != 1 {
		t.Errorf("Link ran %d times, want the one link Plaid already billed", fake.linkCalls)
	}
	if fake.completeCalls != 1 {
		t.Errorf("CompleteLinkSave ran %d times, want once", fake.completeCalls)
	}
}

// A save that fails twice ends the command with a failure the user can act on,
// and with no second link.
func TestLinkOnceReportsTheUnsavedTokenWithoutASecondLink(t *testing.T) {
	fake := &fakeLinker{linkErr: tokenNotSaved(), completeErr: tokenNotSaved()}

	_, err := linkOnce(context.Background(), fake, 730, nil)
	if err == nil {
		t.Fatal("error = nil, want the unsaved token to end the command")
	}
	if fake.linkCalls != 1 {
		t.Errorf("Link ran %d times, want the one link Plaid already billed", fake.linkCalls)
	}
	if fake.completeCalls != 1 {
		t.Errorf("CompleteLinkSave ran %d times, want the one retry", fake.completeCalls)
	}
	if !strings.Contains(err.Error(), "SECOND") {
		t.Errorf("error = %q, want the warning about a second billed item", err)
	}
}

// A retry that fails with a plain error must report that error, not the reason
// the first save failed for. The user acts on what is broken now.
func TestLinkOnceReportsWhyTheRetryFailed(t *testing.T) {
	fake := &fakeLinker{
		linkErr:     tokenNotSaved(),
		completeErr: errors.New("no unsaved link to complete"),
	}

	_, err := linkOnce(context.Background(), fake, 730, nil)
	if err == nil {
		t.Fatal("error = nil, want the failed retry to end the command")
	}
	if !strings.Contains(err.Error(), "no unsaved link to complete") {
		t.Errorf("error = %q, want the reason the retry failed", err)
	}
	if strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error = %q, want it to drop the reason of the first failure", err)
	}
}

// Every other link failure keeps its own error and starts no save.
func TestLinkOnceLeavesAnOrdinaryFailureAlone(t *testing.T) {
	fake := &fakeLinker{linkErr: errors.New("plaid is unavailable")}

	if _, err := linkOnce(context.Background(), fake, 730, nil); err != fake.linkErr {
		t.Errorf("error = %v, want the error Link returned", err)
	}
	if fake.completeCalls != 0 {
		t.Errorf("CompleteLinkSave ran %d times, want none", fake.completeCalls)
	}
}

// The message must name the item the user now pays for, and must not offer a
// new link as the way out.
func TestTokenNotSavedMessageNamesTheBilledItem(t *testing.T) {
	err := tokenNotSavedMessage("TD Canada Trust", "item-new",
		errors.New("write tokens.json: permission denied"))

	for _, want := range []string{
		"TD Canada Trust",
		"item-new",
		"write tokens.json: permission denied",
		"SECOND",
		"fourseas link",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not hold %q:\n%s", want, err)
		}
	}
}
