package apiserver_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

func TestTranscodeSettings_AreAdministratorOnly(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	for _, request := range []*http.Request{
		authedGet(t, cookies, "/api/v1/settings/transcode"),
		authedPut(t, cookies, "/api/v1/settings/transcode", `{}`),
		authedPut(t, cookies, "/api/v1/settings/transcode/libraries/1", `{}`),
		authedGet(t, cookies, "/api/v1/settings/transcode/targets/1"),
		authedPut(t, cookies, "/api/v1/settings/transcode/targets/1", `{}`),
		authedDelete(t, cookies, "/api/v1/settings/transcode/targets/1"),
	} {
		if recorder := fixture.do(request); recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", request.Method, request.URL.Path, recorder.Code)
		}
	}
}

func TestTranscodeSettings_GlobalAndLibraryRowsRoundTripAndAnUnsupportedLibraryIsRefused(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	var videoTarget, musicTarget, bookTarget int64 = 1, 35, 51
	movies, _ := fixture.cache.CreateCategory(t.Context(), "movie", "Movies", 10)
	songs, _ := fixture.cache.CreateCategory(t.Context(), "music", "Songs", 20)
	books, _ := fixture.cache.CreateCategory(t.Context(), "book", "Books", 30)
	bare, _ := fixture.cache.CreateCategory(t.Context(), "tv", "Shows", 40)
	for id, target := range map[int64]int64{movies.ID: videoTarget, songs.ID: musicTarget, books.ID: bookTarget} {
		if err := fixture.cache.SetCategoryQuality(t.Context(), id, nil, nil, &target); err != nil {
			t.Fatal(err)
		}
	}
	read := func() gen.TranscodeSettings {
		recorder := fixture.do(authedGet(t, cookies, "/api/v1/settings/transcode"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("get = %d, %s", recorder.Code, recorder.Body)
		}
		var body gen.TranscodeSettings
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	before := read()
	if before.Global.Enabled || before.Global.Parallel != (gen.TranscodeLaneLimits{Cpu: 1, Gpu: 2, Audio: 4, Books: 4}) || before.Global.OriginalsRetainDays != 7 || before.Global.OriginalsCapGb != 100 {
		t.Fatalf("defaults = %+v", before.Global)
	}
	supported := map[string]bool{}
	for _, library := range before.Libraries {
		supported[library.Name] = library.Supported
	}
	if want := map[string]bool{"Movies": true, "Songs": true, "Books": true, "Shows": false}; !reflect.DeepEqual(supported, want) {
		t.Errorf("supported = %v, want %v", supported, want)
	}

	put := `{"enabled":true,"parallel":{"cpu":2,"gpu":2,"audio":3,"books":3},"playback_parallel":{"cpu":0,"gpu":0,"audio":1,"books":1},"originals_retain_days":14,"originals_cap_gb":0}`
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/transcode", put)); recorder.Code != http.StatusOK {
		t.Fatalf("put = %d, %s", recorder.Code, recorder.Body)
	}
	if got := read().Global; !got.Enabled || got.Parallel.Cpu != 2 || got.OriginalsRetainDays != 14 || got.OriginalsCapGb != 0 {
		t.Errorf("global = %+v", got)
	}
	tooMany := `{"enabled":true,"parallel":{"cpu":17,"gpu":1,"audio":3},"playback_parallel":{"cpu":0,"gpu":0,"audio":1},"originals_retain_days":14,"originals_cap_gb":0}`
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/transcode", tooMany)); recorder.Code != http.StatusBadRequest {
		t.Errorf("17 lanes = %d, want 400", recorder.Code)
	}

	row := `{"enabled":true,"priority":5,"order":"largest"}`
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/transcode/libraries/"+itoa(movies.ID), row)); recorder.Code != http.StatusOK {
		t.Fatalf("put row = %d, %s", recorder.Code, recorder.Body)
	}
	for _, library := range read().Libraries {
		if library.Name == "Movies" && (!library.Enabled || library.Priority != 5 || library.Order != gen.TranscodeLibraryOrder("largest")) {
			t.Errorf("movies = %+v", library)
		}
	}
	for _, id := range []int64{bare.ID} {
		if recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/transcode/libraries/"+itoa(id), row)); recorder.Code != http.StatusConflict {
			t.Errorf("unsupported library %d = %d, want 409", id, recorder.Code)
		}
	}
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/transcode/libraries/9999", row)); recorder.Code != http.StatusNotFound {
		t.Errorf("unknown library = %d, want 404", recorder.Code)
	}
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/transcode/libraries/"+itoa(movies.ID), `{"enabled":true,"priority":500,"order":"found"}`)); recorder.Code != http.StatusBadRequest {
		t.Errorf("priority 500 = %d, want 400", recorder.Code)
	}
}

func TestTranscodeSettings_ASetupStartsAsTheDefaultAndSavingAndResettingRaiseItsRevision(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	path := "/api/v1/settings/transcode/targets/1"
	state := func(recorder interface {
		Result() *http.Response
	}, code int, body []byte) gen.TranscodeSetupState {
		t.Helper()
		if recorder.Result().StatusCode != code {
			t.Fatalf("status = %d, want %d, %s", recorder.Result().StatusCode, code, body)
		}
		var out gen.TranscodeSetupState
		if code == http.StatusOK {
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	get := func() gen.TranscodeSetupState {
		recorder := fixture.do(authedGet(t, cookies, path))
		return state(recorder, http.StatusOK, recorder.Body.Bytes())
	}
	first := get()
	if !first.IsDefault || first.Revision != 0 || first.Setup.Encode.Preset != "slow" && first.Setup.Encode.Preset != "slower" || len(first.Availability) != 1 {
		t.Fatalf("default = %+v", first)
	}

	changed := first.Setup
	changed.Encode.Quality = 20
	raw, _ := json.Marshal(changed)
	recorder := fixture.do(authedPut(t, cookies, path, string(raw)))
	saved := state(recorder, http.StatusOK, recorder.Body.Bytes())
	if saved.IsDefault || saved.Revision != 1 || saved.Setup.Encode.Quality != 20 {
		t.Errorf("saved = %+v", saved)
	}
	recorder = fixture.do(authedPut(t, cookies, path, `{"version":1,"encode":{"quality":22}}`))
	partial := state(recorder, http.StatusOK, recorder.Body.Bytes())
	if partial.Revision != 2 || partial.Setup.Encode.Quality != 22 || partial.Setup.Skip.RangePct != 20 {
		t.Errorf("a missing field must take its default: %+v", partial)
	}
	if recorder := fixture.do(authedPut(t, cookies, path, `{"version":1,"encoed":{}}`)); recorder.Code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", recorder.Code)
	}
	if recorder := fixture.do(authedPut(t, cookies, path, `{"version":1,"ladder":{"extra_passes":9}}`)); recorder.Code != http.StatusBadRequest {
		t.Errorf("extra_passes 9 = %d, want 400", recorder.Code)
	}
	recorder = fixture.do(authedDelete(t, cookies, path))
	reset := state(recorder, http.StatusOK, recorder.Body.Bytes())
	if reset.Revision != 3 || reset.Setup.Encode.Quality == 22 {
		t.Errorf("reset = revision %d quality %v", reset.Revision, reset.Setup.Encode.Quality)
	}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/settings/transcode/targets/9999")); recorder.Code != http.StatusNotFound {
		t.Errorf("unknown target = %d, want 404", recorder.Code)
	}
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/settings/transcode/targets/9999", `{}`)); recorder.Code != http.StatusNotFound {
		t.Errorf("put unknown target = %d, want 404", recorder.Code)
	}
}
