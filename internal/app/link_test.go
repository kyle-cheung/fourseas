package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
	"github.com/kyle-cheung/fourseas/providence/internal/tokens"
)

// linkOK is a browser step that always completes.
func linkOK(context.Context, plaid.Config, int, bool) (plaid.LinkResult, error) {
	return plaid.LinkResult{
		ItemID: "item-new", AccessToken: "secret", Institution: "TD Canada Trust",
	}, nil
}

// linkNever fails the test when the browser step is reached.
func linkNever(t *testing.T) linkFunc {
	return func(context.Context, plaid.Config, int, bool) (plaid.LinkResult, error) {
		t.Error("the browser step ran although the settings are not usable")
		return plaid.LinkResult{}, nil
	}
}

// The token is what names a live item at Plaid, so it must be on disk before
// Link returns.
func TestLinkSavesCompletedItem(t *testing.T) {
	for _, liabilities := range []bool{true, false} {
		t.Run(fmt.Sprintf("liabilities %t", liabilities), func(t *testing.T) {
			cfg := tempConfig(t, "sandbox")
			link := func(_ context.Context, _ plaid.Config, _ int, gotLiabilities bool) (plaid.LinkResult, error) {
				if gotLiabilities != liabilities {
					t.Errorf("provider liabilities = %t, want %t", gotLiabilities, liabilities)
				}
				return plaid.LinkResult{
					ItemID: "item-new", AccessToken: "secret", Institution: "TD Canada Trust",
				}, nil
			}
			var reported []string

			linked, err := newWith(cfg, link, nil, nil, nil, nil).Link(context.Background(), 730, liabilities,
				func(text string) { reported = append(reported, text) })
			if err != nil {
				t.Fatalf("Link: %v", err)
			}

			if linked.ItemID != "item-new" || linked.Institution != "TD Canada Trust" {
				t.Errorf("linked = %+v, want item-new at TD Canada Trust", linked)
			}
			saved, found := loadTokens(t, cfg.TokensPath).Find("item-new")
			if !found {
				t.Fatal("the token file has no item-new after Link returned")
			}
			if saved.AccessToken != "secret" || saved.Env != "sandbox" {
				t.Errorf("saved = %+v, want the secret token in sandbox", saved)
			}
			if saved.Liabilities != liabilities {
				t.Errorf("saved liabilities = %t, want %t", saved.Liabilities, liabilities)
			}
			if saved.LinkedAt.IsZero() {
				t.Error("the saved item has no link time")
			}
			if len(reported) == 0 {
				t.Error("Link reported no progress")
			}
		})
	}
}

func TestLinkPreservesAnItemAddedDuringTheBrowserWait(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	first := item("item-first", "First Bank", "sandbox")
	seedTokens(t, cfg.TokensPath, first)
	concurrent := item("item-concurrent", "Concurrent Bank", "sandbox")
	link := func(context.Context, plaid.Config, int, bool) (plaid.LinkResult, error) {
		if err := tokens.Save(cfg.TokensPath, loadTokens(t, cfg.TokensPath).Upsert(concurrent)); err != nil {
			t.Fatalf("save concurrent Item: %v", err)
		}
		return plaid.LinkResult{
			ItemID: "item-new", AccessToken: "item-new-token", Institution: "New Bank",
		}, nil
	}

	if _, err := newWith(cfg, link, nil, nil, nil, nil).Link(context.Background(), 730, true, nil); err != nil {
		t.Fatalf("Link: %v", err)
	}
	saved := loadTokens(t, cfg.TokensPath)
	for _, itemID := range []string{first.ItemID, concurrent.ItemID, "item-new"} {
		if _, found := saved.Find(itemID); !found {
			t.Errorf("token file lost %s", itemID)
		}
	}
}

// An unusable token file must be found before the browser opens. Plaid bills
// the item it creates there, and the access token is the only handle to it: a
// token file that cannot be written afterwards loses that item for good. This
// is the guarantee the command line now relies on.
func TestLinkRefusesAnUnreadableTokenFileBeforeTheBrowserStep(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	if err := os.WriteFile(cfg.TokensPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write %s: %v", cfg.TokensPath, err)
	}

	_, err := newWith(cfg, linkNever(t), nil, nil, nil, nil).Link(context.Background(), 730, true, nil)
	if err == nil {
		t.Fatal("error = nil, want the unreadable token file")
	}
	if !strings.Contains(err.Error(), "tokens.json") {
		t.Errorf("error = %q, want it to name the token file", err)
	}
}

