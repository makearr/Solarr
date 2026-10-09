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
