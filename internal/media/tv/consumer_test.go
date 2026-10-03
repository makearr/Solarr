package tv_test

import (
	"context"
	"path"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

const scanRoot = "/library"

type categories struct{}

func (categories) Category(int64) (settings.Category, bool)             { return settings.Category{}, false }
func (categories) ProfileFor(_ settings.Category, fallback int64) int64 { return fallback }

// searchSkyhook answers a search with the one series and the full series for any id.
type searchSkyhook struct{ series skyhook.Series }

func (s searchSkyhook) Series(context.Context, int64) (skyhook.Series, error) { return s.series, nil }
func (s searchSkyhook) Search(context.Context, string) ([]skyhook.Series, error) {
	hit := s.series
	hit.Episodes = nil
	return []skyhook.Series{hit}, nil
}

// choosing is a library whose scan policy is the given TV monitor choice.
type choosing struct{ policy string }

func (c choosing) Category(int64) (settings.Category, bool) {
	return settings.Category{TVMonitor: c.policy}, true
}
func (choosing) ProfileFor(_ settings.Category, fallback int64) int64 { return fallback }

func newConsumer(t *testing.T, sky tv.SeriesSource) (tv.Consumer, *store.Store, *fsx.Mem) {
	return newConsumerIn(t, sky, categories{})
}

type scanCategories interface {
	Category(int64) (settings.Category, bool)
	ProfileFor(settings.Category, int64) int64
}

func newConsumerIn(t *testing.T, sky tv.SeriesSource, policy scanCategories) (tv.Consumer, *store.Store, *fsx.Mem) {
	t.Helper()
	opened := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	service := tv.Service{Skyhook: sky, TVmaze: fakeTVmaze{err: tvmaze.ErrNotFound}, Series: library.NewSeries(opened), Desired: library.NewDesired(opened), Clock: fake}
	consumer := tv.NewConsumer(tv.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Unidentified: library.NewUnidentified(opened), Service: service,
		Reader: library.NewSeriesReader(opened), Clock: fake, DefaultProfileID: 1, Categories: policy,
	})
	return consumer, opened, fsys
}

func writeVideo(t *testing.T, fsys *fsx.Mem, rel string) scan.Observed {
	t.Helper()
	absolute := path.Join(scanRoot, rel)
	if err := fsys.MkdirAll(path.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(absolute)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("video " + rel)
	_, _ = file.Write(content)
	_ = file.Close()
	return scan.Observed{
		File:  scan.Found{RelPath: rel, SizeBytes: int64(len(content)), ModTime: time.Unix(0, 0)},
		Probe: mediatool.Probe{Path: absolute},
	}
}

func TestScanAddsTheSeriesAndAttachesEpisodes(t *testing.T) {
	sky := fixtureSkyhook()
	consumer, opened, fsys := newConsumer(t, searchSkyhook{series: sky})
	batch := []scan.Observed{
		writeVideo(t, fsys, "Example Show/Season 1/Example Show - S01E01 - Episode.mkv"),
		writeVideo(t, fsys, "Example Show/Season 1/Example Show - S01E02 - Episode.mkv"),
	}
	if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: scanRoot, Online: true}, 3, batch); err != nil {
		t.Fatal(err)
	}

	reader := library.NewSeriesReader(opened)
	list, err := reader.List(t.Context(), 3, library.SeriesCursor{}, 10, addNow)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	// "Existing": only the two episodes with files are wanted, and both are present.
	if !list[0].Monitored {
		t.Error("a season whose episodes were found on disk is monitored")
	}
	if c := list[0].Counts; c.Monitored != 2 || c.Present != 2 || c.Total != 5 {
		t.Errorf("counts = %+v", c)
	}
	detail, err := reader.Get(t.Context(), 3, list[0].WorkID, addNow)
	if err != nil {
		t.Fatal(err)
	}
	present := 0
	for _, episode := range detail.Episodes {
		if episode.Present {
			present++
		}
	}
	if present != 2 {
		t.Errorf("present episodes = %d", present)
	}
}

