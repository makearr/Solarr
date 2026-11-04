package movie_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/statistics/statisticstest"
	"github.com/makearr/Solarr/internal/media/movie"
)

// Radarr's automatic search sends the best accepted release of the film, and never searches a film already downloading.
func TestGrab_SendsTheBestReleaseAndNeverSearchesAFilmAlreadyDownloading(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: movieProfile(t, cache, "Movie HD 1080p"), Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"movie": {
		feedRaw("The.Example.Film.1999.480p.DVDRip.x264-GRP", "small"),
		feedRaw("The.Example.Film.1999.1080p.BluRay.x264-GRP", "right"),
		feedRaw("The.Example.Sequel.2003.1080p.BluRay.x264-GRP", "sequel"),
	}}}
	client := &sender{}
	search := movie.Search{
		Desired: library.NewDesired(opened), Reader: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)),
		Settings: cache, Clock: atAddNow(),
	}
	grab := movieGrab(t, opened, search, dispatch.DownloaderConn{ID: 5, Protocol: "usenet", Client: client})
	conns := []judge.Conn{{ID: 1, Name: "test", Impl: "newznab", Client: fake}}

	first, err := grab.SearchAndSend(t.Context(), added.ItemID, conns, dispatch.GrabAutomatic, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if first.Sent == nil || first.Sent.CandidateTitle != "The.Example.Film.1999.1080p.BluRay.x264-GRP" {
		t.Fatalf("sent %+v, want the 1080p bluray", first.Sent)
	}
	if len(client.requests) != 1 || client.requests[0].Name != first.Sent.CandidateTitle {
		t.Errorf("client got %+v, want the same release once", client.requests)
	}

	calls := len(fake.requests)
	second, err := grab.SearchAndSend(t.Context(), added.ItemID, conns, dispatch.GrabAutomatic, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.Best, dispatch.Best{AlreadyInFlight: true}) || len(fake.requests) != calls || len(client.requests) != 1 {
		t.Errorf("second = %+v after %d more indexer calls; want AlreadyInFlight, no search, no second send", second.Best, len(fake.requests)-calls)
	}
}

// Finding nothing acceptable is no error and sends nothing.
func TestGrab_NothingAcceptableSendsNothing(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: movieProfile(t, cache, "Movie HD 1080p"), Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"movie": {feedRaw("The.Example.Film.1999.480p.DVDRip.x264-GRP", "small")}}}
	client := &sender{}
	search := movie.Search{
		Desired: library.NewDesired(opened), Reader: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)),
		Settings: cache, Clock: atAddNow(),
	}
	grab := movieGrab(t, opened, search, dispatch.DownloaderConn{ID: 5, Protocol: "usenet", Client: client})
	result, err := grab.SearchAndSend(t.Context(), added.ItemID, []judge.Conn{{ID: 1, Impl: "newznab", Client: fake}}, dispatch.GrabAutomatic, addNow)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sent != nil || len(client.requests) != 0 {
		t.Errorf("sent %+v, client %+v; want nothing sent", result.Sent, client.requests)
	}
}

// The film is not searched again while its download is on its way, with statistics.db emptied after the send
// (revision 2, D7's invariant).
func TestGrab_AlreadyInFlightSurvivesStatisticsEmptied(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: movieProfile(t, cache, "Movie HD 1080p"), Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"movie": {feedRaw("The.Example.Film.1999.1080p.BluRay.x264-GRP", "right")}}}
	stats := statisticstest.Open(t)
	search := movie.Search{
		Desired: library.NewDesired(opened), Reader: library.NewSeriesReader(opened), Candidates: statistics.NewCandidates(stats),
		Settings: cache, Clock: atAddNow(),
	}
	grab := movieGrab(t, opened, search, dispatch.DownloaderConn{ID: 5, Protocol: "usenet", Client: &sender{}})
	conns := []judge.Conn{{ID: 1, Name: "test", Impl: "newznab", Client: fake}}
	if first, err := grab.SearchAndSend(t.Context(), added.ItemID, conns, dispatch.GrabAutomatic, addNow); err != nil || first.Sent == nil {
		t.Fatalf("first = %+v, %v; want a send", first, err)
	}
	for _, table := range []string{"candidate_decision", "candidate", "decision_rollup", "history", "stat_rollup"} {
		if _, err := stats.WriteDB().ExecContext(t.Context(), "DELETE FROM "+table); err != nil {
			t.Fatalf("empty %s: %v", table, err)
		}
	}
	calls := len(fake.requests)
	second, err := grab.SearchAndSend(t.Context(), added.ItemID, conns, dispatch.GrabAutomatic, addNow)
	if err != nil || !second.AlreadyInFlight || len(fake.requests) != calls {
		t.Errorf("second = %+v, %v after %d more indexer calls; want AlreadyInFlight and no search", second.Best, err, len(fake.requests)-calls)
	}
}
