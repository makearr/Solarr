package httpx_test

import (
	"net/http"
	"testing"

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
