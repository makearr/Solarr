package apiserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/settings"
)

func authedPatch(t *testing.T, cookies []*http.Cookie, target, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPatch, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	return request
}

// ADR-53's own 18 shipped defaults are seeded by the settings.db migration
// itself, so any fixture with a real settings.db (every worksFixture) already
// has them -- this is what the list endpoint must surface.
func TestListExclusionPatterns_IncludesTheShippedDefaults(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/exclusions"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body []struct {
		Pattern string `json:"pattern"`
		Builtin bool   `json:"builtin"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 18 {
		t.Fatalf("len(patterns) = %d, want 18 shipped defaults", len(body))
	}
	for _, p := range body {
		if !p.Builtin {
			t.Errorf("pattern %q is not marked builtin", p.Pattern)
		}
	}
}

func TestListExclusionPatterns_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)

	recorder := fixture.do(authedGet(t, nil, "/api/v1/exclusions"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for no credential", recorder.Code)
	}
}

func TestCreateExclusionPattern_AddsAPatternAdministratorsCanSeeImmediately(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/exclusions",
		`{"pattern":"*.rar","action":"ignore","note":"an archive"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var created struct {
		ID      int64  `json:"id"`
		Pattern string `json:"pattern"`
		Builtin bool   `json:"builtin"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Pattern != "*.rar" || created.Builtin || !created.Enabled {
		t.Errorf("created = %+v, want pattern=*.rar builtin=false enabled=true", created)
	}

	found, ok := fixture.cache.Snapshot().ExclusionPattern(created.ID)
	if !ok || found.Note != "an archive" {
		t.Errorf("stored pattern = %+v, want Note=\"an archive\"", found)
	}
}

func TestCreateExclusionPattern_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/exclusions",
		`{"pattern":"*.rar","action":"ignore"}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

func TestCreateExclusionPattern_InvalidActionAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/exclusions",
		`{"pattern":"*.rar","action":"delete"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid action, body = %s", recorder.Code, recorder.Body)
	}
}

func TestUpdateExclusionPattern_ChangesContentButNeverBuiltin(t *testing.T) {
	fixture := newWorksFixture(t)
	created, err := fixture.cache.CreateExclusionPattern(t.Context(), settings.ExclusionPattern{
		Pattern: "*.rar", Action: settings.ExclusionIgnore, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateExclusionPattern: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/exclusions/"+strconv.FormatInt(created.ID, 10),
		`{"pattern":"*.rar","action":"refuse","note":"changed my mind","enabled":false}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	found, ok := fixture.cache.Snapshot().ExclusionPattern(created.ID)
	if !ok || found.Action != settings.ExclusionRefuse || found.Enabled || found.Note != "changed my mind" {
		t.Errorf("stored pattern = %+v, want the updated fields", found)
	}
	if found.Builtin {
		t.Error("Update must never turn a pattern builtin")
	}
}

func TestUpdateExclusionPattern_MissingPatternAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/exclusions/999999",
		`{"pattern":"*.rar","action":"ignore"}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a missing pattern", recorder.Code)
	}
}

func TestDeleteExclusionPattern_RemovesEvenABuiltinOne(t *testing.T) {
	fixture := newWorksFixture(t)
	var sfvID int64
	for _, p := range fixture.cache.Snapshot().ExclusionPatterns() {
		if p.Pattern == "*.sfv" {
			sfvID = p.ID
		}
	}
	if sfvID == 0 {
		t.Fatal("*.sfv shipped default not found")
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/exclusions/"+strconv.FormatInt(sfvID, 10)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if _, ok := fixture.cache.Snapshot().ExclusionPattern(sfvID); ok {
		t.Error("builtin pattern still exists after delete -- ADR-53 requires shipped defaults stay removable")
	}
}

func TestDeleteExclusionPattern_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	created, err := fixture.cache.CreateExclusionPattern(t.Context(), settings.ExclusionPattern{
		Pattern: "*.rar", Action: settings.ExclusionIgnore, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateExclusionPattern: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/exclusions/"+strconv.FormatInt(created.ID, 10)))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}