// The command line builds its own sign-in instruction from this line, so both
// the wording and the place it is reported are part of the contract: it must
// come before the browser step, and it must start with the URL to open.
func TestLinkReportsTheSignInURLBeforeTheBrowserStep(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	var reported []string
	link := func(_ context.Context, _ plaid.Config, _ int, _ bool) (plaid.LinkResult, error) {
		if len(reported) != 1 {
			t.Errorf("reported %q before the browser step, want the sign-in line only", reported)
		}
		return plaid.LinkResult{ItemID: "item-new", AccessToken: "secret"}, nil
	}

	if _, err := newWith(cfg, link, nil, nil, nil, nil).Link(context.Background(), 730, true,
		func(text string) { reported = append(reported, text) }); err != nil {
		t.Fatalf("Link: %v", err)
	}

	want := "Open " + plaid.LinkURL(cfg.Plaid) + " in your browser"
	if len(reported) == 0 || reported[0] != want {
		t.Errorf("first reported line = %q, want %q", reported, want)
	}
}

// Bad settings must be found before anything on disk is touched, so nothing
// can fail halfway.
func TestLinkValidatesBeforeLoadingTokens(t *testing.T) {
	cfg := Config{
		Plaid:      plaid.Config{Env: "sandbox", LinkPort: testPort},
		DBPath:     blockedPath(t),
		TokensPath: blockedPath(t),
	}

	_, err := newWith(cfg, linkNever(t), nil, nil, nil, nil).Link(context.Background(), 90, true, nil)
	if err == nil {
		t.Fatal("error = nil, want the Plaid validation failure")
	}
	if !strings.Contains(err.Error(), "PLAID_CLIENT_ID") {
		t.Errorf("error = %q, want the Plaid validation failure", err)
	}
}

func TestLinkRejectsOutOfRangeBeforeLoadingTokens(t *testing.T) {
	cfg := Config{
		Plaid:      validPlaid("sandbox"),
		DBPath:     blockedPath(t),
		TokensPath: blockedPath(t),
	}

	for _, days := range []int{MinLinkDays - 1, MaxLinkDays + 1} {
		_, err := newWith(cfg, linkNever(t), nil, nil, nil, nil).Link(context.Background(), days, true, nil)
		if err == nil {
			t.Fatalf("Link(%d) error = nil, want the day range failure", days)
		}
		if !strings.Contains(err.Error(), "history must be from 30 through 730 days") {
			t.Errorf("Link(%d) error = %q, want the day range failure", days, err)
		}
	}
}

// The database is no part of linking, and the browser step can take ten
// minutes. Holding DuckDB open for that time buys nothing.
func TestLinkDoesNotOpenDuckDB(t *testing.T) {
	cfg := tempConfig(t, "sandbox")
	cfg.DBPath = blockedPath(t)

	if _, err := newWith(cfg, linkOK, nil, nil, nil, nil).Link(context.Background(), 730, true, nil); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if _, found := loadTokens(t, cfg.TokensPath).Find("item-new"); !found {
		t.Error("the token was not saved although the database is not part of Link")
	}
}

// secretToken is the access token of the tests below. It is written to look
// like nothing else on the screen, so a leak of it is easy to find.
const secretToken = "access-token-DO-NOT-LEAK"

// linkSecret is a browser step that returns the secret token.
func linkSecret(context.Context, plaid.Config, int, bool) (plaid.LinkResult, error) {
	return plaid.LinkResult{
		ItemID: "item-new", AccessToken: secretToken, Institution: "TD Canada Trust",
	}, nil
}

// closedDirConfig returns a configuration whose token file does not exist yet
// and cannot be created, because its directory refuses a write. The returned
// function opens the directory again, which lets the save succeed.
func closedDirConfig(t *testing.T) (Config, func()) {
	t.Helper()
	skipAsRoot(t)
	dir := t.TempDir()
	cfg := Config{
		Plaid:      validPlaid("sandbox"),
		DBPath:     filepath.Join(dir, "fourseas.duckdb"),
		TokensPath: filepath.Join(dir, "tokens.json"),
	}
	open := func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("chmod %s: %v", dir, err)
		}
	}
	// The cleanup of the temporary directory needs the write permission back.
	t.Cleanup(open)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	return cfg, open
}

