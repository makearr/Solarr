package movie_test

import (
	"context"
	"path"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

const scanRoot = "/library"

type categories struct{ policy string }

func (c categories) Category(int64) (settings.Category, bool) {
	return settings.Category{TVMonitor: c.policy}, true
}
func (categories) ProfileFor(_ settings.Category, fallback int64) int64 { return fallback }

// byTitle is a TMDB whose search answers the movies it is given and whose Movie answers the one asked for.
type byTitle struct{ movies []tmdb.Movie }

func (byTitle) Configured() bool { return true }
func (b byTitle) Movie(_ context.Context, id int64) (tmdb.Movie, error) {
	for _, movie := range b.movies {
		if movie.ID == id {
			return movie, nil
		}
	}
	return tmdb.Movie{}, tmdb.ErrNotFound
}
func (b byTitle) SearchMovies(_ context.Context, _ string, _, _ int) ([]tmdb.MovieHit, error) {
	var hits []tmdb.MovieHit
	for _, movie := range b.movies {
		hits = append(hits, tmdb.MovieHit{ID: movie.ID, Title: movie.Title, ReleaseDate: movie.ReleaseDate})
	}
	return hits, nil
}
func (byTitle) FindMovieByIMDb(context.Context, string) (tmdb.MovieHit, error) {
	return tmdb.MovieHit{}, tmdb.ErrNotFound
}
func (byTitle) Collection(context.Context, int64) (tmdb.CollectionDetail, error) {
	return tmdb.CollectionDetail{}, nil
}

func simpleMovie(id int64, title, date string) tmdb.Movie {
	return tmdb.Movie{ID: id, Title: title, ReleaseDate: date, Runtime: 100}
}

func newConsumer(t *testing.T, source movie.TMDBSource, policy string) (movie.Consumer, *store.Store, *fsx.Mem) {
	t.Helper()
	opened := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	service := movie.Service{
		TMDB: source, Series: library.NewSeries(opened), Reader: library.NewSeriesReader(opened), Desired: library.NewDesired(opened),
		Franchises: library.NewFranchises(opened), Exclusions: library.NewExclusions(opened), Clock: fake,
	}
	consumer := movie.NewConsumer(movie.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Unidentified: library.NewUnidentified(opened), Service: service,
		Reader: library.NewSeriesReader(opened), Clock: fake, DefaultProfileID: 1, Categories: categories{policy},
	})
	return consumer, opened, fsys
}

