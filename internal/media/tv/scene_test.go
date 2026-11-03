package tv_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/statistics/statisticstest"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/thexem"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
)

type fakeScene struct {
	mappings []thexem.Mapping
	names    []string
	err      error
}

func (f *fakeScene) Mappings(context.Context, int64) ([]thexem.Mapping, error) {
	return f.mappings, f.err
}
func (f *fakeScene) Names(context.Context, int64) ([]string, error) { return f.names, f.err }

// TheTVDB lists five episodes where the scene released two seasons and a third: S01E03 is the scene's S02E01.
func sceneSkyhook() skyhook.Series {
	episode := func(id int64, season, number int, date string) skyhook.Episode {
		return skyhook.Episode{TVDBID: id, SeasonNumber: season, EpisodeNumber: number, Title: "Episode", AirDate: date}
	}
	return skyhook.Series{
		TVDBID: 1234, Title: "Example Show", FirstAired: "2025-01-05", Status: "Continuing",
		Episodes: []skyhook.Episode{
			episode(1, 1, 1, "2025-01-05"), episode(2, 1, 2, "2025-01-12"), episode(3, 1, 3, "2025-01-19"), episode(4, 1, 4, "2025-01-26"),
			episode(5, 2, 1, "2025-06-01"),
		},
	}
}

func sceneMap() *fakeScene {
	mapping := func(season, episode, sceneSeason, sceneEpisode int) thexem.Mapping {
		return thexem.Mapping{TVDB: thexem.Number{Season: season, Episode: episode}, Scene: thexem.Number{Season: sceneSeason, Episode: sceneEpisode}}
	}
	return &fakeScene{
		mappings: []thexem.Mapping{mapping(1, 1, 1, 1), mapping(1, 2, 1, 2), mapping(1, 3, 2, 1), mapping(1, 4, 2, 2), mapping(2, 1, 3, 1)},
		names:    []string{"Example Show", "Example Show Scene"},
	}
}

func sceneNumbersOf(t *testing.T, reader library.SeriesReader, workID int64) map[string]string {
	t.Helper()
	detail, err := reader.Get(t.Context(), 7, workID, addNow)
	if err != nil {
		t.Fatal(err)
	}
	numbers := map[string]string{}
	for _, episode := range detail.Episodes {
		if episode.Scene != "" {
			numbers[tv.MediaRef(episode.Season, episode.Number)] = episode.Scene
		}
	}
	return numbers
}

func seasonItem(t *testing.T, desired library.Desired, workID int64, ref string) int64 {
	t.Helper()
	items, err := desired.AlbumItemsForWork(t.Context(), workID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.MediaRef == ref {
			return item.ID
		}
	}
	t.Fatalf("no item %s", ref)
	return 0
}

func TestSceneNumbersAreStoredSearchedByAndEitherNumberIsAccepted(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeSkyhook{series: sceneSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Scene = sceneMap()
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "Any"), Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	reader := library.NewSeriesReader(opened)
	want := map[string]string{"s01:e01": "s01:e01", "s01:e02": "s01:e02", "s01:e03": "s02:e01", "s01:e04": "s02:e02", "s02:e01": "s03:e01"}
	if got := sceneNumbersOf(t, reader, added.WorkID); !reflect.DeepEqual(got, want) {
		t.Errorf("scene numbers = %v, want %v", got, want)
	}
	detail, _ := reader.Get(t.Context(), 7, added.WorkID, addNow)
	if !slices.Contains(detail.Aliases, "Example Show Scene") {
		t.Errorf("aliases = %q, want TheXEM's scene name", detail.Aliases)
	}

	release := func(title string) indexer.RawItem {
		return indexer.RawItem{Title: title, GUID: title, Link: "https://example.test/" + title, SizeBytes: 2e9}
	}
	sceneNumbered, tvdbNumbered := "Example.Show.S02E01.1080p.WEB-DL.DDP5.1.H.264-GRP", "Example.Show.S01E04.1080p.WEB-DL.DDP5.1.H.264-GRP"
	fake := &queryIndexer{answers: map[string][]indexer.RawItem{
		"Example Show S02":    {release(sceneNumbered)},
		"Example Show S01":    {release(tvdbNumbered)},
		"Example Show S03E01": {release("Example.Show.S03E01.1080p.WEB-DL.DDP5.1.H.264-GRP"), release(sceneNumbered)},
	}}
	search := tv.Search{Desired: library.NewDesired(opened), Series: reader, Candidates: statistics.NewCandidates(statisticstest.Open(t)), Settings: cache, Clock: clockAt(addNow)}
	conns := []judge.Conn{{ID: 1, Impl: "newznab", Client: fake}}

	first, err := search.SearchItem(t.Context(), seasonItem(t, library.NewDesired(opened), added.WorkID, "s01"), conns, addNow)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, decision := range first.Decisions {
		found = append(found, decision.ReleaseCandidate.GUID)
	}
	slices.Sort(found)
	if wantFound := []string{tvdbNumbered, sceneNumbered}; !slices.Equal(found, wantFound) {
		t.Errorf("TheTVDB season 1 found %q after %q, want the scene's S02E01 and TheTVDB's S01E04", found, fake.requests)
	}
	if !slices.Contains(fake.requests, "Example Show S02") {
		t.Errorf("requests = %q, want the scene's season 2 asked for TheTVDB's season 1", fake.requests)
	}

	fake.requests = nil
	second, err := search.SearchItem(t.Context(), seasonItem(t, library.NewDesired(opened), added.WorkID, "s02"), conns, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Decisions) != 1 || second.Decisions[0].ReleaseCandidate.GUID != "Example.Show.S03E01.1080p.WEB-DL.DDP5.1.H.264-GRP" {
		t.Errorf("TheTVDB season 2 decided %+v, want only the scene's S03E01: the scene's S02E01 is TheTVDB's S01E03", second.Decisions)
	}
	if slices.Contains(fake.requests, "Example Show S02") {
		t.Errorf("requests = %q: TheTVDB's season 2 is the scene's 3", fake.requests)
	}
}

func TestARefreshReplacesSceneNumbersAndAnOutageKeepsThem(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeSkyhook{series: sceneSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Reader = library.NewSeriesReader(opened)
	scene := sceneMap()
	service.Scene = scene
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "Any"), Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	refresh := func() map[string]string {
		t.Helper()
		if _, err := service.Refresh(t.Context(), tv.RefreshRequest{WorkID: added.WorkID, CategoryID: 7}); err != nil {
			t.Fatal(err)
		}
		return sceneNumbersOf(t, service.Reader, added.WorkID)
	}

	scene.err = errors.New("thexem: status 502")
	if got := refresh(); len(got) != 5 {
		t.Errorf("after an outage = %v, want the five numbers kept", got)
	}
	scene.err, scene.mappings = nil, scene.mappings[:3]
	if got, want := refresh(), map[string]string{"s01:e01": "s01:e01", "s01:e02": "s01:e02", "s01:e03": "s02:e01"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after TheXEM dropped two = %v, want %v", got, want)
	}
	scene.mappings = nil
	if got := refresh(); len(got) != 0 {
		t.Errorf("after TheXEM mapped nothing = %v, want none", got)
	}
}
