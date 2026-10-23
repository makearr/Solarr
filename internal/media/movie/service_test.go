package movie_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/radarrmeta"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

var addNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func newLibrary(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
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

type fakeTMDB struct {
	configured bool
	movie      tmdb.Movie
	err        error
}

func (f fakeTMDB) Configured() bool { return f.configured }
func (f fakeTMDB) Movie(context.Context, int64) (tmdb.Movie, error) {
	return f.movie, f.err
}
func (f fakeTMDB) SearchMovies(context.Context, string, int, int) ([]tmdb.MovieHit, error) {
	return []tmdb.MovieHit{{ID: f.movie.ID, Title: f.movie.Title, ReleaseDate: f.movie.ReleaseDate}}, f.err
}
func (f fakeTMDB) FindMovieByIMDb(context.Context, string) (tmdb.MovieHit, error) {
	return tmdb.MovieHit{ID: f.movie.ID}, f.err
}
func (f fakeTMDB) Collection(context.Context, int64) (tmdb.CollectionDetail, error) {
	return tmdb.CollectionDetail{}, nil
}

type fakeProxy struct {
	movie radarrmeta.Movie
	err   error
}

func (f fakeProxy) Movie(context.Context, int64) (radarrmeta.Movie, error)   { return f.movie, f.err }
func (f fakeProxy) ByIMDb(context.Context, string) (radarrmeta.Movie, error) { return f.movie, f.err }
func (f fakeProxy) Search(context.Context, string, int) ([]radarrmeta.Movie, error) {
	return []radarrmeta.Movie{f.movie}, f.err
}

func theExample() tmdb.Movie {
	movie := tmdb.Movie{ID: 603, IMDbID: "tt0000001", Title: "The Example Film", OriginalTitle: "The Example Film", Overview: "A hacker.", ReleaseDate: "1999-03-30",
		Runtime: 136, OriginalLanguage: "en", VoteAverage: 8.2, VoteCount: 100, PosterPath: "/p.jpg", Genres: []tmdb.Genre{{ID: 1, Name: "Action"}}}
	movie.Collection = &tmdb.Collection{ID: 2344, Name: "The Example Collection"}
	movie.Credits.Cast = []tmdb.MovieCast{{ID: 6384, Name: "Jane Actor", Character: "Neo", Order: 0}}
	movie.Credits.Crew = []tmdb.MovieCrew{{ID: 9340, Name: "Sam Director", Job: "Director"}}
	movie.ReleaseDates.Results = []struct {
		Country string             `json:"iso_3166_1"`
		Dates   []tmdb.ReleaseDate `json:"release_dates"`
	}{{Country: "US", Dates: []tmdb.ReleaseDate{
		{Type: tmdb.ReleaseTheatrical, Date: "1999-03-31T00:00:00Z", Certification: "R"},
		{Type: tmdb.ReleasePhysical, Date: "1999-09-21T00:00:00Z"},
	}}}
	return movie
}

func newService(t *testing.T, source movie.TMDBSource, proxy movie.ProxySource) (movie.Service, *store.Store) {
	t.Helper()
	opened := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	return movie.Service{
		TMDB: source, Proxy: proxy, Series: library.NewSeries(opened), Reader: library.NewSeriesReader(opened),
		Desired: library.NewDesired(opened), Franchises: library.NewFranchises(opened), Exclusions: library.NewExclusions(opened), Clock: fake,
	}, opened
}

// A film no source knows is refused, unless the caller knows it: Radarr's own title, year and ids (48 live films of a
// real Radarr, shorts and TV specials, were refused on import 2026-10-21).
func TestAddStoresTheFallbackWhenNoSourceKnowsTheMovie(t *testing.T) {
	service, opened := newService(t, fakeTMDB{configured: true, err: tmdb.ErrNotFound}, nil)
	request := movie.AddRequest{IDs: movie.IDs{TMDB: 779451}, CategoryID: 5, ProfileID: 1, Monitor: movie.MonitorMovieOnly}
	if _, err := service.Add(t.Context(), request); !errors.Is(err, movie.ErrNoSource) {
		t.Fatalf("without a fallback: err = %v, want ErrNoSource", err)
	}
	request.Fallback = &movie.Movie{IDs: movie.IDs{TMDB: 779451, IMDb: "tt7524614"}, Title: "A Short Return", Year: 2017}
	added, err := service.Add(t.Context(), request)
	if err != nil || !added.Created {
		t.Fatalf("with a fallback: %+v, %v", added, err)
	}
	series := library.NewSeries(opened)
	for provider, id := range map[string]string{movie.ProviderTMDB: "779451", movie.ProviderIMDb: "tt7524614"} {
		if work, found, err := series.WorkByProviderID(t.Context(), movie.MediaType, provider, id); err != nil || !found || work != added.WorkID {
			t.Errorf("work by %s = %d %v %v", provider, work, found, err)
		}
	}
	// A source that answers without a title is no answer either.
	untitled, _ := newService(t, fakeTMDB{configured: true, movie: tmdb.Movie{ID: 779452, ReleaseDate: "2010-06-01"}}, nil)
	request.IDs.TMDB, request.Fallback.IDs.TMDB = 779452, 779452
	if named, err := untitled.Add(t.Context(), request); err != nil || named.Movie.Title != "A Short Return" {
		t.Errorf("an untitled answer: %q, %v; want the fallback's title", named.Movie.Title, err)
	}
	var title string
	var year sql.NullInt64
	if err := opened.Read().QueryRowContext(t.Context(), "SELECT canonical_title, year FROM work WHERE id = ?", added.WorkID).Scan(&title, &year); err != nil ||
		title != "A Short Return" || year.Int64 != 2017 {
		t.Errorf("work = %q %v, %v", title, year, err)
	}
}

func TestAddStoresTheMovieItsOneWantedItemItsReleaseDayAndFranchise(t *testing.T) {
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: 1, Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	if !added.Created || added.WorkID == 0 || added.ItemID == 0 {
		t.Fatalf("add = %+v", added)
	}
	item, err := library.NewDesired(opened).Get(t.Context(), added.ItemID)
	if err != nil || item.SatisfactionKind != "SET" || item.Monitored != 1 || !item.RequiredCount.Valid || item.RequiredCount.Int64 != 1 {
		t.Errorf("item = %+v, %v; want a monitored SET item needing one file", item, err)
	}
	if work, found, err := library.NewSeries(opened).WorkByProviderID(t.Context(), movie.MediaType, movie.ProviderIMDb, "tt0000001"); err != nil || !found || work != added.WorkID {
		t.Errorf("work by imdb id = %d %v %v", work, found, err)
	}
	if id, found, err := library.NewSeries(opened).WorkByProviderID(t.Context(), movie.MediaType, movie.ProviderTMDB, "603"); err != nil || !found || id != added.WorkID {
		t.Errorf("work by tmdb id = %d %v %v", id, found, err)
	}
	collections, err := library.NewFranchises(opened).Join(t.Context(), movie.MediaType, movie.ProviderCollection, "2344", "The Example Collection", added.WorkID)
	if err != nil || collections == 0 {
		t.Errorf("the add already joined the franchise; joining again = %d, %v", collections, err)
	}
	if again, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: 1}); err != nil || again.WorkID != added.WorkID || again.Created {
		t.Errorf("adding a held movie = %+v, %v; want the same work, not created", again, err)
	}
}

