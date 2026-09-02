package httpx_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/httpx"
)

// roundTripper is the fake provider. ADR-17 requires the fakes to model failure,
// so this one returns statuses and transport errors on demand.
type roundTripper struct {
	responses []*http.Response
	errs      []error
	calls     int
	requests  []*http.Request
}

func (r *roundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	index := min(r.calls, len(r.responses)-1)
	r.calls++
	r.requests = append(r.requests, request)

	if index < len(r.errs) && r.errs[index] != nil {
		return nil, r.errs[index]
	}
	if index < 0 || index >= len(r.responses) {
		return nil, errors.New("fake: no response configured")
	}
	return r.responses[index], nil
}

func respond(status int, headers map[string]string) *http.Response {
	response := &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("body")),
	}
	for key, value := range headers {
		response.Header.Set(key, value)
	}
	return response
}

func newTestClient(t *testing.T, transport http.RoundTripper) (*httpx.Client, *httpx.Limiter, *httpx.Breaker, *clock.Fake) {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(fakeResolver{}, refusingDial, nil)
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 600}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 2, Cooldown: time.Minute})

	client := httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider:  "indexer",
		UserAgent: httpx.UserAgent("1.2.3"),
		Transport: transport,
	})
	return client, limiter, breaker, fake
}

func TestClient_SendsTheIdentifyingUserAgent(t *testing.T) {
	transport := &roundTripper{responses: []*http.Response{respond(http.StatusOK, nil)}}
	client, _, _, _ := newTestClient(t, transport)

	response, err := client.Get(t.Context(), "https://indexer.invalid/api")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = response.Body.Close()

	// MusicBrainz blocks generic agents, so this is functional (ADR-29).
	got := transport.requests[0].Header.Get("User-Agent")
	if want := "MediaNexus/1.2.3 ( https://github.com/makearr/MediaNexus )"; got != want {
		t.Errorf("User-Agent = %q, want %q", got, want)
	}
}

func TestClient_RejectsANonHTTPSchemeBeforeAnythingElse(t *testing.T) {
	transport := &roundTripper{responses: []*http.Response{respond(http.StatusOK, nil)}}
	client, _, _, _ := newTestClient(t, transport)

	if _, err := client.Get(t.Context(), "file:///etc/passwd"); !errors.Is(err, httpx.ErrBlockedScheme) {
		t.Fatalf("Get(file://) = %v, want ErrBlockedScheme", err)
	}
	if transport.calls != 0 {
		t.Errorf("the transport was reached %d times for a blocked scheme", transport.calls)
	}
}

func TestClient_TerminalStatusDoesNotOpenTheBreaker(t *testing.T) {
	transport := &roundTripper{responses: []*http.Response{respond(http.StatusNotFound, nil)}}
	client, _, breaker, _ := newTestClient(t, transport)

	for range 5 {
		_, err := client.Get(t.Context(), "https://indexer.invalid/missing")
		var status *httpx.StatusError
		if !errors.As(err, &status) {
			t.Fatalf("Get = %v, want a StatusError", err)
		}
		if status.Class != httpx.ClassTerminal {
			t.Errorf("Class = %q, want terminal", status.Class)
		}
		if status.Retryable() {
			t.Error("a 404 reported itself as retryable")
		}
	}

	// A 404 says our request was wrong, not that the indexer is unhealthy.
	if got := breaker.State("indexer").State; got != httpx.BreakerClosed {
		t.Errorf("breaker = %q after five 404s, want closed", got)
	}
}

func TestClient_RetryableStatusOpensTheBreaker(t *testing.T) {
	transport := &roundTripper{responses: []*http.Response{respond(http.StatusBadGateway, nil)}}
	client, _, breaker, fake := newTestClient(t, transport)

	for attempt := range 2 {
		if _, err := client.Get(t.Context(), "https://indexer.invalid/api"); err == nil {
			t.Fatalf("attempt %d on a 502 returned no error", attempt)
		}
		// A retryable failure also pauses the provider in the limiter, so the breaker's threshold is
		// only reached once that backoff has elapsed.
		fake.Advance(5 * time.Second)
	}

	if got := breaker.State("indexer").State; got != httpx.BreakerOpen {
		t.Fatalf("breaker = %q after two 502s with a threshold of 2, want open", got)
	}
	if _, err := client.Get(t.Context(), "https://indexer.invalid/api"); !errors.Is(err, httpx.ErrCircuitOpen) {
		t.Errorf("Get with the circuit open = %v, want ErrCircuitOpen", err)
	}
	if transport.calls != 2 {
		t.Errorf("the transport was called %d times; an open circuit must skip the provider entirely", transport.calls)
	}
}

