package httpx

import (
	"io"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// Meter is told how long a provider takes to answer and how fast its bodies arrive; it never causes a request.
type Meter interface {
	// Answered is the time from sending a request to the first response header.
	Answered(provider string, d time.Duration)
	// Received is body bytes read and the time the reads took.
	Received(provider string, bytes int64, d time.Duration)
}

type meteredTransport struct {
	inner    http.RoundTripper
	meter    Meter
	provider string
	clock    clock.Clock
}

func (t meteredTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	started := t.clock.Now()
	response, err := t.inner.RoundTrip(request)
	if err != nil {
		return response, err
	}
	t.meter.Answered(t.provider, t.clock.Now().Sub(started))
	response.Body = &meteredBody{ReadCloser: response.Body, t: t}
	return response, nil
}

type meteredBody struct {
	io.ReadCloser
	t meteredTransport
}

func (b *meteredBody) Read(p []byte) (int, error) {
	started := b.t.clock.Now()
	n, err := b.ReadCloser.Read(p)
	b.t.meter.Received(b.t.provider, int64(n), b.t.clock.Now().Sub(started))
	return n, err
}

// CloseIdleConnections keeps the wrapped transport's pool closable through the client.
func (t meteredTransport) CloseIdleConnections() {
	if closer, ok := t.inner.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
