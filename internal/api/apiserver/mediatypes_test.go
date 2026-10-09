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
		},
		{Key: "tv", Label: "TV", Work: apiserver.MediaTerm{One: "Series", Many: "Series"}},
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
			"follow_options": []any{}, "facets": []any{}, "monitor_options": []any{},
		},
		{
			"key": "tv", "label": "TV", "work": term("Series", "Series"), "part": term("", ""), "group": term("", ""),
			"lead_people": term("", ""), "organisation": term("", ""), "roles": []any{}, "follow_options": []any{}, "facets": []any{}, "monitor_options": []any{},
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
