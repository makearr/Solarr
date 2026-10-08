package apiserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// fakePeopleReader mirrors fakeWorksReader: records the arguments it was
// called with and answers canned rows, without a real library.db.
type fakePeopleReader struct {
	rows       []gen.PersonEntry
	detail     gen.PersonDetail
	credits    []apiserver.PersonCreditRow
	detailErr  error
	lastQuery  apiserver.PeopleQuery
	lastCursor paginate.TextCursor
	lastID     int64
	// lastVisible is what the detail read was told the caller may see.
	lastVisible []int64
}

func (f *fakePeopleReader) ListPeople(
	_ context.Context, query apiserver.PeopleQuery, cursor paginate.TextCursor, pageSize int,
) ([]gen.PersonEntry, error) {
	f.lastQuery = query
	f.lastCursor = cursor
	if len(f.rows) > pageSize {
		return f.rows[:pageSize], nil
	}
	return f.rows, nil
}

func (f *fakePeopleReader) GetPersonDetail(_ context.Context, id int64, visible []int64) (gen.PersonDetail, []apiserver.PersonCreditRow, error) {
	f.lastID, f.lastVisible = id, visible
	if f.detailErr != nil {
		return gen.PersonDetail{}, nil, f.detailErr
	}
	return f.detail, f.credits, nil
}

// fakePeopleSubscriber records the arguments Subscribe was called with.
type fakePeopleSubscriber struct {
	err                    error
	called                 bool
	lastPersonID           int64
	lastUserID             int64
	lastCategoryID         int64
	lastMediaTypes         []string
	lastRoles              []string
	lastMonitorBack        bool
	lastIncludePrimaries   *[]string
	lastExcludeSecondaries *[]string

	removeCalled   bool
	removePersonID int64
	removeUserID   int64
}

func (f *fakePeopleSubscriber) Remove(_ context.Context, personID, userID int64) error {
	f.removeCalled = true
	f.removePersonID, f.removeUserID = personID, userID
	return f.err
}

func (f *fakePeopleSubscriber) Subscribe(
	_ context.Context, personID, userID, categoryID int64, mediaTypes, roles []string,
	monitorBackCatalogue bool, includePrimaryTypes, excludeSecondaryTypes *[]string,
) error {
	f.called = true
	f.lastCategoryID = categoryID
	f.lastIncludePrimaries = includePrimaryTypes
	f.lastPersonID = personID
	f.lastUserID = userID
	f.lastMediaTypes = mediaTypes
	f.lastRoles = roles
	f.lastMonitorBack = monitorBackCatalogue
	f.lastExcludeSecondaries = excludeSecondaryTypes
	return f.err
}

// fakeSubscriptionPreviewer records the id Preview was called with.
type fakeSubscriptionPreviewer struct {
	row    apiserver.SubscriptionPreviewRow
	err    error
	lastID int64
}

func (f *fakeSubscriptionPreviewer) Preview(_ context.Context, personID int64) (apiserver.SubscriptionPreviewRow, error) {
	f.lastID = personID
	if f.err != nil {
		return apiserver.SubscriptionPreviewRow{}, f.err
	}
	return f.row, nil
}

