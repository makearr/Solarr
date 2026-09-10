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
	err             error
	called          bool
	lastPersonID    int64
	lastUserID      int64
	lastMediaTypes  []string
	lastRoles       []string
	lastMonitorBack bool
}

func (f *fakePeopleSubscriber) Subscribe(
	_ context.Context, personID, userID int64, mediaTypes, roles []string, monitorBackCatalogue bool,
) error {
	f.called = true
	f.lastPersonID = personID
	f.lastUserID = userID
	f.lastMediaTypes = mediaTypes
	f.lastRoles = roles
	f.lastMonitorBack = monitorBackCatalogue
	return f.err
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
	fixture.people.detail = apiserver.PersonDetailRow{
		PersonRow:   apiserver.PersonRow{ID: 7, Name: "Vutu Doci", SortName: "Bush, Vutu"},
		Biography:   &biography,
		Aliases:     []string{"Cathy"},
		Discography: []apiserver.PersonWorkRow{{ID: 41, MediaType: "music", Title: "Cotevo of Love", SortTitle: "cotevo of love"}},
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
		Discography []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
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
	if len(body.Discography) != 1 || body.Discography[0].Title != "Cotevo of Love" {
		t.Errorf("Discography = %+v, want [Cotevo of Love]", body.Discography)
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
