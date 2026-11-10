package tv_test

import (
	"context"
	"database/sql"
	"log/slog"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// fakeFilmIDs answers TheTVDB's films by id, ErrNotFound for the rest, and records what was asked.
type fakeFilmIDs struct {
	films map[int64][]tvdb.RemoteID
	asked []int64
}

func (f *fakeFilmIDs) MovieRemoteIDs(_ context.Context, id int64) (tvdb.Movie, error) {
	f.asked = append(f.asked, id)
	remote, ok := f.films[id]
	if !ok {
		return tvdb.Movie{}, tvdb.ErrNotFound
	}
	return tvdb.Movie{ID: id, RemoteIDs: remote}, nil
}

func addFilm(t *testing.T, service tv.Service, title string, ids ...library.ExternalIDWrite) int64 {
	t.Helper()
	stored, err := service.Series.Upsert(t.Context(), addNow, library.SeriesWrite{
		Work: library.WorkIdentity{MediaType: "movie", CanonicalTitle: title, SortTitle: title}, IDs: ids,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored.WorkID
}

func addHeldSeries(t *testing.T, service tv.Service, ids ...int64) map[int64]int64 {
	t.Helper()
	works := map[int64]int64{}
	for _, id := range ids {
		added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: id}, CategoryID: 7, ProfileID: 1, Monitor: tv.MonitorNone})
		if err != nil {
			t.Fatal(err)
		}
		works[id] = added.WorkID
	}
	return works
}