func TestListPeople_ResponseShapeAndCursor(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	disambiguation := "band"
	fixture.people.rows = []gen.PersonEntry{
		{Id: 1, Name: "Katupoken", SortName: "Katupoken", Disambiguation: &disambiguation, Monitored: true, Roles: []string{"artist"}, Works: 3},
		{Id: 2, Name: "Vutu Doci", SortName: "Bush, Vutu", Roles: []string{"artist"}},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people?limit=1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body struct {
		Items []struct {
			ID             int64   `json:"id"`
			Name           string  `json:"name"`
			Disambiguation *string `json:"disambiguation"`
			Monitored      bool    `json:"monitored"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Name != "Katupoken" {
		t.Fatalf("items = %+v, want exactly the first row (limit=1)", body.Items)
	}
	if body.Items[0].Disambiguation == nil || *body.Items[0].Disambiguation != "band" {
		t.Errorf("Disambiguation = %v, want \"band\"", body.Items[0].Disambiguation)
	}
	if !body.Items[0].Monitored {
		t.Error("Monitored = false, want true")
	}
	if body.NextCursor == "" {
		t.Error("NextCursor is empty, want a cursor: there is a second row beyond the limit")
	}
}

func TestListPeople_MonitoredAndQueryParamsReachTheFilter(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people?monitored=true&q=Rad&role=actor,%20director&sort=works&dir=desc"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	query := fixture.people.lastQuery
	if query.Monitored == nil || !*query.Monitored || query.Prefix != "Rad" || !slices.Equal(query.Roles, []string{"actor", "director"}) ||
		query.Sort != "works" || !query.Desc {
		t.Errorf("query = %+v, want monitored, prefix Rad, the two roles, by works descending", query)
	}
}

func TestGetPerson_ResponseShape(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	biography := "English singer-songwriter."
	primaryType := "Album"
	wantedState := "PARTIAL"
	fixture.people.detail = gen.PersonDetail{
		Id: 7, Name: "Vutu Doci", SortName: "Bush, Vutu", Biography: &biography,
		Aliases: []string{"Cathy"}, Genres: []string{"Art Pop"}, Ids: map[string]string{},
		Libraries: []gen.PersonLibrary{{CategoryId: 3, Role: "artist", Works: 1}},
	}
	fixture.people.credits = []apiserver.PersonCreditRow{{CategoryID: 3, Role: "artist", Work: apiserver.PersonWorkRow{
		ID: 41, MediaType: "music", Title: "Cotevo of Love", SortTitle: "cotevo of love",
		PrimaryType: &primaryType, WantedState: &wantedState, OnDisk: true,
	}}}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/7"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.people.lastID != 7 {
		t.Errorf("GetPersonDetail called with id %d, want 7", fixture.people.lastID)
	}

	var body struct {
		Name      string   `json:"name"`
		Biography *string  `json:"biography"`
		Aliases   []string `json:"aliases"`
		Genres    []string `json:"genres"`
		Credits   []struct {
			CategoryID int64  `json:"category_id"`
			Role       string `json:"role"`
			Work       struct {
				ID          int64   `json:"id"`
				Title       string  `json:"title"`
				PrimaryType *string `json:"primary_type"`
				WantedState *string `json:"wanted_state"`
				OnDisk      bool    `json:"on_disk"`
				Status      string  `json:"status"`
			} `json:"work"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Name != "Vutu Doci" {
		t.Errorf("Name = %q, want Vutu Doci", body.Name)
	}
	if body.Biography == nil || *body.Biography != biography {
		t.Errorf("Biography = %v, want %q", body.Biography, biography)
	}
	if len(body.Aliases) != 1 || body.Aliases[0] != "Cathy" {
		t.Errorf("Aliases = %+v, want [Cathy]", body.Aliases)
	}
	if len(body.Genres) != 1 || body.Genres[0] != "Art Pop" {
		t.Errorf("Genres = %+v, want [Art Pop]", body.Genres)
	}
	if len(body.Credits) != 1 || body.Credits[0].Work.Title != "Cotevo of Love" || body.Credits[0].CategoryID != 3 {
		t.Fatalf("Credits = %+v, want [Cotevo of Love] in library 3", body.Credits)
	}
	work := body.Credits[0].Work
	if work.PrimaryType == nil || *work.PrimaryType != "Album" {
		t.Errorf("PrimaryType = %v, want Album", work.PrimaryType)
	}
	if work.WantedState == nil || *work.WantedState != "PARTIAL" {
		t.Errorf("WantedState = %v, want PARTIAL", work.WantedState)
	}
	if !work.OnDisk {
		t.Errorf("OnDisk = false, want true")
	}
}

func TestGetPerson_MissingPersonAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	fixture.people.detailErr = apiserver.ErrPersonNotFound

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/404"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a person id the reader reports missing", recorder.Code)
	}
}

func TestGetPerson_NonNumericIDAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/not-a-number"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-numeric id", recorder.Code)
	}
}

