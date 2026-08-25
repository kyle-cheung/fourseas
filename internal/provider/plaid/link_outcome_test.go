package plaid

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	plaidsdk "github.com/plaid/plaid-go/v40/plaid"
)

// fakeServer stands in for *http.Server. Shutdown runs onShutdown, which lets a
// test model a handler that is still running when the server stops.
type fakeServer struct {
	mu sync.Mutex
	// err is what Shutdown reports. A deadline error models a grace that
	// expired with a handler still running.
	err        error
	calls      int
	onShutdown func()
}

func (f *fakeServer) Shutdown(ctx context.Context) error {
	f.mu.Lock()
	f.calls++
	run := f.onShutdown
	err := f.err
	f.mu.Unlock()
	if run != nil {
		run()
	}
	return err
}

func (f *fakeServer) shutdownCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// drained reports whether the results channel still holds a token. A token left
// behind is a live Plaid item that nobody can reach again.
func drained(t *testing.T, results <-chan LinkResult) {
	t.Helper()
	select {
	case lost := <-results:
		t.Errorf("a token was left in the results channel for item %q", lost.ItemID)
	default:
	}
}

func TestWaitForOutcomeReturnsTheToken(t *testing.T) {
	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	results <- LinkResult{AccessToken: "token-a", ItemID: "item-a"}

	got, err := waitForOutcome(context.Background(), &fakeServer{}, results, failures)
	if err != nil {
		t.Fatalf("waitForOutcome returned an error: %v", err)
	}
	if got.ItemID != "item-a" {
		t.Errorf("item = %q, want item-a", got.ItemID)
	}
	drained(t, results)
}

func TestWaitForOutcomeReturnsTheFailureWhenNothingIsInFlight(t *testing.T) {
	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	want := errors.New("link was closed before the bank sign-in finished")
	failures <- want

	_, err := waitForOutcome(context.Background(), &fakeServer{}, results, failures)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	drained(t, results)
}

// The core case. A stale tab posts /exit while /exchange is still running. The
// exchange finishes during the shutdown grace, so its token must win.
func TestWaitForOutcomePrefersATokenThatArrivesDuringShutdown(t *testing.T) {
	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	server := &fakeServer{onShutdown: func() {
		results <- LinkResult{AccessToken: "token-b", ItemID: "item-b"}
	}}
	failures <- errors.New("link was closed before the bank sign-in finished")

	got, err := waitForOutcome(context.Background(), server, results, failures)
	if err != nil {
		t.Fatalf("waitForOutcome returned an error, want the token: %v", err)
	}
	if got.ItemID != "item-b" {
		t.Errorf("item = %q, want item-b", got.ItemID)
	}
	if server.shutdownCalls() == 0 {
		t.Error("the server was not stopped, so an in-flight exchange could not finish")
	}
	drained(t, results)
}

// Both channels hold a value. select picks at random, so the drain after the
// shutdown is what makes the token win. Run with -count=100.
func TestWaitForOutcomePrefersTheTokenWhenBothAreReady(t *testing.T) {
	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	results <- LinkResult{AccessToken: "token-c", ItemID: "item-c"}
	failures <- errors.New("link was closed before the bank sign-in finished")

	got, err := waitForOutcome(context.Background(), &fakeServer{}, results, failures)
	if err != nil {
		t.Fatalf("waitForOutcome returned an error, want the token: %v", err)
	}
	if got.ItemID != "item-c" {
		t.Errorf("item = %q, want item-c", got.ItemID)
	}
	drained(t, results)
}

