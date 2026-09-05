package apiserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/settings"
)

// probed/unprobed come from library.ProbeCoverage's own "probed"/"total_files"
// metric pair, reconciled into the same "library" counter scope the rest of
// LibraryCounts already reads -- distinct metrics, not a collision. unprobed is
// computed here (total_files - probed) rather than stored a third time.
func TestGetDashboard_IncludesProbeCoverage(t *testing.T) {
	librarySize := fakeLibrarySize{byScope: map[string]map[string]int64{
		"library": {"works": 10, "files": 20, "monitored": 5, "missing": 1,
			"probed": 15, "total_files": 20},
	}}
	fixture := newWorksFixtureWithLibrarySize(t, librarySize)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/dashboard"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var body struct {
		Library struct {
			Probed   int `json:"probed"`
			Unprobed int `json:"unprobed"`
		} `json:"library"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Library.Probed != 15 || body.Library.Unprobed != 5 {
		t.Errorf("library = %+v, want probed=15 unprobed=5 (20 total - 15 probed)", body.Library)
	}
}

// No probe sweep has run yet: both counters are absent from the map, and
// unprobed must not go negative from the zero-value subtraction.
func TestGetDashboard_UnreconciledProbeCoverageAnswersZero(t *testing.T) {
	librarySize := fakeLibrarySize{byScope: map[string]map[string]int64{"library": {}}}
	fixture := newWorksFixtureWithLibrarySize(t, librarySize)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/dashboard"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var body struct {
		Library struct {
			Probed   int `json:"probed"`
			Unprobed int `json:"unprobed"`
		} `json:"library"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Library.Probed != 0 || body.Library.Unprobed != 0 {
		t.Errorf("library = %+v, want probed=0 unprobed=0, not an error or a negative", body.Library)
	}
}
