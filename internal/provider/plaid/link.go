package plaid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

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
func Link(ctx context.Context, cfg Config, days int) (LinkResult, error) {
	client, err := newClient(cfg)
	if err != nil {
		return LinkResult{}, err
	}

	linkToken, err := createLinkToken(ctx, client, cfg, days)
	if err != nil {
		return LinkResult{}, err
	}

	page, err := renderLinkPage(linkToken)
	if err != nil {
		return LinkResult{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, linkTimeout)
	defer cancel()

	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)

	mux := http.NewServeMux()
	serve := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	}
	mux.HandleFunc("/", serve)
	// Plaid sends the browser back here after an OAuth bank sign-in.
	mux.HandleFunc("/oauth", serve)
	mux.HandleFunc("/exchange", exchangeHandler(client, results, failures))
	mux.HandleFunc("/exit", exitHandler(failures))

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

// shutdownGrace is how long a stopping server may take to let the handlers
// that are still running finish.
const shutdownGrace = 5 * time.Second

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
		stopServer(server)

		select {
		case result := <-results:
			return result, nil
		default:
			return LinkResult{}, err
		}
	case <-ctx.Done():
		stopServer(server)

		select {
		case result := <-results:
			return result, nil
		default:
			return LinkResult{}, fmt.Errorf("link was not completed: %w", ctx.Err())
		}
	}
}

// stopServer stops the server and waits, for a bounded time, for the handlers
// that are still running.
func stopServer(server shutdowner) {
	shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), shutdownGrace)
	defer stopShutdown()
	_ = server.Shutdown(shutdownCtx)
}

// LinkURL is the local address the Link widget serves on. Both the CLI and
// the TUI show this URL to the user before the browser opens.
func LinkURL(cfg Config) string {
	return fmt.Sprintf("http://localhost:%d", cfg.LinkPort)
}

func createLinkToken(ctx context.Context, client *plaidsdk.APIClient, cfg Config, days int) (string, error) {
	req := linkTokenRequest(cfg, days)

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
func linkTokenRequest(cfg Config, days int) *plaidsdk.LinkTokenCreateRequest {
	user := plaidsdk.NewLinkTokenCreateRequestUser("fourseas-local-user")
	req := plaidsdk.NewLinkTokenCreateRequest(
		"Fourseas",
		"en",
		[]plaidsdk.CountryCode{plaidsdk.COUNTRYCODE_US, plaidsdk.COUNTRYCODE_CA},
	)
	req.SetUser(*user)
	req.SetProducts([]plaidsdk.Products{plaidsdk.PRODUCTS_TRANSACTIONS})
	if cfg.RedirectURI != "" {
		req.SetRedirectUri(cfg.RedirectURI)
	}
	if days > 0 {
		transactions := plaidsdk.NewLinkTokenTransactions()
		transactions.SetDaysRequested(int32(days))
		req.SetTransactions(*transactions)
	}
	return req
}

func exchangeHandler(client *plaidsdk.APIClient, results chan<- LinkResult, failures chan<- error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PublicToken string `json:"public_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PublicToken == "" {
			http.Error(w, "expected a public_token", http.StatusBadRequest)
			return
		}

		req := plaidsdk.NewItemPublicTokenExchangeRequest(body.PublicToken)
		resp, httpResp, err := client.PlaidApi.ItemPublicTokenExchange(r.Context()).
			ItemPublicTokenExchangeRequest(*req).Execute()
		if err != nil {
			wrapped := apiError("exchange public token", err, httpResp)
			http.Error(w, wrapped.Error(), http.StatusBadGateway)
			report(failures, wrapped)
			return
		}

		result := LinkResult{
			AccessToken: resp.AccessToken,
			ItemID:      resp.ItemId,
			Institution: institutionName(r.Context(), client, resp.AccessToken),
		}
		w.Write([]byte("ok"))
		deliver(results, result)
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

func exitHandler(failures chan<- error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ErrorCode    string `json:"error_code"`
			ErrorMessage string `json:"error_message"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte("ok"))

		if body.ErrorCode == "" {
			report(failures, fmt.Errorf("link was closed before the bank sign-in finished"))
			return
		}
		report(failures, fmt.Errorf("link failed: %s: %s", body.ErrorCode, body.ErrorMessage))
	}
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
      onExit: (err) => {
        status.textContent = 'Link closed. Go back to the terminal.';
        fetch('/exit', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
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

func renderLinkPage(token string) ([]byte, error) {
	var buf bytes.Buffer
	if err := linkPage.Execute(&buf, struct{ Token string }{Token: token}); err != nil {
		return nil, fmt.Errorf("build the link page: %w", err)
	}
	return buf.Bytes(), nil
}
