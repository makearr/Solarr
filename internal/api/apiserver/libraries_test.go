package apiserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
)

// fakeNavigation answers a fixed set of levels per media type, so a test can
// assert ListLibraries renders a declaration without a real internal/media/music.
type fakeNavigation map[string][]model.Level

func (f fakeNavigation) LevelsFor(mediaType string) ([]model.Level, bool) {
	levels, ok := f[mediaType]
	return levels, ok
}

type libraryLevelBody struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Entity      string `json:"entity"`
	DefaultSort string `json:"default_sort"`
	Count       *int   `json:"count"`
}

type libraryBody struct {
	ID        int64              `json:"id"`
	MediaType string             `json:"media_type"`
	Name      string             `json:"name"`
	Levels    []libraryLevelBody `json:"levels"`
	LastLevel *string            `json:"last_level"`
}

// TestListLibraries_RendersTheDeclaredLevelsWithTheirCounts is ADR-54's core
// guarantee: core renders whatever the media type declares, and each level's
// count comes from the counter table keyed by its entity, not a live COUNT.
func TestListLibraries_RendersTheDeclaredLevelsWithTheirCounts(t *testing.T) {
	navigation := fakeNavigation{
		"music": {
			{Key: "artists", Label: "Artists", Entity: model.LevelEntityPerson, DefaultSort: "name"},
			{Key: "albums", Label: "Albums", Entity: model.LevelEntityWork, DefaultSort: "title"},
			{Key: "tracks", Label: "Tracks", Entity: model.LevelEntityWorkPart, DefaultSort: "artist"},
		},
	}
	librarySize := fakeLibrarySize{byScope: map[string]map[string]int64{}}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, librarySize, navigation)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	librarySize.byScope["category:"+strconv.FormatInt(category.ID, 10)] = map[string]int64{
		"works": 12, "parts": 155, "artists": 3, "files": 200,
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/libraries"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body []libraryBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].ID != category.ID || body[0].MediaType != "music" {
		t.Fatalf("libraries = %+v", body)
	}
	levels := body[0].Levels
	if len(levels) != 3 {
		t.Fatalf("levels = %+v, want 3", levels)
	}
	want := map[string]int{"artists": 3, "albums": 12, "tracks": 155}
	for _, level := range levels {
		if level.Count == nil || *level.Count != want[level.Key] {
			t.Errorf("level %q count = %v, want %d", level.Key, level.Count, want[level.Key])
		}
	}
}

// A category the caller cannot read must not appear, the same ADR-27
// guarantee GET /categories already gives.
func TestListLibraries_OnlyGrantedCategoriesAppear(t *testing.T) {
	navigation := fakeNavigation{"music": {{Key: "albums", Label: "Albums", Entity: model.LevelEntityWork}}}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	visible, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	hidden, err := fixture.cache.CreateCategory(t.Context(), "tv", "Private Stash", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/libraries"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	var body []libraryBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].ID != visible.ID {
		t.Fatalf("libraries = %+v, want exactly %d", body, visible.ID)
	}
	for _, library := range body {
		if library.ID == hidden.ID {
			t.Errorf("libraries = %+v, must not include the ungranted category %d", body, hidden.ID)
		}
	}
}

// A media type with no registered NavigationProvider entry must answer no
// levels rather than failing the whole list -- the same "render without the
// tile" fallback categoryCounts uses for a single missing figure.
func TestListLibraries_UnregisteredMediaTypeAnswersNoLevels(t *testing.T) {
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, fakeNavigation{})
	if _, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/libraries"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	var body []libraryBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].Levels == nil || len(body[0].Levels) != 0 {
		t.Errorf("libraries = %+v, want one library with an empty (not null) levels array", body)
	}
}

// ADR-54's own point: a library the caller has visited before reports the
// level they were last on, so the sidebar tree costs one click, not three.
func TestListLibraries_ReportsTheLastVisitedLevel(t *testing.T) {
	navigation := fakeNavigation{
		"music": {
			{Key: "albums", Label: "Albums", Entity: model.LevelEntityWork},
			{Key: "tracks", Label: "Tracks", Entity: model.LevelEntityWorkPart},
		},
	}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	putRecorder := fixture.do(authedPut(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/last-level", category.ID), `{"level":"tracks"}`))
	if putRecorder.Code != http.StatusNoContent {
		t.Fatalf("PUT status %d (%s)", putRecorder.Code, putRecorder.Body)
	}

	getRecorder := fixture.do(authedGet(t, cookies, "/api/v1/libraries"))
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET status %d (%s)", getRecorder.Code, getRecorder.Body)
	}
	var body []libraryBody
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].LastLevel == nil || *body[0].LastLevel != "tracks" {
		t.Errorf("libraries = %+v, want last_level=\"tracks\"", body)
	}
}

