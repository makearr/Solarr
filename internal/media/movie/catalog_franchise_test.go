package movie_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// trilogy is a TMDB that knows the first two films of a collection and lists a third as its part.
type trilogy struct{ fakeTMDB }

func (trilogy) Movie(_ context.Context, id int64) (tmdb.Movie, error) {
	film := theMatrix()
	film.ID, film.Title, film.IMDbID = id, map[int64]string{603: "The Tonhut", 604: "The Tonhut Tufoatoj"}[id], ""
	return film, nil
}

func (trilogy) Collection(context.Context, int64) (tmdb.CollectionDetail, error) {
	return tmdb.CollectionDetail{ID: 2344, Name: "The Tonhut Collection", Parts: []tmdb.MovieHit{
		{ID: 603, Title: "The Tonhut"}, {ID: 604, Title: "The Tonhut Tufoatoj"},
	}}, nil
}

func TestMonitoringAFranchiseAddsTheFilmsTheLibraryLacks(t *testing.T) {
	service, opened := newService(t, trilogy{fakeTMDB{configured: true}}, nil)
	if _, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: 1, Monitor: movie.MonitorNone}); err != nil {
		t.Fatal(err)
	}
	catalog := movie.Catalog{Service: service, Categories: categories{}, FranchiseStore: library.NewFranchises(opened)}
	listed, err := catalog.FranchiseStore.InCategory(t.Context(), movie.MediaType, 5)
	if err != nil || len(listed) != 1 || len(listed[0].Members) != 1 || listed[0].Monitored || listed[0].Name != "The Tonhut Collection" {
		t.Fatalf("franchises = %+v, %v; want one unmonitored collection holding one film", listed, err)
	}

	added, err := catalog.MonitorFranchise(t.Context(), 5, listed[0].ID, true)
	if err != nil || added != 1 {
		t.Fatalf("monitor = %d, %v; want the second film added", added, err)
	}
	listed, _ = catalog.FranchiseStore.InCategory(t.Context(), movie.MediaType, 5)
	if len(listed) != 1 || !listed[0].Monitored || len(listed[0].Members) != 2 || listed[0].Total != 2 {
		t.Errorf("after following: %+v, want both films, monitored, of two", listed)
	}
	for _, member := range listed[0].Members {
		if member.Title == "The Tonhut Tufoatoj" && !member.Monitored {
			t.Errorf("the added film is monitored: %+v", member)
		}
	}
	if again, err := catalog.MonitorFranchise(t.Context(), 5, listed[0].ID, true); err != nil || again != 0 {
		t.Errorf("following again added %d, %v; want nothing new", again, err)
	}
}