func TestWaitForOutcomePrefersATokenAfterTheContextEnds(t *testing.T) {
	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	server := &fakeServer{onShutdown: func() {
		results <- LinkResult{AccessToken: "token-d", ItemID: "item-d"}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := waitForOutcome(ctx, server, results, failures)
	if err != nil {
		t.Fatalf("waitForOutcome returned an error, want the token: %v", err)
	}
	if got.ItemID != "item-d" {
		t.Errorf("item = %q, want item-d", got.ItemID)
	}
	drained(t, results)
}

func TestWaitForOutcomeReportsAnUnfinishedLink(t *testing.T) {
	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForOutcome(ctx, &fakeServer{}, results, failures)
	if err == nil {
		t.Fatal("waitForOutcome returned no error, want an unfinished link")
	}
	if !strings.Contains(err.Error(), "link was not completed") {
		t.Errorf("error = %q, want it to report an unfinished link", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap the context cause", err)
	}
	drained(t, results)
}

// A second post must not wedge a handler. A wedged handler holds the server
// open until the shutdown deadline and delays every caller behind it.
func TestExitHandlerDropsADuplicatePost(t *testing.T) {
	failures := make(chan error, 1)
	handler := exitHandler("test-session-nonce", failures)

	post := func() {
		body := strings.NewReader(`{"nonce":"test-session-nonce","error_code":"","error_message":""}`)
		req := httptest.NewRequest(http.MethodPost, "/exit", body)
		handler(httptest.NewRecorder(), req)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		post()
		post()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a duplicate post to /exit wedged the handler")
	}

	select {
	case <-failures:
	default:
		t.Error("the first post recorded no failure")
	}
}

func TestExitHandlerAcceptsOnlyOneExactNonceBoundObject(t *testing.T) {
	const nonce = "test-session-nonce"
	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
		wantReport bool
	}{
		{name: "trusted body", method: http.MethodPost, body: `{"nonce":"test-session-nonce","error_code":"TEST","error_message":"closed"}`, wantStatus: http.StatusOK, wantReport: true},
		{name: "get", method: http.MethodGet, wantStatus: http.StatusMethodNotAllowed},
		{name: "missing nonce", method: http.MethodPost, body: `{"error_code":"TEST","error_message":"closed"}`, wantStatus: http.StatusBadRequest},
		{name: "wrong nonce", method: http.MethodPost, body: `{"nonce":"wrong","error_code":"TEST","error_message":"closed"}`, wantStatus: http.StatusBadRequest},
		{name: "missing error code", method: http.MethodPost, body: `{"nonce":"test-session-nonce","error_message":"closed"}`, wantStatus: http.StatusBadRequest},
		{name: "missing error message", method: http.MethodPost, body: `{"nonce":"test-session-nonce","error_code":"TEST"}`, wantStatus: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, body: `{"nonce":"test-session-nonce","error_code":"TEST","error_message":"closed","public_token":"no"}`, wantStatus: http.StatusBadRequest},
		{name: "trailing value", method: http.MethodPost, body: `{"nonce":"test-session-nonce","error_code":"TEST","error_message":"closed"} {}`, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failures := make(chan error, 1)
			handler := exitHandler(nonce, failures)
			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(tt.method, "/exit", strings.NewReader(tt.body)))
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.method != http.MethodPost && rec.Header().Get("Allow") != http.MethodPost {
				t.Errorf("Allow = %q, want POST", rec.Header().Get("Allow"))
			}
			select {
			case <-failures:
				if !tt.wantReport {
					t.Error("invalid exit request reported a failure")
				}
			default:
				if tt.wantReport {
					t.Error("valid exit request reported no failure")
				}
			}
		})
	}
}

func TestExitHandlerSanitizesTerminalControlsAndCapsFields(t *testing.T) {
	failures := make(chan error, 1)
	handler := exitHandler("test-session-nonce", failures)
	message := "before\n\x1b[31mred\x1b[0m\rafter" + strings.Repeat("x", 1000)
	encodedMessage, marshalErr := json.Marshal(message)
	if marshalErr != nil {
		t.Fatalf("encode message: %v", marshalErr)
	}
	body := fmt.Sprintf(`{"nonce":"test-session-nonce","error_code":"BAD\u001b[2J\nCODE","error_message":%s}`,
		encodedMessage)
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/exit", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	err := <-failures
	if strings.ContainsAny(err.Error(), "\x1b\n\r") {
		t.Errorf("returned error contains terminal controls: %q", err)
	}
	if len(err.Error()) > 2*maxLinkErrorFieldLength+40 {
		t.Errorf("returned error has %d bytes, want a bounded error", len(err.Error()))
	}
}

func TestReportDropsASecondFailure(t *testing.T) {
	failures := make(chan error, 1)
	first := errors.New("an earlier failure")
	failures <- first

	done := make(chan struct{})
	go func() {
		defer close(done)
		report(failures, errors.New("a second failure"))
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a second failure wedged the sender")
	}

	if got := <-failures; !errors.Is(got, first) {
		t.Errorf("failure = %v, want the first failure to be kept", got)
	}
}

