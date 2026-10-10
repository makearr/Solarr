package library_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// statFigure is one measured figure as one library sees it.
type statFigure struct {
	Key   string
	Value float64
	Count int64
}

// figuresOf measures library.db and answers one metric's figures for one library.
func figuresOf(t *testing.T, statistics library.Statistics, roots map[int64]int64, now int64, metric string, categoryID int64) []statFigure {
	t.Helper()
	rows, err := statistics.Figures(t.Context(), roots, now)
	if err != nil {
		t.Fatalf("Figures: %v", err)
	}
	prefix := fmt.Sprintf("%d|", categoryID)
	var figures []statFigure
	for _, row := range rows {
		if row.Metric == metric && strings.HasPrefix(row.Dimension, prefix) {
			figures = append(figures, statFigure{strings.TrimPrefix(row.Dimension, prefix), row.Value, row.Count})
		}
	}
	return figures
}

// Each figure is filed under its own library.
func TestStatistics_FiguresArePerLibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	statistics := library.NewStatistics(opened)
	work := createWork(t, opened, "music", "Album")
	createDesired(t, opened, work.ID, "SET", 10)
	other := createWork(t, opened, "music", "Elsewhere")
	createDesiredInCategory(t, opened, other.ID, 2)

	if got := figuresOf(t, statistics, map[int64]int64{}, 5_000, library.StatAlbumsByState, 1); !reflect.DeepEqual(got, []statFigure{{"MISSING", 1, 1}}) {
		t.Fatalf("library 1 albums = %+v, want one MISSING", got)
	}
	if got := figuresOf(t, statistics, map[int64]int64{}, 5_000, library.StatAlbumsByState, 3); len(got) != 0 {
		t.Errorf("a library with nothing got %+v", got)
	}
}

// An artist with an album held is listed with the release groups held, what their discography is compared against.
func TestStatistics_ArtistsHeldWithTheirReleaseGroups(t *testing.T) {
	opened, _ := newLibrary(t)
	statistics := library.NewStatistics(opened)
	people := library.NewPeople(opened)
	physical := library.NewPhysical(opened)

	var held []library.ArtistHeld
	for index, name := range []string{"Few Missing", "Many Missing", "Complete"} {
		person := resolvePersonNamed(t, people, "mbid-"+name, name)
		work := createWork(t, opened, "music", name+" Album")
		createDesired(t, opened, work.ID, "SET", 10)
		if err := people.AddLeadCredit(t.Context(), work.ID, person, "artist"); err != nil {
			t.Fatal(err)
		}
		registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
			{RelPath: name + "/01.flac", SizeBytes: 1, QuickHash: "h" + name, FirstSeenAt: time.Unix(1, 0)},
		})
		if err != nil {
			t.Fatal(err)
		}
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			if err := q.LinkWorkFile(ctx, librarydb.LinkWorkFileParams{WorkID: work.ID, MediaObjectID: registered[0].MediaObjectID, FirstSeenAt: 1}); err != nil {
				return err
			}
			return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
				Provider: "musicbrainz:release-group", ExternalID: "rg-" + name, WorkID: work.ID, Confidence: 1, FirstSeenAt: 1,
			})
		})
		_ = index
	}
	artists, err := statistics.ArtistsHeld(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	held = artists
	if len(held) != 3 || held[0].ArtistMBID != "mbid-Few Missing" || len(held[0].ReleaseGroups) != 1 || held[0].ReleaseGroups[0] != "rg-Few Missing" {
		t.Fatalf("artists held = %+v, want three, each holding its one release group", held)
	}
}

// Growth is the files first seen per month, summed over a library's roots.
func TestStatistics_GrowthByMonth(t *testing.T) {
	opened, _ := newLibrary(t)
	statistics := library.NewStatistics(opened)
	const september, october = 1_788_220_800, 1_790_812_800 // 2026-09-01, 2026-10-01
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for index, file := range []struct{ root, seen, size int64 }{
			{1, september, 100}, {1, september + 86_400, 50}, {2, october, 10}, {3, october, 7},
		} {
			object, err := q.CreateMediaObject(ctx, librarydb.CreateMediaObjectParams{
				SizeBytes: file.size, QuickHash: fmt.Sprintf("h%d", index), FirstSeenAt: file.seen,
			})
			if err != nil {
				return err
			}
			if _, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
				MediaObjectID: object, RootFolderID: file.root, RelPath: fmt.Sprintf("f%d.flac", index), LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	item := createDesired(t, opened, createWork(t, opened, "music", "Album").ID, "SET", 10)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		candidate, err := q.UpsertCandidate(ctx, librarydb.UpsertCandidateParams{
			IndexerID: 1, Guid: "g", Title: "t", Protocol: "usenet", DownloadUrl: "u", Source: "search", FirstSeenAt: 1, ExpiresAt: 1 << 40,
		})
		if err != nil {
			return err
		}
		for _, decided := range []struct {
			outcome string
			at      int64
		}{{"REJECT", 9_000_000}, {"REJECT", 9_000_001}, {"ACCEPT", 9_000_002}, {"DEFER", 1}} {
			if _, err := q.RecordDecision(ctx, librarydb.RecordDecisionParams{
				CandidateID: candidate.ID, DesiredItemID: item.ID, Outcome: decided.outcome, ComponentsJson: "[]", DecidedAt: decided.at,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	now := int64(9_000_100)
	roots := map[int64]int64{1: 1, 2: 1, 3: 2}
	growth := figuresOf(t, statistics, roots, now, library.StatGrowthByMonth, 1)
	got := map[string][2]float64{}
	for _, figure := range growth {
		got[figure.Key] = [2]float64{figure.Value, float64(figure.Count)}
	}
	if want := (map[string][2]float64{"2026-09": {150, 2}, "2026-10": {10, 1}}); !reflect.DeepEqual(got, want) {
		t.Errorf("library 1 growth = %v, want %v (library 2's file left out)", got, want)
	}
}

// Each library keeps its own 20 biggest works, whatever the works of another library weigh.
func TestStatistics_BiggestWorksAreTwentyPerLibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	statistics := library.NewStatistics(opened)
	for index := range 22 {
		work := createWork(t, opened, "music", fmt.Sprintf("Album %02d", index))
		item := createDesiredInCategory(t, opened, work.ID, 1)
		attach(t, opened, item.ID, createObject(t, opened, fmt.Sprintf("a%d", index), int64(100+index)).ID, "primary", "")
	}
	for index := range 25 {
		work := createWork(t, opened, "tv", fmt.Sprintf("Series %02d", index))
		item := createDesiredInCategory(t, opened, work.ID, 2)
		attach(t, opened, item.ID, createObject(t, opened, fmt.Sprintf("s%d", index), int64(1_000_000+index)).ID, "primary", "")
	}
	for category, biggest := range map[int64]int64{1: 121, 2: 1_000_024} {
		got := figuresOf(t, statistics, map[int64]int64{}, 5_000, library.StatSeriesSize, category)
		largest := int64(0)
		for _, figure := range got {
			largest = max(largest, int64(figure.Value))
		}
		if len(got) != 20 || largest != biggest {
			t.Errorf("library %d: %d works, the largest %d; want 20 with %d", category, len(got), largest, biggest)
		}
	}
}
