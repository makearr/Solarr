package apiserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestVideoHashing_IsOnUntilAnAdministratorTurnsItOff(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	admin, listener := fixture.signIn(t, "admin"), fixture.signIn(t, "listener")

	if recorder := fixture.do(authedGet(t, listener, "/api/v1/settings/video-hashing")); !strings.Contains(recorder.Body.String(), `"enabled":true`) {
		t.Errorf("default: %s, want enabled", recorder.Body)
	}
	put := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/video-hashing", strings.NewReader(`{"enabled":false}`))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		return fixture.do(request)
	}
	if recorder := put(listener); recorder.Code != http.StatusForbidden {
		t.Errorf("listener PUT: status %d, want 403", recorder.Code)
	}
	if recorder := put(admin); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"enabled":false`) {
		t.Errorf("admin PUT: status %d, body %s, want disabled", recorder.Code, recorder.Body)
	}
	if recorder := fixture.do(authedGet(t, admin, "/api/v1/settings/video-hashing")); !strings.Contains(recorder.Body.String(), `"enabled":false`) {
		t.Errorf("after PUT: %s, want disabled", recorder.Body)
	}
}
