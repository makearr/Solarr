package tv_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func newLibrary(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}
	return opened
}

type fakeSkyhook struct {
	series skyhook.Series
	err    error
}

func (f fakeSkyhook) Series(context.Context, int64) (skyhook.Series, error) { return f.series, f.err }
func (f fakeSkyhook) Search(context.Context, string) ([]skyhook.Series, error) {
	return []skyhook.Series{f.series}, f.err
}

type fakeTVmaze struct {
	show     tvmaze.Show
	episodes []tvmaze.Episode
	err      error
}

func (f fakeTVmaze) Show(context.Context, int64) (tvmaze.Show, error)       { return f.show, f.err }
func (f fakeTVmaze) LookupTVDB(context.Context, int64) (tvmaze.Show, error) { return f.show, f.err }
func (f fakeTVmaze) Episodes(context.Context, int64) ([]tvmaze.Episode, error) {
	return f.episodes, f.err
}
func (f fakeTVmaze) Aliases(context.Context, int64) ([]string, error) {
	return []string{"Alt Name"}, f.err
}
func (f fakeTVmaze) Cast(context.Context, int64) ([]tvmaze.CastMember, error) {
	return nil, f.err
}

var addNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T, sky fakeSkyhook, maze fakeTVmaze) (tv.Service, *store.Store) {
	t.Helper()
	opened := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	return tv.Service{
		Skyhook: sky, TVmaze: maze, Series: library.NewSeries(opened), Desired: library.NewDesired(opened), Clock: fake,
	}, opened
}

// A show that aired two seasons (the second still has its last two episodes to come) plus a recap special.
func fixtureSkyhook() skyhook.Series {
	episode := func(id int64, season, number int, date string) skyhook.Episode {
		return skyhook.Episode{TVDBID: id, SeasonNumber: season, EpisodeNumber: number, Title: "Episode", AirDate: date}
	}
	return skyhook.Series{
		TVDBID: 1234, TVMazeID: 42, Title: "Example Show", FirstAired: "2025-01-05", Status: "Continuing",
		Episodes: []skyhook.Episode{
			episode(1, 0, 1, "2024-12-30"),
			episode(2, 1, 1, "2025-01-05"), episode(3, 1, 2, "2025-01-12"),
			episode(4, 2, 1, "2026-09-20"), episode(5, 2, 2, "2026-10-05"), episode(6, 2, 3, "2026-10-12"),
		},
	}
}

func partMonitoring(t *testing.T, opened *store.Store, workID int64) map[string]int64 {
	t.Helper()
	parts, err := librarydb.New(opened.Read()).ListWorkParts(t.Context(), workID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, part := range parts {
		out[part.MediaRef] = part.Monitored
	}
	return out
}

func TestAddStoresTheSeriesWithASeasonItemEach(t *testing.T) {
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{show: tvmaze.Show{ID: 42, Name: "Example Show"}})
	result, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: 3, Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Episodes != 6 || result.Seasons != 3 {
		t.Fatalf("result = %+v", result)
	}
	items, err := librarydb.New(opened.Read()).ListDesiredItemsForWork(t.Context(), result.WorkID)
	if err != nil || len(items) != 3 {
		t.Fatalf("items = %d, %v", len(items), err)
	}
	byRef := map[string]librarydb.DesiredItem{}
	for _, item := range items {
		byRef[item.MediaRef] = item
	}
	if byRef["s01"].RequiredCount.Int64 != 2 || byRef["s02"].RequiredCount.Int64 != 3 || byRef["s00"].Monitored != 0 {
		t.Errorf("seasons = %+v", byRef)
	}
	if byRef["s01"].CategoryID != 7 || byRef["s01"].SatisfactionKind != "SET" {
		t.Errorf("season item = %+v", byRef["s01"])
	}
}