// The real exchange handler, against a Plaid backend that refuses the
// exchange. A repeated post must report once and never wedge.
func TestExchangeHandlerReportsAndDropsADuplicate(t *testing.T) {
	const publicToken = "public-sandbox-1"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error_code":"INVALID_PUBLIC_TOKEN","error_type":"INVALID_INPUT","error_message":"bad public-sandbox-1"}`))
	}))
	defer backend.Close()

	cfg := plaidsdk.NewConfiguration()
	cfg.Servers = plaidsdk.ServerConfigurations{{URL: backend.URL}}
	client := plaidsdk.NewAPIClient(cfg)

	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	handler := exchangeHandler(client, results, failures)

	post := func() *httptest.ResponseRecorder {
		body := strings.NewReader(`{"public_token":"` + publicToken + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/exchange", body)
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		post()
		post()
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a duplicate post to /exchange wedged the handler")
	}

	select {
	case err := <-failures:
		if !strings.Contains(err.Error(), "exchange public token") {
			t.Errorf("failure = %q, want it to name the failed exchange", err)
		}
		if strings.Contains(err.Error(), publicToken) {
			t.Error("failure contains the public token")
		}
	default:
		t.Error("the first post recorded no failure")
	}
	drained(t, results)
}

func TestExchangeHandlerAcceptsOnlyOneExactObject(t *testing.T) {
	var backendCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"access-sandbox-1","item_id":"item-1","request_id":"r1"}`))
	}))
	defer backend.Close()

	cfg := plaidsdk.NewConfiguration()
	cfg.Servers = plaidsdk.ServerConfigurations{{URL: backend.URL}}
	client := plaidsdk.NewAPIClient(cfg)
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantResult bool
	}{
		{name: "valid public token", body: `{"public_token":"public-sandbox-1"}`, wantStatus: http.StatusOK, wantResult: true},
		{name: "unknown field", body: `{"public_token":"public-sandbox-1","unexpected":true}`, wantStatus: http.StatusBadRequest},
		{name: "trailing value", body: `{"public_token":"public-sandbox-1"} {}`, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := backendCalls.Load()
			results := make(chan LinkResult, 1)
			failures := make(chan error, 1)
			rec := httptest.NewRecorder()
			exchangeHandler(client, results, failures)(rec,
				httptest.NewRequest(http.MethodPost, "/exchange", strings.NewReader(tt.body)))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			select {
			case <-results:
				if !tt.wantResult {
					t.Error("invalid exchange delivered a result")
				}
			default:
				if tt.wantResult {
					t.Error("valid exchange delivered no result")
				}
			}
			if !tt.wantResult && backendCalls.Load() != before {
				t.Error("invalid exchange called Plaid")
			}
		})
	}
}

// The grace expired with a handler still open, so the token gets one more
// bounded wait. Without it the token is lost to the exit error.
func TestWaitForOutcomeWaitsForATokenAfterAnUncleanStop(t *testing.T) {
	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	server := &fakeServer{
		err: context.DeadlineExceeded,
		onShutdown: func() {
			go func() {
				time.Sleep(20 * time.Millisecond)
				results <- LinkResult{AccessToken: "token-g", ItemID: "item-g"}
			}()
		},
	}
	failures <- errors.New("link was closed before the bank sign-in finished")

	got, err := waitForOutcome(context.Background(), server, results, failures)
	if err != nil {
		t.Fatalf("waitForOutcome returned an error, want the token: %v", err)
	}
	if got.ItemID != "item-g" {
		t.Errorf("item = %q, want item-g", got.ItemID)
	}
	drained(t, results)
}

// An unclean stop with no token still reports the failure, and does not wait
// beyond tokenGrace.
func TestWaitForOutcomeGivesUpAfterTheTokenGrace(t *testing.T) {
	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	want := errors.New("link was closed before the bank sign-in finished")
	failures <- want
	server := &fakeServer{err: context.DeadlineExceeded}

	start := time.Now()
	_, err := waitForOutcome(context.Background(), server, results, failures)
	waited := time.Since(start)

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if waited > 2*tokenGrace {
		t.Errorf("waited %s, want no more than the token grace of %s", waited, tokenGrace)
	}
	drained(t, results)
}

// The institution lookup is cosmetic and must not gate the token. Its backend
// never answers, so only the bound lets the handler finish.
func TestExchangeHandlerDoesNotLetASlowLookupHoldTheToken(t *testing.T) {
	release := make(chan struct{})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "public_token/exchange") {
			w.Write([]byte(`{"access_token":"access-sandbox-1","item_id":"item-h","request_id":"r1"}`))
			return
		}
		// Every enrichment call hangs until the test ends.
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	// The hanging handler is released before the backend stops, because
	// httptest.Server.Close waits for its handlers.
	defer backend.Close()
	defer close(release)

	cfg := plaidsdk.NewConfiguration()
	cfg.Servers = plaidsdk.ServerConfigurations{{URL: backend.URL}}
	client := plaidsdk.NewAPIClient(cfg)

	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	handler := exchangeHandler(client, results, failures)

	done := make(chan struct{})
	go func() {
		defer close(done)
		body := strings.NewReader(`{"public_token":"public-sandbox-1"}`)
		handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/exchange", body))
	}()

	// The handler must deliver well inside the shutdown grace, which is what
	// the caller allows it after an exit.
	select {
	case <-done:
	case <-time.After(shutdownGrace):
		t.Fatal("a slow institution lookup held the token past the shutdown grace")
	}

	select {
	case got := <-results:
		if got.ItemID != "item-h" {
			t.Errorf("item = %q, want item-h", got.ItemID)
		}
		if got.Institution != "" {
			t.Errorf("institution = %q, want it empty when the lookup times out", got.Institution)
		}
	default:
		t.Error("the handler delivered no token")
	}
}

func TestDeliverDropsASecondResult(t *testing.T) {
	results := make(chan LinkResult, 1)
	results <- LinkResult{ItemID: "item-e"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		deliver(results, LinkResult{ItemID: "item-f"})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a second result wedged the sender")
	}

	got := <-results
	if got.ItemID != "item-e" {
		t.Errorf("item = %q, want the first result item-e to be kept", got.ItemID)
	}
}

func TestNewUpdateNonceUsesThirtyTwoRandomBytes(t *testing.T) {
	first, err := newUpdateNonce()
	if err != nil {
		t.Fatalf("newUpdateNonce: %v", err)
	}
	second, err := newUpdateNonce()
	if err != nil {
		t.Fatalf("newUpdateNonce: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("decoded nonce has %d bytes, want 32", len(decoded))
	}
	if first == second {
		t.Error("two update sessions received the same nonce")
	}
}

func TestUpdateCompleteHandlerAcceptsOnlyTheSessionNonce(t *testing.T) {
	const nonce = "test-session-nonce"
	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
		wantResult bool
	}{
		{name: "correct nonce", method: http.MethodPost, body: `{"nonce":"test-session-nonce"}`, wantStatus: http.StatusOK, wantResult: true},
		{name: "get", method: http.MethodGet, wantStatus: http.StatusMethodNotAllowed},
		{name: "missing nonce", method: http.MethodPost, body: `{}`, wantStatus: http.StatusBadRequest},
		{name: "wrong nonce", method: http.MethodPost, body: `{"nonce":"wrong"}`, wantStatus: http.StatusBadRequest},
		{name: "public token field", method: http.MethodPost, body: `{"nonce":"test-session-nonce","public_token":"must-not-be-accepted"}`, wantStatus: http.StatusBadRequest},
		{name: "trailing value", method: http.MethodPost, body: `{"nonce":"test-session-nonce"} {}`, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make(chan LinkResult, 1)
			handler := updateCompleteHandler(nonce, results)
			req := httptest.NewRequest(tt.method, "/update-complete", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			handler(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.method != http.MethodPost && rec.Header().Get("Allow") != http.MethodPost {
				t.Errorf("Allow = %q, want POST", rec.Header().Get("Allow"))
			}
			select {
			case got := <-results:
				if !tt.wantResult {
					t.Error("invalid completion delivered a result")
				}
				if got != (LinkResult{}) {
					t.Errorf("result = %+v, want an empty update result", got)
				}
			default:
				if tt.wantResult {
					t.Error("valid completion delivered no result")
				}
			}
		})
	}
}

func TestLinkMuxRegistersOnlyTheCompletionRouteForItsMode(t *testing.T) {
	client := plaidsdk.NewAPIClient(plaidsdk.NewConfiguration())
	tests := []struct {
		name          string
		options       linkRequest
		path          string
		wantStatus    int
		forbiddenPath string
		forbiddenBody string
	}{
		{
			name:          "new mode",
			path:          "/exchange",
			wantStatus:    http.StatusBadRequest,
			forbiddenPath: "/update-complete",
			forbiddenBody: `{"nonce":"test-session-nonce"}`,
		},
		{
			name:          "update mode",
			options:       linkRequest{accessToken: "test-existing-access-token"},
			path:          "/update-complete",
			wantStatus:    http.StatusOK,
			forbiddenPath: "/exchange",
			forbiddenBody: `{"public_token":"must-not-be-exchanged"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make(chan LinkResult, 1)
			failures := make(chan error, 1)
			cfg := Config{LinkPort: 8080}
			mux := linkMux(client, results, failures, []byte("link page"), cfg, tt.options, "test-session-nonce")

			rec := httptest.NewRecorder()
			body := strings.NewReader(`{"nonce":"test-session-nonce"}`)
			if tt.path == "/exchange" {
				body = strings.NewReader(`{}`)
			}
			mux.ServeHTTP(rec, trustedCallbackRequest(http.MethodPost, tt.path, body))
			if rec.Code != tt.wantStatus {
				t.Errorf("registered route status = %d, want %d", rec.Code, tt.wantStatus)
			}

			forbidden := httptest.NewRecorder()
			mux.ServeHTTP(forbidden, trustedCallbackRequest(http.MethodPost, tt.forbiddenPath, strings.NewReader(tt.forbiddenBody)))
			if forbidden.Code != http.StatusNotFound {
				t.Errorf("forbidden route status = %d, want 404", forbidden.Code)
			}
			select {
			case got := <-results:
				if tt.options.accessToken == "" {
					t.Errorf("new mode delivered unexpected result %+v", got)
				}
			default:
			}
		})
	}
}

