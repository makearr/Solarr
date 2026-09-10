package httpx_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// roundTripper is the fake provider. ADR-17 requires the fakes to model failure,
// so this one returns statuses and transport errors on demand.
type roundTripper struct {
	responses []*http.Response
	errs      []error
	calls     int
	requests  []*http.Request
	// build, when set, answers every call instead of responses -- for a test
	// that needs the response body to see the real per-request context Do
	// constructs (request.Context()), not a canned *http.Response built
	// before that context exists.
	build func(*http.Request) *http.Response
}

func (r *roundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	index := min(r.calls, len(r.responses)-1)
	r.calls++
	r.requests = append(r.requests, request)

	if r.build != nil {
		return r.build(request), nil
	}
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

// Invariant 65: a proxied request's own destination is still validated, even
// though DialContext never sees it -- proven by the transport never firing.
func TestClient_ProxiedRequestStillChecksItsOwnDestination(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(fakeResolver{"internal-service.invalid": {mustParseAddr(t, "192.168.1.50")}}, refusingDial, nil)
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 600}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 2, Cooldown: time.Minute})
	transport := &roundTripper{responses: []*http.Response{respond(http.StatusOK, nil)}}

	proxyURL := mustParseURL(t, "socks5://10.0.0.5:1080")
	client := httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: "indexer", Transport: transport,
		Proxy: func() httpx.ProxyPolicy { return httpx.ProxyPolicy{URL: proxyURL} },
	})

	_, err := client.Get(t.Context(), "https://internal-service.invalid/api")
	if !errors.Is(err, httpx.ErrBlockedAddress) {
		t.Fatalf("Get = %v, want ErrBlockedAddress", err)
	}
	if transport.calls != 0 {
		t.Errorf("the fake transport was called %d times, want 0 -- the destination check must run before the round trip", transport.calls)
	}
}

func mustParseAddr(t *testing.T, raw string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		t.Fatalf("netip.ParseAddr(%q): %v", raw, err)
	}
	return addr
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

// ctxAwareBody stands in for what net/http's own response body really does:
// a Read after its request's context is cancelled fails with that context's
// own error, exactly like a real connection's body reader watches for
// cancellation. respond()'s own strings.Reader body does not model this at
// all, which is why this bug (found live against a real MusicBrainz
// response, todo 4.17's Discover search) had nothing to catch it before.
type ctxAwareBody struct {
	ctx    context.Context
	data   string
	served bool
}

func (b *ctxAwareBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.served {
		return 0, io.EOF
	}
	b.served = true
	return copy(p, b.data), nil
}

func (b *ctxAwareBody) Close() error { return nil }

// Do must not cancel its own request-scoped timeout until the caller is done
// with the response body -- a caller always reads the body *after* Do
// returns (getJSON's own shape: `response, err := client.Get(...)` then
// `io.ReadAll(response.Body)`), so cancelling on Do's own return raced that
// read against the cancellation signal. Mutation-tested: reverting the fix
// (restoring a bare `defer cancel()` right after the timeout is created)
// fails this test with "context canceled" -- exactly the live failure.
func TestClient_ResponseBodyStaysReadableAfterDoReturns(t *testing.T) {
	// The body is built once RoundTrip sees the real (per-request) context Do
	// constructs, not the caller's own outer one -- WithContext on the
	// request is what carries it, so the body has to be built inside build.
	transport := &roundTripper{build: func(request *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: &ctxAwareBody{ctx: request.Context(), data: "released"},
		}
	}}
	client, _, _, _ := newTestClient(t, transport)

	response, err := client.Get(t.Context(), "https://indexer.invalid/api")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body after Do returned: %v, want no error", err)
	}
	if string(body) != "released" {
		t.Errorf("body = %q, want %q", body, "released")
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
