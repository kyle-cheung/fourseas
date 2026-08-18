package plaid

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	handler := exitHandler(failures)

	post := func() {
		body := strings.NewReader(`{"error_code":"","error_message":""}`)
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
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error_code":"INVALID_PUBLIC_TOKEN","error_type":"INVALID_INPUT","error_message":"bad token"}`))
	}))
	defer backend.Close()

	cfg := plaidsdk.NewConfiguration()
	cfg.Servers = plaidsdk.ServerConfigurations{{URL: backend.URL}}
	client := plaidsdk.NewAPIClient(cfg)

	results := make(chan LinkResult, 1)
	failures := make(chan error, 1)
	handler := exchangeHandler(client, results, failures)

	post := func() *httptest.ResponseRecorder {
		body := strings.NewReader(`{"public_token":"public-sandbox-1"}`)
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
	default:
		t.Error("the first post recorded no failure")
	}
	drained(t, results)
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
