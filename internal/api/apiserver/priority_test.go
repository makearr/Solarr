package apiserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
)

type prioritySettingsBody struct {
	UserRequestWeight int64 `json:"user_request_weight"`
	NewReleaseWeight  int64 `json:"new_release_weight"`
	GapWeight         int64 `json:"gap_weight"`
	UpgradeWeight     int64 `json:"upgrade_weight"`
	NewReleaseDays    int64 `json:"new_release_days"`
}

func TestGetPrioritySettings_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/priority", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestGetPrioritySettings_DefaultsToTheShippedWeights(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/priority"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body prioritySettingsBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := prioritySettingsBody{
		UserRequestWeight: settings.DefaultPriorityUserRequestWeight, NewReleaseWeight: settings.DefaultPriorityNewReleaseWeight,
		GapWeight: settings.DefaultPriorityGapWeight, UpgradeWeight: settings.DefaultPriorityUpgradeWeight,
		NewReleaseDays: settings.DefaultPriorityNewReleaseDays,
	}
	if body != want {
		t.Errorf("body = %+v, want the shipped defaults %+v", body, want)
	}
}

func TestUpdatePrioritySettings_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	request := authedPatch(t, cookies, "/api/v1/priority",
		`{"user_request_weight":40,"new_release_weight":30,"gap_weight":20,"upgrade_weight":10,"new_release_days":7}`)
	recorder := fixture.do(request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestUpdatePrioritySettings_RoundTrips(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	request := authedPatch(t, cookies, "/api/v1/priority",
		`{"user_request_weight":50,"new_release_weight":35,"gap_weight":15,"upgrade_weight":5,"new_release_days":14}`)
	recorder := fixture.do(request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body prioritySettingsBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := prioritySettingsBody{UserRequestWeight: 50, NewReleaseWeight: 35, GapWeight: 15, UpgradeWeight: 5, NewReleaseDays: 14}
	if body != want {
		t.Errorf("body = %+v, want %+v", body, want)
	}

	// A fresh read confirms it was actually persisted, not just echoed back.
	recorder = fixture.do(authedGet(t, cookies, "/api/v1/priority"))
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body != want {
		t.Errorf("body after re-read = %+v, want %+v", body, want)
	}
}

func TestUpdatePrioritySettings_RejectsANegativeWeight(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	request := authedPatch(t, cookies, "/api/v1/priority",
		`{"user_request_weight":-1,"new_release_weight":30,"gap_weight":20,"upgrade_weight":10,"new_release_days":7}`)
	recorder := fixture.do(request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
}

// ADR-26 (todo 4.14): "a setting not in the registry cannot be written." An
// empty registry stands in for a write path that forgot to register its own
// key -- the mutation the Registry.LookupAll check in UpdatePrioritySettings
// guards against.
func TestUpdatePrioritySettings_RefusesWhenTheKeyIsNotRegistered(t *testing.T) {
	fixture := newWorksFixtureWithRegistry(t, settings.NewRegistry())
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	request := authedPatch(t, cookies, "/api/v1/priority",
		`{"user_request_weight":40,"new_release_weight":30,"gap_weight":20,"upgrade_weight":10,"new_release_days":7}`)
	recorder := fixture.do(request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s, want 500", recorder.Code, recorder.Body)
	}
	if got := fixture.cache.Snapshot().PrioritySettings().NewReleaseDays; got != settings.DefaultPriorityNewReleaseDays {
		t.Errorf("NewReleaseDays = %d, want unchanged (the default)", got)
	}
}
