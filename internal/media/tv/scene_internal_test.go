package tv

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsetv"
)

// TheTVDB's season 1 holds four episodes; the scene released its last two as season 2, numbered 25 and 26 absolute.
func sceneEpisodes() []library.SeriesEpisode {
	return []library.SeriesEpisode{
		{Season: 1, Number: 1, Scene: "s01:e01", SceneAbsolute: "1"},
		{Season: 1, Number: 2, Scene: "s01:e02", SceneAbsolute: "2"},
		{Season: 1, Number: 3, Scene: "s02:e01", SceneAbsolute: "25"},
		{Season: 1, Number: 4, Scene: "s02:e02", SceneAbsolute: "26"},
	}
}

func parsed(t *testing.T, name string) parsetv.Info {
	t.Helper()
	info, ok := parsetv.Parse(name, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatalf("%q does not parse", name)
	}
	return info
}

func TestADownloadIsPlacedByItsSceneNumbers(t *testing.T) {
	scene := newSceneNumbers(sceneEpisodes())
	for name, want := range map[string][]library.PartKey{
		"Example.Show.S02E01.1080p.WEB-DL-GRP":         {{MediaRef: "s01:e03"}},
		"Example.Show.S02E01E02.1080p.WEB-DL-GRP":      {{MediaRef: "s01:e03"}, {MediaRef: "s01:e04"}},
		"Example.Show.S01E04.1080p.WEB-DL-GRP":         {{MediaRef: "s01:e04"}},
		"[Group] Example Show - 25 (1080p) [ABCD1234]": {{MediaRef: "s01:e03"}},
		"[Group] Example Show - 99 (1080p) [ABCD1234]": {{AltScheme: "absolute", AltRef: "99"}},
	} {
		if got := scene.partKeys(parsed(t, name)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: keys = %+v, want %+v", name, got, want)
		}
	}
	if got := (sceneNumbers{}).partKeys(parsed(t, "Example.Show.S02E01.1080p.WEB-DL-GRP")); !reflect.DeepEqual(got, []library.PartKey{{MediaRef: "s02:e01"}}) {
		t.Errorf("unmapped series: keys = %+v, want TheTVDB's own", got)
	}
}

func TestAScenePackHoldsTheEpisodesTheSceneReleasesUnderItsSeason(t *testing.T) {
	episodes := sceneEpisodes()
	w := wanted{season: 1, title: "Example Show", missing: map[int]library.SeriesEpisode{}, scene: newSceneNumbers(episodes)}
	for _, episode := range episodes {
		w.missing[episode.Number] = episode
	}
	w.names = w.titles()
	for name, want := range map[string][]int{
		"Example.Show.S01.1080p.WEB-DL-GRP":     {1, 2},
		"Example.Show.S02.1080p.WEB-DL-GRP":     {3, 4},
		"Example.Show.S01-S02.1080p.WEB-DL-GRP": {1, 2, 3, 4},
	} {
		got, ok := w.coverage(parsed(t, name))
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: covers %v (%v), want %v", name, got, ok, want)
		}
	}
	if got := w.sceneSeasons(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("scene seasons = %v, want 1 and 2", got)
	}
}
