package plaid

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

// linkTimeout is how long fourseas waits for the browser part to finish.
const linkTimeout = 10 * time.Minute

// LinkResult is one newly linked institution.
type LinkResult struct {
	AccessToken string
	ItemID      string
	Institution string
}

// linkRequest holds the choices for one Link flow. accessToken is reserved for
// an update flow; a new link leaves it empty.
type linkRequest struct {
	days        int
	liabilities bool
	accessToken string
}

// Link runs the browser part of Plaid Link and returns the access token.
//
// It creates a link token, serves the Link widget on localhost, opens the
// browser, waits for the user to sign in to the bank, then exchanges the
// public token. The server stops before Link returns.
//
// days is how much transaction history to request for the new item. Plaid fixes
// this amount when the item is created and does not permit a later change, so
// it can only be chosen here. A value that is not positive leaves the choice to
// Plaid, which requests 90 days.
func Link(ctx context.Context, cfg Config, days int, liabilities bool) (LinkResult, error) {
	return runLink(ctx, cfg, linkRequest{days: days, liabilities: liabilities})
}

// UpdateLiabilities opens Link in update mode to collect liabilities consent
// for an existing Item. It never receives or exchanges a public token.
func UpdateLiabilities(ctx context.Context, cfg Config, accessToken string) (LinkResult, error) {
	result, err := runLink(ctx, cfg, linkRequest{accessToken: accessToken})
	return result, redactLinkSecret(err, accessToken)
}

func runLink(ctx context.Context, cfg Config, options linkRequest) (LinkResult, error) {
	client, err := newClient(cfg)
	if err != nil {
		return LinkResult{}, err
	}

	linkToken, err := createLinkToken(ctx, client, cfg, options)
	if err != nil {
		return LinkResult{}, err
	}

	data := linkPageData{Token: linkToken, Update: options.accessToken != ""}
	data.Nonce, err = newUpdateNonce()
	if err != nil {
		return LinkResult{}, fmt.Errorf("prepare the link session: %w", err)
	}
	page, err := renderLinkPage(data)
	if err != nil {
		return LinkResult{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, linkTimeout)
	defer cancel()

	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)

	mux := linkMux(client, results, failures, page, cfg, options, data.Nonce)

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.LinkPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return LinkResult{}, fmt.Errorf("listen on %s: %w (is another fourseas running?)", addr, err)
	}

	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	// The last stop is bounded too, and closes the server by force when the
	// bound expires. An unbounded wait here would undo the bound on the stops
	// inside waitForOutcome and hold the caller with no way out.
	defer func() {
		shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), shutdownGrace)
		defer stopShutdown()
		if err := server.Shutdown(shutdownCtx); err != nil {
			server.Close()
		}
	}()

	openBrowser(LinkURL(cfg))

	return waitForOutcome(ctx, server, results, failures)
}

