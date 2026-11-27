package httpx

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type meterTally struct {
	answered []string
	bytes    int64
}

func (m *meterTally) Answered(provider string, _ time.Duration) {
	m.answered = append(m.answered, provider)
}
func (m *meterTally) Received(_ string, n int64, _ time.Duration) { m.bytes += n }

func TestMeteredTransportReportsAnswerAndBody(t *testing.T) {
	tally := &meterTally{}
	transport := meteredTransport{
		inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("abcdef"))}, nil
		}),
		meter: tally, provider: "tmdb", clock: clock.NewFakeAtEpoch(),
	}
	request, _ := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if len(tally.answered) != 1 || tally.answered[0] != "tmdb" || tally.bytes != 6 {
		t.Fatalf("answered %v, bytes %d", tally.answered, tally.bytes)
	}
}