func TestAddWithNoMonitoringLeavesTheItemUnmonitored(t *testing.T) {
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: 1, Monitor: movie.MonitorNone})
	if err != nil {
		t.Fatal(err)
	}
	if item, _ := library.NewDesired(opened).Get(t.Context(), added.ItemID); item.Monitored != 0 {
		t.Errorf("monitored = %d, want 0", item.Monitored)
	}
}

func TestFetchFallsBackToRadarrsServiceWhenTMDBHasNoKey(t *testing.T) {
	proxy := fakeProxy{movie: radarrmeta.Movie{TMDBID: 603, IMDbID: "tt0000001", Title: "The Example Film", Year: 1999, InCinemas: "1999-03-31T00:00:00Z", PhysicalRelease: "1999-11-25T00:00:00Z"}}
	service, _ := newService(t, fakeTMDB{configured: false}, proxy)
	fetched, err := service.Fetch(t.Context(), movie.IDs{TMDB: 603})
	if err != nil || fetched.Title != "The Example Film" || fetched.Physical != "1999-11-25" || len(fetched.Sources) != 1 || fetched.Sources[0] != "radarr" {
		t.Errorf("fetched = %+v, %v", fetched, err)
	}
	none, _ := newService(t, fakeTMDB{configured: false}, nil)
	if _, err := none.Fetch(t.Context(), movie.IDs{TMDB: 603}); err == nil {
		t.Error("with no source at all a fetch must fail")
	}
}

func TestFetchMergesTMDBAndTheProxyWithTMDBFirst(t *testing.T) {
	proxy := fakeProxy{movie: radarrmeta.Movie{TMDBID: 603, Title: "Example (other)", DigitalRelease: "2001-03-19T00:00:00Z",
		MovieRatings: map[string]radarrmeta.Rating{"Imdb": {Count: 10, Value: 8.7}}}}
	service, _ := newService(t, fakeTMDB{configured: true, movie: theExample()}, proxy)
	fetched, err := service.Fetch(t.Context(), movie.IDs{TMDB: 603})
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Title != "The Example Film" || fetched.Digital != "2001-03-19" || len(fetched.Ratings) != 2 {
		t.Errorf("merged = %+v: TMDB's title wins, the proxy fills the digital date and adds IMDb's rating", fetched)
	}
}

