package apiserver_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeCalendar records which libraries each call was scoped to.
type fakeCalendar struct {
	entriesCategories, datesCategories []int64
	rows                               []apiserver.CalendarRow
}

func (f *fakeCalendar) Entries(
	_ context.Context, _, _ string, categoryIDs []int64, _ int,
) ([]apiserver.CalendarRow, error) {
	f.entriesCategories = categoryIDs
	return f.rows, nil
}

func (f *fakeCalendar) Dates(_ context.Context, _, _ string, categoryIDs []int64) ([]apiserver.CalendarDate, error) {
	f.datesCategories = categoryIDs
	return []apiserver.CalendarDate{{Date: "2026-06-01", Releases: 2}}, nil
}

// The calendar shows only libraries the caller can see (2026-09-19 review).
func TestCalendar_ScopedToTheCallersLibraries(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.cache.CreateCategory(t.Context(), "music", "Hidden", 20); err != nil {
		t.Fatal(err)
	}
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatal(err)
	}
	cookies := fixture.signIn(t, "listener")

	for _, target := range []string{
		"/api/v1/calendar?from=2026&to=2026-12-31", "/api/v1/calendar/dates?from=2026&to=2026-12-31",
	} {
		if recorder := fixture.do(authedGet(t, cookies, target)); recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, %s", target, recorder.Code, recorder.Body)
		}
	}
	want := []int64{visible.ID}
	if !reflect.DeepEqual(fixture.calendar.entriesCategories, want) {
		t.Errorf("entries scoped to %v, want %v", fixture.calendar.entriesCategories, want)
	}
	if !reflect.DeepEqual(fixture.calendar.datesCategories, want) {
		t.Errorf("dates scoped to %v, want %v", fixture.calendar.datesCategories, want)
	}
}