// The breaker is consulted before the rate limiter, so a skipped provider costs nothing: no
// rate token is spent and no backoff is waited out (§11.3).
func TestClient_OpenCircuitReturnsWithoutWaitingOutTheBackoff(t *testing.T) {
	transport := &roundTripper{responses: []*http.Response{respond(http.StatusBadGateway, nil)}}
	client, limiter, _, fake := newTestClient(t, transport)

	for range 2 {
		_, _ = client.Get(t.Context(), "https://indexer.invalid/api")
		fake.Advance(5 * time.Second)
	}
	// Leave the limiter paused, so waiting on it would block indefinitely here.
	limiter.Observe("indexer", httpx.Outcome{Success: false, RetryAfter: time.Hour})

	done := make(chan error, 1)
	go func() {
		_, err := client.Get(t.Context(), "https://indexer.invalid/api")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, httpx.ErrCircuitOpen) {
			t.Errorf("Get = %v, want ErrCircuitOpen", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Get blocked on the limiter despite an open circuit")
	}
}

func TestClient_RetryAfterReachesTheLimiter(t *testing.T) {
	transport := &roundTripper{responses: []*http.Response{
		respond(http.StatusTooManyRequests, map[string]string{"Retry-After": "120"}),
	}}
	client, limiter, _, fake := newTestClient(t, transport)

	_, err := client.Get(t.Context(), "https://indexer.invalid/api")
	var status *httpx.StatusError
	if !errors.As(err, &status) {
		t.Fatalf("Get = %v, want a StatusError", err)
	}
	if status.RetryAfter != 2*time.Minute {
		t.Errorf("StatusError.RetryAfter = %v, want 2m", status.RetryAfter)
	}
	if want := fake.Now().Add(2 * time.Minute); !limiter.State("indexer").PausedUntil.Equal(want) {
		t.Errorf("limiter PausedUntil = %v, want %v", limiter.State("indexer").PausedUntil, want)
	}
}

func TestClient_TransportErrorCountsAgainstTheProvider(t *testing.T) {
	transport := &roundTripper{
		responses: []*http.Response{nil},
		errs:      []error{errors.New("connection reset")},
	}
	client, _, breaker, fake := newTestClient(t, transport)

	for range 2 {
		if _, err := client.Get(t.Context(), "https://indexer.invalid/api"); err == nil {
			t.Fatal("Get returned no error on a transport failure")
		}
		fake.Advance(5 * time.Second) // past the limiter's backoff, inside the breaker's cooldown
	}
	if got := breaker.State("indexer").State; got != httpx.BreakerOpen {
		t.Errorf("breaker = %q after two transport errors, want open", got)
	}
}

// A cancelled shutdown must not look like a provider outage, or every circuit
// opens on the way down and the next start is degraded for no reason.
func TestClient_CancellationIsNotEvidenceAgainstTheProvider(t *testing.T) {
	transport := &roundTripper{responses: []*http.Response{respond(http.StatusOK, nil)}}
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(fakeResolver{}, refusingDial, nil)
	limiter := httpx.NewLimiter(fake, httpx.Config{
		Defaults: httpx.Limits{RequestsPerMinute: 1, MinInterval: time.Hour},
	}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 1, Cooldown: time.Minute})
	client := httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: "indexer", Transport: transport,
	})

	first, err := client.Get(t.Context(), "https://indexer.invalid/api")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	_ = first.Body.Close()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := client.Get(ctx, "https://indexer.invalid/api")
		done <- err
	}()

	fake.BlockUntilWaiters(1)
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Get = %v, want context.Canceled", err)
	}
	if got := breaker.State("indexer").State; got != httpx.BreakerClosed {
		t.Errorf("breaker = %q after a cancellation, want closed", got)
	}
}

func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		status int
		want   httpx.ResponseClass
	}{
		{200, httpx.ClassSuccess},
		{201, httpx.ClassSuccess},
		{204, httpx.ClassSuccess},
		{301, httpx.ClassTerminal},
		{400, httpx.ClassTerminal},
		{401, httpx.ClassTerminal},
		{403, httpx.ClassTerminal},
		{404, httpx.ClassTerminal},
		{408, httpx.ClassRetryable},
		{429, httpx.ClassRetryable},
		{500, httpx.ClassRetryable},
		{502, httpx.ClassRetryable},
		{503, httpx.ClassRetryable},
	}

	for _, testCase := range cases {
		if got := httpx.ClassifyStatus(testCase.status); got != testCase.want {
			t.Errorf("ClassifyStatus(%d) = %q, want %q", testCase.status, got, testCase.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"120", 2 * time.Minute},
		{"0", 0},
		{"-5", 0},
		{"nonsense", 0},
		{"Sun, 01 Mar 2026 12:05:00 GMT", 5 * time.Minute},
		{"Sun, 01 Mar 2026 11:00:00 GMT", 0}, // already past
	}

	for _, testCase := range cases {
		if got := httpx.ParseRetryAfter(testCase.header, now); got != testCase.want {
			t.Errorf("ParseRetryAfter(%q) = %v, want %v", testCase.header, got, testCase.want)
		}
	}
}