// requestingListener signs in a user holding permission in the one music library.
func requestingListener(t *testing.T, permission settings.Permission) (worksFixture, settings.User, []*http.Cookie) {
	t.Helper()
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, permission); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	return fixture, user, fixture.signIn(t, "listener")
}

// Subscribing makes an artist's albums wanted: it needs request, and the albums go to that library.
func TestSubscribePerson_NeedsRequestInALibraryOfTheMediaType(t *testing.T) {
	fixture, _, cookies := requestingListener(t, settings.PermissionRead)
	body := `{"media_types":["music"],"roles":["artist"]}`
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/subscribe", body)); recorder.Code != http.StatusForbidden ||
		fixture.subscriber.called {
		t.Errorf("read only: status %d, subscribed %v; want 403 and nothing subscribed", recorder.Code, fixture.subscriber.called)
	}

	fixture, _, cookies = requestingListener(t, settings.PermissionRequest)
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/subscribe", body))
	music := fixture.cache.Snapshot().Categories()[0]
	if recorder.Code != http.StatusNoContent || fixture.subscriber.lastCategoryID != music.ID {
		t.Errorf("request: status %d, library %d; want 204 into library %d", recorder.Code, fixture.subscriber.lastCategoryID, music.ID)
	}
}

func TestSubscribePerson_ReachesTheSubscriberWithTheCallersUserID(t *testing.T) {
	fixture, user, cookies := requestingListener(t, settings.PermissionRequest)

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/subscribe",
		`{"media_types":["music"],"roles":["artist"],"monitor_back_catalogue":true}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if !fixture.subscriber.called {
		t.Fatal("Subscribe was not called")
	}
	if fixture.subscriber.lastPersonID != 7 {
		t.Errorf("personID = %d, want 7", fixture.subscriber.lastPersonID)
	}
	if fixture.subscriber.lastUserID != user.ID {
		t.Errorf("userID = %d, want the signed-in user's id %d", fixture.subscriber.lastUserID, user.ID)
	}
	if len(fixture.subscriber.lastMediaTypes) != 1 || fixture.subscriber.lastMediaTypes[0] != "music" {
		t.Errorf("MediaTypes = %+v, want [music]", fixture.subscriber.lastMediaTypes)
	}
	if len(fixture.subscriber.lastRoles) != 1 || fixture.subscriber.lastRoles[0] != "artist" {
		t.Errorf("Roles = %+v, want [artist]", fixture.subscriber.lastRoles)
	}
	if !fixture.subscriber.lastMonitorBack {
		t.Error("MonitorBackCatalogue = false, want true")
	}
}

func TestSubscribePerson_EmptyMediaTypesAnswers400(t *testing.T) {
	fixture, _, cookies := requestingListener(t, settings.PermissionRequest)

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/subscribe", `{"media_types":[]}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for empty media_types", recorder.Code)
	}
	if fixture.subscriber.called {
		t.Error("Subscribe was called despite the invalid body")
	}
}

func TestSubscribePerson_MissingPersonAnswers404(t *testing.T) {
	fixture, _, cookies := requestingListener(t, settings.PermissionRequest)

	fixture.subscriber.err = sql.ErrNoRows

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/404/subscribe", `{"media_types":["music"]}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when the subscriber reports the person missing", recorder.Code)
	}
}

// Absent exclude_secondary_types must reach the subscriber as a nil pointer
// (todo 4.4: "use the media type's own default"), not an empty slice --
// music.SubscriptionFilters treats the two very differently.
func TestSubscribePerson_AbsentExcludeSecondaryTypesReachesTheSubscriberAsNil(t *testing.T) {
	fixture, _, cookies := requestingListener(t, settings.PermissionRequest)

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/subscribe", `{"media_types":["music"]}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.subscriber.lastExcludeSecondaries != nil {
		t.Errorf("lastExcludeSecondaries = %v, want nil for an absent field", *fixture.subscriber.lastExcludeSecondaries)
	}
}

