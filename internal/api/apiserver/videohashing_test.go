package apiserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestVideoHashing_IsOffUntilAnAdministratorTurnsItOn(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	admin, listener := fixture.signIn(t, "admin"), fixture.signIn(t, "listener")

	if recorder := fixture.do(authedGet(t, listener, "/api/v1/settings/video-hashing")); !strings.Contains(recorder.Body.String(), `"enabled":false`) {
		t.Errorf("default: %s, want disabled", recorder.Body)
	}
	put := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/video-hashing", strings.NewReader(`{"enabled":true}`))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		return fixture.do(request)
	}
	if recorder := put(listener); recorder.Code != http.StatusForbidden {
		t.Errorf("listener PUT: status %d, want 403", recorder.Code)
	}
	if recorder := put(admin); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"enabled":true`) {
		t.Errorf("admin PUT: status %d, body %s, want enabled", recorder.Code, recorder.Body)
	}
	if recorder := fixture.do(authedGet(t, admin, "/api/v1/settings/video-hashing")); !strings.Contains(recorder.Body.String(), `"enabled":true`) {
		t.Errorf("after PUT: %s, want enabled", recorder.Body)
	}
}

func TestImportDuplicates_CheckIsOnAndBlocklistOffUntilAnAdministratorChangesThem(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	admin, listener := fixture.signIn(t, "admin"), fixture.signIn(t, "listener")

	if recorder := fixture.do(authedGet(t, listener, "/api/v1/settings/import-duplicates")); !strings.Contains(recorder.Body.String(), `"check":true`) ||
		!strings.Contains(recorder.Body.String(), `"blocklist":false`) {
		t.Errorf("default: %s, want the check on and the blocklist off", recorder.Body)
	}
	put := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/import-duplicates", strings.NewReader(`{"check":true,"blocklist":true}`))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		return fixture.do(request)
	}
	if recorder := put(listener); recorder.Code != http.StatusForbidden {
		t.Errorf("listener PUT: status %d, want 403", recorder.Code)
	}
	if recorder := put(admin); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"blocklist":true`) {
		t.Errorf("admin PUT: status %d, body %s, want the blocklist on", recorder.Code, recorder.Body)
	}
}
