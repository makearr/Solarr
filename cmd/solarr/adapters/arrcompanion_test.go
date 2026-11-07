package adapters

import (
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
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
