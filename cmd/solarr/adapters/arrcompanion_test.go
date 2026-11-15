package adapters

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestCompanionTarget_SendsMoviesByTMDBToRadarrAndSeriesByTVDBToSonarrOnly(t *testing.T) {
	for _, test := range []struct {
		added   apiserver.AddedWork
		app     settings.ArrApp
		id      int64
		handled bool
	}{
		{apiserver.AddedWork{Kind: "movie", Provider: "tmdb", ExternalID: "603"}, settings.ArrAppRadarr, 603, true},
		{apiserver.AddedWork{Kind: "series", Provider: "tvdb", ExternalID: "81189"}, settings.ArrAppSonarr, 81189, true},
		{apiserver.AddedWork{Kind: "series", Provider: "tvmaze", ExternalID: "5"}, "", 0, false},
		{apiserver.AddedWork{Kind: "movie", Provider: "tmdb", ExternalID: "tt0133093"}, "", 0, false},
		{apiserver.AddedWork{Kind: "album", Provider: "musicbrainz", ExternalID: "1"}, "", 0, false},
	} {
		app, id, handled := companionTarget(test.added)
		if app != test.app || id != test.id || handled != test.handled {
			t.Errorf("%+v: got %q %d %v, want %q %d %v", test.added, app, id, handled, test.app, test.id, test.handled)
		}
	}
}

func TestMostUsed_PicksTheCommonestAndIgnoresTheEmptyKey(t *testing.T) {
	if got := mostUsed(map[int64]int{0: 9, 4: 2, 7: 3}); got != 7 {
		t.Errorf("mostUsed profiles = %d, want 7", got)
	}
	if got := mostUsed(map[string]int{"": 5, "/movies": 2, "/old": 2}); got != "/movies" {
		t.Errorf("mostUsed roots = %q, want /movies (ties to the smaller)", got)
	}
	if got := mostUsed(map[int64]int{}); got != 0 {
		t.Errorf("mostUsed of nothing = %d, want 0", got)
	}
}

func TestMediaTarget_SendsAlbumsAndArtistsToLidarrAndScenesToWhisparr(t *testing.T) {
	for _, test := range []struct {
		added apiserver.AddedWork
		app   settings.ArrApp
		link  string
	}{
		{apiserver.AddedWork{Kind: "album", Provider: "musicbrainz:release-group", ExternalID: "a1"}, settings.ArrAppLidarr, "album"},
		{apiserver.AddedWork{Kind: "artist", Provider: "musicbrainz:artist", ExternalID: "a2"}, settings.ArrAppLidarr, "artist"},
		{apiserver.AddedWork{Kind: "adult", Provider: "stashdb", ExternalID: "s1"}, settings.ArrAppWhisparr, "scene"},
		{apiserver.AddedWork{Kind: "adult", Provider: "theporndb", ExternalID: "s2"}, settings.ArrAppWhisparr, "scene"},
	} {
		target, ok := mediaTarget(test.added)
		if !ok || target.app != test.app || target.linkKind != test.link || target.ref != test.added.ExternalID {
			t.Errorf("%+v: got %+v %v", test.added, target, ok)
		}
	}
	if _, ok := mediaTarget(apiserver.AddedWork{Kind: "album", Provider: "tvdb", ExternalID: "1"}); ok {
		t.Error("an album from another provider was handed to Lidarr")
	}
}

func TestSceneHasID_MatchesTheForeignIdByItsSuffixOrAFieldOfItsOwn(t *testing.T) {
	scene := map[string]any{"foreignId": "tpdbId:84833799", "title": "x"}
	if !sceneHasID(scene, "84833799") || sceneHasID(scene, "other") || !sceneHasID(map[string]any{"stashId": "abc"}, "abc") {
		t.Error("scene id matching is wrong")
	}
}

// recordingArr answers what an *Arr's list and add endpoints would and records every write.
type recordingArr struct {
	movies string
	writes []string
}

func (r *recordingArr) RoundTrip(request *http.Request) (*http.Response, error) {
	body := "[]"
	if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/movie") {
		body = r.movies
	}
	if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/qualityprofile") {
		body = `[{"id":1,"name":"Any"}]`
	}
	if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/rootfolder") {
		body = `[{"path":"/movies"}]`
	}
	if request.Method != http.MethodGet {
		payload, _ := io.ReadAll(request.Body)
		r.writes = append(r.writes, request.Method+" "+request.URL.Path+" "+string(payload))
		body = `{"id":5}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func radarrOver(transport http.RoundTripper) arrimport.RadarrClient {
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return arrimport.NewLidarrClient(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
		Provider: "companion-test", UserAgent: "test", Transport: transport,
	}), "http://radarr.local", "key").Radarr()
}

// SYNC-3: what the add dialog chose reaches the *Arr; an unmonitored add is not monitored or searched there.
func TestCompanionMovie_FollowsTheAddDialog(t *testing.T) {
	for name, tc := range map[string]struct {
		movies string
		added  apiserver.AddedWork
		want   []string
	}{
		"unmonitored add":          {"[]", apiserver.AddedWork{Title: "Lonavi"}, []string{`"monitored":false`, `"searchForMovie":false`}},
		"monitored, no search":     {"[]", apiserver.AddedWork{Monitored: true}, []string{`"monitored":true`, `"searchForMovie":false`}},
		"monitored and searched":   {"[]", apiserver.AddedWork{Monitored: true, Search: true}, []string{`"monitored":true`, `"searchForMovie":true`}},
		"held, not monitored here": {`[{"id":9,"tmdbId":603,"monitored":false}]`, apiserver.AddedWork{}, nil},
	} {
		arr := &recordingArr{movies: tc.movies}
		if _, err := (ArrSync{}).companionMovie(t.Context(), radarrOver(arr), 603, tc.added); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		joined := strings.Join(arr.writes, "\n")
		if tc.want == nil && joined != "" {
			t.Errorf("%s: wrote %s, want nothing", name, joined)
		}
		for _, want := range tc.want {
			if !strings.Contains(joined, want) {
				t.Errorf("%s: writes = %s, want %s", name, joined, want)
			}
		}
	}
}

func TestSonarrMonitor_MapsTheAddDialogsOptions(t *testing.T) {
	for option, want := range map[string]string{"": "future", "future": "future", "all": "all", "first_season": "firstSeason", "last_season": "lastSeason", "none": "none", "pilot": "pilot", "missing": "missing", "existing": "existing"} {
		if got, warning := sonarrMonitor(option); got != want || warning != "" {
			t.Errorf("sonarrMonitor(%q) = %q, %q, want %q", option, got, warning, want)
		}
	}
	if got, warning := sonarrMonitor("recent"); got != "none" || warning == "" {
		t.Errorf("an option Sonarr lacks = %q, %q, want none with a warning", got, warning)
	}
}
