package tv_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/platform/paginate"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

func sorted(values []string) []string {
	slices.Sort(values)
	return values
}

func secondLookSeries() skyhook.Series {
	series := fixtureSkyhook()
	series.Episodes = []skyhook.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, Title: "The Long Road", AirDate: "2010-01-01"},
		{SeasonNumber: 1, EpisodeNumber: 2, Title: "Cold Harbour", AirDate: "2010-01-08"},
		{SeasonNumber: 2, EpisodeNumber: 1, Title: "A Quiet Night", AirDate: "2011-01-01"},
		{SeasonNumber: 2, EpisodeNumber: 2, Title: "Same Name", AirDate: "2011-01-08"},
		{SeasonNumber: 2, EpisodeNumber: 3, Title: "Same Name", AirDate: "2011-01-15"},
	}
	return series
}

// A file named after its episode finds it when the name is the exact, unique title; anything less stays in Review.
func TestScan_AnUnnumberedFileIsPlacedByItsExactUniqueEpisodeTitle(t *testing.T) {
	series := secondLookSeries()
	consumer, opened, fsys := newConsumer(t, searchSkyhook{series: series})
	folder := series.Title + "/Archive/De/"
	batch := []scan.Observed{
		writeVideo(t, fsys, series.Title+"/Season 01/"+series.Title+" - S01E01.mkv"),
		writeVideo(t, fsys, folder+"Cold Harbour.mkv"),
		writeVideo(t, fsys, folder+series.Title+" - A Quiet Night.mkv"),
		writeVideo(t, fsys, folder+"Same Name.mkv"),
		writeVideo(t, fsys, folder+"Something Else.mkv"),
	}
	if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: scanRoot, Online: true}, 3, batch); err != nil {
		t.Fatal(err)
	}
	queue, err := library.NewUnidentified(opened).List(t.Context(), []int64{1}, paginate.Cursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var waiting []string
	for _, row := range queue {
		waiting = append(waiting, row.RelPath)
	}
	if want := []string{folder + "Same Name.mkv", folder + "Something Else.mkv"}; !slices.Equal(sorted(waiting), want) {
		t.Errorf("waiting = %v, want %v", waiting, want)
	}
}

// A release numbering a show as one long season: S01E03 of a first season of two is S02E01, offered in Review as a
// duplicate of the file held there, and never placed by itself.
func TestScan_ANumberPastTheSeasonsEndIsOfferedAsTheEpisodeItCountsOnTo(t *testing.T) {
	series := secondLookSeries()
	consumer, opened, fsys := newConsumer(t, searchSkyhook{series: series})
	root := settings.RootFolder{ID: 1, Path: scanRoot, Online: true}
	batch := []scan.Observed{
		writeVideo(t, fsys, series.Title+"/Season 02/"+series.Title+" - S02E01.mkv"),
		writeVideo(t, fsys, series.Title+"/Season 01/"+series.Title+" - S01E03.mkv"),
		writeVideo(t, fsys, series.Title+"/Season 01/"+series.Title+" - S01E40.mkv"),
	}
	if err := consumer.Observe(t.Context(), root, 3, batch); err != nil {
		t.Fatal(err)
	}
	queue, err := library.NewUnidentified(opened).List(t.Context(), []int64{1}, paginate.Cursor{}, 10)
	if err != nil || len(queue) != 2 {
		t.Fatalf("queue = %+v, %v; want both files past the season", queue, err)
	}
	for _, row := range queue {
		switch row.RelPath {
		case series.Title + "/Season 01/" + series.Title + " - S01E03.mkv":
			want := []library.Candidate{{
				Strategy: "numbering", Provider: tv.ProviderContinued, ExternalID: "1234:2:1", Confidence: 0.5,
				Title: `Duplicate episode: a second copy of S02E01 "A Quiet Night", numbered on past season 1`,
			}}
			if !reflect.DeepEqual(row.Candidates, want) {
				t.Errorf("candidates = %+v, want %+v", row.Candidates, want)
			}
		default:
			if len(row.Candidates) != 0 {
				t.Errorf("%s runs past the last season, yet was offered %+v", row.RelPath, row.Candidates)
			}
		}
	}
}

func TestReadContinued(t *testing.T) {
	tvdb, group, parts, ok := tv.ReadContinued("78804:2:1,2")
	if !ok || tvdb != "78804" || group != 2 || !reflect.DeepEqual(parts, []int{1, 2}) {
		t.Errorf("got %q %d %v %v", tvdb, group, parts, ok)
	}
	for _, bad := range []string{"", "78804:2", "78804:x:1", "78804:2:y"} {
		if _, _, _, ok := tv.ReadContinued(bad); ok {
			t.Errorf("%q was read", bad)
		}
	}
}
