package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakeDiscoverPool struct {
	items     []apiserver.DiscoverItem
	queries   []apiserver.DiscoverQuery
	dismissed map[int64][]string
}

func (f *fakeDiscoverPool) List(_ context.Context, query apiserver.DiscoverQuery) ([]apiserver.DiscoverItem, error) {
	f.queries = append(f.queries, query)
	var out []apiserver.DiscoverItem
	for _, item := range f.items {
		if item.CategoryID == query.CategoryIDs[0] && int64(len(out)) < query.Limit && (!query.HasCursor || item.Popularity < query.AfterPopularity) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeDiscoverPool) Item(_ context.Context, id int64) (apiserver.DiscoverItem, bool, error) {
	for _, item := range f.items {
		if item.ID == id {
			return item, true, nil
		}
	}
	return apiserver.DiscoverItem{}, false, nil
}

func (*fakeDiscoverPool) Genres(context.Context, int64, string) ([]apiserver.DiscoverGenre, error) {
	return []apiserver.DiscoverGenre{{Genre: "Drama", Items: 2}}, nil
}

func (*fakeDiscoverPool) FeedStates(context.Context, int64) ([]apiserver.DiscoverFeedState, error) {
	return []apiserver.DiscoverFeedState{{Type: "trending", Kind: "movie", RefreshedAt: 9}}, nil
}

func (f *fakeDiscoverPool) Dismiss(_ context.Context, userID int64, _ string, item apiserver.DiscoverItem) error {
	if f.dismissed == nil {
		f.dismissed = map[int64][]string{}
	}
	f.dismissed[userID] = append(f.dismissed[userID], item.ExternalID)
	return nil
}

func (*fakeDiscoverPool) Restore(context.Context, int64, string, string, string) error { return nil }

func (*fakeDiscoverPool) Dismissals(context.Context, int64) ([]apiserver.DiscoverDismissal, error) {
	return nil, nil
}

func discoverItems(categoryID int64, count int, idBase int64) []apiserver.DiscoverItem {
	var out []apiserver.DiscoverItem
	for index := range count {
		id := idBase + int64(index)
		out = append(out, apiserver.DiscoverItem{ID: id, CategoryID: categoryID, Kind: "movie", Provider: "tmdb", ExternalID: string(rune('a' + id)), Title: "Film", Popularity: float64(100 - id)})
	}
	return out
}

func TestRecommendations_AnswerNotFoundWhileDiscoverIsOff(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	for _, path := range []string{"/api/v1/discover/libraries", "/api/v1/discover/dashboard", "/api/v1/discover/dismissals", "/api/v1/discover/genres?category_id=1"} {
		if recorder := fixture.do(authedGet(t, cookies, path)); recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, recorder.Code)
		}
	}
}

func TestRecommendations_PagesByCursorAndHidesLibrariesTheCallerCannotRead(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	movies, _ := fixture.cache.CreateCategory(t.Context(), "movie", "Movies", 10)
	if err := fixture.cache.SetDiscoverEnabled(t.Context(), fixture.clock, true); err != nil {
		t.Fatal(err)
	}
	fixture.discoverPool.items = discoverItems(movies.ID, 5, 1)

	var seen []int64
	path := "/api/v1/discover/recommendations?category_id=" + itoa(movies.ID) + "&limit=2"
	for range 5 {
		recorder := fixture.do(authedGet(t, cookies, path))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET = %d, %s", recorder.Code, recorder.Body)
		}
		var page gen.RecommendationPage
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			seen = append(seen, item.Id)
		}
		if page.NextCursor == nil {
			break
		}
		query := fixture.discoverPool.queries[len(fixture.discoverPool.queries)-1]
		if query.Limit != 3 {
			t.Errorf("limit asked = %d, want one more than the page", query.Limit)
		}
		path = "/api/v1/discover/recommendations?category_id=" + itoa(movies.ID) + "&limit=2&cursor=" + *page.NextCursor
	}
	if want := []int64{1, 2, 3, 4, 5}; !reflect.DeepEqual(seen, want) {
		t.Errorf("seen = %v, want %v", seen, want)
	}
	last := fixture.discoverPool.queries[len(fixture.discoverPool.queries)-1]
	if !last.HasCursor || last.AfterID != 4 || len(fixture.discoverPool.queries) != 3 {
		t.Errorf("cursor not decoded: %+v", last)
	}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/discover/recommendations?category_id=999")); recorder.Code != http.StatusNotFound {
		t.Errorf("unknown library = %d, want 404", recorder.Code)
	}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/discover/recommendations?category_id=1&cursor=!!")); recorder.Code != http.StatusBadRequest {
		t.Errorf("bad cursor = %d, want 400", recorder.Code)
	}
}

