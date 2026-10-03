package arrimport_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// fakeTransport answers canned responses keyed by the exact URL requested,
// and records every request it saw (headers included) -- the same shape
// musicbrainz's own tests use, duplicated here rather than exported across
// packages for a test-only type.
type fakeTransport struct {
	responses map[string]fakeResponse
	requested []*http.Request
}

type fakeResponse struct {
	status int
	body   string
}

func (f *fakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f.requested = append(f.requested, request)
	response, known := f.responses[request.URL.String()]
	if !known {
		return nil, errors.New("fake: no response configured for " + request.URL.String())
	}
	return &http.Response{
		StatusCode: response.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(response.body)),
		Request:    request,
	}, nil
}

func newTestClient(t *testing.T, transport http.RoundTripper) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: "lidarr-test", UserAgent: "test", Transport: transport,
	})
}

func TestLidarrClient_SystemStatusSendsTheAPIKeyHeader(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://lidarr.local/api/v1/system/status": {status: 200, body: `{"version":"2.5.1.1"}`},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://lidarr.local", "the-real-key")

	status, err := client.SystemStatus(t.Context())
	if err != nil {
		t.Fatalf("SystemStatus: %v", err)
	}
	if status.Version != "2.5.1.1" {
		t.Errorf("Version = %q, want %q", status.Version, "2.5.1.1")
	}
	if len(transport.requested) != 1 {
		t.Fatalf("requested %d times, want 1", len(transport.requested))
	}
	if got := transport.requested[0].Header.Get("X-Api-Key"); got != "the-real-key" {
		t.Errorf("X-Api-Key header = %q, want %q", got, "the-real-key")
	}
}