func TestSearchPrefersTMDBAndFallsBackToTheProxy(t *testing.T) {
	service, _ := newService(t, fakeTMDB{configured: true, movie: theExample()}, fakeProxy{})
	if got, err := service.Search(t.Context(), "example", 1999, 10); err != nil || len(got) != 1 || got[0].TMDBID != 603 || got[0].Year != 1999 {
		t.Errorf("tmdb search = %+v, %v", got, err)
	}
	proxyOnly, _ := newService(t, fakeTMDB{configured: false}, fakeProxy{movie: radarrmeta.Movie{TMDBID: 1, Title: "Only Here", Year: 2020}})
	if got, err := proxyOnly.Search(t.Context(), "only", 0, 10); err != nil || len(got) != 1 || got[0].Title != "Only Here" {
		t.Errorf("proxy search = %+v, %v", got, err)
	}
}

func TestALibrarysOrderDecidesWhoSuppliesAndWhoOnlyFills(t *testing.T) {
	proxy := fakeProxy{movie: radarrmeta.Movie{TMDBID: 603, Title: "Example (other)", DigitalRelease: "2001-03-19T00:00:00Z"}}
	service, _ := newService(t, fakeTMDB{configured: true, movie: theExample()}, proxy)
	orders := map[int64][]string{1: {"radarr", "tmdb"}, 2: {"tmdb"}, 3: {"radarr"}}
	service.PriorityFor = func(categoryID int64) []string { return orders[categoryID] }

	first, _ := service.FetchFor(t.Context(), 1, movie.IDs{TMDB: 603})
	if first.Title != "Example (other)" || first.RuntimeMin == 0 {
		t.Errorf("radarr first: %q runtime %d min; want its title, with what it lacks filled from TMDB", first.Title, first.RuntimeMin)
	}
	onlyTMDB, _ := service.FetchFor(t.Context(), 2, movie.IDs{TMDB: 603})
	if onlyTMDB.Title != "The Example Film" || onlyTMDB.Digital != "" {
		t.Errorf("a provider left out is not asked: %+v", onlyTMDB)
	}
	onlyRadarr, _ := service.FetchFor(t.Context(), 3, movie.IDs{TMDB: 603})
	if onlyRadarr.Title != "Example (other)" || len(onlyRadarr.Sources) != 1 {
		t.Errorf("only radarr: %+v", onlyRadarr)
	}
}

// pagedTMDB answers `total` hits in TMDB's pages of 20, recording which pages were asked for.
type pagedTMDB struct {
	fakeTMDB
	total int
	pages *[]int
}

func (f pagedTMDB) SearchMovies(_ context.Context, _ string, _, page int) ([]tmdb.MovieHit, error) {
	*f.pages = append(*f.pages, page)
	var hits []tmdb.MovieHit
	for index := (page - 1) * tmdb.SearchPageSize; index < min(page*tmdb.SearchPageSize, f.total); index++ {
		hits = append(hits, tmdb.MovieHit{ID: int64(index + 1)})
	}
	return hits, nil
}

// The Add page's More asks for a larger limit: TMDB's pages are read until it is met or a page runs short.
func TestSearchReadsAsManyTMDBPagesAsTheLimitNeeds(t *testing.T) {
	for _, test := range []struct {
		total, limit int
		wantPages    []int
		wantHits     int
	}{
		{total: 45, limit: 20, wantPages: []int{1}, wantHits: 20},
		{total: 45, limit: 40, wantPages: []int{1, 2}, wantHits: 40},
		{total: 45, limit: 100, wantPages: []int{1, 2, 3}, wantHits: 45},
		{total: 5, limit: 60, wantPages: []int{1}, wantHits: 5},
	} {
		var pages []int
		service := movie.Service{TMDB: pagedTMDB{fakeTMDB: fakeTMDB{configured: true}, total: test.total, pages: &pages}}
		found, err := service.Search(t.Context(), "example", 0, test.limit)
		if err != nil {
			t.Fatalf("Search(limit %d): %v", test.limit, err)
		}
		if len(found) != test.wantHits || !slices.Equal(pages, test.wantPages) {
			t.Errorf("Search(total %d, limit %d) = %d hits from pages %v, want %d from %v",
				test.total, test.limit, len(found), pages, test.wantHits, test.wantPages)
		}
	}
}
