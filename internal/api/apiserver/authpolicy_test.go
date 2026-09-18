package apiserver_test

import (
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

// ADR-26 (todo 4.14): "a setting not in the registry cannot be written." An
// empty registry stands in for a write path that forgot to register its own
// key -- the mutation the Registry.LookupAll check in SetAuthPolicy guards
// against.
func TestSetAuthPolicy_RefusesWhenTheKeyIsNotRegistered(t *testing.T) {
	fixture := newWorksFixtureWithRegistry(t, settings.NewRegistry())
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/auth", `{"auth_required":false}`))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s, want 500", recorder.Code, recorder.Body)
	}
	if got := fixture.cache.Snapshot().AuthRequired(); !got {
		t.Errorf("AuthRequired = %v, want unchanged (true, nothing should have been written)", got)
	}
}