// Plaid bills the item it has already created, and the access token is the only
// handle to it. A failed save must therefore hand the token back to the caller,
// not drop it.
func TestLinkKeepsTheTokenWhenTheSaveFails(t *testing.T) {
	cfg, open := closedDirConfig(t)

	_, err := newWith(cfg, linkSecret, nil, nil, nil, nil).Link(context.Background(), 730, true, nil)
	if err == nil {
		t.Fatal("error = nil, want the failed save")
	}
	var notSaved *TokenNotSavedError
	if !errors.As(err, &notSaved) {
		t.Fatalf("error = %v (%T), want a *TokenNotSavedError", err, err)
	}
	if notSaved.Pending.ItemID() != "item-new" {
		t.Errorf("pending item id = %q, want item-new", notSaved.Pending.ItemID())
	}
	if notSaved.Pending.Institution() != "TD Canada Trust" {
		t.Errorf("pending institution = %q, want TD Canada Trust", notSaved.Pending.Institution())
	}

	open()
	linked, err := newWith(cfg, linkNever(t), nil, nil, nil, nil).CompleteLinkSave(notSaved.Pending)
	if err != nil {
		t.Fatalf("CompleteLinkSave: %v", err)
	}
	if linked.ItemID != "item-new" || linked.Institution != "TD Canada Trust" {
		t.Errorf("linked = %+v, want item-new at TD Canada Trust", linked)
	}
	saved, found := loadTokens(t, cfg.TokensPath).Find("item-new")
	if !found {
		t.Fatal("the token file has no item-new after the save was completed")
	}
	if saved.AccessToken != secretToken || saved.Env != "sandbox" {
		t.Errorf("saved = %+v, want the secret token in sandbox", saved)
	}
	if saved.LinkedAt.IsZero() {
		t.Error("the saved item has no link time")
	}
}

// The user may repair the token file in another terminal between the failure
// and the retry. The snapshot taken at the failure would throw that work away,
// so the save reads the file again first.
func TestCompleteLinkSaveKeepsWhatTheFileHoldsNow(t *testing.T) {
	cfg, open := closedDirConfig(t)

	_, err := newWith(cfg, linkSecret, nil, nil, nil, nil).Link(context.Background(), 730, true, nil)
	var notSaved *TokenNotSavedError
	if !errors.As(err, &notSaved) {
		t.Fatalf("error = %v (%T), want a *TokenNotSavedError", err, err)
	}

	// The user repairs the file and puts another item in it.
	open()
	seedTokens(t, cfg.TokensPath, item("item-other", "Amex", "sandbox"))

	if _, err := newWith(cfg, linkNever(t), nil, nil, nil, nil).CompleteLinkSave(notSaved.Pending); err != nil {
		t.Fatalf("CompleteLinkSave: %v", err)
	}

	file := loadTokens(t, cfg.TokensPath)
	if len(file.Items) != 2 {
		t.Fatalf("the token file holds %d items, want the repaired one and the new one", len(file.Items))
	}
	other, found := file.Find("item-other")
	if !found {
		t.Fatal("the item the user added between the failure and the retry is gone")
	}
	if other.AccessToken != "item-other-token" {
		t.Errorf("item-other = %+v, want the token the user wrote", other)
	}
	if _, found := file.Find("item-new"); !found {
		t.Error("the token file has no item-new after the save was completed")
	}
}

// A second failure has to stay recoverable, or the retry is a one-shot.
func TestCompleteLinkSaveReportsTheTokenAsStillUnsaved(t *testing.T) {
	cfg, _ := closedDirConfig(t)

	_, err := newWith(cfg, linkSecret, nil, nil, nil, nil).Link(context.Background(), 730, true, nil)
	var notSaved *TokenNotSavedError
	if !errors.As(err, &notSaved) {
		t.Fatalf("error = %v (%T), want a *TokenNotSavedError", err, err)
	}

	_, err = newWith(cfg, linkNever(t), nil, nil, nil, nil).CompleteLinkSave(notSaved.Pending)
	var again *TokenNotSavedError
	if !errors.As(err, &again) {
		t.Fatalf("second error = %v (%T), want a *TokenNotSavedError", err, err)
	}
	if again.Pending.ItemID() != "item-new" {
		t.Errorf("pending item id = %q, want item-new", again.Pending.ItemID())
	}
}