func trustedCallbackRequest(method, path string, body *strings.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.Host = "localhost:8080"
	req.Header.Set("Origin", "http://localhost:8080")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestCallbackRoutesRejectUntrustedBrowserRequests(t *testing.T) {
	client := plaidsdk.NewAPIClient(plaidsdk.NewConfiguration())
	tests := []struct {
		name       string
		options    linkRequest
		path       string
		body       string
		mutate     func(*http.Request)
		wantStatus int
	}{
		{name: "update untrusted host", options: linkRequest{accessToken: "access"}, path: "/update-complete", body: `{"nonce":"test-session-nonce"}`, mutate: func(r *http.Request) { r.Host = "attacker.test" }, wantStatus: http.StatusBadRequest},
		{name: "update missing origin", options: linkRequest{accessToken: "access"}, path: "/update-complete", body: `{"nonce":"test-session-nonce"}`, mutate: func(r *http.Request) { r.Header.Del("Origin") }, wantStatus: http.StatusForbidden},
		{name: "update wrong origin", options: linkRequest{accessToken: "access"}, path: "/update-complete", body: `{"nonce":"test-session-nonce"}`, mutate: func(r *http.Request) { r.Header.Set("Origin", "https://attacker.test") }, wantStatus: http.StatusForbidden},
		{name: "update get", options: linkRequest{accessToken: "access"}, path: "/update-complete", body: `{"nonce":"test-session-nonce"}`, mutate: func(r *http.Request) { r.Method = http.MethodGet }, wantStatus: http.StatusMethodNotAllowed},
		{name: "update missing content type", options: linkRequest{accessToken: "access"}, path: "/update-complete", body: `{"nonce":"test-session-nonce"}`, mutate: func(r *http.Request) { r.Header.Del("Content-Type") }, wantStatus: http.StatusUnsupportedMediaType},
		{name: "update wrong content type", options: linkRequest{accessToken: "access"}, path: "/update-complete", body: `{"nonce":"test-session-nonce"}`, mutate: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, wantStatus: http.StatusUnsupportedMediaType},
		{name: "update oversized body", options: linkRequest{accessToken: "access"}, path: "/update-complete", body: `{"nonce":"test-session-nonce","padding":"` + strings.Repeat("x", 5000) + `"}`, mutate: func(*http.Request) {}, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "exit untrusted host", path: "/exit", body: `{"nonce":"test-session-nonce","error_code":"","error_message":""}`, mutate: func(r *http.Request) { r.Host = "attacker.test" }, wantStatus: http.StatusBadRequest},
		{name: "exit missing origin", path: "/exit", body: `{"nonce":"test-session-nonce","error_code":"","error_message":""}`, mutate: func(r *http.Request) { r.Header.Del("Origin") }, wantStatus: http.StatusForbidden},
		{name: "exit wrong origin", path: "/exit", body: `{"nonce":"test-session-nonce","error_code":"","error_message":""}`, mutate: func(r *http.Request) { r.Header.Set("Origin", "https://attacker.test") }, wantStatus: http.StatusForbidden},
		{name: "exit get", path: "/exit", body: `{"nonce":"test-session-nonce","error_code":"","error_message":""}`, mutate: func(r *http.Request) { r.Method = http.MethodGet }, wantStatus: http.StatusMethodNotAllowed},
		{name: "exit wrong content type", path: "/exit", body: `{"nonce":"test-session-nonce","error_code":"","error_message":""}`, mutate: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, wantStatus: http.StatusUnsupportedMediaType},
		{name: "exit missing content type", path: "/exit", body: `{"nonce":"test-session-nonce","error_code":"","error_message":""}`, mutate: func(r *http.Request) { r.Header.Del("Content-Type") }, wantStatus: http.StatusUnsupportedMediaType},
		{name: "exit oversized body", path: "/exit", body: `{"nonce":"test-session-nonce","error_code":"","error_message":"` + strings.Repeat("x", 5000) + `"}`, mutate: func(*http.Request) {}, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "exchange untrusted host", path: "/exchange", body: `{"public_token":"public-test"}`, mutate: func(r *http.Request) { r.Host = "attacker.test" }, wantStatus: http.StatusBadRequest},
		{name: "exchange wrong origin", path: "/exchange", body: `{"public_token":"public-test"}`, mutate: func(r *http.Request) { r.Header.Set("Origin", "https://attacker.test") }, wantStatus: http.StatusForbidden},
		{name: "exchange wrong content type", path: "/exchange", body: `{"public_token":"public-test"}`, mutate: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, wantStatus: http.StatusUnsupportedMediaType},
		{name: "exchange oversized body", path: "/exchange", body: `{"public_token":"` + strings.Repeat("x", 5000) + `"}`, mutate: func(*http.Request) {}, wantStatus: http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make(chan LinkResult, 1)
			failures := make(chan error, 1)
			mux := linkMux(client, results, failures, nil, Config{LinkPort: 8080}, tt.options, "test-session-nonce")
			req := trustedCallbackRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			tt.mutate(req)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			select {
			case result := <-results:
				t.Errorf("untrusted request delivered result %+v", result)
			default:
			}
			select {
			case err := <-failures:
				t.Errorf("untrusted request delivered failure %v", err)
			default:
			}
		})
	}
}