// An explicit empty list ("exclude nothing") must reach the subscriber as a
// non-nil pointer to an empty slice, distinct from the absent case above.
func TestSubscribePerson_ExplicitEmptyExcludeSecondaryTypesReachesTheSubscriberAsEmptySlice(t *testing.T) {
	fixture, _, cookies := requestingListener(t, settings.PermissionRequest)

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/subscribe",
		`{"media_types":["music"],"exclude_secondary_types":[]}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.subscriber.lastExcludeSecondaries == nil {
		t.Fatal("lastExcludeSecondaries = nil, want a non-nil pointer to an empty slice")
	}
	if len(*fixture.subscriber.lastExcludeSecondaries) != 0 {
		t.Errorf("lastExcludeSecondaries = %v, want empty", *fixture.subscriber.lastExcludeSecondaries)
	}
}

func TestSubscribePerson_ExcludeSecondaryTypesWithValuesReachesTheSubscriber(t *testing.T) {
	fixture, _, cookies := requestingListener(t, settings.PermissionRequest)

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/subscribe",
		`{"media_types":["music"],"exclude_secondary_types":["live","remix"]}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.subscriber.lastExcludeSecondaries == nil {
		t.Fatal("lastExcludeSecondaries = nil, want [live remix]")
	}
	if got := *fixture.subscriber.lastExcludeSecondaries; len(got) != 2 || got[0] != "live" || got[1] != "remix" {
		t.Errorf("lastExcludeSecondaries = %v, want [live remix]", got)
	}
}

