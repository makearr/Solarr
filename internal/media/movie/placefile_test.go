package movie_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

func TestAQueuedFileIsPlacedOnTheMovieAPersonNames(t *testing.T) {
	service, opened := newService(t, byTitle{movies: []tmdb.Movie{simpleMovie(603, "The Tonhut", "1999-03-30")}}, nil)
	physical := library.NewPhysical(opened)
	unidentified := library.NewUnidentified(opened)
	registered, err := physical.RegisterBatch(t.Context(), 9, []library.PhysicalFile{{RelPath: "mystery/film.mkv", SizeBytes: 5, QuickHash: "q", FirstSeenAt: addNow}})
	if err != nil {
		t.Fatal(err)
	}
	if err := unidentified.Record(t.Context(), []library.QueueEntry{{MediaObjectID: registered[0].MediaObjectID, RootFolderID: 9, RelPath: "mystery/film.mkv", At: addNow.Unix()}}); err != nil {
		t.Fatal(err)
	}
	files := movie.Files{
		Resolver: movie.Resolver{Service: service, Reader: library.NewSeriesReader(opened), DefaultProfileID: 1, Categories: categories{}},
		Series:   library.NewSeries(opened), Unidentified: unidentified, Clock: atAddNow(),
	}

	if _, _, err := files.PlaceFile(t.Context(), 5, registered[0].MediaObjectID, media.Placement{Provider: movie.ProviderTMDB, ExternalID: "nonsense"}, false); err == nil {
		t.Error("an id that is no id is refused")
	}
	workID, placed, err := files.PlaceFile(t.Context(), 5, registered[0].MediaObjectID, media.Placement{Provider: movie.ProviderTMDB, ExternalID: "603"}, false)
	if err != nil || workID == 0 || placed != 1 {
		t.Fatalf("placed = %d on work %d, %v", placed, workID, err)
	}
	onRoot, err := physical.PathsForWorkOnRoot(t.Context(), 9, workID)
	if err != nil || len(onRoot) != 1 {
		t.Errorf("files of the movie = %+v, %v; want the queued one", onRoot, err)
	}
	if _, err := unidentified.Get(t.Context(), registered[0].MediaObjectID); err == nil {
		t.Error("the file is no longer waiting")
	}
}
