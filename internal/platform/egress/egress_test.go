package egress

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// echo answers every request with one address and counts the requests.
type echo struct{ asked *int }

func (e echo) RoundTrip(*http.Request) (*http.Response, error) {
	*e.asked++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("203.0.113.7\n")), Header: http.Header{}}, nil
}

// A check that is not enabled stays silent until the status bar asks, then asks at most once per interval.
func TestDemandChecksADisabledProberOncePerInterval(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	asked := 0
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "egress-test", UserAgent: "test", Transport: echo{asked: &asked}})
	prober := New(Config{Endpoint: "https://echo.test", Interval: time.Hour, Client: client, Clock: fake})

	prober.Run(context.Background())
	if asked != 0 {
		t.Fatalf("Run on a disabled prober asked %d times, want 0", asked)
	}

	first := prober.Demand(t.Context())
	want := Status{Enabled: true, PublicIP: "203.0.113.7", Source: "https://echo.test", CheckedAt: fake.Now()}
	if first != want || asked != 1 {
		t.Fatalf("first Demand = %+v after %d asks, want %+v after 1", first, asked, want)
	}

	fake.Advance(59 * time.Minute)
	if again := prober.Demand(t.Context()); again != want || asked != 1 {
		t.Fatalf("Demand within the interval = %+v after %d asks, want the cached %+v after 1", again, asked, want)
	}

	fake.Advance(time.Minute)
	want.CheckedAt = fake.Now()
	if later := prober.Demand(t.Context()); later != want || asked != 2 {
		t.Fatalf("Demand after the interval = %+v after %d asks, want %+v after 2", later, asked, want)
	}
}