func TestPreviewSubscription_ReturnsTheDiscographyAndDefaults(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	fixture.subscriptionPreview.row = apiserver.SubscriptionPreviewRow{
		ReleaseGroups: []apiserver.PreviewReleaseGroupRow{
			{MBID: "rg-1", Title: "KI Pecezinu", PrimaryType: "Album", SecondaryTypes: nil},
			{MBID: "rg-2", Title: "Live at Glastonbury", PrimaryType: "Album", SecondaryTypes: []string{"Live"}},
		},
		DefaultExcludedSecondaryTypes: []string{"compilation", "live", "remix", "dj-mix", "mixtape"},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/7/subscription-preview"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.subscriptionPreview.lastID != 7 {
		t.Errorf("lastID = %d, want 7", fixture.subscriptionPreview.lastID)
	}

	var body struct {
		ReleaseGroups []struct {
			MBID  string `json:"mbid"`
			Title string `json:"title"`
		} `json:"release_groups"`
		DefaultExcludedSecondaryTypes []string `json:"default_excluded_secondary_types"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.ReleaseGroups) != 2 || body.ReleaseGroups[1].Title != "Live at Glastonbury" {
		t.Errorf("release_groups = %+v, want the fake's canned rows round-tripped", body.ReleaseGroups)
	}
	if len(body.DefaultExcludedSecondaryTypes) != 5 {
		t.Errorf("default_excluded_secondary_types = %v, want 5 entries", body.DefaultExcludedSecondaryTypes)
	}
}

func TestPreviewSubscription_NoArtistIDAnswers409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	fixture.subscriptionPreview.err = apiserver.ErrNoArtistID

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/7/subscription-preview"))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 when the person has no MusicBrainz artist id yet", recorder.Code)
	}
}

func TestPreviewSubscription_MissingPersonAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	fixture.subscriptionPreview.err = sql.ErrNoRows

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/404/subscription-preview"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

func TestRemovePerson_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/1/remove", ""))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if fixture.subscriber.removeCalled {
		t.Error("a non-administrator must not reach the engine")
	}
}

// The subscription removed is the caller's own, so the user id has to reach
// the engine rather than being guessed there.
func TestRemovePerson_PassesThePersonAndCallingUser(t *testing.T) {
	fixture := newWorksFixture(t)
	user := fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/remove", ""))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s, want 204", recorder.Code, recorder.Body)
	}
	if fixture.subscriber.removePersonID != 7 {
		t.Errorf("removePersonID = %d, want 7", fixture.subscriber.removePersonID)
	}
	if fixture.subscriber.removeUserID != user.ID {
		t.Errorf("removeUserID = %d, want %d", fixture.subscriber.removeUserID, user.ID)
	}
}

// The spec declares a 404 for a missing person; before this was checked the
// handler answered 500, which is the "declares a 404 that can never fire"
// defect this codebase has now hit twice.
func TestRemovePerson_MissingPersonIsNotFound(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.subscriber.err = sql.ErrNoRows

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/999/remove", ""))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}

// The per-artist override the user asked for (2026-09-19): naming types on
// the request reaches the subscriber, and naming none leaves it to resolve
// the library's own setting.
func TestSubscribePerson_CarriesThePrimaryTypeOverride(t *testing.T) {
	fixture, _, cookies := requestingListener(t, settings.PermissionRequest)

	request := authedPost(t, cookies, "/api/v1/people/7/subscribe",
		`{"media_types":["music"],"include_primary_types":["Album"]}`)
	if recorder := fixture.do(request); recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	got := fixture.subscriber.lastIncludePrimaries
	if got == nil || len(*got) != 1 || (*got)[0] != "Album" {
		t.Errorf("lastIncludePrimaries = %v, want [Album]", got)
	}

	request = authedPost(t, cookies, "/api/v1/people/7/subscribe", `{"media_types":["music"]}`)
	if recorder := fixture.do(request); recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.subscriber.lastIncludePrimaries != nil {
		t.Errorf("lastIncludePrimaries = %v, want nil so the library's own setting is used",
			*fixture.subscriber.lastIncludePrimaries)
	}
}

// fakeArtistActions records which libraries an artist search named.
type fakeArtistActions struct{ searchedCategories []int64 }

func (f *fakeArtistActions) SearchMissing(_ context.Context, _ int64, categoryIDs []int64) (bool, error) {
	f.searchedCategories = categoryIDs
	return false, nil
}
func (f *fakeArtistActions) Refresh(context.Context, int64) (bool, error)     { return false, nil }
func (f *fakeArtistActions) MonitorAll(context.Context, int64) (int64, error) { return 0, nil }

// "Search all missing" sends downloads: only the libraries the caller may request in are searched.
func TestSearchPersonMissing_SearchesOnlyTheLibrariesTheCallerMayRequestIn(t *testing.T) {
	fixture, _, cookies := requestingListener(t, settings.PermissionRead)
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/search", "")); recorder.Code != http.StatusForbidden ||
		fixture.artistActions.searchedCategories != nil {
		t.Errorf("read only: status %d, searched %v; want 403 and nothing searched", recorder.Code, fixture.artistActions.searchedCategories)
	}

	fixture, _, cookies = requestingListener(t, settings.PermissionRequest)
	music := fixture.cache.Snapshot().Categories()[0]
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/search", ""))
	if recorder.Code != http.StatusAccepted || !slices.Equal(fixture.artistActions.searchedCategories, []int64{music.ID}) {
		t.Errorf("request: status %d, searched %v; want 202 in library %d", recorder.Code, fixture.artistActions.searchedCategories, music.ID)
	}
}

// The people list and a person's page are read as the caller: only the libraries they may see decide who exists
// for them, and a person the reader does not find for them is a 404.
func TestPeople_AreReadAsTheCallerMaySeeThem(t *testing.T) {
	fixture := newWorksFixture(t)
	shown, err := fixture.cache.CreateCategory(t.Context(), "music", "Shown", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := fixture.cache.CreateCategory(t.Context(), "tv", "Hidden", 20); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, shown.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/people")); recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body)
	}
	want := []int64{shown.ID}
	if got := fixture.people.lastQuery.Visible; !reflect.DeepEqual(got, want) {
		t.Errorf("the list was read as %+v, want %+v", got, want)
	}

	fixture.people.detailErr = apiserver.ErrPersonNotFound
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/9"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("a person the caller may not know of: status = %d, want 404", recorder.Code)
	}
	if got := fixture.people.lastVisible; !reflect.DeepEqual(got, want) {
		t.Errorf("the person was read as %+v, want %+v", got, want)
	}
}