func linkMux(
	client *plaidsdk.APIClient,
	results chan<- LinkResult,
	failures chan<- error,
	page []byte,
	cfg Config,
	options linkRequest,
	nonce string,
) *http.ServeMux {
	mux := http.NewServeMux()
	serve := func(w http.ResponseWriter, r *http.Request) {
		if !validLinkHost(r, cfg) {
			http.Error(w, "invalid callback host", http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/" && r.URL.Path != "/oauth" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	}
	mux.HandleFunc("/", serve)
	// Plaid sends the browser back here after an OAuth bank sign-in.
	mux.HandleFunc("/oauth", serve)
	if options.accessToken == "" {
		mux.HandleFunc("/exchange", secureCallback(cfg, exchangeHandler(client, results, failures)))
	} else {
		mux.HandleFunc("/update-complete", secureCallback(cfg, updateCompleteHandler(nonce, results)))
	}
	mux.HandleFunc("/exit", secureCallback(cfg, exitHandler(nonce, failures)))
	return mux
}

const maxCallbackBody = 4096

func validLinkHost(r *http.Request, cfg Config) bool {
	want := fmt.Sprintf("localhost:%d", cfg.LinkPort)
	return strings.EqualFold(r.Host, want)
}

func secureCallback(cfg Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validLinkHost(r, cfg) {
			http.Error(w, "invalid callback host", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Origin") != LinkURL(cfg) {
			http.Error(w, "invalid callback origin", http.StatusForbidden)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxCallbackBody+1))
		if err != nil {
			http.Error(w, "invalid callback body", http.StatusBadRequest)
			return
		}
		if len(body) > maxCallbackBody {
			http.Error(w, "callback body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next(w, r)
	}
}

// shutdownGrace is how long a stopping server may take to let the handlers
// that are still running finish.
const shutdownGrace = 5 * time.Second

// tokenGrace is the extra time a token gets when the server did not stop
// inside shutdownGrace, because then a handler may still be about to deliver
// one.
const tokenGrace = 2 * time.Second

// enrichmentGrace bounds the cosmetic institution lookup that runs after an
// exchange, so it cannot delay the token past shutdownGrace.
const enrichmentGrace = 2 * time.Second

// shutdowner is the part of *http.Server that waitForOutcome needs.
type shutdowner interface {
	Shutdown(ctx context.Context) error
}

// waitForOutcome waits for the browser part to produce a token, a failure, or
// nothing before the deadline.
func waitForOutcome(ctx context.Context, server shutdowner, results <-chan LinkResult, failures <-chan error) (LinkResult, error) {
	select {
	case result := <-results:
		return result, nil
	case err := <-failures:
		// An exit can arrive while an exchange is still running: the OAuth
		// flow serves the widget twice, so a stale tab can report an exit
		// after the other tab signed in. Stop the server first, which lets
		// that exchange finish, then look for its token. A token always wins,
		// because it is the only handle on an item that Plaid now bills.
		if result, ok := lastToken(server, results); ok {
			return result, nil
		}
		return LinkResult{}, err
	case <-ctx.Done():
		if result, ok := lastToken(server, results); ok {
			return result, nil
		}
		return LinkResult{}, fmt.Errorf("link was not completed: %w", ctx.Err())
	}
}

// lastToken stops the server and reports a token from a handler that was still
// running. A Shutdown error means the grace expired with a handler still open,
// so a token may be moments away: that case, and only that case, gets one more
// bounded wait before the token is given up for lost.
func lastToken(server shutdowner, results <-chan LinkResult) (LinkResult, bool) {
	shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), shutdownGrace)
	stopped := server.Shutdown(shutdownCtx)
	stopShutdown()

	if stopped != nil {
		select {
		case result := <-results:
			return result, true
		case <-time.After(tokenGrace):
		}
	}

	select {
	case result := <-results:
		return result, true
	default:
		return LinkResult{}, false
	}
}

// LinkURL is the local address the Link widget serves on. Both the CLI and
// the TUI show this URL to the user before the browser opens.
func LinkURL(cfg Config) string {
	return fmt.Sprintf("http://localhost:%d", cfg.LinkPort)
}

func createLinkToken(ctx context.Context, client *plaidsdk.APIClient, cfg Config, options linkRequest) (string, error) {
	req := linkTokenRequest(cfg, options)

	resp, httpResp, err := client.PlaidApi.LinkTokenCreate(ctx).
		LinkTokenCreateRequest(*req).Execute()
	if err != nil {
		return "", apiError("create link token", err, httpResp)
	}
	return resp.LinkToken, nil
}

// linkTokenRequest builds the link token call. transactions.days_requested is
// the only place the amount of history can be set: this call initializes the
// transactions product, and Plaid then refuses to change the amount for the
// life of the item.
func linkTokenRequest(cfg Config, options linkRequest) *plaidsdk.LinkTokenCreateRequest {
	user := plaidsdk.NewLinkTokenCreateRequestUser("fourseas-local-user")
	req := plaidsdk.NewLinkTokenCreateRequest(
		"Fourseas",
		"en",
		[]plaidsdk.CountryCode{plaidsdk.COUNTRYCODE_US, plaidsdk.COUNTRYCODE_CA},
	)
	req.SetUser(*user)
	if options.accessToken != "" {
		req.SetAccessToken(options.accessToken)
		req.SetAdditionalConsentedProducts([]plaidsdk.Products{plaidsdk.PRODUCTS_LIABILITIES})
	} else {
		req.SetProducts([]plaidsdk.Products{plaidsdk.PRODUCTS_TRANSACTIONS})
	}
	if options.accessToken == "" && options.liabilities {
		req.SetAdditionalConsentedProducts([]plaidsdk.Products{plaidsdk.PRODUCTS_LIABILITIES})
	}
	if cfg.RedirectURI != "" {
		req.SetRedirectUri(cfg.RedirectURI)
	}
	if options.accessToken == "" && options.days > 0 {
		transactions := plaidsdk.NewLinkTokenTransactions()
		transactions.SetDaysRequested(int32(options.days))
		req.SetTransactions(*transactions)
	}
	return req
}

func exchangeHandler(client *plaidsdk.APIClient, results chan<- LinkResult, failures chan<- error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PublicToken string `json:"public_token"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.PublicToken == "" {
			http.Error(w, "expected a public_token", http.StatusBadRequest)
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			http.Error(w, "expected a public_token", http.StatusBadRequest)
			return
		}

		req := plaidsdk.NewItemPublicTokenExchangeRequest(body.PublicToken)
		resp, httpResp, err := client.PlaidApi.ItemPublicTokenExchange(r.Context()).
			ItemPublicTokenExchangeRequest(*req).Execute()
		if err != nil {
			wrapped := redactLinkSecret(apiError("exchange public token", err, httpResp), body.PublicToken)
			http.Error(w, wrapped.Error(), http.StatusBadGateway)
			report(failures, wrapped)
			return
		}

		// The access token exists now, and it is the only handle on an item
		// that Plaid bills from here on. The institution name is cosmetic and
		// costs two more calls, so it gets a short bound of its own: a slow
		// lookup must never keep the token from reaching the reader before the
		// server stops. institutionName already returns "" on any error.
		nameCtx, stopName := context.WithTimeout(r.Context(), enrichmentGrace)
		institution := institutionName(nameCtx, client, resp.AccessToken)
		stopName()

		result := LinkResult{
			AccessToken: resp.AccessToken,
			ItemID:      resp.ItemId,
			Institution: institution,
		}
		w.Write([]byte("ok"))
		deliver(results, result)
	}
}

type redactedLinkError struct {
	err  error
	text string
}

func (e *redactedLinkError) Error() string { return e.text }
func (e *redactedLinkError) Unwrap() error { return e.err }

func redactLinkSecret(err error, secret string) error {
	if err == nil || secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return &redactedLinkError{err: err, text: strings.ReplaceAll(err.Error(), secret, "[REDACTED]")}
}

func newUpdateNonce() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func updateCompleteHandler(nonce string, results chan<- LinkResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var body struct {
			Nonce string `json:"nonce"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Nonce == "" {
			http.Error(w, "invalid update completion", http.StatusBadRequest)
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			http.Error(w, "invalid update completion", http.StatusBadRequest)
			return
		}
		if subtle.ConstantTimeCompare([]byte(body.Nonce), []byte(nonce)) != 1 {
			http.Error(w, "invalid update completion", http.StatusBadRequest)
			return
		}

		w.Write([]byte("ok"))
		deliver(results, LinkResult{})
	}
}

// deliver puts the result on the channel if the channel is empty. The reader
// takes one result only, so a repeated exchange must not hold the handler open:
// a handler that waits forever keeps the server from stopping.
func deliver(results chan<- LinkResult, result LinkResult) {
	select {
	case results <- result:
	default:
	}
}

// report puts the failure on the channel if the channel is empty, for the same
// reason as deliver. The first failure is the one that describes the problem.
func report(failures chan<- error, err error) {
	select {
	case failures <- err:
	default:
	}
}

const maxLinkErrorFieldLength = 200

func exitHandler(nonce string, failures chan<- error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Nonce        *string `json:"nonce"`
			ErrorCode    *string `json:"error_code"`
			ErrorMessage *string `json:"error_message"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Nonce == nil || body.ErrorCode == nil || body.ErrorMessage == nil {
			http.Error(w, "invalid exit report", http.StatusBadRequest)
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			http.Error(w, "invalid exit report", http.StatusBadRequest)
			return
		}
		if subtle.ConstantTimeCompare([]byte(*body.Nonce), []byte(nonce)) != 1 {
			http.Error(w, "invalid exit report", http.StatusBadRequest)
			return
		}
		w.Write([]byte("ok"))

		code := sanitizeLinkErrorField(*body.ErrorCode)
		message := sanitizeLinkErrorField(*body.ErrorMessage)
		if code == "" {
			report(failures, fmt.Errorf("link was closed before the bank sign-in finished"))
			return
		}
		report(failures, fmt.Errorf("link failed: %s: %s", code, message))
	}
}

func sanitizeLinkErrorField(value string) string {
	value = ansi.Strip(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > maxLinkErrorFieldLength {
		value = string(runes[:maxLinkErrorFieldLength])
	}
	return value
}

// openBrowser tries to open the page. A failure is not fatal, because the
// program already printed the URL.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}

// linkPage serves Plaid Link and posts the result back to fourseas.
// On an OAuth return, Plaid requires the widget to restart with the same link
// token and the full redirect URL.
var linkPage = template.Must(template.New("link").Parse(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Fourseas</title>
  <script src="https://cdn.plaid.com/link/v2/stable/link-initialize.js"></script>
  <style>
    body { font-family: -apple-system, system-ui, sans-serif; margin: 4rem auto; max-width: 32rem; }
    #status { color: #444; }
  </style>
</head>
<body>
  <h1>Fourseas</h1>
  <p id="status">Opening Plaid Link…</p>
  <script>
    const status = document.getElementById('status');
    const isOAuthReturn = window.location.pathname === '/oauth';

    const config = {
      token: {{ .Token }},
      {{ if .Update }}
      onSuccess: () => {
        status.textContent = 'Updated. Finishing…';
        fetch('/update-complete', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ nonce: {{ .Nonce }} }),
        }).then((r) => {
          status.textContent = r.ok
            ? 'Done. Go back to the terminal.'
            : 'Fourseas could not confirm the update. Check the terminal.';
        });
      },
      {{ else }}
      onSuccess: (publicToken) => {
        status.textContent = 'Linked. Finishing…';
        fetch('/exchange', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ public_token: publicToken }),
        }).then((r) => {
          status.textContent = r.ok
            ? 'Done. Go back to the terminal.'
            : 'Fourseas could not exchange the token. Check the terminal.';
        });
      },
      {{ end }}
      onExit: (err) => {
        status.textContent = 'Link closed. Go back to the terminal.';
        fetch('/exit', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            nonce: {{ .Nonce }},
            error_code: err ? err.error_code : '',
            error_message: err ? err.display_message || err.error_message : '',
          }),
        });
      },
    };

    if (isOAuthReturn) {
      config.receivedRedirectUri = window.location.href;
    }

    Plaid.create(config).open();
  </script>
</body>
</html>
`))

type linkPageData struct {
	Token  string
	Nonce  string
	Update bool
}

func renderLinkPage(data linkPageData) ([]byte, error) {
	var buf bytes.Buffer
	if err := linkPage.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("build the link page: %w", err)
	}
	return buf.Bytes(), nil
}
