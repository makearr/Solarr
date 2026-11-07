package arrimport_test

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/makearr/Solarr/internal/importing/arrimport"
)

func bodyOf(t *testing.T, transport *fakeTransport, index int) map[string]any {
	t.Helper()
	data, err := io.ReadAll(transport.requested[index].Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("body %q: %v", data, err)
	}
	return body
}

func TestRadarrClient_AddMovieAsksRadarrToSearchAndAnswersItsId(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://radarr.local/api/v3/movie": {status: 201, body: `{"id":42,"title":"Lonavi"}`},
	}}
	radarr := arrimport.NewRadarrClient(newTestClient(t, transport), "http://radarr.local", "key")

	id, err := radarr.AddMovie(t.Context(), arrimport.RadarrAdd{Title: "Lonavi", TMDBID: 603, QualityProfileID: 4, RootFolderPath: "/movies"})
	if err != nil || id != 42 {
		t.Fatalf("AddMovie = %d, %v; want 42", id, err)
	}
	request := transport.requested[0]
	body := bodyOf(t, transport, 0)
	options, _ := body["addOptions"].(map[string]any)
	if request.Method != "POST" || request.Header.Get("X-Api-Key") != "key" || body["tmdbId"] != float64(603) ||
		body["monitored"] != true || options["searchForMovie"] != true || body["rootFolderPath"] != "/movies" {
		t.Errorf("request %s, body %v", request.Method, body)
	}
}

func TestRadarrClient_SearchMovieSendsTheCommand(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://radarr.local/api/v3/command": {status: 201, body: `{"id":1}`},
	}}
	radarr := arrimport.NewRadarrClient(newTestClient(t, transport), "http://radarr.local", "key")
	if err := radarr.SearchMovie(t.Context(), 42); err != nil {
		t.Fatalf("SearchMovie: %v", err)
	}
	body := bodyOf(t, transport, 0)
	if body["name"] != "MoviesSearch" {
		t.Errorf("body = %v, want a MoviesSearch command", body)
	}
}

func TestSonarrClient_AddSeriesMonitorsAllAndSearchesForMissingEpisodes(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://sonarr.local/api/v3/series": {status: 201, body: `{"id":7}`},
	}}
	sonarr := arrimport.NewLidarrClient(newTestClient(t, transport), "http://sonarr.local", "key").Sonarr()
	id, err := sonarr.AddSeries(t.Context(), arrimport.SonarrAdd{Title: "Zurafo", TVDBID: 81189, QualityProfileID: 3, LanguageProfileID: 1, RootFolderPath: "/series"})
	if err != nil || id != 7 {
		t.Fatalf("AddSeries = %d, %v; want 7", id, err)
	}
	body := bodyOf(t, transport, 0)
	options, _ := body["addOptions"].(map[string]any)
	if body["tvdbId"] != float64(81189) || options["searchForMissingEpisodes"] != true || body["languageProfileId"] != float64(1) {
		t.Errorf("body = %v", body)
	}
}
