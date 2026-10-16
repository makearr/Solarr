package tv_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
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
	// The library's own clock decides which episodes are out, so it reads the hour the tests add at.
	opened, err := store.Open(t.Context(), clock.NewFake(addNow), store.Config{
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

func newService(t *testing.T, sky tv.SeriesSource, maze fakeTVmaze) (tv.Service, *store.Store) {
	t.Helper()
	opened := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	return tv.Service{
		Skyhook: sky, TVmaze: maze, Series: library.NewSeries(opened), Exclusions: library.NewExclusions(opened), Desired: library.NewDesired(opened), Clock: fake,
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
	// Season 2 owes only the episode that has aired; the two still to come are not missing.
	if byRef["s01"].RequiredCount.Int64 != 2 || byRef["s02"].RequiredCount.Int64 != 1 || byRef["s00"].Monitored != 0 {
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

func TestRefreshMonitorsNewEpisodesByTheirSeasonAndNewSeasonsWhenTheSeriesIsWanted(t *testing.T) {
	sky := fixtureSkyhook()
	service, opened := newService(t, searchSkyhook{series: sky}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Reader = library.NewSeriesReader(opened)
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 3, ProfileID: 1, Monitor: tv.MonitorFirstSeason})
	if err != nil {
		t.Fatal(err)
	}

	grown := fixtureSkyhook()
	extra := func(id int64, season, number int, date string) skyhook.Episode {
		return skyhook.Episode{TVDBID: id, SeasonNumber: season, EpisodeNumber: number, Title: "New", AirDate: date}
	}
	grown.Episodes = append(grown.Episodes, extra(7, 1, 3, "2025-01-19"), extra(8, 2, 4, "2026-10-19"), extra(9, 3, 1, "2026-11-01"), extra(10, 0, 2, "2026-10-01"))
	service.Skyhook = searchSkyhook{series: grown}
	if _, err := service.Refresh(t.Context(), tv.RefreshRequest{WorkID: added.WorkID, CategoryID: 3, ProfileID: 1}); err != nil {
		t.Fatal(err)
	}

	detail, err := library.NewSeriesReader(opened).Get(t.Context(), 3, added.WorkID, addNow)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, episode := range detail.Episodes {
		got[tv.MediaRef(episode.Season, episode.Number)] = episode.Monitored
	}
	want := map[string]bool{"s01:e03": true, "s02:e04": false, "s03:e01": true, "s00:e02": false, "s02:e01": false, "s01:e01": true}
	for ref, monitored := range want {
		if got[ref] != monitored {
			t.Errorf("%s monitored = %v, want %v", ref, got[ref], monitored)
		}
	}
	if len(detail.Episodes) != 10 {
		t.Errorf("episodes = %d, want the original six and the four new ones", len(detail.Episodes))
	}
}

func TestRefreshLeavesANewSeasonUnmonitoredWhenTheLibraryIgnoresNewSeasons(t *testing.T) {
	service, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Reader = library.NewSeriesReader(opened)
	service.IgnoreNewSeasons = func(categoryID int64) bool { return categoryID == 3 }
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 3, ProfileID: 1, Monitor: tv.MonitorFirstSeason})
	if err != nil {
		t.Fatal(err)
	}
	grown := fixtureSkyhook()
	grown.Episodes = append(grown.Episodes, skyhook.Episode{TVDBID: 9, SeasonNumber: 3, EpisodeNumber: 1, Title: "New", AirDate: "2026-11-01"})
	service.Skyhook = searchSkyhook{series: grown}
	if _, err := service.Refresh(t.Context(), tv.RefreshRequest{WorkID: added.WorkID, CategoryID: 3, ProfileID: 1}); err != nil {
		t.Fatal(err)
	}
	if got := partMonitoring(t, opened, added.WorkID)["s03:e01"]; got != 0 {
		t.Errorf("the new season's episode monitored = %d, want 0", got)
	}
}

func TestRefreshRemovesAnEpisodeSkyhookNoLongerListsWhenNothingHoldsOnToIt(t *testing.T) {
	service, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Reader = library.NewSeriesReader(opened)
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 3, ProfileID: 1, Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	shrunk := fixtureSkyhook()
	shrunk.Episodes = shrunk.Episodes[:len(shrunk.Episodes)-1]
	service.Skyhook = searchSkyhook{series: shrunk}
	refreshed, err := service.Refresh(t.Context(), tv.RefreshRequest{WorkID: added.WorkID, CategoryID: 3, ProfileID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(refreshed.Pruned, []string{"s02:e03"}) {
		t.Errorf("pruned = %v, want the episode the list lost", refreshed.Pruned)
	}
	if _, stillThere := partMonitoring(t, opened, added.WorkID)["s02:e03"]; stillThere {
		t.Error("the removed episode is still stored")
	}
}

func TestApplyMonitorReappliesSonarrsChoicesToAHeldSeries(t *testing.T) {
	service, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Reader = library.NewSeriesReader(opened)
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 3, ProfileID: 1, Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	monitored := func() (map[string]int64, map[string]bool) {
		parts := partMonitoring(t, opened, added.WorkID)
		seasons, err := service.Reader.SeasonMonitoring(t.Context(), added.WorkID)
		if err != nil {
			t.Fatal(err)
		}
		bySeason := map[string]bool{}
		for season, on := range seasons {
			bySeason[tv.SeasonRef(season)] = on
		}
		return parts, bySeason
	}

	if err := service.ApplyMonitor(t.Context(), 3, added.WorkID, tv.MonitorLastSeason, false); err != nil {
		t.Fatal(err)
	}
	parts, seasons := monitored()
	if parts["s01:e01"] != 0 || parts["s02:e01"] != 1 || parts["s02:e03"] != 1 || seasons["s01"] || !seasons["s02"] {
		t.Errorf("last season: parts = %v, seasons = %v", parts, seasons)
	}
	if err := service.ApplyMonitor(t.Context(), 3, added.WorkID, tv.MonitorNone, false); err != nil {
		t.Fatal(err)
	}
	parts, seasons = monitored()
	if parts["s02:e01"] != 0 || seasons["s02"] {
		t.Errorf("none: parts = %v, seasons = %v", parts, seasons)
	}
	if err := service.ApplyMonitor(t.Context(), 3, added.WorkID, "sometimes", false); err == nil {
		t.Error("an unknown option was accepted")
	}
}

func TestSetEpisodeMonitoringChangesOnlyTheEpisodesItNames(t *testing.T) {
	service, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Reader = library.NewSeriesReader(opened)
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 3, ProfileID: 1, Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetEpisodeMonitoring(t.Context(), 3, added.WorkID, map[tv.Key]bool{{Season: 1, Number: 2}: false, {Season: 2, Number: 1}: true}); err != nil {
		t.Fatal(err)
	}
	parts := partMonitoring(t, opened, added.WorkID)
	if parts["s01:e01"] != 1 || parts["s01:e02"] != 0 || parts["s02:e01"] != 1 || parts["s02:e03"] != 1 {
		t.Errorf("parts = %v: the unnamed ones stay as the add left them", parts)
	}
}

// seriesIn answers the series a library holds, each as the TV reader sees it on addNow.
func seriesIn(ctx context.Context, opened *store.Store, categoryID int64) ([]library.SeriesSummary, error) {
	rows, err := library.NewWorks(opened).List(ctx, library.WorkFilter{MediaType: "tv", CategoryIDs: []int64{categoryID}}, library.WorkCursor{}, 50)
	if err != nil {
		return nil, err
	}
	summaries := make([]library.SeriesSummary, 0, len(rows))
	for _, row := range rows {
		detail, err := library.NewSeriesReader(opened).Get(ctx, categoryID, row.ID, addNow)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, detail.Summary)
	}
	return summaries, nil
}
