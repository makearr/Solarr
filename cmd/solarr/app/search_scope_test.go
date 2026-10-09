package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// One search for every media type: a season, an episode of it, an album; each reaches its type's search, which
// stops at the missing indexer, and a scope nothing wanted covers stops before.
func TestWorkSearch_ResolvesTheScopeForEveryType(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	_, _, episodes := server.monitored(t, ids.series)
	_, _, tracks := server.monitored(t, ids.album)
	var track int64
	for _, id := range tracks {
		track = id
	}
	for _, tc := range []struct {
		target, body string
		status       int
		title        string
	}{
		{fmt.Sprintf("/works/%d/grab?group=s01", ids.series), "", http.StatusConflict, "No indexers"},
		{fmt.Sprintf("/works/%d/search?part=%d", ids.series, episodes["s02:e01"]), "", http.StatusConflict, "No indexers"},
		{fmt.Sprintf("/works/%d/search?group=s01", ids.series), `{"series":"Other"}`, http.StatusBadRequest, "Bad request"},
		{fmt.Sprintf("/works/%d/grab?group=s09", ids.series), "", http.StatusConflict, "Not wanted"},
		{fmt.Sprintf("/works/%d/grab", ids.series), "", http.StatusConflict, "No indexers"},
		{fmt.Sprintf("/works/%d/grab", ids.album), "", http.StatusConflict, "No indexers"},
		{fmt.Sprintf("/works/%d/search?part=%d", ids.album, track), "", http.StatusConflict, "Whole items only"},
		{fmt.Sprintf("/works/%d/search?part=%d", ids.album, episodes["s01:e01"]), "", http.StatusNotFound, "Not found"},
		{fmt.Sprintf("/works/%d/candidates/1/send?part=%d", ids.album, episodes["s01:e01"]), "", http.StatusNotFound, "Not found"},
	} {
		status, body := server.post(t, tc.target, tc.body)
		var problem struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal([]byte(body), &problem)
		if status != tc.status || problem.Title != tc.title || status == http.StatusNotFound && !strings.Contains(body, "no such part") {
			t.Errorf("POST %s: %d %q, want %d %q (%s)", tc.target, status, problem.Title, tc.status, tc.title, body)
		}
	}
	if candidates := string(server.get(t, fmt.Sprintf("/works/%d/candidates?group=s01", ids.series))); candidates != "[]\n" {
		t.Errorf("candidates of a season never searched = %q, want []", candidates)
	}
}
