package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

type favoriteKey struct {
	user    int64
	subject string
	id      int64
}

// fakeFavorites keeps hearts in memory, so a test sees what the handler asked of the store and for whom.
type fakeFavorites struct{ hearts map[favoriteKey]bool }

func (f *fakeFavorites) Set(_ context.Context, userID int64, subjectType string, subjectID int64, favorite bool) error {
	if f.hearts == nil {
		f.hearts = map[favoriteKey]bool{}
	}
	f.hearts[favoriteKey{userID, subjectType, subjectID}] = favorite
	return nil
}

func (f *fakeFavorites) IDs(_ context.Context, userID int64, subjectType string) ([]int64, error) {
	ids := []int64{}
	for key, hearted := range f.hearts {
		if hearted && key.user == userID && key.subject == subjectType {
			ids = append(ids, key.id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func TestFavorites_AreTheCallersOwn(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	fixture.createUser(t, "housemate", settings.RoleReadonly)
	listener := fixture.signIn(t, "listener")
	housemate := fixture.signIn(t, "housemate")

	for _, target := range []string{"/api/v1/favorites/work/7", "/api/v1/favorites/work/9", "/api/v1/favorites/person/7"} {
		if recorder := fixture.do(authedPut(t, listener, target, "")); recorder.Code != http.StatusNoContent {
			t.Fatalf("PUT %s = %d (%s)", target, recorder.Code, recorder.Body)
		}
	}
	if recorder := fixture.do(authedDelete(t, listener, "/api/v1/favorites/work/9")); recorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d (%s)", recorder.Code, recorder.Body)
	}

	for _, want := range []struct {
		cookies []*http.Cookie
		subject string
		ids     []int64
	}{{listener, "work", []int64{7}}, {listener, "person", []int64{7}}, {housemate, "work", []int64{}}} {
		recorder := fixture.do(authedGet(t, want.cookies, "/api/v1/favorites?subject_type="+want.subject))
		var got []int64
		if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil || recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d (%s): %v", want.subject, recorder.Code, recorder.Body, err)
		}
		if !slices.Equal(got, want.ids) {
			t.Errorf("hearted %ss = %v, want %v", want.subject, got, want.ids)
		}
	}
}

func TestFavorites_RefuseWhatCannotBeHearted(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/favorites/tag/1", "")); recorder.Code != http.StatusBadRequest {
		t.Errorf("hearting a tag = %d, want 400", recorder.Code)
	}
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/favorites/work/abc", "")); recorder.Code != http.StatusBadRequest {
		t.Errorf("hearting work abc = %d, want 400", recorder.Code)
	}
}
