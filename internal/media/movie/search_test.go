package movie_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/statistics/statisticstest"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media/movie"
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

func movieProfile(t *testing.T, cache *settings.Cache, name string) int64 {
	t.Helper()
	for _, profile := range cache.Snapshot().QualityProfiles() {
		if profile.MediaType == movie.MediaType && profile.Name == name {
			return profile.ID
		}
	}
	t.Fatalf("no shipped movie profile %q", name)
	return 0
}

func atAddNow() clock.Clock {
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	return fake
}

func TestSearchJudgesOnlyReleasesOfTheMovieAndAsksByIDFirst(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: movieProfile(t, cache, "Movie HD 1080p"), Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	raw := func(title, guid string, size int64) indexer.RawItem {
		return indexer.RawItem{Title: title, GUID: guid, Link: "https://example.test/" + guid, SizeBytes: size}
	}
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"movie": {
		raw("The.Example.Film.1999.1080p.BluRay.x264-GRP", "right", 9e9),
		raw("The.Example.Sequel.2003.1080p.BluRay.x264-GRP", "sequel", 9e9),
		raw("The.Example.Film.1995.1080p.BluRay.x264-GRP", "wrong-year", 9e9),
		raw("The.Example.Film.1999.480p.DVDRip.x264-GRP", "small", 7e8),
	}}}
	search := movie.Search{
		Desired: library.NewDesired(opened), Reader: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)),
		Settings: cache, Clock: atAddNow(),
	}
	result, err := search.SearchItem(t.Context(), added.ItemID, []judge.Conn{{ID: 1, Name: "test", Impl: "newznab", Client: fake}}, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if result.TierUsed == nil || *result.TierUsed != 0 || len(result.Decisions) != 2 {
		t.Fatalf("tier %v, decisions %d: only the film's own two releases are judged", result.TierUsed, len(result.Decisions))
	}
	if first := result.Decisions[0]; first.ReleaseCandidate.GUID != "right" || first.Decision.Outcome != "ACCEPT" {
		t.Errorf("first = %s %s, want the 1080p bluray accepted", first.ReleaseCandidate.GUID, first.Decision.Outcome)
	}
	if second := result.Decisions[1]; second.ReleaseCandidate.GUID != "small" || second.Decision.Outcome != "REJECT" {
		t.Errorf("second = %s %s, want the DVDRip rejected by the profile's resolution gate", second.ReleaseCandidate.GUID, second.Decision.Outcome)
	}
	if len(fake.requests) == 0 || fake.requests[0].Function != "movie" || fake.requests[0].Extra["imdbid"] != "0000001" {
		t.Errorf("the id query goes first: %+v", fake.requests)
	}
}

func TestSearchWidensToTextWhenTheIDQueryFindsNothing(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, _ := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: movieProfile(t, cache, "Movie Any"), Monitor: movie.MonitorMovieOnly})
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"search": {
		{Title: "The Example Film 1999 720p BluRay x264-GRP", GUID: "g", Link: "https://example.test/g"},
	}}}
	search := movie.Search{
		Desired: library.NewDesired(opened), Reader: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)),
		Settings: cache, Clock: atAddNow(),
	}
	result, err := search.SearchItem(t.Context(), added.ItemID, []judge.Conn{{ID: 1, Impl: "newznab", Client: fake}}, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if result.TierUsed == nil || *result.TierUsed != 1 || len(result.Decisions) != 1 {
		t.Fatalf("tier %v, decisions %d; want the text tier with the year", result.TierUsed, len(result.Decisions))
	}
	last := fake.requests[len(fake.requests)-1]
	if last.Query != "The Example Film 1999" {
		t.Errorf("last query = %q, want the title with its year", last.Query)
	}
}
