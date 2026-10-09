package music_test

import (
	"context"
	"log/slog"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
)

// Lidarr's RSS Sync: a wanted album's release in an indexer's feed is sent without any search,
// and another album, another artist or a video in the same feed is not.
func TestRSSSync_SendsAWantedAlbumsReleaseFromTheFeed(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	feed := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{
			rawItem("guid-show", "Ol.Pejeta.Diaries.S01E01.1080p.WEB-DL.H.264-GROUP"),
			rawItem("guid-other-album", "Nosi Zada-Homework-1997-FLAC-GROUP"),
			rawItem("guid-other-artist", "Pevulimo-Discovery-2001-FLAC-GROUP"),
			rawItem("guid-wanted", "NOSI ZADA-Discovery-2001-FLAC-GROUP"),
		}, nil
	}}
	sender := &fakeSabnzbdSender{clientRef: "nzo-1"}
	feedOf := music.Feed{
		Grab: music.Grab{Search: env.search(), Dispatch: env.dispatch([]music.DownloaderConn{
			{ID: 5, Protocol: "usenet", Client: sender},
		})},
		Indexers: func(context.Context) ([]music.IndexerConn, error) {
			return []music.IndexerConn{{ID: 1, Name: "Feed", Impl: "newznab", Client: feed}}, nil
		},
		WantedByArtist: library.NewHunt(env.library).WantedAlbumsByArtist,
		Logger:         slog.New(slog.DiscardHandler),
	}
	job := rss.NewJob(rss.Config{
		Sources: func() []rss.Source { return []rss.Source{{Name: "music", Feed: feedOf}} },
		Clock:   env.clock, Logger: slog.New(slog.DiscardHandler),
	})

	for range 2 {
		if err := job.Run(t.Context(), jobs.Job{Type: rss.JobType}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	if len(sender.requests) != 1 || sender.requests[0].Name != "NOSI ZADA-Discovery-2001-FLAC-GROUP" {
		t.Fatalf("sent %+v, want the wanted album's release once", sender.requests)
	}
	wantRequest := indexer.SearchRequest{Function: "music", Categories: []int{3000}, Limit: 100}
	if !reflect.DeepEqual(feed.requests[0], wantRequest) {
		t.Errorf("feed request = %+v, want %+v", feed.requests[0], wantRequest)
	}
	decisions, err := env.candidates.ListDecisionsForItem(t.Context(), item.ID, 10)
	if err != nil || len(decisions) != 1 || decisions[0].ReleaseCandidate.Source != "rss" {
		t.Errorf("decisions = %+v, %v; want the one release, recorded as found by RSS", decisions, err)
	}
	searched, err := librarydb.New(env.library.Read()).ListHuntSearchTimesForWork(t.Context(), item.WorkID)
	if err != nil || len(searched) != 0 {
		t.Errorf("search record = %+v, %v; an RSS find is not a search and leaves the backoff alone", searched, err)
	}
	if seen := env.rssSeenAt(t, item.ID); len(decisions) == 0 || seen != decisions[0].ReleaseCandidate.FirstSeenAt {
		t.Errorf("rss sighting = %d, want the release's first sighting: what makes the album fresh (ADR-55)", seen)
	}
}

// A search's find is no RSS sighting: only a feed makes an old album fresh (ADR-55).
func TestSearch_AFindIsNoRSSSighting(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{rawItem("guid-wanted", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
	}}
	grab := music.Grab{Search: env.search(), Dispatch: env.dispatch([]music.DownloaderConn{
		{ID: 5, Protocol: "usenet", Client: &fakeSabnzbdSender{clientRef: "nzo-1"}},
	})}
	if _, err := grab.SearchAndSend(t.Context(), item.ID, []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, music.GrabAutomatic, env.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if seen := env.rssSeenAt(t, item.ID); seen != 0 {
		t.Errorf("rss sighting = %d after a search, want none", seen)
	}
}
