package apiserver_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeMediaTypes is a build with two media types, the second with no people or groups.
type fakeMediaTypes struct{}

func (fakeMediaTypes) List() []apiserver.MediaTypeInfo {
	return []apiserver.MediaTypeInfo{
		{
			Key: "music", Label: "Music",
			Work: apiserver.MediaTerm{One: "Album", Many: "Albums"}, Part: apiserver.MediaTerm{One: "Track", Many: "Tracks"},
			LeadPeople: apiserver.MediaTerm{One: "Artist", Many: "Artists"},
			Roles:      []apiserver.MediaRole{{Key: "artist", Label: "Artist", Lead: true, Followable: true}},
			FileSettings: []apiserver.FileSetting{{Key: "change_file_date", Label: "Change File Date", Help: "On import.",
				Options: []apiserver.MediaChoice{{Key: "release_date", Label: "Album Release Date"}}}},
			ScanMonitor: apiserver.ScanMonitor{Setting: "monitor_discovered", Label: "Monitor discovered",
				Options: []apiserver.MediaChoice{{Key: "none", Label: "None"}}},
			SearchesLibrary: true, CoverSources: true,
		},
		{Key: "tv", Label: "TV", Work: apiserver.MediaTerm{One: "Series", Many: "Series"}, UnidentifiedRetry: "tv_retry", ReleaseFilters: true, Kinds: []apiserver.LibraryKind{
			{Key: "series", Label: "Series", QualityProfiles: []string{"WEB 1080p"}},
			{Key: "anime", Label: "Anime", Help: "Anime.", QualityProfiles: []string{"Anime 1080p", "WEB 1080p", "No such profile"},
				Language: &settings.LanguageProfile{Name: "Japanese first", AudioPreferred: []string{"ja", "en"}}},
		}},
	}
}

func (fakeMediaTypes) Known(mediaType string) bool { return mediaType == "music" || mediaType == "tv" }

func TestListMediaTypes_AnswersOnlyTheRegisteredTypesInOrder(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/mediatypes"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var got []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	term := func(one, many string) map[string]any { return map[string]any{"one": one, "many": many} }
	want := []map[string]any{
		{
			"key": "music", "label": "Music", "work": term("Album", "Albums"), "part": term("Track", "Tracks"),
			"group": term("", ""), "lead_people": term("Artist", "Artists"), "organisation": term("", ""),
			"roles": []any{map[string]any{
				"key": "artist", "label": "Artist", "lead": true, "followable": true, "part_scoped": false, "character": false,
			}},
			"follow_options": []any{}, "facets": []any{}, "monitor_options": []any{}, "search_terms": []any{}, "work_image": "", "wanted_grain": "", "add_kinds": []any{}, "commits_held_imports": false, "commit_guess_floor": float64(0), "duplicates_by_name": false,
			"file_settings": []any{map[string]any{"key": "change_file_date", "label": "Change File Date", "help": "On import.",
				"options": []any{map[string]any{"key": "release_date", "label": "Album Release Date"}}}},
			"scan_monitor": map[string]any{"setting": "monitor_discovered", "label": "Monitor discovered",
				"options": []any{map[string]any{"key": "none", "label": "None"}}},
			"searches_library": true, "cover_sources": true, "release_filters": false,
		},
		{
			"key": "tv", "label": "TV", "work": term("Series", "Series"), "part": term("", ""), "group": term("", ""),
			"lead_people": term("", ""), "organisation": term("", ""), "roles": []any{}, "follow_options": []any{}, "facets": []any{}, "monitor_options": []any{}, "search_terms": []any{}, "work_image": "", "wanted_grain": "", "add_kinds": []any{}, "commits_held_imports": false, "commit_guess_floor": float64(0), "duplicates_by_name": false,
			"kinds":         []any{map[string]any{"key": "series", "label": "Series"}, map[string]any{"key": "anime", "label": "Anime", "help": "Anime."}},
			"file_settings": []any{}, "scan_monitor": map[string]any{"setting": "", "label": "", "options": []any{}},
			"unidentified_retry": "tv_retry", "searches_library": false, "cover_sources": false, "release_filters": true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("media types = %+v\nwant %+v", got, want)
	}
}

func TestListMediaTypes_RequiresSignIn(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(authedGet(t, nil, "/api/v1/mediatypes"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a session", recorder.Code)
	}
}

// A planned media type with no module would be a library that can do nothing, so it is refused.
func TestCreateCategory_RefusesAMediaTypeWithoutAModule(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories", `{"media_type":"movie","name":"Movies"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "movie is not available in this build") {
		t.Errorf("body = %s, want it to name the unavailable media type", recorder.Body)
	}
	if categories := fixture.cache.Snapshot().Categories(); len(categories) != 0 {
		t.Errorf("categories after a refused create = %+v, want none", categories)
	}
}