func TestMonitorOptionsChooseTheStartingEpisodes(t *testing.T) {
	cases := map[tv.MonitorOption][]string{
		tv.MonitorAll:         {"s01:e01", "s01:e02", "s02:e01", "s02:e02", "s02:e03"},
		tv.MonitorFuture:      {"s02:e02", "s02:e03"},
		tv.MonitorRecent:      {"s02:e01", "s02:e02", "s02:e03"},
		tv.MonitorPilot:       {"s01:e01"},
		tv.MonitorFirstSeason: {"s01:e01", "s01:e02"},
		tv.MonitorLastSeason:  {"s02:e01", "s02:e02", "s02:e03"},
		tv.MonitorExisting:    nil,
		tv.MonitorNone:        nil,
	}
	for option, want := range cases {
		service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
		result, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, Monitor: option})
		if err != nil {
			t.Fatalf("%s: %v", option, err)
		}
		got := partMonitoring(t, opened, result.WorkID)
		for ref, monitored := range got {
			expected := false
			for _, ref2 := range want {
				if ref == ref2 {
					expected = true
				}
			}
			if (monitored == 1) != expected {
				t.Errorf("%s: %s monitored=%d, want %v", option, ref, monitored, expected)
			}
		}
		if got["s00:e01"] != 0 {
			t.Errorf("%s: specials stay unmonitored unless asked for", option)
		}
	}
}

func TestSpecialsAreMonitoredOnlyWhenAskedFor(t *testing.T) {
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	result, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, Monitor: tv.MonitorAll, Specials: true})
	if err != nil {
		t.Fatal(err)
	}
	if partMonitoring(t, opened, result.WorkID)["s00:e01"] != 1 {
		t.Error("the special should be monitored")
	}
}

func TestAddingAgainRefreshesWithoutChangingMonitoring(t *testing.T) {
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	first, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, Monitor: tv.MonitorAll, CategoryID: 1, ProfileID: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, Monitor: tv.MonitorNone, CategoryID: 1, ProfileID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.WorkID != first.WorkID {
		t.Fatalf("second = %+v", second)
	}
	if partMonitoring(t, opened, first.WorkID)["s01:e01"] != 1 {
		t.Error("re-adding with a different monitor option must not unmonitor what is monitored")
	}
}

func TestFetchSurvivesOneSourceFailingButNotBoth(t *testing.T) {
	service, _ := newService(t, fakeSkyhook{err: errors.New("proxy down")}, fakeTVmaze{
		show:     tvmaze.Show{ID: 42, Name: "Example Show", Status: "Running"},
		episodes: []tvmaze.Episode{{ID: 9, Name: "Pilot", Season: 1, Number: new(int), Type: "regular"}},
	})
	*service.TVmaze.(fakeTVmaze).episodes[0].Number = 1
	series, err := service.Fetch(t.Context(), tv.IDs{TVDB: 1234})
	if err != nil || series.Title != "Example Show" || len(series.Episodes) != 1 {
		t.Fatalf("series = %+v, %v", series, err)
	}

	both, _ := newService(t, fakeSkyhook{err: errors.New("down")}, fakeTVmaze{err: tvmaze.ErrNotFound})
	if _, err := both.Fetch(t.Context(), tv.IDs{TVDB: 1234}); !errors.Is(err, tv.ErrNoSource) {
		t.Errorf("err = %v, want ErrNoSource", err)
	}
}

func TestSearchAnswersCandidatesUpToTheLimit(t *testing.T) {
	sky := skyhook.Series{TVDBID: 5, Title: "Alpha", FirstAired: "2019-04-01", Network: "Net", Status: "Ended",
		Images: []skyhook.Image{{CoverType: "Banner", URL: "b"}, {CoverType: "Poster", URL: "p"}}}
	service, _ := newService(t, fakeSkyhook{series: sky}, fakeTVmaze{})
	got, err := service.Search(t.Context(), "alpha", 5)
	if err != nil || len(got) != 1 || got[0].Year != 2019 || got[0].Poster != "p" || got[0].Status != tv.StatusEnded {
		t.Errorf("candidates = %+v, %v", got, err)
	}
}
