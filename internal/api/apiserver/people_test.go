package apiserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
)

// fakePeopleReader mirrors fakeWorksReader: records the arguments it was
// called with and answers canned rows, without a real library.db.
type fakePeopleReader struct {
	rows       []apiserver.PersonRow
	detail     apiserver.PersonDetailRow
	detailErr  error
	lastFilter apiserver.PersonFilter
	lastCursor apiserver.PersonCursor
	lastID     int64
}

func (f *fakePeopleReader) ListPeople(
	_ context.Context, filter apiserver.PersonFilter, cursor apiserver.PersonCursor, pageSize int,
) ([]apiserver.PersonRow, error) {
	f.lastFilter = filter
	f.lastCursor = cursor
	if len(f.rows) > pageSize {
		return f.rows[:pageSize], nil
	}
	return f.rows, nil
}

func (f *fakePeopleReader) GetPersonDetail(_ context.Context, id int64) (apiserver.PersonDetailRow, error) {
	f.lastID = id
	if f.detailErr != nil {
		return apiserver.PersonDetailRow{}, f.detailErr
	}
	return f.detail, nil
}

// fakePeopleSubscriber records the arguments Subscribe was called with.
type fakePeopleSubscriber struct {
	err                    error
	called                 bool
	lastPersonID           int64
	lastUserID             int64
	lastMediaTypes         []string
	lastRoles              []string
	lastMonitorBack        bool
	lastExcludeSecondaries *[]string
}

func (f *fakePeopleSubscriber) Subscribe(
	_ context.Context, personID, userID int64, mediaTypes, roles []string,
	monitorBackCatalogue bool, excludeSecondaryTypes *[]string,
) error {
	f.called = true
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
	fixture.people.rows = []apiserver.PersonRow{
		{ID: 1, Name: "Katupoken", SortName: "Katupoken", Disambiguation: &disambiguation, Monitored: true},
		{ID: 2, Name: "Vutu Doci", SortName: "Bush, Vutu"},
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

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people?monitored=true&q=Rad"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	if fixture.people.lastFilter.Monitored == nil || !*fixture.people.lastFilter.Monitored {
		t.Errorf("Monitored = %v, want true", fixture.people.lastFilter.Monitored)
	}
	if fixture.people.lastFilter.NamePrefix != "Rad" {
		t.Errorf("NamePrefix = %q, want Rad", fixture.people.lastFilter.NamePrefix)
	}
}

func TestGetPerson_ResponseShape(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	biography := "English singer-songwriter."
	primaryType := "Album"
	wantedState := "PARTIAL"
	fixture.people.detail = apiserver.PersonDetailRow{
		PersonRow: apiserver.PersonRow{ID: 7, Name: "Vutu Doci", SortName: "Bush, Vutu"},
		Biography: &biography,
		Aliases:   []string{"Cathy"},
		Genres:    []string{"Art Pop"},
		Discography: []apiserver.PersonWorkRow{{
			ID: 41, MediaType: "music", Title: "Cotevo of Love", SortTitle: "cotevo of love",
			PrimaryType: &primaryType, WantedState: &wantedState, OnDisk: true,
		}},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/7"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.people.lastID != 7 {
		t.Errorf("GetPersonDetail called with id %d, want 7", fixture.people.lastID)
	}

	var body struct {
		Name        string   `json:"name"`
		Biography   *string  `json:"biography"`
		Aliases     []string `json:"aliases"`
		Genres      []string `json:"genres"`
		Discography []struct {
			ID          int64   `json:"id"`
			Title       string  `json:"title"`
			PrimaryType *string `json:"primary_type"`
			WantedState *string `json:"wanted_state"`
			OnDisk      bool    `json:"on_disk"`
		} `json:"discography"`
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
	if len(body.Discography) != 1 || body.Discography[0].Title != "Cotevo of Love" {
		t.Fatalf("Discography = %+v, want [Cotevo of Love]", body.Discography)
	}
	work := body.Discography[0]
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

	fixture.people.detailErr = sql.ErrNoRows

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

func TestSubscribePerson_ReachesTheSubscriberWithTheCallersUserID(t *testing.T) {
	fixture := newWorksFixture(t)
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

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
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/people/7/subscribe", `{"media_types":[]}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for empty media_types", recorder.Code)
	}
	if fixture.subscriber.called {
		t.Error("Subscribe was called despite the invalid body")
	}
}

func TestSubscribePerson_MissingPersonAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

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
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

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
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

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
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

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
