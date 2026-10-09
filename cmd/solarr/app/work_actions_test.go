package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A series' type is an editable facet of the one Edit: set, read back, returned to its sources; an album has none.
func TestWorkEdit_SetsTheTypesEditableFacets(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	edit := fmt.Sprintf("/works/%d/edit", ids.series)
	seriesType := func() string {
		var work struct {
			Facets map[string][]string `json:"facets"`
		}
		if err := json.Unmarshal(server.get(t, fmt.Sprintf("/works/%d", ids.series)), &work); err != nil {
			t.Fatal(err)
		}
		return strings.Join(work.Facets["series_type"], ",")
	}

	if status, body := server.send(t, http.MethodPut, edit, `{"monitored":true,"profile_id":null,"tags":[],"fields":{"series_type":"anime"}}`); status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	if got := seriesType(); got != "anime" {
		t.Errorf("series type after the edit = %q, want anime", got)
	}
	if got := string(server.get(t, edit)); !strings.Contains(got, `"fields":{"series_type":"anime"}`) {
		t.Errorf("the edit reads back %s, want the user's anime", got)
	}
	if status, _ := server.send(t, http.MethodPut, edit, `{"monitored":true,"profile_id":null,"tags":[],"fields":{"series_type":""}}`); status != http.StatusOK {
		t.Fatalf("clearing: %d", status)
	}
	if got := string(server.get(t, edit)); strings.Contains(got, "series_type") {
		t.Errorf("after returning the type to its sources the edit reads %s", got)
	}
	for target, body := range map[string]string{
		edit:                                     `{"monitored":true,"profile_id":null,"tags":[],"fields":{"series_type":"sitcom"}}`,
		fmt.Sprintf("/works/%d/edit", ids.album): `{"monitored":true,"profile_id":null,"tags":[],"fields":{"series_type":"anime"}}`,
	} {
		if status, _ := server.send(t, http.MethodPut, target, body); status != http.StatusBadRequest {
			t.Errorf("PUT %s %s: status %d, want 400", target, body, status)
		}
	}
}

// Removed and excluded, a series goes although its files stay (its scan leaves it out); an album with files stays.
func TestRemoveWork_ExcludedGoesWhereTheScanHonoursIt(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	for _, tc := range []struct {
		work        int64
		wantDeleted bool
	}{{ids.series, true}, {ids.album, false}} {
		status, body := server.post(t, fmt.Sprintf("/works/%d/remove", tc.work), `{"exclude":true}`)
		if status != http.StatusOK || !strings.Contains(body, fmt.Sprintf(`"work_deleted":%v`, tc.wantDeleted)) {
			t.Errorf("remove work %d: %d %s, want work_deleted %v", tc.work, status, body, tc.wantDeleted)
		}
	}
}

// A list orders by a type's sortable facets and refuses one that is not; a work removed and excluded is listed as such
// for its type and allowed back.
func TestWorks_SortByAFacetAndListTheExcluded(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	for target, want := range map[string]int{
		fmt.Sprintf("/works?category_id=%d&sort=facet:network", ids.tv):          http.StatusOK,
		fmt.Sprintf("/works?category_id=%d&sort=facet:network&dir=desc", ids.tv): http.StatusOK,
		fmt.Sprintf("/works?category_id=%d&sort=facet:runtime", ids.tv):          http.StatusBadRequest,
		fmt.Sprintf("/works?category_id=%d&sort=facet:label", ids.music):         http.StatusOK,
		fmt.Sprintf("/works?category_id=%d&sort=network", ids.tv):                http.StatusBadRequest,
	} {
		if status, body := server.send(t, http.MethodGet, target, ""); status != want {
			t.Errorf("GET %s: %d %s, want %d", target, status, body, want)
		}
	}
	if status, body := server.post(t, fmt.Sprintf("/works/%d/remove", ids.series), `{"exclude":true}`); status != http.StatusOK {
		t.Fatalf("remove: %d %s", status, body)
	}
	var excluded []struct {
		Title, Provider string
		ExternalID      string `json:"external_id"`
	}
	if err := json.Unmarshal(server.get(t, "/works/exclusions?media_type=tv"), &excluded); err != nil || len(excluded) != 1 || excluded[0].Title != "The Marble Stations" {
		t.Fatalf("excluded series = %+v, %v; want the one removed", excluded, err)
	}
	if got := string(server.get(t, "/works/exclusions?media_type=music")); got != "[]\n" {
		t.Errorf("excluded albums = %s, want none", got)
	}
	allow := fmt.Sprintf("/works/exclusions?media_type=tv&provider=%s&external_id=%s", excluded[0].Provider, excluded[0].ExternalID)
	if status, body := server.send(t, http.MethodDelete, allow, ""); status != http.StatusNoContent {
		t.Fatalf("allow: %d %s", status, body)
	}
	if got := string(server.get(t, "/works/exclusions?media_type=tv")); got != "[]\n" {
		t.Errorf("after allowing it again, excluded series = %s", got)
	}
}

