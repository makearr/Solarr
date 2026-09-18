package sabnzbd_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// recordingTransport answers one canned response to every request, and
// records each request it saw -- the same shape internal/acquiring/indexer's
// own tests use, duplicated here rather than exported across packages for a
// test-only type.
type recordingTransport struct {
	status    int
	body      string
	requested []*http.Request
}

func (f *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f.requested = append(f.requested, request)
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Request:    request,
	}, nil
}

// newTestClient builds an httpx.Client over transport -- a custom Transport
// bypasses the guard's own dialer entirely, the same setup internal/
// acquiring/indexer and internal/importing/arrimport's own tests use for a
// connection this package does not actually reach over the network.
func newTestClient(t *testing.T, transport http.RoundTripper) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: "sabnzbd-test", UserAgent: "test", Transport: transport,
	})
}
