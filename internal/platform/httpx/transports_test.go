package httpx_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestTransports_ReuseOneUntilItsFingerprintChanges(t *testing.T) {
	var pool httpx.Transports
	builds := 0
	build := func() *http.Transport { builds++; return &http.Transport{} }

	first := pool.For("downloader:1", "sab.lan", build)
	if again := pool.For("downloader:1", "sab.lan", build); again != first || builds != 1 {
		t.Errorf("same connection: %d builds, same transport %v; want 1 build and the same transport", builds, again == first)
	}
	if other := pool.For("downloader:2", "sab.lan", build); other == first || builds != 2 {
		t.Errorf("another connection shared a transport (%d builds)", builds)
	}
	if moved := pool.For("downloader:1", "sab2.lan", build); moved == first || builds != 3 {
		t.Errorf("a changed address kept its transport (%d builds)", builds)
	}
}

// Replacing a transport closes the sockets the old one left idle on the far side.
func TestTransports_AChangedFingerprintClosesTheOldIdleSockets(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	server.Start()
	defer server.Close()

	var pool httpx.Transports
	old := pool.For("downloader:1", "a", func() *http.Transport { return &http.Transport{} })
	response, err := (&http.Client{Transport: old}).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	pool.For("downloader:1", "b", func() *http.Transport { return &http.Transport{} })
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the old transport's idle socket stayed open")
	}
}