// Each library adds what its type declares: a series is no album, an artist no series; a query under the floor is
// refused before any source is asked.
func TestLookupWorks_TakesTheKindsOfTheLibrarysType(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	for target, want := range map[string]int{
		fmt.Sprintf("/works/lookup?category_id=%d&q=marble&kind=album", ids.tv):     http.StatusBadRequest,
		fmt.Sprintf("/works/lookup?category_id=%d&q=marble&kind=series", ids.music): http.StatusBadRequest,
		fmt.Sprintf("/works/lookup?category_id=%d&q=m", ids.tv):                     http.StatusBadRequest,
		fmt.Sprintf("/works/lookup?category_id=%d&q=m&kind=artist", ids.music):      http.StatusBadRequest,
	} {
		if status, body := server.send(t, http.MethodGet, target, ""); status != want {
			t.Errorf("GET %s: %d %s, want %d", target, status, body, want)
		}
	}
	body := fmt.Sprintf(`{"category_id":%d,"kind":"album","provider":"tvdb","external_id":"1"}`, ids.tv)
	if status, answer := server.post(t, "/works", body); status != http.StatusBadRequest {
		t.Errorf("adding an album to a TV library: %d %s, want 400", status, answer)
	}
}

// Saving a series' Edit with only its tags changed keeps its season-by-season monitoring; switching it off reaches
// every season.
func TestWorkEdit_KeepsTheGroupsMonitoringUnlessItChanges(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	server.monitor(t, fmt.Sprintf("/works/%d/monitor", ids.series), `{"monitored":false,"group":"s02"}`)
	edit := fmt.Sprintf("/works/%d/edit", ids.series)
	if got := string(server.get(t, edit)); !strings.Contains(got, `"monitored":true`) {
		t.Fatalf("the edit reads %s, want monitored (a season is)", got)
	}
	if status, body := server.send(t, http.MethodPut, edit, `{"monitored":true,"profile_id":null,"tags":["Watch"]}`); status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	groups, _, _ := server.monitored(t, ids.series)
	if !groups["s01"] || groups["s02"] {
		t.Errorf("after saving tags, seasons = %v; want s01 still monitored and s02 still not", groups)
	}
	if status, _ := server.send(t, http.MethodPut, edit, `{"monitored":false,"profile_id":null,"tags":["Watch"]}`); status != http.StatusOK {
		t.Fatal("unmonitor failed")
	}
	if groups, _, _ = server.monitored(t, ids.series); groups["s01"] || groups["s02"] {
		t.Errorf("after unmonitoring, seasons = %v; want none", groups)
	}
}

// Editing a selection sets the type's editable fields and applies a monitor option to every work of it.
func TestBulkEdit_SetsFieldsAndAMonitorOption(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	body := fmt.Sprintf(`{"ids":[%d],"fields":{"series_type":"anime"},"monitor_option":"none"}`, ids.series)
	if status, answer := server.post(t, "/works/bulk/edit", body); status != http.StatusOK || !strings.Contains(answer, `"count":1`) {
		t.Fatalf("bulk edit: %d %s", status, answer)
	}
	if got := string(server.get(t, fmt.Sprintf("/works/%d/edit", ids.series))); !strings.Contains(got, `"fields":{"series_type":"anime"}`) || !strings.Contains(got, `"monitored":false`) {
		t.Errorf("after the bulk edit the series reads %s; want anime and nothing monitored", got)
	}
}

// A series' poster is chosen through the one picture choice: its sources' posters listed, the pick kept first.
func TestWorkPictures_ChooseASeriesPoster(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	var listed struct {
		Options []struct {
			URL string `json:"url"`
		} `json:"options"`
	}
	if err := json.Unmarshal(server.get(t, fmt.Sprintf("/works/%d/artwork/options", ids.series)), &listed); err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, option := range listed.Options {
		urls = append(urls, option.URL)
	}
	want := []string{"https://artworks.thetvdb.com/marble-1.jpg", "https://artworks.thetvdb.com/marble-2.jpg"}
	if strings.Join(urls, " ") != strings.Join(want, " ") {
		t.Fatalf("options = %v, want %v", urls, want)
	}

	pick := fmt.Sprintf("/works/%d/artwork", ids.series)
	if status, body := server.send(t, http.MethodPut, pick, `{"url":"https://artworks.thetvdb.com/elsewhere.jpg"}`); status != http.StatusBadRequest {
		t.Errorf("an unlisted poster: %d %s, want 400", status, body)
	}
	if status, body := server.send(t, http.MethodPut, pick, fmt.Sprintf(`{"url":%q}`, want[1])); status != http.StatusNoContent {
		t.Fatalf("pick: %d %s", status, body)
	}
	var work struct {
		Pictures struct {
			Poster string `json:"poster"`
		} `json:"pictures"`
	}
	if err := json.Unmarshal(server.get(t, fmt.Sprintf("/works/%d", ids.series)), &work); err != nil {
		t.Fatal(err)
	}
	if work.Pictures.Poster != want[1] {
		t.Errorf("poster after the pick = %q, want %q", work.Pictures.Poster, want[1])
	}
}