// A library never visited before reports no last level, not an error.
func TestListLibraries_NeverVisitedReportsNoLastLevel(t *testing.T) {
	navigation := fakeNavigation{"music": {{Key: "albums", Label: "Albums", Entity: model.LevelEntityWork}}}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	if _, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/libraries"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	var body []libraryBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].LastLevel != nil {
		t.Errorf("libraries = %+v, want last_level absent", body)
	}
}

// Setting a level unknown to this library's media type answers 404, the
// same guard GET /libraries/{id}/{level} already applies.
func TestSetLibraryLastLevel_UnknownLevelAnswers404(t *testing.T) {
	navigation := fakeNavigation{"music": {{Key: "albums", Label: "Albums", Entity: model.LevelEntityWork}}}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPut(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/last-level", category.ID), `{"level":"nonsense"}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// Unauthenticated requests answer 401, same as every other route in the
// secured group.
func TestListLibraries_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, fakeNavigation{})
	fixture.createUser(t, "admin", settings.RoleAdmin)

	recorder := fixture.do(authedGet(t, nil, "/api/v1/libraries"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for no credential", recorder.Code)
	}
}

type libraryLevelItemBody struct {
	ID       int64   `json:"id"`
	Title    string  `json:"title"`
	Subtitle *string `json:"subtitle"`
	Year     *int    `json:"year"`
}

type libraryLevelPageBody struct {
	Items      []libraryLevelItemBody `json:"items"`
	NextCursor *string                `json:"next_cursor"`
}

// A work-entity level (Albums) delegates to the existing WorksReader -- a
// level is a filter and a default sort, not a new list implementation.
func TestListLibraryLevel_WorkEntityDelegatesToWorksReader(t *testing.T) {
	navigation := fakeNavigation{"music": {{Key: "albums", Label: "Albums", Entity: model.LevelEntityWork}}}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	year := int64(1997)
	artist := "Katupoken"
	fixture.works.rows = []apiserver.WorkRow{{ID: 1, Title: "KI Pecezinu", SortTitle: "ki pecezinu", Year: &year, Artist: &artist}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/albums", category.ID)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	var body libraryLevelPageBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Title != "KI Pecezinu" ||
		body.Items[0].Subtitle == nil || *body.Items[0].Subtitle != "Katupoken" ||
		body.Items[0].Year == nil || *body.Items[0].Year != 1997 {
		t.Errorf("items = %+v, want KI Pecezinu / Katupoken / 1997", body.Items)
	}
	if len(fixture.works.lastFilter.CategoryIDs) != 1 || fixture.works.lastFilter.CategoryIDs[0] != category.ID {
		t.Errorf("filter = %+v, want scoped to category %d", fixture.works.lastFilter, category.ID)
	}
}

// A work_part-entity level (Tracks) delegates to LibraryTracksReader.
func TestListLibraryLevel_WorkPartEntityDelegatesToLibraryTracksReader(t *testing.T) {
	navigation := fakeNavigation{"music": {{Key: "tracks", Label: "Tracks", Entity: model.LevelEntityWorkPart}}}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	title := "Sesoge"
	artist := "Katupoken"
	fixture.libraryTracks.rows = []apiserver.TrackRow{
		{ID: 9, Title: &title, AlbumTitle: "KI Pecezinu", ArtistName: &artist},
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/tracks?q=night&where=%s", category.ID, url.QueryEscape(`[{"field":"has_file","op":"is","value":"true"}]`))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if got := fixture.libraryTracks.lastFilter; got.Search != "night" || got.Where != `[{"field":"has_file","op":"is","value":"true"}]` {
		t.Errorf("filter = %+v, want the search and the custom filter passed on", got)
	}
	var body libraryLevelPageBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Title != "Sesoge" ||
		body.Items[0].Subtitle == nil || *body.Items[0].Subtitle != "Katupoken -- KI Pecezinu" {
		t.Errorf("items = %+v, want Sesoge / \"Katupoken -- KI Pecezinu\"", body.Items)
	}
	if len(fixture.libraryTracks.lastFilter.CategoryIDs) != 1 ||
		fixture.libraryTracks.lastFilter.CategoryIDs[0] != category.ID {
		t.Errorf("filter = %+v, want scoped to category %d", fixture.libraryTracks.lastFilter, category.ID)
	}
}

// A level with no reader wired up answers 503 rather than crashing or
// silently returning nothing.
func TestListLibraryLevel_PersonEntityWithNoReaderAnswers503(t *testing.T) {
	navigation := fakeNavigation{"music": {{Key: "artists", Label: "Artists", Entity: model.LevelEntityPerson}}}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/artists", category.ID)))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", recorder.Code)
	}
}

