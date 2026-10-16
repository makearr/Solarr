package apiserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakeReleaseFilters struct{ held []gen.ReleaseFilter }

func (f *fakeReleaseFilters) List(context.Context, int64) ([]gen.ReleaseFilter, error) {
	return f.held, nil
}

func (f *fakeReleaseFilters) Replace(_ context.Context, _ int64, filters []gen.ReleaseFilter) error {
	f.held = filters
	return nil
}

func TestReleaseFilters_AdministratorsReplaceAndReadThemBack(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPut(t, cookies, "/api/v1/works/5/release-filters", `[{"kind":"group_deny","value":"BAD"}]`))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"group_deny"`) || len(fixture.releaseFilters.held) != 1 {
		t.Fatalf("PUT = %d %s, held %+v", recorder.Code, recorder.Body, fixture.releaseFilters.held)
	}
	if recorder = fixture.do(authedGet(t, cookies, "/api/v1/works/5/release-filters")); !strings.Contains(recorder.Body.String(), `"BAD"`) {
		t.Errorf("GET = %d %s, want the rule back", recorder.Code, recorder.Body)
	}
}

func TestReleaseFilters_ARegularUserIsRefused(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPut(t, cookies, "/api/v1/works/5/release-filters", `[]`))
	if recorder.Code != http.StatusForbidden || fixture.releaseFilters.held != nil {
		t.Errorf("PUT = %d, held %+v; want 403 and nothing stored", recorder.Code, fixture.releaseFilters.held)
	}
}