func classify(t *testing.T, opened *store.Store, workID int64, value string) {
	t.Helper()
	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetWorkClassification(ctx, librarydb.SetWorkClassificationParams{WorkID: workID, Kind: library.FranchiseListKind, Value: value, Source: "tvdb"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTheFranchisePassMarksTheHeldSeriesAndFilmsOfAnOfficialListAndAsksEachFilmOnce(t *testing.T) {
	service, opened := newService(t, seriesByID{
		11: eventSeries(11, "Glimmerton", eventEpisode(111, 1, 1, "Pilot", "2018-01-01")),
		22: eventSeries(22, "Glimmerton Nights", eventEpisode(222, 1, 1, "Pilot", "2019-01-01")),
	}, fakeTVmaze{err: tvmaze.ErrNotFound})
	series := addHeldSeries(t, service, 11, 22)
	byTMDB := addFilm(t, service, "Glimmerton: The Film", library.ExternalIDWrite{Provider: tmdb.KeyMovie, ExternalID: "4242"})
	byIMDb := addFilm(t, service, "Glimmerton Again", library.ExternalIDWrite{Provider: "imdb", ExternalID: "tt0000002"})
	stale := addFilm(t, service, "Unrelated Film", library.ExternalIDWrite{Provider: tmdb.KeyMovie, ExternalID: "5555"})
	// A film no longer on list 500 and a list series 11 is no longer on are both forgotten.
	classify(t, opened, stale, "tvdb:500")
	classify(t, opened, series[11], "tvdb:999")

	lists := &fakeSeriesLists{
		lists: map[int64][]tvdb.List{
			11: {{ID: 500, IsOfficial: true}, {ID: 600}, {ID: 700, IsOfficial: true}},
			22: {{ID: 500, IsOfficial: true}},
		},
		entries: map[int64][]tvdb.ListEntry{
			500: {{SeriesID: 11}, {SeriesID: 22}, {SeriesID: 33}, {MovieID: 900}, {MovieID: 901}, {MovieID: 902}, {MovieID: 903}, {MovieID: 900}},
			// A user's list says nothing, and an official one without films marks nothing.
			600: {{SeriesID: 11}, {MovieID: 904}},
			700: {{SeriesID: 11}, {SeriesID: 44}},
		},
	}
	films := &fakeFilmIDs{films: map[int64][]tvdb.RemoteID{
		900: {{ID: "tt0000001", SourceName: "IMDB"}, {ID: "4242", SourceName: "TheMovieDB.com"}},
		901: {{ID: "9999", SourceName: "TheMovieDB.com"}, {ID: "tt0000002", SourceName: "IMDB"}},
		902: {{ID: "7777", SourceName: "TheMovieDB.com"}},
	}}
	franchises := library.NewFranchises(opened)
	pass := tv.FranchisePass{
		Franchises: franchises, Crossovers: library.NewCrossovers(opened), Series: service.Series, Lists: lists, Films: films, FilmType: "movie",
		Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler),
	}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if want := []int64{900, 901, 902, 903}; !reflect.DeepEqual(films.asked, want) {
		t.Errorf("films asked %v, want %v: each film of the official list once", films.asked, want)
	}
	for mediaType, want := range map[string]map[int64][]string{
		"tv":    {series[11]: {"tvdb:500"}, series[22]: {"tvdb:500"}},
		"movie": {byTMDB: {"tvdb:500"}, byIMDb: {"tvdb:500"}},
	} {
		got, err := franchises.Classifications(t.Context(), mediaType, library.FranchiseListKind)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s lists = %v, want %v", mediaType, got, want)
		}
	}

	films.asked = nil
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(films.asked) != 0 {
		t.Errorf("films asked again within the month: %v", films.asked)
	}
}

func TestTheFranchiseListFilmStepStartsNoSeriesOnceItsLookupsAreSpentAndSkipsAListOfAWholeCatalogue(t *testing.T) {
	service, opened := newService(t, seriesByID{
		11: eventSeries(11, "Glimmerton", eventEpisode(111, 1, 1, "Pilot", "2018-01-01")),
		22: eventSeries(22, "Harbour Nights", eventEpisode(222, 1, 1, "Pilot", "2019-01-01")),
	}, fakeTVmaze{err: tvmaze.ErrNotFound})
	addHeldSeries(t, service, 11, 22)
	entries := map[int64][]tvdb.ListEntry{}
	film := int64(1000)
	// Series 11 is on three lists of 40 films and one of 41; series 22 on one list of one film.
	for list, size := range map[int64]int{501: 40, 502: 40, 503: 40, 504: 41} {
		for range size {
			film++
			entries[list] = append(entries[list], tvdb.ListEntry{MovieID: film})
		}
	}
	entries[600] = []tvdb.ListEntry{{SeriesID: 22}, {MovieID: 1}}
	lists := &fakeSeriesLists{
		lists: map[int64][]tvdb.List{
			11: {{ID: 501, IsOfficial: true}, {ID: 502, IsOfficial: true}, {ID: 503, IsOfficial: true}, {ID: 504, IsOfficial: true}},
			22: {{ID: 600, IsOfficial: true}},
		},
		entries: entries,
	}
	films := &fakeFilmIDs{}
	pass := tv.FranchisePass{
		Franchises: library.NewFranchises(opened), Crossovers: library.NewCrossovers(opened), Series: service.Series, Lists: lists, Films: films, FilmType: "movie",
		Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler),
	}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(films.asked) != 120 || slices.Contains(films.asked, 1) {
		t.Errorf("first pass asked %d films, want the 120 of series 11's three lists and none of the 41-film list or series 22", len(films.asked))
	}
	films.asked = nil
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if want := []int64{1}; !reflect.DeepEqual(films.asked, want) {
		t.Errorf("second pass asked %v, want %v: series 22 only", films.asked, want)
	}
}

func TestFranchiseListFilmsNeedTheirOwnSourceToBeAsked(t *testing.T) {
	service, opened := newService(t, seriesByID{11: eventSeries(11, "Glimmerton", eventEpisode(111, 1, 1, "Pilot", "2018-01-01"))}, fakeTVmaze{err: tvmaze.ErrNotFound})
	addHeldSeries(t, service, 11)
	lists := &fakeSeriesLists{
		lists:   map[int64][]tvdb.List{11: {{ID: 500, IsOfficial: true}}},
		entries: map[int64][]tvdb.ListEntry{500: {{SeriesID: 11}, {MovieID: 900}}},
	}
	pass := tv.FranchisePass{
		Franchises: library.NewFranchises(opened), Crossovers: library.NewCrossovers(opened), Series: service.Series, Lists: lists, FilmType: "movie",
		Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler),
	}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	got, err := library.NewFranchises(opened).Classifications(t.Context(), "tv", library.FranchiseListKind)
	if err != nil || len(got) != 0 {
		t.Errorf("lists = %v, %v; want none without a film source", got, err)
	}
}
