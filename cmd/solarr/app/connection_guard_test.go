package app

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// A connection on the LAN reaches its own host and nothing else private.
func TestConnectionGuard_AllowsOnlyTheConfiguredHost(t *testing.T) {
	guard := connectionGuard("http://10.0.1.133:9696/prowlarr")
	if err := guard.CheckDestination(t.Context(), "10.0.1.133"); err != nil {
		t.Errorf("configured host refused: %v", err)
	}
	if err := guard.CheckDestination(t.Context(), "10.0.1.1"); !errors.Is(err, httpx.ErrBlockedAddress) {
		t.Errorf("another private host = %v, want ErrBlockedAddress", err)
	}
}