func writeVideo(t *testing.T, fsys *fsx.Mem, rel string) scan.Observed {
	t.Helper()
	absolute := path.Join(scanRoot, rel)
	if err := fsys.MkdirAll(path.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(absolute)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("video " + rel)
	_, _ = file.Write(content)
	_ = file.Close()
	return scan.Observed{
		File:  scan.Found{RelPath: rel, SizeBytes: int64(len(content)), ModTime: time.Unix(0, 0)},
		Probe: mediatool.Probe{Path: absolute},
	}
}

func writeText(t *testing.T, fsys *fsx.Mem, rel, text string) {
	t.Helper()
	absolute := path.Join(scanRoot, rel)
	if err := fsys.MkdirAll(path.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(absolute)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte(text))
	_ = file.Close()
}

func root() settings.RootFolder { return settings.RootFolder{ID: 1, Path: scanRoot, Online: true} }

func workTitles(t *testing.T, opened *store.Store, categoryID int64) map[string]int64 {
	t.Helper()
	titles, err := library.NewSeriesReader(opened).TitlesOf(t.Context(), movie.MediaType, categoryID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, title := range titles {
		out[title.Title] = title.WorkID
	}
	return out
}

func TestScanAddsTheMovieAndAttachesItsFile(t *testing.T) {
	consumer, opened, fsys := newConsumer(t, byTitle{movies: []tmdb.Movie{simpleMovie(603, "The Example Film", "1999-03-30")}}, settings.TVMonitorExisting)
	batch := []scan.Observed{writeVideo(t, fsys, "The Example Film (1999)/The Example Film (1999).mkv")}
	if err := consumer.Observe(t.Context(), root(), 3, batch); err != nil {
		t.Fatal(err)
	}
	works := workTitles(t, opened, 3)
	id, found := works["The Example Film"]
	if !found {
		t.Fatalf("works = %v, want The Example Film", works)
	}
	items, err := library.NewDesired(opened).AlbumItemsForWork(t.Context(), id)
	if err != nil || len(items) != 1 || items[0].Monitored != 1 || items[0].PresentCount != 1 || items[0].State != "COMPLETE" {
		t.Fatalf("items = %+v, %v; want one monitored COMPLETE item with its file", items, err)
	}
}

func TestScanFindsAMovieByTheIDInItsNameWithoutSearching(t *testing.T) {
	consumer, opened, fsys := newConsumer(t, byTitle{movies: []tmdb.Movie{simpleMovie(603, "The Example Film", "1999-03-30"), simpleMovie(604, "The Example Sequel", "2003-05-15")}}, settings.TVMonitorExisting)
	batch := []scan.Observed{writeVideo(t, fsys, "My Copy/example.mkv")}
	writeText(t, fsys, "My Copy/example.nfo", `<movie><uniqueid type="tmdb" default="true">604</uniqueid></movie>`)
	if err := consumer.Observe(t.Context(), root(), 3, batch); err != nil {
		t.Fatal(err)
	}
	if works := workTitles(t, opened, 3); len(works) != 1 || works["The Example Sequel"] == 0 {
		t.Errorf("works = %v, want only The Example Sequel, taken from the nfo", works)
	}
}

func TestScanQueuesAFileItCannotPlace(t *testing.T) {
	consumer, opened, fsys := newConsumer(t, byTitle{movies: []tmdb.Movie{simpleMovie(1, "Heat", "1995-12-15"), simpleMovie(2, "Heat", "2013-06-28")}}, settings.TVMonitorExisting)
	// No year in the name, two films of that name: a person decides.
	batch := []scan.Observed{writeVideo(t, fsys, "Heat/movie.mkv")}
	if err := consumer.Observe(t.Context(), root(), 3, batch); err != nil {
		t.Fatal(err)
	}
	if works := workTitles(t, opened, 3); len(works) != 0 {
		t.Errorf("works = %v, want none added on a guess", works)
	}
	if queue, err := library.NewUnidentified(opened).Count(t.Context()); err != nil || queue != 1 {
		t.Errorf("review queue = %d, %v; want the one file", queue, err)
	}
}

func TestScanTakesTheBetterRankedOfTwoFilmsWithTheSameTitleAndYear(t *testing.T) {
	consumer, opened, fsys := newConsumer(t, byTitle{movies: []tmdb.Movie{simpleMovie(329865, "Arrival", "2016-09-01"), simpleMovie(472349, "Arrival", "2016-01-01")}}, settings.TVMonitorExisting)
	if err := consumer.Observe(t.Context(), root(), 3, []scan.Observed{writeVideo(t, fsys, "Arrival (2016)/Arrival (2016).mp4")}); err != nil {
		t.Fatal(err)
	}
	var held int64
	for _, id := range workTitles(t, opened, 3) {
		held = id
	}
	ids, err := library.NewSeriesReader(opened).ProviderIDs(t.Context(), held)
	if err != nil || ids[movie.ProviderTMDB] != "329865" {
		t.Errorf("provider ids = %v, %v; want the first-ranked film, 329865", ids, err)
	}
}

func TestScanKeepsTwoEditionsAsFilesOfOneMovie(t *testing.T) {
	consumer, opened, fsys := newConsumer(t, byTitle{movies: []tmdb.Movie{simpleMovie(78, "Sample Runner", "1982-06-25")}}, settings.TVMonitorExisting)
	batch := []scan.Observed{
		writeVideo(t, fsys, "Sample Runner (1982)/Sample Runner (1982).mkv"),
		writeVideo(t, fsys, "Sample Runner (1982)/Sample Runner (1982) {edition-Final Cut}.mkv"),
	}
	if err := consumer.Observe(t.Context(), root(), 3, batch); err != nil {
		t.Fatal(err)
	}
	works := workTitles(t, opened, 3)
	if len(works) != 1 {
		t.Fatalf("works = %v, want one movie", works)
	}
	var id int64
	for _, work := range works {
		id = work
	}
	files, err := library.NewPhysical(opened).PathsForWorkOnRoot(t.Context(), 1, id)
	if err != nil || len(files) != 2 {
		t.Errorf("files of the movie = %+v, %v; want both editions", files, err)
	}
	if items, _ := library.NewDesired(opened).AlbumItemsForWork(t.Context(), id); len(items) != 1 {
		t.Errorf("items = %+v, want one wanted item", items)
	}
}

func TestScanWithTheNonePolicyAddsTheMovieUnmonitored(t *testing.T) {
	consumer, opened, fsys := newConsumer(t, byTitle{movies: []tmdb.Movie{simpleMovie(603, "The Example Film", "1999-03-30")}}, settings.TVMonitorNone)
	if err := consumer.Observe(t.Context(), root(), 3, []scan.Observed{writeVideo(t, fsys, "The Example Film (1999)/The Example Film (1999).mkv")}); err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, work := range workTitles(t, opened, 3) {
		id = work
	}
	if items, _ := library.NewDesired(opened).AlbumItemsForWork(t.Context(), id); len(items) != 1 || items[0].Monitored != 0 {
		t.Errorf("items = %+v, want one unmonitored item", items)
	}
}

func TestScanTakesTheFoldersYearWhenTheFileIsNamedAnotherYear(t *testing.T) {
	consumer, opened, fsys := newConsumer(t, byTitle{movies: []tmdb.Movie{simpleMovie(603, "The Example Film", "1999-03-30")}}, settings.TVMonitorExisting)
	batch := []scan.Observed{writeVideo(t, fsys, "The Example Film (1999)/The Example Film (2025).mkv")}
	if err := consumer.Observe(t.Context(), root(), 3, batch); err != nil {
		t.Fatal(err)
	}
	if works := workTitles(t, opened, 3); works["The Example Film"] == 0 {
		t.Errorf("works = %v, want The Example Film placed through its folder", works)
	}
	if queue, err := library.NewUnidentified(opened).Count(t.Context()); err != nil || queue != 0 {
		t.Errorf("review queue = %d, %v; want none", queue, err)
	}
}
