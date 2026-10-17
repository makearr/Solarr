package movie_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
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
func (f fakeTMDB) SearchMovies(context.Context, string, int) ([]tmdb.MovieHit, error) {
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
