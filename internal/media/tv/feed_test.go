package tv_test

import (
	"context"
	"log/slog"
	"reflect"
	"sort"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
)

// Sonarr's RSS Sync: a release in an indexer's feed that names a series of the library is judged against that
// series' wanted seasons without any search, and recorded as found by RSS. Another show's release is not, and a
// release of a season that wants nothing records nothing.
func TestFeed_OffersAReleaseToTheWantedSeasonOfTheSeriesItNames(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "WEB 1080p"), Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	desired := library.NewDesired(opened)
	items, err := desired.AlbumItemsForWork(t.Context(), added.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	seasonItem := map[string]int64{}
	for _, item := range items {
		seasonItem[item.MediaRef] = item.ID
	}

	raw := func(title, guid string) indexer.RawItem {
		return indexer.RawItem{Title: title, GUID: guid, Link: "https://example.test/" + guid, SizeBytes: 2e9}
	}
	feed := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"tvsearch": {
		raw("Example.Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP", "wanted"),
		raw("Other.Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP", "other-show"),
		raw("Example.Show.S09E01.1080p.WEB-DL.DDP5.1.H.264-GRP", "no-such-season"),
	}}}
	candidates := library.NewCandidates(opened)
	search := tv.Search{Desired: desired, Series: library.NewSeriesReader(opened), Candidates: candidates, Settings: cache, Clock: clockAt(addNow)}
	noClient := func(context.Context) ([]dispatch.DownloaderConn, error) { return nil, nil }
	grab := tv.Grab{Search: search, Dispatch: dispatch.Dispatch{
		Candidates: candidates, Acquisitions: library.NewAcquisitions(opened), Desired: desired, Downloaders: noClient,
	}}
	source := tv.Feed{
		Grab: grab, Reader: library.NewSeriesReader(opened), Logger: slog.New(slog.DiscardHandler),
		Indexers: func(context.Context) ([]judge.Conn, error) {
			return []judge.Conn{{ID: 1, Name: "Feed", Impl: "newznab", Client: feed}}, nil
		},
		Libraries: func() []int64 { return []int64{7} },
	}
	job := rss.NewJob(rss.Config{
		Sources: func() []rss.Source { return []rss.Source{{Name: "tv", Feed: source}} },
		Clock:   clockAt(addNow), Logger: slog.New(slog.DiscardHandler),
	})
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantRequest := indexer.SearchRequest{Function: "tvsearch", Categories: []int{5000}, Limit: 100}
	if len(feed.requests) != 1 || !reflect.DeepEqual(feed.requests[0], wantRequest) {
		t.Errorf("feed requests = %+v, want one %+v", feed.requests, wantRequest)
	}
	recorded := map[string][]string{}
	for ref, itemID := range seasonItem {
		decisions, err := candidates.ListDecisionsForItem(t.Context(), itemID, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, decision := range decisions {
			recorded[ref] = append(recorded[ref], decision.ReleaseCandidate.GUID+" by "+decision.ReleaseCandidate.Source)
		}
		sort.Strings(recorded[ref])
	}
	if want := map[string][]string{"s01": {"wanted by rss"}}; !reflect.DeepEqual(recorded, want) {
		t.Errorf("decisions recorded per season = %v, want %v", recorded, want)
	}
}