// The access token must never reach a log, an error string, or the screen.
func TestTheUnsavedTokenIsNeverWritten(t *testing.T) {
	cfg, _ := closedDirConfig(t)

	_, err := newWith(cfg, linkSecret, nil, nil, nil, nil).Link(context.Background(), 730, true, nil)
	var notSaved *TokenNotSavedError
	if !errors.As(err, &notSaved) {
		t.Fatalf("error = %v (%T), want a *TokenNotSavedError", err, err)
	}

	const (
		pendingPath = "pending-path-DO-NOT-LEAK"
		savedToken  = "saved-access-token-DO-NOT-LEAK"
		savedItem   = "saved-item-DO-NOT-LEAK"
		itemEnv     = "item-env-DO-NOT-LEAK"
	)
	notSaved.Pending.path = pendingPath
	notSaved.Pending.file = tokens.File{Items: []tokens.Item{{
		ItemID: savedItem, AccessToken: savedToken,
	}}}
	notSaved.Pending.item.Env = itemEnv

	pendingFormats := []string{"%v", "%s", "%+v", "%#v", "%q"}
	var written []string
	for _, format := range pendingFormats {
		text := fmt.Sprintf(format, notSaved.Pending)
		written = append(written, text)
		if !strings.Contains(text, "item-new") || !strings.Contains(text, "TD Canada Trust") {
			t.Errorf("format %q printed %q, want the item id and institution", format, text)
		}
	}

	errorFormats := []string{"%v", "%s", "%+v", "%#v", "%q"}
	for _, format := range errorFormats {
		written = append(written, fmt.Sprintf(format, notSaved))
	}
	written = append(written,
		err.Error(),
		notSaved.Error(),
		notSaved.Pending.String(),
		fmt.Sprintf("%#v", []PendingSave{notSaved.Pending}),
		fmt.Sprintf("%+v", struct{ P PendingSave }{notSaved.Pending}),
		fmt.Sprintf("%q", []PendingSave{notSaved.Pending}),
		fmt.Sprintf("%#v", []error{notSaved}),
		fmt.Sprintf("%+v", struct{ E error }{notSaved}),
		fmt.Sprintf("%q", []error{notSaved}),
	)

	secrets := []string{secretToken, pendingPath, savedToken, savedItem, itemEnv}
	for _, text := range written {
		for _, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Errorf("secret %q appears in %q", secret, text)
			}
		}
	}
	if !strings.Contains(notSaved.Pending.String(), "item-new") {
		t.Errorf("the handle prints %q, want it to name the item", notSaved.Pending.String())
	}
}

// The error still has to say what went wrong, and it must keep the cause for
// errors.Is and errors.As.
func TestTokenNotSavedErrorKeepsItsCause(t *testing.T) {
	cause := errors.New("write tokens.json: permission denied")
	err := error(&TokenNotSavedError{Err: cause})

	if !errors.Is(err, cause) {
		t.Error("the error does not unwrap to its cause")
	}
	if !strings.Contains(err.Error(), cause.Error()) {
		t.Errorf("error = %q, want it to hold the cause", err)
	}
}

// A handle a caller never got from Link saves nothing.
func TestCompleteLinkSaveRefusesAnEmptyHandle(t *testing.T) {
	cfg := tempConfig(t, "sandbox")

	_, err := newWith(cfg, linkNever(t), nil, nil, nil, nil).CompleteLinkSave(PendingSave{})
	if err == nil {
		t.Fatal("error = nil, want a refusal of the empty handle")
	}
	// The type is what matters. A *TokenNotSavedError would tell a presenter
	// that Plaid bills an item whose token is lost, and the interface would
	// warn the user about an item that does not exist.
	var notSaved *TokenNotSavedError
	if errors.As(err, &notSaved) {
		t.Errorf("error = %v (%T), want a plain refusal and not an unsaved token", err, err)
	}
	if _, err := os.Stat(cfg.TokensPath); err == nil {
		t.Error("the empty handle wrote a token file")
	}
}