func TestScanMonitorsWhatTheLibraryAsksFor(t *testing.T) {
	for policy, want := range map[string]int64{settings.TVMonitorExisting: 2, settings.TVMonitorAll: 5, settings.TVMonitorNone: 0} {
		consumer, opened, fsys := newConsumerIn(t, searchSkyhook{series: fixtureSkyhook()}, choosing{policy: policy})
		batch := []scan.Observed{
			writeVideo(t, fsys, "Example Show/Season 1/Example Show - S01E01 - Episode.mkv"),
			writeVideo(t, fsys, "Example Show/Season 1/Example Show - S01E02 - Episode.mkv"),
		}
		if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: scanRoot, Online: true}, 3, batch); err != nil {
			t.Fatal(err)
		}
		list, err := library.NewSeriesReader(opened).List(t.Context(), 3, library.SeriesCursor{}, 10, addNow)
		if err != nil || len(list) != 1 {
			t.Fatalf("%s: list = %+v, %v", policy, list, err)
		}
		if got := list[0].Counts.Monitored; got != want {
			t.Errorf("tv_monitor %q: %d episodes monitored, want %d", policy, got, want)
		}
	}
}

func TestAnExcludedSeriesIsNeitherAddedNorQueued(t *testing.T) {
	consumer, opened, fsys := newConsumer(t, searchSkyhook{series: fixtureSkyhook()})
	if err := library.NewSeries(opened).Exclude(t.Context(), addNow, "Example Show", []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: "1234"}}); err != nil {
		t.Fatal(err)
	}
	batch := []scan.Observed{writeVideo(t, fsys, "Example Show/Season 1/Example Show - S01E01 - Episode.mkv")}
	if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: scanRoot, Online: true}, 3, batch); err != nil {
		t.Fatal(err)
	}
	if list, _ := library.NewSeriesReader(opened).List(t.Context(), 3, library.SeriesCursor{}, 10, addNow); len(list) != 0 {
		t.Errorf("an excluded series was added: %+v", list)
	}
	queued, err := librarydb.New(opened.Read()).CountUnidentified(t.Context(), librarydb.CountUnidentifiedParams{FilterRootFolders: 0, RootFolderIds: "[]"})
	if err != nil || queued != 0 {
		t.Errorf("queued = %d, err = %v; an excluded series' files are left alone, not sent to review", queued, err)
	}
}

func TestScanQueuesAFolderItCannotPlace(t *testing.T) {
	sky := fixtureSkyhook()
	sky.Title = "Something Else Entirely"
	consumer, opened, fsys := newConsumer(t, searchSkyhook{series: sky})
	batch := []scan.Observed{writeVideo(t, fsys, "Unknown Folder/Unknown Folder - S01E01.mkv")}
	if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: scanRoot, Online: true}, 3, batch); err != nil {
		t.Fatal(err)
	}
	if list, _ := library.NewSeriesReader(opened).List(t.Context(), 3, library.SeriesCursor{}, 10, addNow); len(list) != 0 {
		t.Errorf("nothing should have been added: %+v", list)
	}
	queued, err := librarydb.New(opened.Read()).CountUnidentified(t.Context(), librarydb.CountUnidentifiedParams{FilterRootFolders: 0, RootFolderIds: "[]"})
	if err != nil || queued != 1 {
		t.Errorf("queued = %d, %v", queued, err)
	}
}

// switchSkyhook finds nothing until it is switched on, like a provider that was down at the first scan.
type switchSkyhook struct {
	on     *bool
	series skyhook.Series
}

func (s switchSkyhook) Series(ctx context.Context, id int64) (skyhook.Series, error) {
	return searchSkyhook{series: s.series}.Series(ctx, id)
}
func (s switchSkyhook) Search(ctx context.Context, term string) ([]skyhook.Series, error) {
	if !*s.on {
		return nil, nil
	}
	return searchSkyhook{series: s.series}.Search(ctx, term)
}