func TestRecommendations_DashboardInterleavesLibrariesAndDismissalIsPerUser(t *testing.T) {
	fixture := newWorksFixture(t)
	admin := fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	movies, _ := fixture.cache.CreateCategory(t.Context(), "movie", "Movies", 10)
	series, _ := fixture.cache.CreateCategory(t.Context(), "tv", "Series", 20)
	if err := fixture.cache.SetDiscoverEnabled(t.Context(), fixture.clock, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{movies.ID, series.ID} {
		library, _ := fixture.cache.LibraryDiscover(t.Context(), id)
		library.Enabled = true
		if err := fixture.cache.SetLibraryDiscover(t.Context(), library); err != nil {
			t.Fatal(err)
		}
	}
	fixture.discoverPool.items = append(discoverItems(movies.ID, 2, 1), discoverItems(series.ID, 2, 10)...)

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/discover/dashboard?limit=3"))
	var row []gen.Recommendation
	if err := json.Unmarshal(recorder.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, item := range row {
		ids = append(ids, item.Id)
	}
	if want := []int64{1, 10, 2}; !reflect.DeepEqual(ids, want) {
		t.Errorf("row = %v, want %v", ids, want)
	}

	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/discover/recommendations/1/dismiss", `{}`)); recorder.Code != http.StatusNoContent {
		t.Fatalf("dismiss = %d, %s", recorder.Code, recorder.Body)
	}
	if got := fixture.discoverPool.dismissed[admin.ID]; len(got) != 1 {
		t.Errorf("dismissed for the caller = %v", got)
	}
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/discover/recommendations/777/dismiss", `{}`)); recorder.Code != http.StatusNotFound {
		t.Errorf("dismiss of a missing item = %d, want 404", recorder.Code)
	}
	recorder = fixture.do(authedGet(t, cookies, "/api/v1/discover/libraries"))
	var libraries []gen.DiscoverLibrary
	if err := json.Unmarshal(recorder.Body.Bytes(), &libraries); err != nil || len(libraries) != 2 || !libraries[0].CanAdd || len(libraries[0].Feeds) != 1 {
		t.Errorf("libraries = %+v, err = %v", libraries, err)
	}
}

func TestRecommendations_ALibraryTheCallerHasNoGrantOnIsNotFoundAndMissingFromTheDashboard(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	movies, _ := fixture.cache.CreateCategory(t.Context(), "movie", "Movies", 10)
	if err := fixture.cache.SetDiscoverEnabled(t.Context(), fixture.clock, true); err != nil {
		t.Fatal(err)
	}
	library, _ := fixture.cache.LibraryDiscover(t.Context(), movies.ID)
	library.Enabled = true
	if err := fixture.cache.SetLibraryDiscover(t.Context(), library); err != nil {
		t.Fatal(err)
	}
	fixture.discoverPool.items = discoverItems(movies.ID, 2, 1)
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/discover/recommendations?category_id="+itoa(movies.ID))); recorder.Code != http.StatusNotFound {
		t.Errorf("list = %d, want 404", recorder.Code)
	}
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/discover/dashboard"))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "[]\n" {
		t.Errorf("dashboard = %d %q, want an empty row", recorder.Code, recorder.Body.String())
	}
}