func TestLidarrClient_ArtistsDecodesTheRealResponseShape(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://lidarr.local/api/v1/artist": {status: 200, body: `[
			{"id":1,"artistName":"Vutu Doci","sortName":"Bush, Vutu",
			 "foreignArtistId":"3c8e8b8c-102c-4cc0-83e8-a5b4bc430b97",
			 "monitored":true,"rootFolderPath":"/music","qualityProfileId":1,"tags":[1,2]}
		]`},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://lidarr.local", "key")

	artists, err := client.Artists(t.Context())
	if err != nil {
		t.Fatalf("Artists: %v", err)
	}
	if len(artists) != 1 {
		t.Fatalf("got %d artists, want 1", len(artists))
	}
	got := artists[0]
	if got.ArtistName != "Vutu Doci" || got.ForeignArtistID != "3c8e8b8c-102c-4cc0-83e8-a5b4bc430b97" ||
		!got.Monitored || got.RootFolderPath != "/music" || len(got.Tags) != 2 {
		t.Errorf("artist = %+v, want the full decoded shape", got)
	}
}

func TestLidarrClient_AlbumsQueriesByArtistID(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://lidarr.local/api/v1/album?artistId=7": {status: 200, body: `[
			{"id":42,"title":"The Sezo Siluko","artistId":7,
			 "foreignAlbumId":"85513-...replace","monitored":true}
		]`},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://lidarr.local", "key")

	albums, err := client.Albums(t.Context(), 7)
	if err != nil {
		t.Fatalf("Albums: %v", err)
	}
	if len(albums) != 1 || albums[0].Title != "The Sezo Siluko" || albums[0].ArtistID != 7 {
		t.Errorf("albums = %+v, want one album for artist 7", albums)
	}
}

// A response over the size limit must be reported as ErrResponseTooLarge,
// not fed truncated to json.Unmarshal (which fails with a confusing
// "unexpected end of JSON input" that gives no hint why) -- found live
// against a real prolific artist's own Albums response (2,050 albums,
// measured 28 MiB), which silently truncated at the old 16 MiB limit.
func TestLidarrClient_ArtistsRefusesAResponseOverTheSizeLimit(t *testing.T) {
	// One byte over the limit: a valid-looking (if truncated) JSON array
	// padded with whitespace, so a bug that went back to plain truncate-
	// and-decode would hit "unexpected end of JSON input" here too, not
	// some unrelated syntax error -- the size check has to fire first.
	oversized := "[" + strings.Repeat(" ", 128<<20) + "]"
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://lidarr.local/api/v1/artist": {status: 200, body: oversized},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://lidarr.local", "key")

	_, err := client.Artists(t.Context())
	if !errors.Is(err, arrimport.ErrResponseTooLarge) {
		t.Fatalf("err = %v, want it to wrap ErrResponseTooLarge", err)
	}
}

func TestLidarrClient_SystemStatusReportsAnUnauthorizedResponse(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://lidarr.local/api/v1/system/status": {status: 401, body: ""},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://lidarr.local", "wrong-key")

	_, err := client.SystemStatus(t.Context())
	if err == nil {
		t.Fatal("SystemStatus succeeded with a 401 response")
	}
	var statusErr *httpx.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err = %v, want it to wrap *httpx.StatusError", err)
	}
	if statusErr.Status != 401 {
		t.Errorf("StatusError.Status = %d, want 401", statusErr.Status)
	}
}

// Prowlarr and Lidarr answer the same v1 shape; the secrets come back masked and are not read.
func TestLidarrClient_DownloadClientsReadsAddressAndCategory(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://prowlarr.local/api/v1/downloadclient": {status: 200, body: `[
			{"name":"SABnzbd","implementation":"Sabnzbd","protocol":"usenet","enable":true,"fields":[
				{"name":"host","value":"10.0.0.5"},{"name":"port","value":8080},{"name":"useSsl","value":false},
				{"name":"urlBase","value":"/sab/"},{"name":"apiKey","value":"********"},{"name":"category","value":"arr"}]},
			{"name":"Torrent","implementation":"QBittorrent","protocol":"torrent","enable":false,"fields":[
				{"name":"host","value":"qbit"},{"name":"port","value":8086},{"name":"useSsl","value":true},
				{"name":"urlBase","value":null},{"name":"username","value":"admin"},{"name":"password","value":"********"},{"name":"musicCategory","value":"music"}]}
		]`},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://prowlarr.local", "key")

	clients, err := client.DownloadClients(t.Context())
	if err != nil {
		t.Fatalf("DownloadClients: %v", err)
	}
	want := []arrimport.DownloadClient{
		{Name: "SABnzbd", Implementation: "Sabnzbd", Protocol: "usenet", Enabled: true, Host: "10.0.0.5", Port: 8080, URLBase: "/sab/", Category: "arr"},
		{Name: "Torrent", Implementation: "QBittorrent", Protocol: "torrent", Host: "qbit", Port: 8086, UseSSL: true, Category: "music", Username: "admin"},
	}
	if !reflect.DeepEqual(clients, want) {
		t.Errorf("clients = %+v, want %+v", clients, want)
	}
	if got := []string{clients[0].BaseURL(), clients[1].BaseURL()}; !reflect.DeepEqual(got, []string{"http://10.0.0.5:8080/sab", "https://qbit:8086"}) {
		t.Errorf("base URLs = %v", got)
	}
}

func TestSonarrClient_SeriesReadsTheV3APIWithTheKey(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://sonarr.local/api/v3/series": {status: 200, body: `[{"id":7,"title":"Show","tvdbId":123,"monitored":true,` +
			`"rootFolderPath":"/series","seasons":[{"seasonNumber":1,"monitored":false}]}]`},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://sonarr.local", "key").Sonarr()
	series, err := client.Series(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []arrimport.SonarrSeries{{
		ID: 7, Title: "Show", TVDBID: 123, Monitored: true, RootFolderPath: "/series",
		Seasons: []arrimport.SonarrSeason{{SeasonNumber: 1}},
	}}
	if !reflect.DeepEqual(series, want) {
		t.Fatalf("series = %+v, want %+v", series, want)
	}
	if got := transport.requested[0].Header.Get("X-Api-Key"); got != "key" || transport.requested[0].Method != http.MethodGet {
		t.Fatalf("request = %s key %q", transport.requested[0].Method, got)
	}
}

func TestSonarrClient_ReadsEpisodesAndQualityProfiles(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"http://sonarr.local/api/v3/episode?seriesId=7": {status: 200, body: `[{"seasonNumber":1,"episodeNumber":2,"monitored":false,"hasFile":true},{"seasonNumber":0,"episodeNumber":1,"monitored":true}]`},
		"http://sonarr.local/api/v3/qualityprofile":     {status: 200, body: `[{"id":1,"name":"Any","cutoff":1}]`},
	}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://sonarr.local", "key").Sonarr()
	episodes, err := client.Episodes(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if want := []arrimport.SonarrEpisode{{SeasonNumber: 1, EpisodeNumber: 2, HasFile: true}, {SeasonNumber: 0, EpisodeNumber: 1, Monitored: true}}; !reflect.DeepEqual(episodes, want) {
		t.Errorf("episodes = %+v, want %+v", episodes, want)
	}
	profiles, err := client.QualityProfiles(t.Context())
	if err != nil || len(profiles) != 1 || profiles[0].Name != "Any" || profiles[0].ID != 1 {
		t.Errorf("profiles = %+v, err = %v", profiles, err)
	}
}
