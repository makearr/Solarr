package musicbrainz_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// fakeTransport answers canned responses keyed by the exact URL requested, so a
// test can assert both bodies and which URLs were actually asked for.
type fakeTransport struct {
	responses map[string]fakeResponse
	requested []string
}

type fakeResponse struct {
	status int
	body   string
	err    error
	// location is the Location header of a redirect.
	location string
}

func (f *fakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	url := request.URL.String()
	f.requested = append(f.requested, url)

	response, known := f.responses[url]
	if !known {
		return nil, errors.New("fake: no response configured for " + url)
	}
	if response.err != nil {
		return nil, response.err
	}
	header := make(http.Header)
	if response.location != "" {
		header.Set("Location", response.location)
	}
	return &http.Response{
		StatusCode: response.status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(response.body)),
		Request:    request,
	}, nil
}

func newTestClient(t *testing.T, transport http.RoundTripper, provider string) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: provider, UserAgent: "test", Transport: transport,
	})
}
