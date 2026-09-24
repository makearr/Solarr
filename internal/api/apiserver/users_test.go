package apiserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
)

func (f authFixture) as(t *testing.T, username, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	cookies := f.login(t, username, testPassword).Result().Cookies()
	request := httptest.NewRequest(method, target, bytes.NewReader([]byte(body)))
	request.AddCookie(cookies[0])
	return f.do(t, request)
}

// Only an administrator creates accounts (2026-09-24 review).
func TestCreateUser_RefusesANonAdministrator(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	response := fixture.as(t, "housemate", http.MethodPost, "/api/v1/users",
		`{"username":"guest","password":"pw","role":"readonly"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if _, found := fixture.cache.Snapshot().UserByUsername("guest"); found {
		t.Error("the account was created anyway")
	}
}

// Access chosen at creation is what the new account's library list shows, and its limits hold.
func TestCreateUser_TheChosenAccessIsWhatTheAccountSees(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	body := fmt.Sprintf(`{"username":"guest","password":%q,"role":"readonly",
		"access":[{"category_id":%d,"permission":"request"}],
		"limits":{"tier_default":"advanced","tier_max":"advanced","new_library_permission":"none"}}`,
		testPassword, fixture.adult.ID)
	if response := fixture.as(t, "avs", http.MethodPost, "/api/v1/users", body); response.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.Code, response.Body)
	}

	response := fixture.as(t, "guest", http.MethodGet, "/api/v1/libraries", "")
	var libraries []struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&libraries); err != nil {
		t.Fatal(err)
	}
	if want := []struct {
		ID int64 `json:"id"`
	}{{ID: fixture.adult.ID}}; !reflect.DeepEqual(libraries, want) {
		t.Errorf("libraries = %+v, want only Adult", libraries)
	}

	var me struct {
		Tier    string `json:"tier"`
		TierMax string `json:"tier_max"`
	}
	if err := json.NewDecoder(fixture.as(t, "guest", http.MethodGet, "/api/v1/me", "").Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if me.Tier != "advanced" || me.TierMax != "advanced" {
		t.Errorf("me = %+v, want advanced capped at advanced", me)
	}
	if response := fixture.as(t, "guest", http.MethodPatch, "/api/v1/me/tier", `{"tier":"expert"}`); response.Code != http.StatusBadRequest {
		t.Errorf("raising past the maximum: status %d, want 400", response.Code)
	}
}