// A person-entity level (Artists) delegates to LibraryArtistsReader once one
// is actually wired.
func TestListLibraryLevel_PersonEntityDelegatesToLibraryArtistsReader(t *testing.T) {
	navigation := fakeNavigation{"music": {{Key: "artists", Label: "Artists", Entity: model.LevelEntityPerson}}}
	artists := &fakeLibraryArtistsReader{rows: []apiserver.ArtistRow{{ID: 5, Name: "Katupoken", SortName: "Katupoken"}}}
	fixture := newWorksFixtureWithLibrarySizeNavigationAndArtists(t, nil, navigation, artists, nil)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/artists", category.ID)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	var body libraryLevelPageBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Title != "Katupoken" {
		t.Errorf("items = %+v, want Katupoken", body.Items)
	}
	if len(artists.lastFilter.CategoryIDs) != 1 || artists.lastFilter.CategoryIDs[0] != category.ID {
		t.Errorf("filter = %+v, want scoped to category %d", artists.lastFilter, category.ID)
	}
}

// An unknown level key for this library's media type answers 404.
func TestListLibraryLevel_UnknownLevelAnswers404(t *testing.T) {
	navigation := fakeNavigation{"music": {{Key: "albums", Label: "Albums", Entity: model.LevelEntityWork}}}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/nonsense", category.ID)))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// A category the caller cannot read answers 404, the same as GET /works's
// own single-category lookup (Invariant 37).
func TestListLibraryLevel_UngrantedCategoryAnswers404(t *testing.T) {
	navigation := fakeNavigation{"music": {{Key: "albums", Label: "Albums", Entity: model.LevelEntityWork}}}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/albums", category.ID)))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// Adding an artist and immediately reading "Artists 0" says the add failed,
// which it did not: the count is persons credited on works, and credits only
// arrive with enrichment. No number is shown until there is one.
func TestLibraries_ArtistsCountIsOmittedWhenZero(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/libraries"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var libraries []struct {
		Levels []struct {
			Key   string `json:"key"`
			Count *int   `json:"count"`
		} `json:"levels"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &libraries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(libraries) == 0 {
		t.Fatal("no libraries returned")
	}
	for _, level := range libraries[0].Levels {
		if level.Key == "artists" && level.Count != nil {
			t.Errorf("artists count = %d, want it omitted while zero", *level.Count)
		}
	}
}

// A library holding a further media type answers once, the further type inside it with its own levels; its All list
// covers both types' categories, and access to the library is access to both (plan 10-libraries L4).
func TestListLibraries_NestsAFurtherMediaType(t *testing.T) {
	navigation := fakeNavigation{
		"tv":    {{Key: "series", Label: "Series", Entity: model.LevelEntityWork}},
		"movie": {{Key: "movies", Label: "Movies", Entity: model.LevelEntityWork}},
	}
	fixture := newWorksFixtureWithLibrarySizeAndNavigation(t, nil, navigation)
	anime, err := fixture.cache.CreateCategory(t.Context(), "tv", "Anime", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	user := fixture.createUser(t, "watcher", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, anime.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	movies, err := fixture.cache.AddLibraryType(t.Context(), anime.ID, "movie")
	if err != nil {
		t.Fatalf("AddLibraryType: %v", err)
	}
	cookies := fixture.signIn(t, "watcher")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/libraries"))
	var body []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Members []struct {
			ID        int64  `json:"id"`
			MediaType string `json:"media_type"`
			Levels    []struct {
				Key string `json:"key"`
			} `json:"levels"`
		} `json:"members"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal %s: %v", recorder.Body, err)
	}
	if len(body) != 1 || body[0].ID != anime.ID || len(body[0].Members) != 1 || body[0].Members[0].ID != movies.ID ||
		body[0].Members[0].MediaType != "movie" || len(body[0].Members[0].Levels) != 1 || body[0].Members[0].Levels[0].Key != "movies" {
		t.Fatalf("libraries = %s, want Anime once with its movies inside", recorder.Body)
	}

	if recorder := fixture.do(authedGet(t, cookies, fmt.Sprintf("/api/v1/works?category_id=%d&whole_library=true&media_type=movie", anime.ID))); recorder.Code != http.StatusOK {
		t.Fatalf("All list: status %d, %s", recorder.Code, recorder.Body)
	}
	if got := fixture.works.lastFilter.CategoryIDs; len(got) != 2 || got[0] != anime.ID || got[1] != movies.ID || fixture.works.lastFilter.MediaType != "movie" {
		t.Errorf("All list asked for categories %v of type %q, want both of Anime, movies", got, fixture.works.lastFilter.MediaType)
	}
}
