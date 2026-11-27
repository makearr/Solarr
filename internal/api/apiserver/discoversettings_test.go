package apiserver_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

func TestDiscoverSettings_AreAdministratorOnly(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	for _, request := range []*http.Request{
		authedGet(t, cookies, "/api/v1/settings/discover"),
		authedPut(t, cookies, "/api/v1/settings/discover", `{"enabled":true,"exclude_genres":[],"exclude_networks":[]}`),
		authedPut(t, cookies, "/api/v1/settings/discover/libraries/1", `{}`),
	} {
		if recorder := fixture.do(request); recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", request.Method, request.URL.Path, recorder.Code)
		}
	}
}

func TestDiscoverSettings_SwitchLibraryAndGeneralShareOneState(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	movies, err := fixture.cache.CreateCategory(t.Context(), "movie", "Movies", 10)
	if err != nil {
		t.Fatal(err)
	}

	read := func() gen.DiscoverSettings {
		recorder := fixture.do(authedGet(t, cookies, "/api/v1/settings/discover"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("get = %d, %s", recorder.Code, recorder.Body)
		}
		var body gen.DiscoverSettings
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	if before := read(); before.Global.Enabled || len(before.Libraries) != 1 || before.Libraries[0].Enabled || before.Libraries[0].TotalAmount != 60 || len(before.Libraries[0].Types) != 5 {
		t.Fatalf("defaults = %+v", before)
	}
	if recorder := fixture.do(authedPatch(t, cookies, "/api/v1/settings/general", `{"recommendations":true}`)); recorder.Code != http.StatusOK {
		t.Fatalf("patch general = %d, %s", recorder.Code, recorder.Body)
	}
	if !read().Global.Enabled {
		t.Error("the General switch did not reach Discover")
	}
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/discover", `{"enabled":true,"exclude_genres":["Reality"],"exclude_networks":[],"classic_age_years":30,"min_rating":7.5,"min_votes":500,"auto_add_per_day":3,"auto_add_search":false}`)); recorder.Code != http.StatusOK {
		t.Fatalf("put = %d, %s", recorder.Code, recorder.Body)
	}

	body := read().Libraries[0]
	body.Enabled, body.TotalAmount, body.ExcludeGenres = true, 30, []string{"Talk"}
	body.Types[1].Enabled = false
	raw, _ := json.Marshal(body)
	path := "/api/v1/settings/discover/libraries/" + itoa(movies.ID)
	if recorder := fixture.do(authedPut(t, cookies, path, string(raw))); recorder.Code != http.StatusOK {
		t.Fatalf("put library = %d, %s", recorder.Code, recorder.Body)
	}
	after := read()
	if got := after.Global.ExcludeGenres; !reflect.DeepEqual(got, []string{"Reality"}) {
		t.Errorf("global genres = %v", got)
	}
	if got := after.Libraries[0]; !got.Enabled || got.TotalAmount != 30 || !reflect.DeepEqual(got.ExcludeGenres, []string{"Talk"}) || got.Types[1].Enabled {
		t.Errorf("library = %+v", got)
	}

	body.TotalAmount = 0
	raw, _ = json.Marshal(body)
	if recorder := fixture.do(authedPut(t, cookies, path, string(raw))); recorder.Code != http.StatusBadRequest {
		t.Errorf("a total of 0 = %d, want 400", recorder.Code)
	}
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/discover/libraries/9999", string(raw))); recorder.Code != http.StatusNotFound {
		t.Errorf("unknown library = %d, want 404", recorder.Code)
	}
}
