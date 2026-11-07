package music_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type fakeAlbumWikidata struct {
	albums map[string][]wikidata.AlbumFranchise
	series map[string]string
	parts  map[string][]string
	err    error
}

func (f fakeAlbumWikidata) AlbumFranchises(context.Context, []string) (map[string][]wikidata.AlbumFranchise, error) {
	return f.albums, f.err
}

func (f fakeAlbumWikidata) MusicSeriesItems(context.Context, []string) (map[string]string, error) {
	return f.series, nil
}

func (f fakeAlbumWikidata) ItemFranchises(context.Context, []string) (map[string][]string, error) {
	return f.parts, nil
}

type fakeAlbumSeries struct {
	types  map[string][]string
	series map[string][]musicbrainz.Series
	asked  []string
}

func (f *fakeAlbumSeries) Album(_ context.Context, _ musicbrainz.Priority, mbid, _ string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	return musicbrainz.ReleaseGroup{MBID: mbid, SecondaryTypes: f.types[mbid]}, musicbrainz.Release{}, nil
}

func (f *fakeAlbumSeries) ReleaseGroupSeries(_ context.Context, _ musicbrainz.Priority, mbid string) ([]musicbrainz.Series, bool, error) {
	f.asked = append(f.asked, mbid)
	return f.series[mbid], false, nil
}

func addAlbum(t *testing.T, series library.Series, title, mbid string) int64 {
	t.Helper()
	stored, err := series.Upsert(t.Context(), clock.NewFakeAtEpoch().Now(), library.SeriesWrite{
		Work: library.WorkIdentity{MediaType: string(music.MediaType), CanonicalTitle: title, SortTitle: title},
		IDs:  []library.ExternalIDWrite{{Provider: music.ProviderReleaseGroup, ExternalID: mbid}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored.WorkID
}

func TestCollectionPassMakesScoreSeriesAndFranchiseCollectionsThatCarryTheFranchiseKeys(t *testing.T) {
	opened := openKnownAlbumsLibrary(t)
	franchises := library.NewFranchises(opened)
	albums := library.NewSeries(opened)
	scoreOne := addAlbum(t, albums, "Vexmoor: Original Score", "rg-score-1")
	scoreTwo := addAlbum(t, albums, "Vexmoor Returns: Original Score", "rg-score-2")
	studio := addAlbum(t, albums, "Brightwater", "rg-studio")
	addAlbum(t, albums, "Now That's Vexmoor", "rg-compilation")
	sources := &fakeAlbumSeries{
		types: map[string][]string{"rg-score-1": {"Soundtrack"}, "rg-score-2": {"Soundtrack"}, "rg-compilation": {"Compilation"}},
		series: map[string][]musicbrainz.Series{
			"rg-score-1": {{MBID: "series-1", Name: "Vexmoor Scores", Type: "Release group series"}, {MBID: "series-x", Name: "Label catalogue", Type: "Release series"}},
			"rg-score-2": {{MBID: "series-1", Name: "Vexmoor Scores", Type: "Release group series"}},
		},
	}
	saga := wikidata.Franchise{QID: "Q10", Label: "Vexmoor", Named: true}
	trilogy := wikidata.Franchise{QID: "Q11", Label: "Vexmoor trilogy", Named: true, Parents: []string{"Q10"}}
	source := fakeAlbumWikidata{
		albums: map[string][]wikidata.AlbumFranchise{
			"rg-score-1": {{Franchise: trilogy, Soundtrack: true}, {Franchise: saga, Soundtrack: true}},
			"rg-score-2": {{Franchise: trilogy, Soundtrack: true}},
			// A franchise named on one album of its own makes no collection.
			"rg-studio": {{Franchise: wikidata.Franchise{QID: "Q50", Label: "Brightwater sessions", Named: true}}},
		},
		series: map[string]string{"series-1": "Q12"},
		parts:  map[string][]string{"Q12": {"Q10"}},
	}
	pass := music.CollectionPass{Wikidata: source, MusicBrainz: sources, Franchises: franchises, Logger: slog.New(slog.DiscardHandler)}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sources.asked, []string{"rg-score-1", "rg-score-2", "rg-compilation"}) {
		t.Errorf("series asked of %v, want the two soundtracks and the compilation only", sources.asked)
	}
	candidates, err := franchises.LinkCandidates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, candidate := range candidates {
		row, _ := franchises.Get(t.Context(), candidate.ID)
		members, _ := franchises.MembersOf(t.Context(), candidate.ID)
		keys := slices.Clone(candidate.Keys)
		slices.Sort(keys)
		got[row.Name] = keys
		if len(members) != 2 || !slices.Contains(members, scoreOne) || !slices.Contains(members, scoreTwo) {
			t.Errorf("%s members = %v, want both scores", row.Name, members)
		}
	}
	want := map[string][]string{
		// The saga, not its trilogy: one collection for the franchise, declaring the trilogy too.
		"Vexmoor":        {"wikidata:Q10", "wikidata:Q10", "wikidata:Q11"},
		"Vexmoor Scores": {"musicbrainz-series:series-1", "wikidata:Q10", "wikidata:Q12"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collections and keys = %v\nwant %v", got, want)
	}
	if collections, _ := franchises.Summaries(t.Context(), string(music.MediaType)); len(collections) != 2 {
		t.Errorf("collections = %+v, want two (%d holds none)", collections, studio)
	}

	// The series is asked once; Wikidata no longer naming the franchise takes its collection away, a failing one changes nothing.
	sources.asked = nil
	source.albums = map[string][]wikidata.AlbumFranchise{}
	pass.Wikidata = source
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(sources.asked) != 0 {
		t.Errorf("series asked again of %v", sources.asked)
	}
	if collections, _ := franchises.Summaries(t.Context(), string(music.MediaType)); len(collections) != 1 || collections[0].Name != "Vexmoor Scores" {
		t.Errorf("collections = %+v, want the series alone", collections)
	}
	pass.Wikidata = fakeAlbumWikidata{err: errors.New("busy")}
	if err := pass.Run(t.Context(), jobs.Job{}); err == nil {
		t.Error("a failing Wikidata is an error to retry")
	}
}
