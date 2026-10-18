package movie_test

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

type fakeFilmFranchises struct {
	byTMDB map[int64][]wikidata.Franchise
	err    error
}

func (f fakeFilmFranchises) FilmFranchises(context.Context, []int64) (map[int64][]wikidata.Franchise, error) {
	return f.byTMDB, f.err
}

func TestUniversePassGroupsFilmsTMDBLeavesApartAndDropsWhatWikidataNoLongerSays(t *testing.T) {
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	franchises := library.NewFranchises(opened)
	works := map[int64]int64{}
	for _, id := range []int64{1, 2, 3} {
		stored, err := service.Series.Upsert(t.Context(), addNow, library.SeriesWrite{
			Work: library.WorkIdentity{MediaType: movie.MediaType, CanonicalTitle: "Film " + strconv.FormatInt(id, 10), SortTitle: "film " + strconv.FormatInt(id, 10)},
			IDs:  []library.ExternalIDWrite{{Provider: movie.ProviderTMDB, ExternalID: strconv.FormatInt(id, 10)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		works[id] = stored.WorkID
	}
	// TMDB's own collection holds films 1 and 2; Wikidata's universe also has film 3.
	for _, id := range []int64{1, 2} {
		if _, err := franchises.Join(t.Context(), movie.MediaType, movie.ProviderCollection, "77", "Example Collection", works[id]); err != nil {
			t.Fatal(err)
		}
	}
	universe := []wikidata.Franchise{{QID: "Q5", Label: "Example Universe", Named: true}}
	source := fakeFilmFranchises{byTMDB: map[int64][]wikidata.Franchise{1: universe, 2: universe, 3: universe}}
	pass := &movie.UniversePass{Source: source, Franchises: franchises, Logger: slog.New(slog.DiscardHandler)}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	summaries, err := franchises.Summaries(t.Context(), movie.MediaType)
	if err != nil || len(summaries) != 2 {
		t.Fatalf("collections = %+v, %v; want TMDB's and the universe", summaries, err)
	}
	var universeID int64
	for _, summary := range summaries {
		if summary.Source == movie.ProviderWikidata && summary.Name == "Example Universe" {
			universeID = summary.ID
		}
	}
	if members, _ := franchises.MembersOf(t.Context(), universeID); len(members) != 3 {
		t.Fatalf("universe members = %v, want the three films", members)
	}

	// Wikidata then names only films 1 and 2, which TMDB's collection already holds: the universe goes.
	source.byTMDB = map[int64][]wikidata.Franchise{1: universe, 2: universe}
	pass.Source = source
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if summaries, _ := franchises.Summaries(t.Context(), movie.MediaType); len(summaries) != 1 {
		t.Errorf("collections = %+v, want only TMDB's", summaries)
	}

	// An answer that failed changes nothing.
	pass.Source = fakeFilmFranchises{err: errors.New("busy")}
	if err := pass.Run(t.Context(), jobs.Job{}); err == nil {
		t.Error("a failing Wikidata is an error to retry")
	}
	if summaries, _ := franchises.Summaries(t.Context(), movie.MediaType); len(summaries) != 1 {
		t.Errorf("collections = %+v after a failed pass", summaries)
	}
}
