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
)

// fakeServer stands in for *http.Server. Shutdown runs onShutdown, which lets a
// test model a handler that is still running when the server stops.
type fakeServer struct {
	mu         sync.Mutex
	calls      int
	onShutdown func()
}

func (f *fakeServer) Shutdown(ctx context.Context) error {
	f.mu.Lock()
	f.calls++
	run := f.onShutdown
	f.mu.Unlock()
	if run != nil {
		run()
	}
	return nil
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

// The same for /exchange. Its send path is driven through the failure branch,
// which needs no Plaid client.
func TestExchangeHandlerDropsADuplicateFailure(t *testing.T) {
	failures := make(chan error, 1)
	failures <- errors.New("an earlier failure")

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