func TestAFileQueuedForReviewLeavesTheQueueOnceALaterScanPlacesIt(t *testing.T) {
	on := false
	consumer, opened, fsys := newConsumer(t, switchSkyhook{on: &on, series: fixtureSkyhook()})
	root := settings.RootFolder{ID: 1, Path: scanRoot, Online: true}
	batch := []scan.Observed{writeVideo(t, fsys, "Example Show/Season 1/Example Show - S01E01 - Episode.mkv")}
	queued := func() int64 {
		n, err := librarydb.New(opened.Read()).CountUnidentified(t.Context(), librarydb.CountUnidentifiedParams{FilterRootFolders: 0, RootFolderIds: "[]"})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := consumer.Observe(t.Context(), root, 3, batch); err != nil {
		t.Fatal(err)
	}
	if queued() != 1 {
		t.Fatalf("queued = %d after the first scan, want the file waiting for review", queued())
	}
	on = true
	if err := consumer.Observe(t.Context(), root, 3, batch); err != nil {
		t.Fatal(err)
	}
	if queued() != 0 {
		t.Errorf("queued = %d after the file was placed, want the review entry gone", queued())
	}
}

func TestScanFindsASeriesAlreadyInTheLibraryByAliasAndMultiEpisodeFiles(t *testing.T) {
	sky := fixtureSkyhook()
	consumer, opened, fsys := newConsumer(t, searchSkyhook{series: sky})
	root := settings.RootFolder{ID: 1, Path: scanRoot, Online: true}
	first := []scan.Observed{writeVideo(t, fsys, "Example Show/Season 1/Example Show - S01E01 - Episode.mkv")}
	if err := consumer.Observe(t.Context(), root, 3, first); err != nil {
		t.Fatal(err)
	}
	second := []scan.Observed{writeVideo(t, fsys, "example.show (2025)/S01E02-E03.mkv")}
	// S01E03 does not exist in the fixture, so the file covers one episode and is queued for the rest.
	if err := consumer.Observe(t.Context(), root, 3, second); err != nil {
		t.Fatal(err)
	}
	list, _ := library.NewSeriesReader(opened).List(t.Context(), 3, library.SeriesCursor{}, 10, addNow)
	if len(list) != 1 || list[0].Counts.Present != 2 {
		t.Errorf("one series, two episodes present: %+v", list)
	}
}

func TestGuessFolderReadsYearAndIDs(t *testing.T) {
	cases := map[string]tv.FolderGuess{
		"Example Show":                     {Title: "Example Show"},
		"Example Show (2010)":              {Title: "Example Show", Year: 2010},
		"Example Show (2010) {tvdb-123}":   {Title: "Example Show", Year: 2010, TVDBID: 123},
		"Example Show [tvdbid-77]":         {Title: "Example Show", TVDBID: 77},
		"Example Show {imdb-tt123} (2001)": {Title: "Example Show", Year: 2001},
	}
	for name, want := range cases {
		if got := tv.GuessFolder(name); got != want {
			t.Errorf("%q: %+v, want %+v", name, got, want)
		}
	}
}

func TestIsVideoSkipsSamplesAndExtras(t *testing.T) {
	cases := map[string]bool{
		"Show/Season 1/Show - S01E01.mkv":          true,
		"Show/Season 1/Show - S01E01.srt":          false,
		"Show/Extras/Making of.mkv":                false,
		"Show/Season 1/show.s01e01-sample.mkv":     false,
		"Show/Season 1/Sample/clip.mkv":            false,
		"Show/Season 1/Sampler Platter S01E01.mkv": true,
	}
	for rel, want := range cases {
		if got := tv.IsVideo(rel); got != want {
			t.Errorf("%q: %v, want %v", rel, got, want)
		}
	}
}
