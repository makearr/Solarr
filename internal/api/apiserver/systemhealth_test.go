package apiserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

type healthCheckBody struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Link     string `json:"link"`
}

func systemHealth(t *testing.T, fixture worksFixture, cookies []*http.Cookie) []healthCheckBody {
	t.Helper()
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/system/health"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var checks []healthCheckBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &checks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return checks
}

func hasCheck(checks []healthCheckBody, id string) bool {
	for _, check := range checks {
		if check.ID == id {
			return true
		}
	}
	return false
}

func TestGetSystemHealth_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

// An instance with no library at all cannot do anything, so that is an error
// rather than a warning.
func TestGetSystemHealth_ReportsNoLibrary(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	checks := systemHealth(t, fixture, cookies)
	if !hasCheck(checks, "no_library") {
		t.Fatalf("checks = %+v, want no_library", checks)
	}
	for _, check := range checks {
		if check.ID == "no_library" && check.Severity != "error" {
			t.Errorf("no_library severity = %q, want error", check.Severity)
		}
	}
}

// A library with a root folder attached is the healthy case: neither the
// missing-library nor the missing-root check fires.
func TestGetSystemHealth_SilentOnceALibraryHasARoot(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	checks := systemHealth(t, fixture, cookies)
	if hasCheck(checks, "no_library") || hasCheck(checks, "category_without_root") {
		t.Errorf("checks = %+v, want neither no_library nor category_without_root", checks)
	}
}

// Errors sort before warnings, so the worst thing is the first thing read.
func TestGetSystemHealth_ReportsErrorsBeforeWarnings(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	checks := systemHealth(t, fixture, cookies)
	seenWarning := false
	for _, check := range checks {
		if check.Severity == "warning" {
			seenWarning = true
			continue
		}
		if seenWarning {
			t.Fatalf("an error follows a warning in %+v", checks)
		}
	}
}

// Every link must name a route the interface actually has, or "Fix" leads
// nowhere. These are the paths web/src/app/router.tsx declares.
func TestGetSystemHealth_LinksPointAtRealRoutes(t *testing.T) {
	known := map[string]bool{
		"/libraries": true, "/storage/folders": true,
		"/connections/indexers": true, "/connections/downloaders": true,
	}
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	for _, check := range systemHealth(t, fixture, cookies) {
		if check.Link != "" && !known[check.Link] {
			t.Errorf("check %q links to %q, which is not a declared route", check.ID, check.Link)
		}
	}
}