func TestLinkPageRejectsAnUntrustedHost(t *testing.T) {
	mux := linkMux(plaidsdk.NewAPIClient(plaidsdk.NewConfiguration()), make(chan LinkResult, 1),
		make(chan error, 1), []byte("link page"), Config{LinkPort: 8080}, linkRequest{}, "nonce")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "attacker.test"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateLinkPagePostsOnlyTheNonce(t *testing.T) {
	page, err := renderLinkPage(linkPageData{
		Token:  "test-link-token",
		Nonce:  "test-session-nonce",
		Update: true,
	})
	if err != nil {
		t.Fatalf("renderLinkPage: %v", err)
	}
	body := string(page)
	for _, want := range []string{"/update-complete", "JSON.stringify({ nonce:", "test-session-nonce"} {
		if !strings.Contains(body, want) {
			t.Errorf("update page does not contain %q", want)
		}
	}
	for _, forbidden := range []string{"public_token", "publicToken", "/exchange"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("update page contains forbidden text %q", forbidden)
		}
	}
}

func TestNewLinkPageStillPostsOnlyThePublicTokenForExchange(t *testing.T) {
	page, err := renderLinkPage(linkPageData{Token: "test-link-token", Nonce: "test-session-nonce"})
	if err != nil {
		t.Fatalf("renderLinkPage: %v", err)
	}
	body := string(page)
	for _, want := range []string{"/exchange", "publicToken", "public_token: publicToken"} {
		if !strings.Contains(body, want) {
			t.Errorf("new-link page does not contain %q", want)
		}
	}
	for _, want := range []string{"/exit", "nonce: \"test-session-nonce\""} {
		if !strings.Contains(body, want) {
			t.Errorf("new-link page does not contain exit protection %q", want)
		}
	}
	for _, forbidden := range []string{"/update-complete"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("new-link page contains update text %q", forbidden)
		}
	}
}
