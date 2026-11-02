package movie_test

import (
	"context"
	"log/slog"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/statistics/statisticstest"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/movie"
)

// sender is a download client that takes everything and remembers what it was handed.
type sender struct {
	download.Client
	requests []download.AddRequest
}

func (s *sender) Add(_ context.Context, request download.AddRequest) (string, error) {
	s.requests = append(s.requests, request)
	return "nzo-1", nil
}

func movieGrab(t *testing.T, opened *store.Store, search movie.Search, clients ...dispatch.DownloaderConn) movie.Grab {
	t.Helper()
	return movie.Grab{Search: search, Automatic: dispatch.Automatic{Name: "movie", Dispatch: dispatch.Dispatch{
		Candidates: search.Candidates, Acquisitions: library.NewAcquisitions(opened), Desired: search.Desired,
		Downloaders: func(context.Context) ([]dispatch.DownloaderConn, error) { return clients, nil },
		Fetch:       func(context.Context, int64, string) ([]byte, error) { return []byte("nzb"), nil },
	}}}
}

func feedRaw(title, guid string) indexer.RawItem {
	return indexer.RawItem{Title: title, GUID: guid, Link: "https://example.test/" + guid, SizeBytes: 9e9}
}

// Radarr's RSS Sync: a release naming a monitored film and its year is judged and recorded as found by RSS; another
// film, a wrong year and a film of another library are not, and nothing is searched.
func TestFeed_OffersOnlyReleasesNamingTheFilmAndItsYear(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: movieProfile(t, cache, "Movie HD 1080p"), Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	feed := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"movie": {
		feedRaw("The.Example.Film.1999.1080p.BluRay.x264-GRP", "wanted"),
		feedRaw("Other.Film.1999.1080p.BluRay.x264-GRP", "other-film"),
		feedRaw("The.Example.Film.1995.1080p.BluRay.x264-GRP", "wrong-year"),
		feedRaw("The.Example.Film.1080p.BluRay.x264-GRP", "no-year"),
	}}}
	candidates := statistics.NewCandidates(statisticstest.Open(t))
	search := movie.Search{Desired: library.NewDesired(opened), Reader: library.NewSeriesReader(opened), Candidates: candidates, Settings: cache, Clock: atAddNow()}
	source := movie.Feed{
		Grab: movieGrab(t, opened, search), Reader: library.NewSeriesReader(opened), Logger: slog.New(slog.DiscardHandler),
		Indexers: func(context.Context) ([]judge.Conn, error) {
			return []judge.Conn{{ID: 1, Name: "Feed", Impl: "newznab", Client: feed}}, nil
		},
		Libraries: func() []int64 { return []int64{5} },
	}
	run := func(source movie.Feed) {
		job := rss.NewJob(rss.Config{
			Sources: func() []rss.Source { return []rss.Source{{Name: "movies", Feed: source}} },
			Clock:   atAddNow(), Logger: slog.New(slog.DiscardHandler),
		})
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	run(source)

	wantRequest := indexer.SearchRequest{Function: "movie", Categories: []int{2000}, Limit: 100}
	if len(feed.requests) != 1 || !reflect.DeepEqual(feed.requests[0], wantRequest) {
		t.Errorf("feed requests = %+v, want one %+v", feed.requests, wantRequest)
	}
	decisions, err := candidates.ListDecisionsForItem(t.Context(), added.ItemID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for _, decision := range decisions {
		recorded = append(recorded, decision.ReleaseCandidate.GUID+" by "+decision.ReleaseCandidate.Source)
	}
	if want := []string{"wanted by rss"}; !reflect.DeepEqual(recorded, want) {
		t.Errorf("decisions = %v, want %v", recorded, want)
	}

	other := &fakeIndexer{byFunction: feed.byFunction}
	source.Libraries = func() []int64 { return []int64{9} }
	source.Indexers = func(context.Context) ([]judge.Conn, error) {
		return []judge.Conn{{ID: 1, Impl: "newznab", Client: other}}, nil
	}
	before := len(decisions)
	run(source)
	decisions, _ = candidates.ListDecisionsForItem(t.Context(), added.ItemID, 10)
	if len(decisions) != before {
		t.Errorf("decisions = %d after a feed for another library, want the %d kept", len(decisions), before)
	}
}

// An RSS grab sends the accepted release to the client, and a film already downloading is not judged again.
func TestFeed_SendsTheAcceptedReleaseOnce(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: movieProfile(t, cache, "Movie HD 1080p"), Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	client := &sender{}
	candidates := statistics.NewCandidates(statisticstest.Open(t))
	search := movie.Search{Desired: library.NewDesired(opened), Reader: library.NewSeriesReader(opened), Candidates: candidates, Settings: cache, Clock: atAddNow()}
	source := movie.Feed{
		Grab:   movieGrab(t, opened, search, dispatch.DownloaderConn{ID: 5, Protocol: "usenet", Client: client}),
		Reader: library.NewSeriesReader(opened), Logger: slog.New(slog.DiscardHandler),
		Libraries: func() []int64 { return []int64{5} },
	}
	conn := judge.Conn{ID: 1, Impl: "newznab"}
	raws := []indexer.RawItem{
		feedRaw("The.Example.Film.1999.480p.DVDRip.x264-GRP", "small"),
		feedRaw("The.Example.Film.1999.1080p.BluRay.x264-GRP", "right"),
	}
	first := source.OfferFeed(t.Context(), conn, raws, addNow)
	if want := (rss.Tally{Judged: 1, Sent: 1}); first != want {
		t.Errorf("first tally = %+v, want %+v", first, want)
	}
	if len(client.requests) != 1 || client.requests[0].Name != "The.Example.Film.1999.1080p.BluRay.x264-GRP" {
		t.Errorf("client got %+v, want the 1080p release", client.requests)
	}
	if second := source.OfferFeed(t.Context(), conn, raws, addNow); second != (rss.Tally{}) || len(client.requests) != 1 {
		t.Errorf("second tally = %+v, sends %d; want nothing while the first is downloading (item %d)", second, len(client.requests), added.ItemID)
	}
}

// A client whose queue is full holds an automatic grab but takes an RSS one when it is set to (2026-09-29), which
// shows the feed grabs as RSS.
func TestFeed_GrabsAsRSSPastAFullQueue(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: movieProfile(t, cache, "Movie HD 1080p"), Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewAcquisitions(opened).Create(t.Context(), library.NewAcquisition{
		CandidateGUID: "filler", CandidateTitle: "Filler", DownloaderID: 5, DownloadClientRef: "nzo-0", Protocol: "usenet", State: "QUEUED", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	client := &sender{}
	search := movie.Search{Desired: library.NewDesired(opened), Reader: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)), Settings: cache, Clock: atAddNow()}
	grab := movieGrab(t, opened, search, dispatch.DownloaderConn{ID: 5, Protocol: "usenet", Client: client, MaxQueued: 1, AlwaysSendRSS: true})
	ready := library.NewReadyQueue(opened)
	grab.Automatic.Dispatch.Ready = &ready
	conn := judge.Conn{ID: 1, Impl: "newznab"}
	raws := []indexer.RawItem{feedRaw("The.Example.Film.1999.1080p.BluRay.x264-GRP", "right")}

	held, err := grab.SearchAndSend(t.Context(), added.ItemID, []judge.Conn{{ID: 1, Impl: "newznab", Client: &fakeIndexer{byFunction: map[string][]indexer.RawItem{"movie": raws}}}}, dispatch.GrabAutomatic, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if !held.Held || held.Sent != nil || len(client.requests) != 0 {
		t.Fatalf("automatic = held %v sent %+v, want held with the queue full", held.Held, held.Sent)
	}
	sent, err := grab.JudgeFeedAndSend(t.Context(), added.ItemID, conn, raws, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if sent.Sent == nil || len(client.requests) != 1 {
		t.Errorf("rss = %+v, want sent past the full queue", sent.Best)
	}
}
