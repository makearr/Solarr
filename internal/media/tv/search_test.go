package tv_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/statistics/statisticstest"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

type fakeIndexer struct {
	byFunction map[string][]indexer.RawItem
	requests   []indexer.SearchRequest
}

func (f *fakeIndexer) Search(_ context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	f.requests = append(f.requests, request)
	return f.byFunction[request.Function], nil
}

func settingsCache(t *testing.T) *settings.Cache {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{Name: "settings", Path: filepath.Join(dir, "settings.db"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, filepath.Join(dir, "backups"), owner); err != nil {
		t.Fatal(err)
	}
	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func profileID(t *testing.T, cache *settings.Cache, name string) int64 {
	t.Helper()
	for _, profile := range cache.Snapshot().QualityProfiles() {
		if profile.MediaType == "tv" && profile.Name == name {
			return profile.ID
		}
	}
	t.Fatalf("no shipped TV profile %q", name)
	return 0
}

func TestSearchRecordsADecisionPerReleaseThatCoversAWantedEpisode(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "WEB 1080p"), Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	items, err := library.NewDesired(opened).AlbumItemsForWork(t.Context(), added.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	var season1 int64
	for _, item := range items {
		if item.MediaRef == "s01" {
			season1 = item.ID
		}
	}

	raw := func(title, guid string, size int64) indexer.RawItem {
		return indexer.RawItem{Title: title, GUID: guid, Link: "https://example.test/" + guid, SizeBytes: size}
	}
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"tvsearch": {
		raw("Example.Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP", "single", 2e9),
		raw("Example.Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP", "pack", 9e9),
		raw("Other.Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP", "other", 2e9),
		raw("Example.Show.S02E01.1080p.WEB-DL.DDP5.1.H.264-GRP", "season2", 2e9),
		raw("Example.Show.S01E02.480p.HDTV.x264-GRP", "small", 3e8),
	}}}
	conn := judge.Conn{ID: 1, Name: "test", Impl: "newznab", Client: fake}

	candidates := statistics.NewCandidates(statisticstest.Open(t))
	search := tv.Search{Desired: library.NewDesired(opened), Series: library.NewSeriesReader(opened), Candidates: candidates, Settings: cache, Clock: clockAt(addNow)}
	result, err := search.SearchItem(t.Context(), season1, []judge.Conn{conn}, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if result.TierUsed == nil || *result.TierUsed != 0 || len(result.Decisions) != 3 {
		t.Fatalf("tier %v, decisions %d: %+v", result.TierUsed, len(result.Decisions), result.Decisions)
	}
	titles := []string{}
	outcomes := []string{}
	for _, d := range result.Decisions {
		titles = append(titles, d.ReleaseCandidate.GUID)
		outcomes = append(outcomes, d.Decision.Outcome)
	}
	if titles[0] != "pack" || titles[1] != "single" || titles[2] != "small" {
		t.Errorf("order = %v: the pack covering both episodes leads at equal quality", titles)
	}
	if outcomes[0] != "ACCEPT" || outcomes[1] != "ACCEPT" || outcomes[2] != "REJECT" {
		t.Errorf("outcomes = %v: %s / %s", outcomes, result.Decisions[2].Decision.RejectionsJSON, result.Decisions[2].Decision.DeferralsJSON)
	}
	if len(fake.requests) != 1 || fake.requests[0].Extra["tvdbid"] != "1234" || fake.requests[0].Extra["season"] != "1" {
		t.Errorf("the id query goes first: %+v", fake.requests)
	}
}

func TestSearchWidensToTextWhenTheIDQueryFindsNothing(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	added, _ := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "Any"), Monitor: tv.MonitorAll})
	items, _ := library.NewDesired(opened).AlbumItemsForWork(t.Context(), added.WorkID)
	var season1 int64
	for _, item := range items {
		if item.MediaRef == "s01" {
			season1 = item.ID
		}
	}
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"search": {
		{Title: "Example Show S01 720p HDTV x264-GRP", GUID: "g", Link: "https://example.test/g"},
	}}}
	search := tv.Search{Desired: library.NewDesired(opened), Series: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)), Settings: cache, Clock: clockAt(addNow)}
	result, err := search.SearchItem(t.Context(), season1, []judge.Conn{{ID: 1, Impl: "newznab", Client: fake}}, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if result.TierUsed == nil || *result.TierUsed != 1 || len(result.Decisions) != 1 {
		t.Fatalf("tier %v decisions %d", result.TierUsed, len(result.Decisions))
	}
	if len(fake.requests) != 2 || fake.requests[1].Query != "Example Show S01" {
		t.Errorf("requests = %+v", fake.requests)
	}
}

func clockAt(_ interface{ Unix() int64 }) clock.Clock {
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	return fake
}

// queryIndexer answers only the exact query it was given an answer for.
type queryIndexer struct {
	answers    map[string][]indexer.RawItem
	requests   []string
	categories [][]int
}

func (q *queryIndexer) Search(_ context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	q.requests = append(q.requests, request.Query)
	q.categories = append(q.categories, request.Categories)
	return q.answers[request.Query], nil
}

func animeSkyhook() skyhook.Series {
	absolute := func(n int) *int { return &n }
	return skyhook.Series{
		TVDBID: 1234, Title: "Example Show", FirstAired: "2020-01-05", Status: "Continuing", OriginalLanguage: "jpn", AniListIDs: []int64{9},
		Episodes: []skyhook.Episode{
			{TVDBID: 1, SeasonNumber: 1, EpisodeNumber: 1, AbsoluteEpisodeNumber: absolute(1), Title: "One", AirDate: "2020-01-05"},
			{TVDBID: 2, SeasonNumber: 1, EpisodeNumber: 2, AbsoluteEpisodeNumber: absolute(2), Title: "Two", AirDate: "2020-01-12"},
		},
	}
}

