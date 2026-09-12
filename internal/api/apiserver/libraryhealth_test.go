package apiserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
)

func int64Ptr(v int64) *int64 { return &v }

func TestIsNearReserve(t *testing.T) {
	cases := []struct {
		name         string
		online       bool
		freeBytes    *int64
		minFreeBytes int64
		want         bool
	}{
		{"below the 10% margin", true, int64Ptr(105), 100, true},
		{"comfortably above it", true, int64Ptr(200), 100, false},
		{"offline root never warns", false, int64Ptr(50), 100, false},
		{"no reserve configured", true, int64Ptr(1), 0, false},
		{"never measured", true, nil, 100, false},
		{"exactly at the 10% margin is not yet near", true, int64Ptr(110), 100, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := apiserver.IsNearReserveForTest(c.online, c.freeBytes, c.minFreeBytes)
			if got != c.want {
				t.Errorf("isNearReserve(online=%v, free=%v, min=%d) = %v, want %v",
					c.online, c.freeBytes, c.minFreeBytes, got, c.want)
			}
		})
	}
}

func TestGetLibraryHealth_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/library/health", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

// newWorksFixture never wires a LibraryHealth reader (todo 4.8's own
// dependency is optional, the same "absent means 503, not a panic" shape
// every other optional Server field already follows).
func TestGetLibraryHealth_UnavailableWithoutAReader(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/health"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
}
