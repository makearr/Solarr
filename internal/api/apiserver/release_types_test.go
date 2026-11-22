package apiserver_test

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

// A library's release types read as the default, save as chosen, and refuse an empty choice.
func TestLibraryReleaseTypes_ReadSaveAndRefuseNone(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	target := fmt.Sprintf("/api/v1/categories/%d/release-types", category.ID)

	if recorder := fixture.do(authedGet(t, cookies, target)); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"types":["Album","EP","Single"]`) {
		t.Fatalf("default: %d %s", recorder.Code, recorder.Body)
	}
	if recorder := fixture.do(authedPut(t, cookies, target, `{"types":["Album"]}`)); recorder.Code != http.StatusOK {
		t.Fatalf("save: %d %s", recorder.Code, recorder.Body)
	}
	if got, _ := fixture.cache.Snapshot().Category(category.ID); !reflect.DeepEqual(got.MonitorReleaseTypes, []string{"Album"}) {
		t.Errorf("stored = %v, want [Album]", got.MonitorReleaseTypes)
	}
	if recorder := fixture.do(authedPut(t, cookies, target, `{"types":[]}`)); recorder.Code != http.StatusBadRequest {
		t.Errorf("none: %d, want 400", recorder.Code)
	}
}
