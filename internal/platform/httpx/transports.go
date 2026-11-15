package httpx

import (
	"net/http"
	"sync"
)

// Transports keeps one transport per named connection, so its calls reuse sockets instead of each opening a
// connection (and a TLS handshake) and leaving it idle on the far side.
type Transports struct {
	mu     sync.Mutex
	byName map[string]pooled
}

type pooled struct {
	fingerprint string
	transport   *http.Transport
}

// For answers the connection's transport. fingerprint is everything the transport captured (guard hosts, proxy): when
// it changes the old transport's idle sockets are closed and build makes a new one.
func (t *Transports) For(name, fingerprint string, build func() *http.Transport) *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	if current, ok := t.byName[name]; ok && current.fingerprint == fingerprint {
		return current.transport
	}
	if current, ok := t.byName[name]; ok {
		current.transport.CloseIdleConnections()
	}
	if t.byName == nil {
		t.byName = map[string]pooled{}
	}
	built := build()
	t.byName[name] = pooled{fingerprint: fingerprint, transport: built}
	return built
}