func TestAnAnimeSeasonIsAlsoSearchedByAbsoluteNumberAndFoundThatWay(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, searchSkyhook{series: animeSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Reader = library.NewSeriesReader(opened)
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "Any"), Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := library.NewSeriesReader(opened).Get(t.Context(), 7, added.WorkID, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.Fields["series_type"] != "anime" {
		t.Fatalf("series type = %q, want anime detected from the AniList id and the Japanese language", detail.Summary.Fields["series_type"])
	}
	items, _ := library.NewDesired(opened).AlbumItemsForWork(t.Context(), added.WorkID)
	var season1 int64
	for _, item := range items {
		if item.MediaRef == "s01" {
			season1 = item.ID
		}
	}
	fake := &queryIndexer{answers: map[string][]indexer.RawItem{
		"Example Show 002": {{Title: "[SubGroup] Example Show - 02 (1080p) [ABCD1234]", GUID: "g", Link: "https://example.test/g"}},
	}}
	search := tv.Search{Desired: library.NewDesired(opened), Series: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)), Settings: cache, Clock: clockAt(addNow)}
	result, err := search.SearchItem(t.Context(), season1, []judge.Conn{{ID: 1, Impl: "newznab", Client: fake, Categories: []int{5000}}}, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Decisions) != 1 {
		t.Errorf("decisions = %d after queries %v; the absolute-number release should cover episode 2", len(result.Decisions), fake.requests)
	}
	for index, query := range fake.requests {
		if !slices.Equal(fake.categories[index], []int{5000, 5070}) {
			t.Errorf("query %q asked in %v, want the indexer's 5000 and anime's 5070", query, fake.categories[index])
		}
	}
}

// An anime library says what the providers did not: a series they call standard is anime there, and only there.
func TestASeriesAddedToAnAnimeLibraryIsAnime(t *testing.T) {
	cache := settingsCache(t)
	for kind, want := range map[string]string{tv.KindAnime: "anime", tv.KindSeries: "standard"} {
		service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
		service.Reader = library.NewSeriesReader(opened)
		service.KindOf = func(int64) string { return kind }
		added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "Any"), Monitor: tv.MonitorAll})
		if err != nil {
			t.Fatal(err)
		}
		detail, err := library.NewSeriesReader(opened).Get(t.Context(), 7, added.WorkID, addNow)
		if err != nil || detail.Summary.Fields["series_type"] != want {
			t.Errorf("added to a %s library: series type = %q, %v; want %s", kind, detail.Summary.Fields["series_type"], err, want)
		}
	}
}

func TestOneEpisodeCanBeSearchedWhateverItsMonitoring(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	added, _ := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "Any"), Monitor: tv.MonitorNone})
	items, _ := library.NewDesired(opened).AlbumItemsForWork(t.Context(), added.WorkID)
	var season1 int64
	for _, item := range items {
		if item.MediaRef == "s01" {
			season1 = item.ID
		}
	}
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"tvsearch": {
		{Title: "Example Show S01E02 720p HDTV x264-GRP", GUID: "g", Link: "https://example.test/g"},
	}}}
	search := tv.Search{Desired: library.NewDesired(opened), Series: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)), Settings: cache, Clock: clockAt(addNow)}
	nothing, err := search.SearchItem(t.Context(), season1, []judge.Conn{{ID: 1, Impl: "newznab", Client: fake}}, addNow)
	if err != nil || len(nothing.Decisions) != 0 || len(fake.requests) != 0 {
		t.Fatalf("a season with nothing wanted asked %d times and found %d (err %v)", len(fake.requests), len(nothing.Decisions), err)
	}
	result, err := search.SearchEpisode(t.Context(), season1, 2, []judge.Conn{{ID: 1, Impl: "newznab", Client: fake}}, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Decisions) != 1 || len(fake.requests) == 0 || fake.requests[0].Extra["ep"] != "2" {
		t.Errorf("decisions = %d, requests = %+v; want a search for episode 2 that finds its release", len(result.Decisions), fake.requests)
	}
}

func TestSearchHoldsEveryReleaseAgainstTheSeriesOwnFilters(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	added, _ := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "WEB 1080p"), Monitor: tv.MonitorAll})
	items, _ := library.NewDesired(opened).AlbumItemsForWork(t.Context(), added.WorkID)
	var season1 int64
	for _, item := range items {
		if item.MediaRef == "s01" {
			season1 = item.ID
		}
	}
	filters := library.NewReleaseFilters(opened)
	if err := filters.Replace(t.Context(), added.WorkID, []library.ReleaseFilter{{Kind: judge.FilterGroupDeny, Value: "BAD"}}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"tvsearch": {
		{Title: "Example.Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-BAD", GUID: "denied", Link: "https://example.test/denied", SizeBytes: 2e9},
		{Title: "Example.Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-GOOD", GUID: "fine", Link: "https://example.test/fine", SizeBytes: 2e9},
	}}}
	search := tv.Search{Desired: library.NewDesired(opened), Series: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)),
		Settings: cache, Clock: clockAt(addNow), ReleaseFilters: &filters}
	result, err := search.SearchItem(t.Context(), season1, []judge.Conn{{ID: 1, Name: "test", Impl: "newznab", Client: fake}}, addNow)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]string{}
	for _, d := range result.Decisions {
		outcomes[d.ReleaseCandidate.GUID] = d.Decision.Outcome
	}
	if outcomes["denied"] != "REJECT" || outcomes["fine"] != "ACCEPT" {
		t.Errorf("outcomes = %v, want the denied group rejected and the other accepted", outcomes)
	}
}
